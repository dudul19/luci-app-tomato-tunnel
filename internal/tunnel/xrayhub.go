package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/logx"
)

var hubIdle = 10 * time.Second

type xrayHub struct {
	mu   sync.Mutex
	x    *xrayProc
	in   io.WriteCloser
	seq  int
	gen  int
	old  bool
	wait map[string]chan string
}

var hub = &xrayHub{wait: map[string]chan string{}}

func (h *xrayHub) start(log *logx.Logger, o XrayOpts) error {
	if h.x != nil {
		return nil
	}
	if h.old {
		return errNotShared
	}
	bin := XrayBin()
	if bin == "" {
		return ErrNoXray
	}
	rules := xrayBlockRules(o)
	if rules == nil {
		rules = []any{}
	}
	cfg := map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{},
		"outbounds": []any{map[string]any{"tag": "block", "protocol": "blackhole"}},
		"routing":   map[string]any{"domainStrategy": "AsIs", "rules": rules},
		"policy":    xrayPolicy(),
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	dir := o.RunDir
	if dir == "" {
		dir = os.TempDir()
	}
	os.MkdirAll(dir, 0700)

	h.gen++
	conf := filepath.Join(dir, fmt.Sprintf("xray-shared-%d.json", h.gen))
	if err := os.WriteFile(conf, b, 0600); err != nil {
		return err
	}
	cmd := exec.Command(bin, "run", "-c", conf, "-ctl")
	cmd.SysProcAttr = xrayProcAttr()
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	x := &xrayProc{cmd: cmd, conf: conf, done: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		os.Remove(conf)
		return fmt.Errorf("start Xray: %v", err)
	}
	xrayLive.Store(cmd.Process.Pid, true)
	h.x, h.in = x, in
	log.Infof("xray", "Shared Xray started for Balance (one process for every Xray account)")
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 64*1024)
		for sc.Scan() {
			l := sc.Text()
			if rest, ok := strings.CutPrefix(l, "@tt "); ok {
				f := strings.SplitN(rest, " ", 3)
				if len(f) >= 2 {
					msg := ""
					if f[0] != "ok" {
						msg = "rejected"
						if len(f) == 3 {
							msg = f[2]
						}
					}
					h.reply(f[1], msg)
				}
				continue
			}
			x.addLine(l)
			log.Debugf("xray", "%s", cleanXrayLine(l))
		}
		io.Copy(io.Discard, out)
	}()
	go func() {
		<-scanned
		cmd.Wait()
		xrayLive.Delete(cmd.Process.Pid)
		x.mu.Lock()
		x.exited = true
		x.mu.Unlock()
		close(x.done)
		os.Remove(conf)
		h.mu.Lock()
		if h.x == x {
			h.x, h.in = nil, nil
		}
		h.mu.Unlock()
	}()
	return nil
}

func (h *xrayHub) reply(tag, msg string) {
	h.mu.Lock()
	ch := h.wait[tag]
	delete(h.wait, tag)
	h.mu.Unlock()
	if ch != nil {
		ch <- msg
	}
}

func (h *xrayHub) send(op, tag string, conf any) error {
	if h.in == nil {
		return ErrNoSession
	}
	m := map[string]any{"op": op, "tag": tag}
	if conf != nil {
		m["conf"] = conf
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = h.in.Write(append(b, '\n'))
	return err
}

func (h *xrayHub) add(ctx context.Context, log *logx.Logger, o XrayOpts, port int, out map[string]any) (*xrayProc, error) {
	h.mu.Lock()
	if err := h.start(log, o); err != nil {
		h.mu.Unlock()
		return nil, err
	}
	h.seq++
	tag := fmt.Sprintf("a%d", h.seq)
	out["tag"] = "out-" + tag
	in := map[string]any{"tag": "in-" + tag, "listen": "127.0.0.1", "port": port, "protocol": "socks",
		"settings": map[string]any{"auth": "noauth", "udp": true, "ip": "127.0.0.1"}}
	if o.BlockAds {
		in["sniffing"] = map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}
	}
	conf := map[string]any{
		"inbounds":  []any{in},
		"outbounds": []any{out},
		"routing": map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"in-" + tag},
			"outboundTag": "out-" + tag, "ruleTag": "r-" + tag}}},
	}
	ch := make(chan string, 1)
	h.wait[tag] = ch
	parent := h.x
	parent.refs++
	err := h.send("add", tag, conf)
	h.mu.Unlock()
	if err == nil {
		select {
		case msg := <-ch:
			if msg != "" {
				err = fmt.Errorf("%s", cleanXrayLine(msg))
			}
		case <-parent.done:
			err = fmt.Errorf("shared Xray stopped: %s", parent.lastErr())
			if parent.tailHas("-ctl") {
				h.mu.Lock()
				h.old = true
				h.mu.Unlock()
				log.Warnf("xray", "The Xray add-on is older than this Tomato Tunnel: every Xray account runs its own Xray. Update the add-on in Settings › About & update.")
				err = errNotShared
			}
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err != nil {
		h.release(tag, parent)
		return nil, err
	}
	return &xrayProc{port: port, done: parent.done, tag: tag, parent: parent}, nil
}

func (h *xrayHub) release(tag string, parent *xrayProc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.wait, tag)
	if h.x != parent {
		return
	}
	parent.refs--
	h.send("rm", tag, nil)
	if parent.refs > 0 {
		return
	}
	x := h.x
	time.AfterFunc(hubIdle, func() {
		h.mu.Lock()
		if h.x != x || x.refs > 0 {
			h.mu.Unlock()
			return
		}
		in := h.in
		h.x, h.in = nil, nil
		h.mu.Unlock()
		in.Close()
		select {
		case <-x.done:
		case <-time.After(3 * time.Second):
			x.stop()
		}
	})
}

func (x *xrayProc) track(key any, close func()) bool {
	if x == nil || x.tag == "" {
		return true
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.gone {
		return false
	}
	if x.conns == nil {
		x.conns = map[any]func(){}
	}
	x.conns[key] = close
	return true
}

func (x *xrayProc) untrack(key any) {
	if x == nil || x.tag == "" {
		return
	}
	x.mu.Lock()
	delete(x.conns, key)
	x.mu.Unlock()
}

func (x *xrayProc) leave() {
	x.mu.Lock()
	if x.gone {
		x.mu.Unlock()
		return
	}
	x.gone = true
	cs := x.conns
	x.conns = nil
	x.mu.Unlock()
	for _, close := range cs {
		close()
	}
	hub.release(x.tag, x.parent)
}

func (x *xrayProc) tailHas(sub string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, l := range x.tail {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}
