package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/logx"
	"github.com/dudul19/tomato-tunnel/internal/proxy"
	"github.com/dudul19/tomato-tunnel/internal/tunnel"
	"github.com/dudul19/tomato-tunnel/internal/update"
)

var (
	RunDir     = envOr("TT_RUNDIR", "/var/run/tomato-tunnel")
	StatusFile = RunDir + "/status.json"
	LogFile    = RunDir + "/tunnel.log"
	NetScript  = envOr("TT_NETSCRIPT", "/usr/libexec/tomato-tunnel/net.sh")
)

const TunName = "tt0"

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var Version = "dev"

type Status struct {
	State       string `json:"state"`
	Message     string `json:"message,omitempty"`
	Profile     string `json:"profile"`
	ProfileName string `json:"profile_name"`
	Mode        string `json:"mode"`
	Enhanced    bool   `json:"enhanced"`
	NetMode     string `json:"net_mode"`
	Iface       string `json:"iface,omitempty"`
	Since       int64  `json:"since,omitempty"`
	PublicIP    string `json:"public_ip,omitempty"`
	Country     string `json:"country,omitempty"`
	ISP         string `json:"isp,omitempty"`
	PingMS      int64  `json:"ping_ms"`
	RX          int64  `json:"rx"`
	TX          int64  `json:"tx"`
	RateRX      int64  `json:"rate_rx"`
	RateTX      int64  `json:"rate_tx"`
	Sessions    int    `json:"sessions"`
	Reconnects  int    `json:"reconnects_today"`
	Banner      string `json:"banner,omitempty"`
	ServerMsg   string `json:"server_msg,omitempty"`
	Kex         string `json:"kex,omitempty"`
	Legacy      bool   `json:"legacy"`
	Version     string `json:"version"`
	Updated     int64  `json:"updated"`
	PID         int    `json:"pid"`
}

type Daemon struct {
	cfg  *config.Config
	log  *logx.Logger
	pool atomic.Pointer[tunnel.Pool]

	mu     sync.Mutex
	wmu    sync.Mutex
	st     Status
	rxBase int64
	txBase int64
	day    int

	netUp    bool
	hev      *exec.Cmd
	hevDone  chan struct{}
	hevFails int
	svcs     []interface{ Close() }
}

func (d *Daemon) Dial(addr string) (net.Conn, error) {
	p := d.pool.Load()
	if p == nil {
		return nil, tunnel.ErrNoSession
	}
	return p.Dial(addr)
}

func (d *Daemon) UDPGWPort() int {
	if p := d.pool.Load(); p != nil {
		return p.P.UDPGW
	}
	return 0
}

func Run(cfgPath string, foreground bool) error {
	os.MkdirAll(RunDir, 0700)
	log := logx.New(LogFile, 1000, foreground)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Errorf("config", "%v", err)
		return err
	}
	log.SetLevel(cfg.Main.LogLevel)
	d := &Daemon{cfg: cfg, log: log}
	d.st = Status{State: "starting", NetMode: cfg.Main.NetMode, Version: Version, PID: os.Getpid()}
	d.writeStatus()
	log.Infof("start", "Starting Tomato Tunnel %s", Version)

	if len(cfg.Profiles) == 0 {
		d.set(func(s *Status) { s.State, s.Message = "failed", "no profile configured" })
		log.Errorf("config", "No profile configured")
		return fmt.Errorf("no profile")
	}

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)
	go func() {
		for s := range sig {
			if s == syscall.SIGUSR1 {
				log.Clear()
				continue
			}
			log.Infof("stop", "Stopping")
			cancel()
			return
		}
	}()

	if err := d.startServices(); err != nil {
		log.Errorf("start", "%v", err)
		d.set(func(s *Status) { s.State, s.Message = "failed", err.Error() })
		cancel()
		return err
	}
	go d.statusLoop(ctx)
	if cfg.Main.UpdateCheck {
		go d.updateLoop(ctx)
	}

	d.supervise(ctx)

	d.shutdown()
	return nil
}

func (d *Daemon) startServices() error {
	m := d.cfg.Main
	socks := &proxy.Socks{Addr: net.JoinHostPort(m.SocksListen, strconv.Itoa(m.SocksPort)), Up: d, Log: d.log}
	if err := socks.Start(); err != nil {
		return fmt.Errorf("SOCKS5 %s: %v", socks.Addr, err)
	}
	d.svcs = append(d.svcs, socks)
	d.log.Debugf("socks", "SOCKS5 listening on %s", socks.Addr)
	if m.NetMode == "redirect" {
		r := &proxy.Redir{Addr: net.JoinHostPort("0.0.0.0", strconv.Itoa(m.RedirPort)), Up: d, Log: d.log}
		if err := r.Start(); err != nil {
			return fmt.Errorf("redirect port %d: %v", m.RedirPort, err)
		}
		d.svcs = append(d.svcs, r)
	}
	if m.DNSMode == "tunnel" {
		dns := &proxy.DNS{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(m.DNSPort)), Server: net.JoinHostPort(m.DNSServer, "53"), Up: d, Log: d.log}
		if err := dns.Start(); err != nil {
			return fmt.Errorf("DNS port %d: %v", m.DNSPort, err)
		}
		d.svcs = append(d.svcs, dns)
	}
	return nil
}

func (d *Daemon) supervise(ctx context.Context) {
	m := d.cfg.Main
	profiles := d.cfg.Ordered()
	if !m.Failover {
		profiles = profiles[:1]
	}
	pi := 0
	fails := 0
	rounds := 0
	first := true
	for ctx.Err() == nil {
		p := profiles[pi%len(profiles)]
		if err := p.Validate(); err != nil {
			d.log.Errorf("config", "%v", err)
			d.set(func(s *Status) { s.State, s.Message = "failed", err.Error() })
			pi++
			if pi%len(profiles) == 0 {
				sleepCtx(ctx, 30*time.Second)
			}
			continue
		}
		state := "connecting"
		if !first {
			state = "reconnecting"
		}
		d.set(func(s *Status) {
			s.State, s.Message = state, ""
			s.Profile, s.ProfileName, s.Mode, s.Enhanced = p.ID, p.Name, p.Mode, p.Enhanced
			s.PublicIP, s.Country, s.ISP, s.PingMS = "", "", "", 0
		})

		pool := tunnel.NewPool(p, m.SSHCompat, m.WanIface, d.log)
		cctx, ccancel := context.WithTimeout(ctx, 60*time.Second)
		err := pool.Connect(cctx, m.Sessions)
		ccancel()
		if err != nil {
			pool.Close()
			if ctx.Err() != nil {
				return
			}
			d.log.Errorf("connect", "Connection failed: %v", err)
			d.set(func(s *Status) { s.Message = err.Error() })
			fails++
			if fails >= 3 {
				fails = 0
				pi++
				if pi%len(profiles) == 0 {
					rounds++
					if m.RestartModem && m.ModemIface != "" {
						d.log.Warnf("modem", "Every profile failed, restarting modem %s", m.ModemIface)
						exec.Command("ifup", m.ModemIface).Run()
						sleepCtx(ctx, 20*time.Second)
					}
				}
				if len(profiles) > 1 {
					d.log.Warnf("failover", "Switching to profile %s", profiles[pi%len(profiles)].Name)
				}
			}
			backoff := time.Duration(2<<min(fails+rounds, 4)) * time.Second
			sleepCtx(ctx, backoff)
			continue
		}
		fails, rounds = 0, 0
		d.pool.Store(pool)
		d.mu.Lock()
		d.rxBase, d.txBase = 0, 0
		if !first {
			d.countReconnect()
		}
		d.st.State, d.st.Message = "connected", ""
		d.st.Since = time.Now().Unix()
		d.st.Banner, d.st.Kex, d.st.Legacy = pool.Info.Banner, pool.Info.Kex, pool.Info.Legacy
		d.st.ServerMsg = pool.Info.ServerMsg
		d.mu.Unlock()
		first = false

		if !d.netUp {
			if err := d.netSetup(); err != nil {
				d.log.Errorf("net", "Network setup failed: %v", err)
			}
		}
		go d.fetchIP(pool)

		reason := d.monitor(ctx, pool)
		d.pool.Store(nil)
		pool.Close()
		if ctx.Err() != nil {
			return
		}
		d.log.Warnf("reconnect", "Reconnecting (%s)", reason)
		d.set(func(s *Status) { s.State, s.Message = "reconnecting", reason })
	}
}

func (d *Daemon) monitor(ctx context.Context, pool *tunnel.Pool) string {
	m := d.cfg.Main
	t := time.NewTicker(time.Duration(m.CheckInterval) * time.Second)
	defer t.Stop()
	failed := 0
	host := m.CheckURL
	if u, err := url.Parse(m.CheckURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	stats := time.NewTicker(5 * time.Minute)
	defer stats.Stop()
	for {
		select {
		case <-stats.C:
			d.mu.Lock()
			st := d.st
			d.mu.Unlock()
			d.log.Infof("stats", "Traffic: download %s, upload %s", human(st.RX), human(st.TX))
		case <-ctx.Done():
			return "stopped"
		case <-pool.Dead():
			return "all SSH sessions lost"
		case <-t.C:
			d.ensureHev()
			ms, err := check(pool, m.CheckURL)
			if err != nil {
				failed++
				d.log.Warnf("ping", "Ping %s failed: %s (%d/%d)", host, shortErr(err), failed, m.CheckFail)
				if failed >= m.CheckFail {
					return fmt.Sprintf("check failed %d×", failed)
				}
				continue
			}
			d.log.Okf("ping", "Ping %s %d ms OK", host, ms)
			failed = 0
			d.set(func(s *Status) { s.PingMS = ms })
		}
	}
}

func check(pool *tunnel.Pool, url string) (int64, error) {
	tr := &http.Transport{
		DialContext:       func(ctx context.Context, _, addr string) (net.Conn, error) { return pool.Dial(addr) },
		DisableKeepAlives: true,
	}
	cl := &http.Client{Transport: tr, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := time.Now()
	resp, err := cl.Get(url)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return time.Since(start).Milliseconds(), nil
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i > 0 && strings.HasPrefix(s, "Get ") {
		s = s[i+2:]
	}
	return s
}

func (d *Daemon) fetchIP(pool *tunnel.Pool) {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return pool.Dial(addr) }}
	cl := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	resp, err := cl.Get("http://ip-api.com/json/?fields=query,country,isp,as")
	if err != nil {
		d.log.Debugf("ip", "%v", err)
		return
	}
	defer resp.Body.Close()
	var r struct{ Query, Country, ISP, AS string }
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&r) == nil && r.Query != "" {
		isp := r.AS
		if isp == "" {
			isp = r.ISP
		}
		d.set(func(s *Status) { s.PublicIP, s.Country, s.ISP = r.Query, r.Country, isp })
		d.log.Okf("ip", "Public IP %s (%s), %s", r.Query, r.Country, isp)
	}
}

func (d *Daemon) countReconnect() {
	today := time.Now().YearDay()
	if d.day != today {
		d.day, d.st.Reconnects = today, 0
	}
	d.st.Reconnects++
}

func (d *Daemon) netSetup() error {
	m := d.cfg.Main
	if m.NetMode == "tun" {
		if err := d.startHev(); err != nil {
			return err
		}
	}
	env := append(os.Environ(),
		"NET_MODE="+m.NetMode, "TUN="+TunName, "LAN="+m.LanIface,
		"REDIR_PORT="+strconv.Itoa(m.RedirPort), "DNS_PORT="+strconv.Itoa(m.DNSPort),
		"DNS_MODE="+m.DNSMode, "MANAGE_DNSMASQ="+b2s(m.ManageDnsmasq),
		"BYPASS="+strings.Join(m.Bypass, " "))
	cmd := exec.Command(NetScript, "up")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if s := strings.TrimSpace(string(out)); s != "" {
		d.log.Debugf("net", "%s", s)
	}
	if err != nil {
		return fmt.Errorf("net.sh up: %v", err)
	}
	d.netUp = true
	dns := "DNS from the ISP"
	if m.DNSMode == "tunnel" {
		dns = "DNS through the tunnel (" + m.DNSServer + ")"
	}
	switch m.NetMode {
	case "tun":
		d.log.Okf("ready", "Connected. All LAN traffic goes through the tunnel, %s", dns)
	case "redirect":
		d.log.Okf("ready", "Connected. LAN TCP goes through the tunnel, %s", dns)
	default:
		d.log.Okf("ready", "Connected. SOCKS5 proxy ready on %s:%d", m.SocksListen, m.SocksPort)
	}
	return nil
}

func (d *Daemon) startHev() error {
	m := d.cfg.Main
	bin, err := exec.LookPath("hev-socks5-tunnel")
	if err != nil {
		return fmt.Errorf("hev-socks5-tunnel is not installed (needed for TUN mode)")
	}
	conf := filepath.Join(RunDir, "hev.yml")
	host := m.SocksListen
	if host == "0.0.0.0" || host == "" {
		host = "127.0.0.1"
	}
	yml := fmt.Sprintf(`tunnel:
  name: %s
  mtu: 8500
  multi-queue: false
  ipv4: 172.29.255.1
  ipv6: 'fd7a:7454::1'
socks5:
  port: %d
  address: %s
  udp: 'udp'
misc:
  log-level: warn
  limit-nofile: 65535
`, TunName, m.SocksPort, host)
	if err := os.WriteFile(conf, []byte(yml), 0600); err != nil {
		return err
	}
	killStaleHev(conf)
	for i := 0; i < 30; i++ {
		if _, err := net.InterfaceByName(TunName); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cmd := exec.Command(bin, conf)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = hevProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	d.hev = cmd
	done := make(chan struct{})
	d.hevDone = done
	go func() { cmd.Wait(); close(done) }()
	for i := 0; i < 30; i++ {
		select {
		case <-done:
			msg := strings.TrimSpace(out.String())
			if msg == "" {
				msg = "exited right away"
			}
			return fmt.Errorf("TUN helper failed: %s", msg)
		default:
		}
		if _, err := net.InterfaceByName(TunName); err == nil {
			d.log.Infof("tun", "TUN interface %s up", TunName)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("%s did not appear", TunName)
}

func killStaleHev(conf string) {
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
		if len(args) >= 2 && strings.HasSuffix(args[0], "hev-socks5-tunnel") && args[1] == conf {
			if p, err := os.FindProcess(pid); err == nil {
				p.Signal(syscall.SIGTERM)
			}
		}
	}
}

func (d *Daemon) ensureHev() {
	if d.cfg.Main.NetMode != "tun" || !d.netUp || d.hev == nil {
		return
	}
	select {
	case <-d.hevDone:
	default:
		return
	}
	if d.hevFails >= 3 {
		return
	}
	d.log.Warnf("tun", "TUN helper exited, restarting it")
	if err := d.startHev(); err != nil {
		d.hevFails++
		d.log.Errorf("tun", "%v", err)
		return
	}
	d.hevFails = 0
	exec.Command(NetScript, "routes").Run()
}

func (d *Daemon) shutdown() {
	d.set(func(s *Status) { s.State = "stopped" })
	if p := d.pool.Swap(nil); p != nil {
		p.Close()
	}
	if d.netUp {
		m := d.cfg.Main
		cmd := exec.Command(NetScript, "down")
		cmd.Env = append(os.Environ(), "NET_MODE="+m.NetMode, "TUN="+TunName, "LAN="+m.LanIface,
			"MANAGE_DNSMASQ="+b2s(m.ManageDnsmasq), "DNS_MODE="+m.DNSMode)
		cmd.Run()
	}
	if d.hev != nil && d.hev.Process != nil {
		d.hev.Process.Signal(syscall.SIGTERM)
	}
	for _, s := range d.svcs {
		s.Close()
	}
	d.log.Infof("stop", "Disconnected")
	d.writeStatus()
}

func (d *Daemon) set(f func(*Status)) {
	d.mu.Lock()
	f(&d.st)
	d.mu.Unlock()
	d.writeStatus()
}

func (d *Daemon) statusLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		p := d.pool.Load()
		d.mu.Lock()
		if p != nil {
			rx, tx := p.RX.Load(), p.TX.Load()
			d.st.RateRX, d.st.RateTX = rx-d.rxBase, tx-d.txBase
			d.st.RX += d.st.RateRX
			d.st.TX += d.st.RateTX
			d.rxBase, d.txBase = rx, tx
			d.st.Sessions = p.Sessions()
		} else {
			d.st.RateRX, d.st.RateTX, d.st.Sessions = 0, 0, 0
		}
		d.mu.Unlock()
		d.writeStatus()
	}
}

func (d *Daemon) writeStatus() {
	d.mu.Lock()
	d.st.Updated = time.Now().Unix()
	b, _ := json.Marshal(d.st)
	d.mu.Unlock()
	d.wmu.Lock()
	defer d.wmu.Unlock()
	tmp := StatusFile + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		os.Rename(tmp, StatusFile)
	}
}

func (d *Daemon) updateLoop(ctx context.Context) {
	sleepCtx(ctx, 2*time.Minute)
	for ctx.Err() == nil {
		if d.pool.Load() != nil {
			if r, err := update.Check(d.tunnelHTTP(), d.cfg.Main.UpdateRepo, d.cfg.Main.UpdateChannel, Version); err == nil {
				update.Save(r)
				if r.Available {
					d.log.Infof("update", "Update v%s is available", r.Latest)
				}
			} else {
				d.log.Debugf("update", "%v", err)
			}
		}
		sleepCtx(ctx, 24*time.Hour)
	}
}

func (d *Daemon) tunnelHTTP() *http.Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return d.Dial(addr) },
		TLSHandshakeTimeout: 20 * time.Second}
	return &http.Client{Transport: tr, Timeout: 5 * time.Minute}
}

func human(n int64) string {
	f := float64(n)
	for _, u := range []string{"B", "KB", "MB", "GB"} {
		if f < 1024 || u == "GB" {
			if u == "B" {
				return fmt.Sprintf("%d B", n)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return ""
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func b2s(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
