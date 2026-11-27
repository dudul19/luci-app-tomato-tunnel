package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
)

const XrayDefaultPayload = "GET / HTTP/1.1[crlf]Host: [bug][crlf]Connection: Keep-Alive[crlf]User-Agent: [ua][crlf][crlf]"

func xpayProfiles(pr config.Profile, fwd int) (inj, xr config.Profile) {
	inj = pr
	inj.UsePayload, inj.UseDNS = true, false
	inj.UseTLS = pr.XSec == "tls" && pr.XPayPos != "before"
	inj.TLSName = pr.SNI
	if strings.TrimSpace(inj.Payload) == "" {
		inj.Payload = XrayDefaultPayload
	}

	xr = pr
	if xr.XHost == "" && net.ParseIP(pr.Host) == nil {
		xr.XHost = pr.Host
	}
	if pr.XSec == "tls" {
		if inj.UseTLS {
			xr.XSec = "none"
		} else if xr.SNI == "" {
			xr.SNI = pr.Bug
			if xr.SNI == "" && net.ParseIP(pr.Host) == nil {
				xr.SNI = pr.Host
			}
		}
	}
	xr.Host, xr.Port, xr.XFlow = "127.0.0.1", fwd, ""
	xr.XPay, xr.Enhanced, xr.Proxy = false, false, ""
	return inj, xr
}

func xpayUntil101(pr config.Profile) bool {
	if pr.XNet != "ws" && pr.XNet != "httpupgrade" {
		return false
	}
	return !(pr.XSec == "tls" && pr.XPayPos == "before")
}

func (p *Pool) payloadForward(ctx context.Context, pr config.Profile) (config.Profile, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return pr, err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		ln.Close()
		return pr, ErrNoSession
	}
	p.dnsLn = ln
	p.mu.Unlock()
	port := ln.Addr().(*net.TCPAddr).Port
	inj, xr := xpayProfiles(pr, port)
	d := &Dialer{P: inj, Iface: p.dialer.Iface, Log: p.Log, Raw: true}
	until101 := xpayUntil101(pr)
	where := "inside TLS"
	switch {
	case pr.XSec != "tls":
		where = "plain"
	case !inj.UseTLS:
		where = "before TLS"
	}
	enh := ""
	if pr.Enhanced {
		enh = ", Enhanced"
	}
	p.Log.Infof("payload", "Xray payload injector on 127.0.0.1:%d → %s:%d (payload %s%s)", port, pr.Host, pr.Port, where, enh)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.payRelay(d, c, until101)
		}
	}()
	return xr, nil
}

func (p *Pool) payRelay(d *Dialer, c net.Conn, until101 bool) {
	st, info, err := d.Dial(p.ctx)
	if err != nil {
		c.Close()
		if d.quiet.Load() {
			p.Log.Debugf("payload", "Xray payload connection: %v", err)
		} else {
			p.Log.Warnf("payload", "Xray payload connection: %v", err)
		}
		return
	}
	var r io.Reader = st
	if d.P.Enhanced {
		r = &xpayFilter{c: st, br: bufio.NewReaderSize(st, 16*1024), until101: until101, reqs: payloadShape(info.Sent), d: d}
	} else {
		d.Quiet()
	}
	relayPair(c, st, r, p.dead)
}

func relayPair(a, b net.Conn, rb io.Reader, dead <-chan struct{}) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(b, a)
		if cw, ok := b.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		io.Copy(a, rb)
		if cw, ok := a.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	select {
	case <-done:
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		case <-dead:
		}
	case <-dead:
	}
	a.Close()
	b.Close()
}

var reReqLine = regexp.MustCompile(`^([A-Z]{3,10}) \S+ HTTP/1\.[01]$`)

type payReq struct {
	method  string
	upgrade bool
}

func payloadShape(sent []byte) []payReq {
	var rs []payReq
	for _, l := range strings.Split(string(sent), "\n") {
		l = strings.TrimRight(l, "\r")
		if m := reReqLine.FindStringSubmatch(l); m != nil {
			rs = append(rs, payReq{method: m[1]})
		} else if len(rs) > 0 && len(l) > 8 && strings.EqualFold(l[:8], "upgrade:") {
			rs[len(rs)-1].upgrade = true
		}
	}
	return rs
}

var errXPay = errors.New("Xray payload: no answer to Xray's request (payload rejected?)")

type xpayFilter struct {
	c        net.Conn
	br       *bufio.Reader
	until101 bool
	reqs     []payReq
	d        *Dialer
	done     bool
	pre      []byte
	replies  []string
}

func (f *xpayFilter) Read(b []byte) (int, error) {
	if !f.done {
		f.done = true
		if err := f.prepare(); err != nil {
			if len(f.replies) > 0 {
				err = fmt.Errorf("%v · server replied %s", err, strings.Join(f.replies, " → "))
			}
			f.d.infofWarn("payload", "Xray payload: %v", err)
			return 0, err
		}
		f.d.Quiet()
	}
	if len(f.pre) > 0 {
		n := copy(b, f.pre)
		f.pre = f.pre[n:]
		return n, nil
	}
	return f.br.Read(b)
}

func (f *xpayFilter) prepare() error {
	idx, skipped := 0, 0
	for {
		if !f.until101 && idx >= len(f.reqs) {
			return nil
		}
		head, err := f.br.Peek(5)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errXPay
			}
			return err
		}
		if !bytes.Equal(head, []byte("HTTP/")) {
			if !f.until101 {
				return nil
			}
			line, err := f.br.ReadSlice('\n')
			if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
				return err
			}
			if skipped += len(line); skipped > maxSkip {
				return errXPay
			}
			continue
		}
		hdr, st, err := readHTTPHead(f.br)
		if err != nil {
			return err
		}
		req := payReq{method: "GET"}
		if idx < len(f.reqs) {
			req = f.reqs[idx]
		}
		idx++
		if f.until101 && st.code == 101 {
			if !req.upgrade {
				f.pre = hdr
				f.d.infof("payload", "Xray upgrade answered: %s", st.line)
				return nil
			}

			f.c.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
			nb, perr := f.br.Peek(1)
			f.c.SetReadDeadline(time.Time{})
			var ne net.Error
			if perr != nil && errors.As(perr, &ne) && ne.Timeout() {
				f.pre = hdr
				f.d.infof("payload", "Xray upgrade answered: %s", st.line)
				return nil
			}
			if perr != nil {
				return perr
			}
			if nb[0] != 'H' {
				f.pre = hdr
				return nil
			}
		} else if req.method != "HEAD" && st.code >= 200 && st.code != 204 && st.code != 304 {
			if err := skipBody(f.br, st); err != nil {
				return err
			}
		}
		f.replies = append(f.replies, st.line)
		f.d.infof("payload", "Enhanced: skipped %q", st.line)
		if skipped += len(hdr); skipped > maxSkip {
			return errXPay
		}
	}
}

func (d *Dialer) infofWarn(cat, f string, a ...any) {
	if d.quiet.Load() {
		d.Log.Debugf(cat, f, a...)
	} else {
		d.Log.Warnf(cat, f, a...)
	}
}

type httpStatus struct {
	line    string
	code    int
	clen    int64
	chunked bool
}

func readHTTPHead(br *bufio.Reader) ([]byte, httpStatus, error) {
	var hdr []byte
	st := httpStatus{clen: -1}
	for {
		line, err := br.ReadString('\n')
		hdr = append(hdr, line...)
		if err != nil {
			return hdr, st, err
		}
		if len(hdr) > maxSkip {
			return hdr, st, errXPay
		}
		t := strings.TrimRight(line, "\r\n")
		if st.line == "" {
			st.line = t
			if f := strings.Fields(t); len(f) >= 2 {
				st.code, _ = strconv.Atoi(f[1])
			}
			continue
		}
		if t == "" {
			return hdr, st, nil
		}
		k, v, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "content-length":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
				st.clen = n
			}
		case "transfer-encoding":
			st.chunked = strings.Contains(strings.ToLower(v), "chunked")
		}
	}
}

const maxBody = 1 << 20

func skipBody(br *bufio.Reader, st httpStatus) error {
	if st.chunked {
		total := 0
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return err
			}
			sz, _, _ := strings.Cut(strings.TrimSpace(line), ";")
			n, err := strconv.ParseInt(sz, 16, 64)
			if err != nil || n < 0 {
				return fmt.Errorf("Xray payload: bad chunk in a reply")
			}
			if n == 0 {
				for {
					l, err := br.ReadString('\n')
					if err != nil {
						return err
					}
					if strings.TrimRight(l, "\r\n") == "" {
						return nil
					}
				}
			}
			if total += int(n); total > maxBody {
				return errXPay
			}
			if _, err := br.Discard(int(n)); err != nil {
				return err
			}
			if _, err := br.ReadString('\n'); err != nil {
				return err
			}
		}
	}
	if st.clen > 0 {
		if st.clen > maxBody {
			return errXPay
		}
		_, err := br.Discard(int(st.clen))
		return err
	}
	return nil
}
