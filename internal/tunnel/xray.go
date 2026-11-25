package tunnel

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
)

type XrayOpts struct {
	DirectDomains []string
	BlockAds      bool
	BlockQUIC     bool // UDP 443 to blackhole, so clients fall back to TCP
	CheckURL      string
	RunDir        string
}

var XrayPaths = []string{"/usr/libexec/tomato-tunnel/xray"}

func XrayBin() string {
	for _, p := range XrayPaths {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0111 != 0 {
			return p
		}
	}
	return ""
}

var ErrNoXray = errors.New("the Xray add-on is not installed. Install it in Settings › About & update › Xray add-on (a system xray-core is not used)")

var adDomains = []string{
	"doubleclick.net", "googlesyndication.com", "googleadservices.com", "google-analytics.com",
	"adservice.google.com", "pagead2.googlesyndication.com", "admob.com", "app-measurement.com",
	"applovin.com", "applvn.com", "unityads.unity3d.com", "adcolony.com", "vungle.com", "inmobi.com",
	"chartboost.com", "mopub.com", "taboola.com", "outbrain.com", "criteo.com", "criteo.net",
	"adnxs.com", "moatads.com", "scorecardresearch.com", "ads-api.tiktok.com", "analytics.tiktok.com",
	"ads.youtube.com", "adsafeprotected.com", "pubmatic.com", "rubiconproject.com", "openx.net",
	"smaato.net", "startappservice.com", "ironsrc.mobi", "supersonicads.com", "yandexadexchange.net",
}

// xrayLive holds the PID of every Xray this process started and that has not
// exited yet, so the daemon can tell its own running pools from leftovers.
var xrayLive sync.Map

// XrayLive reports whether pid is an Xray started by this process and still owned by a pool
// (or by a connect in progress).
func XrayLive(pid int) bool { _, ok := xrayLive.Load(pid); return ok }

// IdleTCP is how long an idle TCP connection may stay open through Xray and the TUN.
const IdleTCP = time.Hour

type xrayProc struct {
	cmd    *exec.Cmd
	port   int
	conf   string
	done   chan struct{}
	mu     sync.Mutex
	tail   []string
	exited bool
}

func (x *xrayProc) addLine(l string) {
	x.mu.Lock()
	x.tail = append(x.tail, l)
	if len(x.tail) > 12 {
		x.tail = x.tail[len(x.tail)-12:]
	}
	x.mu.Unlock()
}

func (x *xrayProc) lastErr() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	for i := len(x.tail) - 1; i >= 0; i-- {
		l := x.tail[i]
		if strings.Contains(l, " started") || strings.Contains(l, "deprecated") || strings.Contains(l, "will be removed") {
			continue
		}
		if strings.Contains(l, "[Error]") || strings.Contains(l, "[Warning]") || strings.Contains(l, "failed") || strings.Contains(l, "config:") || strings.Contains(l, "start:") {
			return cleanXrayLine(l)
		}
	}
	return ""
}

func cleanXrayLine(l string) string {
	if i := strings.Index(l, "] "); i > 0 && i < 40 {
		l = l[i+2:]
	}
	if i := strings.Index(l, "] "); i > 0 && i < 30 && strings.HasPrefix(l, "[") {
		l = l[i+2:]
	}
	if len(l) > 220 {
		l = l[:220] + "…"
	}
	return l
}

func (x *xrayProc) stop() {
	if x == nil || x.cmd == nil || x.cmd.Process == nil {
		return
	}
	x.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-x.done:
	case <-time.After(3 * time.Second):
		x.cmd.Process.Kill()
		<-x.done
	}
	if x.conf != "" {
		os.Remove(x.conf)
	}
}

func publicIP(h string) bool {
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	return !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
}

func XrayConfig(p config.Profile, port int, iface, serverIP string, o XrayOpts) ([]byte, error) {
	addr := p.Host
	hosts := map[string]any{}
	if net.ParseIP(addr) == nil {
		if serverIP != "" {
			hosts[addr] = serverIP
		}
	} else if p.XProto == "vless" && p.XSec == "none" && publicIP(addr) {
		alias := "tt-" + strings.NewReplacer(".", "-", ":", "-").Replace(addr) + ".internal"
		hosts[alias] = addr
		addr = alias
	}

	var settings map[string]any
	switch p.XProto {
	case "vless":
		u := map[string]any{"id": p.UUID, "encryption": "none"}
		if p.XFlow != "" {
			u["flow"] = p.XFlow
		}
		settings = map[string]any{"vnext": []any{map[string]any{"address": addr, "port": p.Port, "users": []any{u}}}}
	case "vmess":
		settings = map[string]any{"vnext": []any{map[string]any{"address": addr, "port": p.Port,
			"users": []any{map[string]any{"id": p.UUID, "security": "auto"}}}}}
	case "trojan":
		settings = map[string]any{"servers": []any{map[string]any{"address": addr, "port": p.Port, "password": p.Pass}}}
	default:
		return nil, fmt.Errorf("unknown Xray protocol %q", p.XProto)
	}

	hostHdr := p.XHost
	sni := p.SNI
	if sni == "" && p.XSec == "tls" {
		sni = hostHdr
		if sni == "" && net.ParseIP(p.Host) == nil {
			sni = p.Host
		}
	}
	ss := map[string]any{"network": p.XNet, "security": p.XSec}
	path := p.XPath
	if path == "" {
		path = "/"
	}
	switch p.XNet {
	case "ws":
		// a WebSocket ping every 30 s: bug hosts and CDNs in front of the server
		// close a WebSocket that is quiet for about 90 s, which cut push channels
		// (FCM, WhatsApp) long before their next heartbeat
		ws := map[string]any{"path": path, "heartbeatPeriod": 30}
		if hostHdr != "" {
			ws["host"] = hostHdr
		}
		ss["wsSettings"] = ws
	case "httpupgrade":
		hu := map[string]any{"path": path}
		if hostHdr != "" {
			hu["host"] = hostHdr
		}
		ss["httpupgradeSettings"] = hu
	case "grpc":
		ss["grpcSettings"] = map[string]any{"serviceName": p.XService}
	case "xhttp":
		xh := map[string]any{"path": path, "mode": p.XMode}
		if hostHdr != "" {
			xh["host"] = hostHdr
		}
		ss["xhttpSettings"] = xh
	}
	switch p.XSec {
	case "tls":
		t := map[string]any{"fingerprint": p.FP, "allowInsecure": p.Insecure}
		if sni != "" {
			t["serverName"] = sni
		}
		if p.PCS != "" {
			t["pinnedPeerCertSha256"] = p.PCS
		}
		if p.VCN != "" {
			t["verifyPeerCertByName"] = p.VCN
		}
		if p.XNet == "ws" || p.XNet == "httpupgrade" {
			t["alpn"] = []string{"http/1.1"}
		}
		ss["tlsSettings"] = t
	case "reality":
		r := map[string]any{"serverName": sni, "fingerprint": p.FP, "publicKey": p.PBK, "shortId": p.SID}
		if p.SpiderX != "" {
			r["spiderX"] = p.SpiderX
		}
		ss["realitySettings"] = r
	}
	sock := map[string]any{}
	if iface != "" {
		sock["interface"] = iface
	}
	if len(hosts) > 0 {
		sock["domainStrategy"] = "UseIPv4"
	}
	if len(sock) > 0 {
		ss["sockopt"] = sock
	}

	proxyOut := map[string]any{"tag": "proxy", "protocol": p.XProto, "settings": settings, "streamSettings": ss}
	direct := map[string]any{"tag": "direct", "protocol": "freedom"}
	if iface != "" {
		direct["streamSettings"] = map[string]any{"sockopt": map[string]any{"interface": iface}}
	}
	outs := []any{proxyOut, direct, map[string]any{"tag": "block", "protocol": "blackhole"}}

	var rules []any
	if len(o.DirectDomains) > 0 {
		var ds []string
		for _, d := range o.DirectDomains {
			ds = append(ds, "domain:"+d)
		}
		rules = append(rules, map[string]any{"type": "field", "domain": ds, "outboundTag": "direct"})
	}
	if o.BlockQUIC {
		rules = append(rules, map[string]any{"type": "field", "network": "udp", "port": "443", "outboundTag": "block"})
	}
	if o.BlockAds {
		var ds []string
		for _, d := range adDomains {
			ds = append(ds, "domain:"+d)
		}
		rules = append(rules, map[string]any{"type": "field", "domain": ds, "outboundTag": "block"})
	}

	in := map[string]any{"tag": "in", "listen": "127.0.0.1", "port": port, "protocol": "socks",
		"settings": map[string]any{"auth": "noauth", "udp": true, "ip": "127.0.0.1"}}
	if len(rules) > 0 {
		in["sniffing"] = map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}
	}
	cfg := map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{in},
		"outbounds": outs,
		// Xray closes a connection idle for 300 s by default, which cuts push
		// channels (FCM, WhatsApp) between their heartbeats.
		"policy": map[string]any{"levels": map[string]any{"0": map[string]any{"connIdle": int(IdleTCP.Seconds())}}},
	}
	if len(hosts) > 0 {
		cfg["dns"] = map[string]any{"hosts": hosts, "servers": []string{"localhost"}}
	}
	if len(rules) > 0 {
		cfg["routing"] = map[string]any{"domainStrategy": "AsIs", "rules": rules}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

func (p *Pool) IsXray() bool { return p.P.Type == "xray" }

func (p *Pool) connectXray(ctx context.Context) error {
	pr := p.P
	bin := XrayBin()
	if bin == "" {
		return ErrNoXray
	}
	p.Log.Infof("connect", "Profile %s: %s", pr.Name, pr.Label())
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	serverIP := ""
	if net.ParseIP(pr.Host) == nil {
		ip, err := p.dialer.resolve(cctx, pr.Host)
		if err != nil {
			return fmt.Errorf("resolve %s: %v", pr.Host, err)
		}
		serverIP = ip
		p.Log.Infof("xray", "Server %s is %s", pr.Host, ip)
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	b, err := XrayConfig(pr, port, p.dialer.Iface, serverIP, p.X)
	if err != nil {
		return err
	}
	dir := p.X.RunDir
	if dir == "" {
		dir = os.TempDir()
	}
	os.MkdirAll(dir, 0700)
	conf := filepath.Join(dir, fmt.Sprintf("xray-%d.json", port))
	if err := os.WriteFile(conf, b, 0600); err != nil {
		return err
	}
	cmd := exec.Command(bin, "run", "-c", conf)
	cmd.SysProcAttr = xrayProcAttr()
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	x := &xrayProc{cmd: cmd, port: port, conf: conf, done: make(chan struct{})}
	t0 := time.Now()
	if err := cmd.Start(); err != nil {
		os.Remove(conf)
		return fmt.Errorf("start Xray: %v", err)
	}
	xrayLive.Store(cmd.Process.Pid, true)
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 64*1024)
		for sc.Scan() {
			l := sc.Text()
			x.addLine(l)
			p.Log.Debugf("xray", "%s", cleanXrayLine(l))
		}
	}()
	go func() {
		cmd.Wait()
		xrayLive.Delete(cmd.Process.Pid)
		x.mu.Lock()
		x.exited = true
		x.mu.Unlock()
		close(x.done)
	}()

	up := false
	for !up {
		select {
		case <-x.done:
			msg := x.lastErr()
			os.Remove(conf)
			if msg == "" {
				msg = "exited right away"
			}
			return fmt.Errorf("Xray: %s", msg)
		case <-cctx.Done():
			x.stop()
			return fmt.Errorf("Xray did not start in time")
		case <-time.After(100 * time.Millisecond):
		}
		if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond); err == nil {
			c.Close()
			up = true
		}
	}
	p.mu.Lock()
	closed := p.closed
	if !closed {
		p.xr = x
	}
	p.mu.Unlock()
	if closed {
		x.stop() // the pool was closed while Xray was starting: nobody would stop it later
		return ErrNoSession
	}
	p.Log.Infof("xray", "Xray started (%s), connecting to %s:%d", filepath.Base(bin), pr.Host, pr.Port)

	check := p.X.CheckURL
	if check == "" {
		check = "http://cp.cloudflare.com/generate_204"
	}
	var lastErr error
	for try := 0; try < 3 && cctx.Err() == nil; try++ {
		if lastErr = p.xrayCheck(cctx, check); lastErr == nil {
			break
		}
		select {
		case <-x.done:
			try = 3
		case <-cctx.Done():
		case <-time.After(time.Second):
		}
	}
	if lastErr != nil {
		why := x.lastErr()
		p.mu.Lock()
		p.xr = nil
		p.mu.Unlock()
		x.stop()
		if why != "" {
			return fmt.Errorf("Xray: no answer through the server (%s)", why)
		}
		return fmt.Errorf("Xray: no answer through the server. Check UUID/password, path, SNI and security (%v)", shortQUIC(lastErr))
	}
	os.Remove(conf)
	p.Log.Okf("xray", "%s connected in %d ms", pr.XrayLabel(), time.Since(t0).Milliseconds())
	p.Info = &HandshakeInfo{Banner: "Xray " + pr.XrayLabel(), Kex: pr.XSec}
	go func() {
		<-x.done
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if !closed {
			msg := x.lastErr()
			if msg == "" {
				msg = "process exited"
			}
			p.Log.Warnf("xray", "Xray stopped: %s", msg)
			p.kill()
		}
	}()
	return nil
}

func (p *Pool) xrayCheck(ctx context.Context, url string) error {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, a string) (net.Conn, error) { return p.dialXray(a) }, DisableKeepAlives: true}
	cl := &http.Client{Transport: tr, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return nil
}

func socksAddr(addr string) ([]byte, error) {
	host, ps, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(ps)
	if err != nil {
		return nil, err
	}
	var b []byte
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			b = append([]byte{1}, ip4...)
		} else {
			b = append([]byte{4}, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, fmt.Errorf("host too long")
		}
		b = append([]byte{3, byte(len(host))}, host...)
	}
	return binary.BigEndian.AppendUint16(b, uint16(port)), nil
}

func socksHandshake(c net.Conn, cmd byte, addr []byte) (*net.UDPAddr, error) {
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		return nil, err
	}
	r := make([]byte, 2)
	if _, err := io.ReadFull(c, r); err != nil {
		return nil, err
	}
	if r[0] != 5 || r[1] != 0 {
		return nil, fmt.Errorf("socks: bad auth reply")
	}
	if _, err := c.Write(append([]byte{5, cmd, 0}, addr...)); err != nil {
		return nil, err
	}
	h := make([]byte, 4)
	if _, err := io.ReadFull(c, h); err != nil {
		return nil, err
	}
	if h[1] != 0 {
		return nil, fmt.Errorf("Xray refused the connection (code %d)", h[1])
	}
	var ip net.IP
	switch h[3] {
	case 1:
		ip = make(net.IP, 4)
		if _, err := io.ReadFull(c, ip); err != nil {
			return nil, err
		}
	case 4:
		ip = make(net.IP, 16)
		if _, err := io.ReadFull(c, ip); err != nil {
			return nil, err
		}
	case 3:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return nil, err
		}
		n := make([]byte, l[0])
		if _, err := io.ReadFull(c, n); err != nil {
			return nil, err
		}
		ip = net.ParseIP(string(n))
	default:
		return nil, fmt.Errorf("socks: bad address type")
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return nil, err
	}
	return &net.UDPAddr{IP: ip, Port: int(binary.BigEndian.Uint16(pb))}, nil
}

func (p *Pool) XrayPort() int { return p.xrayPort() }

func (p *Pool) xrayPort() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.xr == nil {
		return 0
	}
	return p.xr.port
}

func (p *Pool) dialXray(addr string) (net.Conn, error) {
	port := p.xrayPort()
	if port == 0 {
		return nil, ErrNoSession
	}
	a, err := socksAddr(addr)
	if err != nil {
		return nil, err
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := socksHandshake(c, 1, a); err != nil {
		c.Close()
		return nil, err
	}
	c.SetDeadline(time.Time{})
	p.streams.Add(1)
	return &countConn{Conn: c, p: p}, nil
}

type XrayUDP struct {
	ctrl  net.Conn
	pc    *net.UDPConn
	relay *net.UDPAddr
	p     *Pool
	once  sync.Once
}

func (p *Pool) XrayUDP(onPacket func(src *net.UDPAddr, data []byte)) (*XrayUDP, error) {
	port := p.xrayPort()
	if port == 0 {
		return nil, ErrNoSession
	}
	ctrl, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return nil, err
	}
	ctrl.SetDeadline(time.Now().Add(10 * time.Second))
	relay, err := socksHandshake(ctrl, 3, []byte{1, 0, 0, 0, 0, 0, 0})
	if err != nil {
		ctrl.Close()
		return nil, err
	}
	ctrl.SetDeadline(time.Time{})
	if relay.IP == nil || relay.IP.IsUnspecified() {
		relay.IP = net.IPv4(127, 0, 0, 1)
	}
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		ctrl.Close()
		return nil, err
	}
	u := &XrayUDP{ctrl: ctrl, pc: pc, relay: relay, p: p}
	go func() {
		io.Copy(io.Discard, ctrl)
		u.Close()
	}()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n < 10 || buf[2] != 0 {
				continue
			}
			var src *net.UDPAddr
			off := 4
			switch buf[3] {
			case 1:
				src = &net.UDPAddr{IP: net.IP(append([]byte(nil), buf[4:8]...)), Port: int(binary.BigEndian.Uint16(buf[8:10]))}
				off = 10
			case 4:
				if n < 22 {
					continue
				}
				src = &net.UDPAddr{IP: net.IP(append([]byte(nil), buf[4:20]...)), Port: int(binary.BigEndian.Uint16(buf[20:22]))}
				off = 22
			case 3:
				l := int(buf[4])
				if n < 7+l {
					continue
				}
				ips, err := net.LookupIP(string(buf[5 : 5+l]))
				if err != nil || len(ips) == 0 {
					continue
				}
				src = &net.UDPAddr{IP: ips[0], Port: int(binary.BigEndian.Uint16(buf[5+l : 7+l]))}
				off = 7 + l
			default:
				continue
			}
			p.RX.Add(int64(n - off))
			onPacket(src, append([]byte(nil), buf[off:n]...))
		}
	}()
	return u, nil
}

func (u *XrayUDP) Send(dst *net.UDPAddr, data []byte) error {
	h := []byte{0, 0, 0}
	if ip4 := dst.IP.To4(); ip4 != nil {
		h = append(h, 1)
		h = append(h, ip4...)
	} else {
		h = append(h, 4)
		h = append(h, dst.IP.To16()...)
	}
	h = binary.BigEndian.AppendUint16(h, uint16(dst.Port))
	u.p.TX.Add(int64(len(data)))
	_, err := u.pc.WriteToUDP(append(h, data...), u.relay)
	return err
}

func (u *XrayUDP) Close() {
	u.once.Do(func() {
		u.ctrl.Close()
		u.pc.Close()
	})
}
