package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/dudul19/tomato-tunnel/internal/usage"
)

var (
	RunDir     = envOr("TT_RUNDIR", "/var/run/tomato-tunnel")
	StatusFile = RunDir + "/status.json"
	LogFile    = RunDir + "/tunnel.log"
	CmdFile    = RunDir + "/cmd"
	NetScript  = envOr("TT_NETSCRIPT", "/usr/libexec/tomato-tunnel/net.sh")
)

const TunName = "tt0"

const HistLen = 30

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var Version = "dev"

type Status struct {
	State         string           `json:"state"`
	Message       string           `json:"message,omitempty"`
	Profile       string           `json:"profile"`
	ProfileName   string           `json:"profile_name"`
	Mode          string           `json:"mode"`
	Enhanced      bool             `json:"enhanced"`
	NetMode       string           `json:"net_mode"`
	Iface         string           `json:"iface,omitempty"`
	Since         int64            `json:"since,omitempty"`
	PublicIP      string           `json:"public_ip,omitempty"`
	Country       string           `json:"country,omitempty"`
	ISP           string           `json:"isp,omitempty"`
	PingMS        int64            `json:"ping_ms"`
	RX            int64            `json:"rx"`
	TX            int64            `json:"tx"`
	RateRX        int64            `json:"rate_rx"`
	RateTX        int64            `json:"rate_tx"`
	Sessions      int              `json:"sessions"`
	Reconnects    int              `json:"reconnects_today"`
	Policy        string           `json:"policy"`
	Switching     string           `json:"switching,omitempty"`
	LoginFailed   map[string]int64 `json:"login_failed,omitempty"`
	Banner        string           `json:"banner,omitempty"`
	ServerMsg     string           `json:"server_msg,omitempty"`
	Kex           string           `json:"kex,omitempty"`
	Legacy        bool             `json:"legacy"`
	PeriodBytes   int64            `json:"period_bytes"`
	TodayBytes    int64            `json:"today_bytes"`
	QuotaGB       int              `json:"quota_gb"`
	ChangingIP    bool             `json:"changing_ip,omitempty"`
	LastIPChange  int64            `json:"last_ip_change,omitempty"`
	IPChangeMsg   string           `json:"ip_change_msg,omitempty"`
	IPChangeOld   string           `json:"ip_change_old,omitempty"`
	IPChangeModem string           `json:"ip_change_modem,omitempty"`
	Modems        []ModemStatus    `json:"modems,omitempty"`
	Balance       []BalanceStatus  `json:"balance,omitempty"`
	Hist          [][]int64        `json:"hist"`
	Version       string           `json:"version"`
	Updated       int64            `json:"updated"`
	PID           int              `json:"pid"`
}

type Daemon struct {
	cfg     *config.Config
	cfgPath string
	log     *logx.Logger
	pool    atomic.Pointer[tunnel.Pool]

	pol       atomic.Value
	switchCh  chan string
	autoKick  chan struct{}
	autoRes   chan LatencyReport
	measuring atomic.Bool
	swCancel  context.CancelFunc

	mu     sync.Mutex
	wmu    sync.Mutex
	st     Status
	rxBase int64
	txBase int64
	day    int

	netUp    bool
	hevPort  int
	direct   atomic.Bool
	tunRX    int64
	tunTX    int64
	hev      *exec.Cmd
	hevDone  chan struct{}
	hevFails int
	svcs     []interface{ Close() }

	use          *usage.Store
	ipBusy       atomic.Bool
	ipCh         chan string
	lastIPChange time.Time
	lastIP       map[string]time.Time
	bal          *balancer
	flows        flowTable
	mainBad      atomic.Bool
	failRun      int
}

func (d *Daemon) Dial(addr string) (net.Conn, error) {
	if h, _, err := net.SplitHostPort(addr); err == nil && d.directFor(h) {
		c, err := tunnel.DirectDialer(d.directIface()).Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		return d.track(c, addr, "Direct"), nil
	}
	p := d.pool.Load()
	if d.balancing() {
		if c, ok, err := d.balanceDial(p, addr); ok {
			if err != nil {
				return c, err
			}
			name := ""
			if bc, ok := c.(*balConn); ok && bc.pool != nil {
				name = bc.pool.P.Name
			}
			return d.track(c, addr, name), nil
		}
	}
	if p == nil {
		return nil, tunnel.ErrNoSession
	}
	c, err := p.Dial(addr)
	if err != nil {
		return c, err
	}
	return d.track(c, addr, p.P.Name), nil
}

func (d *Daemon) track(c net.Conn, addr, account string) net.Conn {
	proto := "tcp"
	if h, _, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
			proto, addr = "udp", "UDPGW"
		}
	}
	return &trackConn{Conn: c, f: d.flows.add(proto, addr, account), t: &d.flows}
}

func (d *Daemon) BlockQUIC() bool {
	switch d.cfg.Main.QUICMode {
	case "on":
		return true
	case "off":
		return false
	}
	p := d.pool.Load()
	return p == nil || !p.IsZivpn()
}

func (d *Daemon) NativeUDPTo(dst *net.UDPAddr, onPacket func(src *net.UDPAddr, data []byte)) (proxy.UDPRelay, bool, error) {
	if dst != nil && d.directFor(dst.IP.String()) {
		var fp atomic.Pointer[flow]
		r, err := d.newDirectUDP(func(src *net.UDPAddr, data []byte) {
			if f := fp.Load(); f != nil {
				f.rx.Add(int64(len(data)))
			}
			onPacket(src, data)
		})
		if err != nil {
			return nil, true, err
		}
		f := d.flows.add("udp", dst.String(), "Direct")
		fp.Store(f)
		return &trackRelay{UDPRelay: r, f: f, t: &d.flows}, true, nil
	}
	return d.nativeUDPFor(dst, onPacket)
}

func (d *Daemon) NativeUDP(onPacket func(src *net.UDPAddr, data []byte)) (proxy.UDPRelay, bool, error) {
	return d.nativeUDPFor(nil, onPacket)
}

func (d *Daemon) nativeUDPFor(dst *net.UDPAddr, onPacket func(src *net.UDPAddr, data []byte)) (proxy.UDPRelay, bool, error) {
	var fp atomic.Pointer[flow]
	counted := func(src *net.UDPAddr, data []byte) {
		if f := fp.Load(); f != nil {
			f.rx.Add(int64(len(data)))
		}
		onPacket(src, data)
	}
	r, native, err := d.nativeUDP(dst, counted)
	if r == nil || err != nil {
		return r, native, err
	}
	name := ""
	if br, ok := r.(*balRelay); ok && br.pool != nil {
		name = br.pool.P.Name
	} else if p := d.pool.Load(); p != nil {
		name = p.P.Name
	}
	f := d.flows.add("udp", "", name)
	fp.Store(f)
	return &trackRelay{UDPRelay: r, f: f, t: &d.flows}, native, nil
}

func (d *Daemon) nativeUDP(dst *net.UDPAddr, onPacket func(src *net.UDPAddr, data []byte)) (proxy.UDPRelay, bool, error) {
	p := d.pool.Load()
	if d.balancing() {
		if r, ok := d.balanceUDP(p, dst, onPacket); ok {
			return r, true, nil
		}
	}
	if p != nil && p.IsXray() {
		u, err := p.XrayUDP(onPacket)
		if err != nil {
			return nil, true, err
		}
		return u, true, nil
	}
	if p == nil || !p.IsZivpn() {
		return nil, false, nil
	}
	u, err := p.ZivpnUDP(onPacket)
	if err != nil {
		return nil, true, err
	}
	return u, true, nil
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
	if logx.OnOpen != nil {
		logx.OnOpen(log)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Errorf("config", "%v", err)
		return err
	}
	log.SetLevel(cfg.Main.LogLevel)
	d := &Daemon{cfg: cfg, cfgPath: cfgPath, log: log,
		switchCh: make(chan string, 1), autoKick: make(chan struct{}, 1), autoRes: make(chan LatencyReport, 1),
		ipCh: make(chan string, 1), lastIP: map[string]time.Time{}, bal: newBalancer(), use: usage.Load(UsageFile, UsageLiveFile), lastIPChange: time.Now()}
	d.st = Status{State: "starting", NetMode: cfg.Main.NetMode, Policy: cfg.Main.Policy, Version: Version, PID: os.Getpid()}
	os.Remove(CmdFile)
	d.writeStatus()
	log.Infof("start", "Starting Tomato Tunnel %s", Version)

	if len(cfg.Profiles) == 0 {
		d.set(func(s *Status) { s.State, s.Message = "failed", "no saved account" })
		log.Errorf("config", "No saved account in /etc/config/tomato-tunnel. Add one in the Accounts tab and press Save & connect.")
		return fmt.Errorf("no profile")
	}

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1, syscall.SIGUSR2)
	go func() {
		for s := range sig {
			if s == syscall.SIGUSR1 {
				log.Clear()
				continue
			}
			if s == syscall.SIGUSR2 {
				d.handleCmds(ctx)
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
	go d.scheduleLoop(ctx)
	go d.modemStatus()
	go d.balanceLoop(ctx)
	go reapXrayLoop(ctx, d.log)
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
		dns := &proxy.DNS{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(m.DNSPort)), Server: net.JoinHostPort(m.DNSServer, "53"), Up: d, Log: d.log, OnAnswer: d.flows.learnName}
		if err := dns.Start(); err != nil {
			return fmt.Errorf("DNS port %d: %v", m.DNSPort, err)
		}
		d.svcs = append(d.svcs, dns)
	}
	return nil
}

func (d *Daemon) policy() string {
	if v, ok := d.pol.Load().(string); ok && v != "" {
		return v
	}
	return d.cfg.Main.Policy
}

func (d *Daemon) reorder(id string) []config.Profile {
	c := *d.cfg
	c.Main.Profile = id
	return c.Ordered()
}

func (d *Daemon) waitOrSwitch(ctx context.Context, dur time.Duration) string {
	select {
	case <-ctx.Done():
	case <-time.After(dur):
	case id := <-d.switchCh:
		return id
	case t := <-d.ipCh:
		d.changeIP(ctx, "Change IP requested", t)
	}
	return ""
}

func (d *Daemon) takeSwitch(id string) ([]config.Profile, bool) {
	if c, err := config.Load(d.cfgPath); err == nil && len(c.Profiles) > 0 {
		d.cfg.Profiles = c.Profiles
	}
	p, ok := d.cfg.Find(id)
	if !ok {
		d.log.Errorf("switch", "Profile %s not found", id)
		return nil, false
	}
	return d.reorder(ProfileKey(p)), true
}

func (d *Daemon) activate(pool *tunnel.Pool, countReconnect bool) {
	p := pool.P
	d.pool.Store(pool)
	d.mu.Lock()
	d.rxBase, d.txBase = pool.RX.Load(), pool.TX.Load()
	d.mainBad.Store(false)
	if countReconnect {
		d.countReconnect()
	}
	d.st.State, d.st.Message, d.st.Switching = "connected", "", ""
	d.st.Profile, d.st.ProfileName, d.st.Mode, d.st.Enhanced = ProfileKey(p), p.Name, p.Mode, p.Enhanced
	d.st.PublicIP, d.st.Country, d.st.ISP, d.st.PingMS = "", "", "", 0
	d.st.Since = time.Now().Unix()
	d.st.Banner, d.st.Kex, d.st.Legacy = pool.Info.Banner, pool.Info.Kex, pool.Info.Legacy
	d.st.ServerMsg = pool.Info.ServerMsg
	delete(d.st.LoginFailed, ProfileKey(p))
	d.mu.Unlock()
	d.retargetHev()
	d.writeStatus()
}

func (d *Daemon) hevTarget() (string, int) {
	m := d.cfg.Main
	if p := d.pool.Load(); p != nil && p.IsXray() && !d.balancing() {
		if port := p.XrayPort(); port > 0 {
			return "127.0.0.1", port
		}
	}
	host := m.SocksListen
	if host == "0.0.0.0" || host == "" {
		host = "127.0.0.1"
	}
	return host, m.SocksPort
}

func (d *Daemon) retargetHev() {
	if d.cfg.Main.NetMode != "tun" || !d.netUp || d.hev == nil {
		return
	}
	if _, port := d.hevTarget(); port == d.hevPort {
		return
	}
	d.stopHev()
	if err := d.startHev(); err != nil {
		d.log.Errorf("tun", "%v", err)
		return
	}
	exec.Command(NetScript, "routes").Run()
}

const IdleTCP = tunnel.IdleTCP

func (d *Daemon) stopHev() {
	if d.hev == nil || d.hev.Process == nil {
		return
	}
	d.hev.Process.Signal(syscall.SIGTERM)
	select {
	case <-d.hevDone:
	case <-time.After(3 * time.Second):
		d.hev.Process.Kill()
		<-d.hevDone
	}
}

func XrayOpts(m config.Main) tunnel.XrayOpts {
	return tunnel.XrayOpts{DirectDomains: m.DirectDomains, BlockAds: m.BlockAds, BlockQUIC: m.QUICMode != "off", CheckURL: m.CheckURL, RunDir: RunDir}
}

func (d *Daemon) newPool(p config.Profile) *tunnel.Pool {
	m := d.cfg.Main
	iface := m.WanIface
	if p.Wan != "" {
		iface = ifaceDev(p.Wan)
	}
	np := tunnel.NewPool(p, m.SSHCompat, iface, d.log)
	np.X = XrayOpts(m)

	np.X.Shared = d.balancing()
	return np
}

func (d *Daemon) markLogin(key string, failed bool) {
	d.set(func(s *Status) {
		if !failed {
			delete(s.LoginFailed, key)
			return
		}
		if s.LoginFailed == nil {
			s.LoginFailed = map[string]int64{}
		}
		s.LoginFailed[key] = time.Now().Unix()
	})
}

func (d *Daemon) loginFailed(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.st.LoginFailed[key]
	return ok
}

func (d *Daemon) supervise(ctx context.Context) {
	m := d.cfg.Main
	profiles := d.cfg.Ordered()
	pi := 0
	fails := 0
	rounds := 0
	first := true
	var pool *tunnel.Pool
	for ctx.Err() == nil {
		if pool == nil {
			pi, _ = d.skipExpired(profiles, pi)
			p := profiles[pi%len(profiles)]
			if err := p.Validate(); err != nil {
				d.log.Errorf("config", "%v", err)
				d.set(func(s *Status) { s.State, s.Message = "failed", err.Error() })
				wait := time.Duration(0)
				if d.policy() == "manual" || len(profiles) == 1 {
					wait = 30 * time.Second
				} else {
					pi++
					if pi%len(profiles) == 0 {
						wait = 30 * time.Second
					}
				}
				if wait > 0 {
					if id := d.waitOrSwitch(ctx, wait); id != "" {
						if o, ok := d.takeSwitch(id); ok {
							d.log.Infof("switch", "Switching to %s", o[0].Name)
							profiles, pi, fails, rounds = o, 0, 0, 0
						}
					}
				}
				continue
			}
			state := "connecting"
			if !first {
				state = "reconnecting"
			}
			d.set(func(s *Status) {
				s.State, s.Message, s.Switching = state, "", ""
				s.Profile, s.ProfileName, s.Mode, s.Enhanced = ProfileKey(p), p.Name, p.Mode, p.Enhanced
				s.PublicIP, s.Country, s.ISP, s.PingMS = "", "", "", 0
			})

			np := d.newPool(p)
			cctx, ccancel := context.WithTimeout(ctx, 60*time.Second)
			err := np.Connect(cctx, m.Sessions)
			ccancel()
			if err != nil {
				np.Close()
				if ctx.Err() != nil {
					return
				}
				d.log.Errorf("connect", "Connection failed: %v", err)
				d.set(func(s *Status) { s.Message = err.Error() })
				if errors.Is(err, tunnel.ErrAuth) {
					d.markLogin(ProfileKey(p), true)
				}
				fails++
				d.failRun++
				authFail := errors.Is(err, tunnel.ErrAuth) && d.policy() != "manual" && len(profiles) > 1
				if authFail {
					fails = 3
				}
				if fails >= 3 {
					fails = 0
					if d.policy() != "manual" {
						pi++
						if pi%len(profiles) == 0 {
							rounds++
						}
						if len(profiles) > 1 {
							d.log.Warnf("failover", "Switching to profile %s", profiles[pi%len(profiles)].Name)
						}
					} else {
						rounds++
					}
				}
				if ip := d.cfg.Main.IP; d.ipEnabled() && ip.AfterFails && d.failRun >= ip.AfterN && !errors.Is(err, tunnel.ErrAuth) {
					d.failRun = 0
					d.changeIP(ctx, fmt.Sprintf("%d failed connects in a row", ip.AfterN), "")
				}
				backoff := time.Duration(2<<min(fails+rounds, 4)) * time.Second
				if authFail && pi%len(profiles) != 0 {
					backoff = 0
				}
				if id := d.waitOrSwitch(ctx, backoff); id != "" {
					if o, ok := d.takeSwitch(id); ok {
						d.log.Infof("switch", "Switching to %s", o[0].Name)
						profiles, pi, fails, rounds = o, 0, 0, 0
					}
				}
				continue
			}
			fails, rounds, d.failRun = 0, 0, 0
			pool = np
			d.activate(pool, !first)
			first = false
			if !d.netUp {
				if err := d.netSetup(); err != nil {
					d.log.Errorf("net", "Network setup failed: %v", err)
				}
			}
			go d.fetchIP(pool)
		}

		res := d.monitor(ctx, pool)
		if res.pool != nil {
			old := pool
			pool = res.pool
			profiles, pi, fails, rounds = res.order, 0, 0, 0
			d.activate(pool, false)
			d.log.Okf("switch", "Traffic moved to %s", pool.P.Name)
			if d.balGive(old) {
				d.log.Infof("switch", "%s keeps running for Balance", old.P.Name)
			} else {
				old.Close()
				d.log.Infof("switch", "Closed old session %s", old.P.Name)
			}
			go d.fetchIP(pool)
			continue
		}
		d.pool.Store(nil)
		pool.Close()
		pool = nil
		if ctx.Err() != nil {
			return
		}
		if res.order != nil {
			profiles, pi, fails, rounds = res.order, 0, 0, 0
		}
		d.log.Warnf("reconnect", "Reconnecting (%s)", res.reason)
		d.set(func(s *Status) { s.State, s.Message = "reconnecting", res.reason })
	}
}

type monResult struct {
	reason string
	pool   *tunnel.Pool
	order  []config.Profile
}

func (d *Daemon) trySwitch(ctx context.Context, cur *tunnel.Pool, id, why string) (*tunnel.Pool, []config.Profile, error) {
	order, ok := d.takeSwitch(id)
	if !ok {
		return nil, nil, fmt.Errorf("profile not found")
	}
	p := order[0]
	if ProfileKey(p) == ProfileKey(cur.P) && p.Name == cur.P.Name {
		d.log.Infof("switch", "%s is already active", p.Name)
		return nil, nil, fmt.Errorf("already active")
	}
	if err := p.Validate(); err != nil {
		d.log.Errorf("switch", "%v", err)
		return nil, nil, err
	}
	if np := d.balTake(ProfileKey(p)); np != nil {
		d.log.Okf("switch", "%sMaking %s the main account, its Balance session is already up", why, p.Name)
		return np, order, nil
	}
	m := d.cfg.Main
	d.log.Warnf("switch", "%sSwitching to %s, %s stays up until it is ready", why, p.Name, cur.P.Name)
	d.set(func(s *Status) { s.Switching = p.Name })
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	d.mu.Lock()
	d.swCancel = cancel
	d.mu.Unlock()
	t0 := time.Now()
	np := d.newPool(p)
	err := np.Connect(sctx, m.Sessions)
	cerr := sctx.Err()
	cancel()
	d.mu.Lock()
	d.swCancel = nil
	d.mu.Unlock()
	if err != nil {
		np.Close()
		if ctx.Err() == nil && cerr == context.Canceled {
			d.log.Warnf("switch", "Switch to %s cancelled, staying on %s", p.Name, cur.P.Name)
		} else {
			if errors.Is(err, tunnel.ErrAuth) {
				d.markLogin(ProfileKey(p), true)
			}
			d.log.Errorf("switch", "%s failed: %s", p.Name, shortErr(err))
			d.log.Okf("switch", "Staying on %s, nothing was cut", cur.P.Name)
		}
		d.set(func(s *Status) { s.Switching = "" })
		return nil, nil, err
	}
	d.log.Okf("switch", "Connected %s in %.1f s", p.Name, time.Since(t0).Seconds())
	return np, order, nil
}

func (d *Daemon) startMeasure(ctx context.Context) {
	var profiles []config.Profile
	cur := ""
	if p := d.pool.Load(); p != nil {
		cur = ProfileKey(p.P)
	}
	for _, p := range d.cfg.Profiles {
		if p.Dashboard || ProfileKey(p) == cur {
			profiles = append(profiles, p)
		}
	}
	d.measureOf(ctx, profiles)
}

func (d *Daemon) measureOf(ctx context.Context, profiles []config.Profile) {
	if len(profiles) == 0 || !d.measuring.CompareAndSwap(false, true) {
		return
	}
	m := d.cfg.Main
	go func() {
		defer d.measuring.Store(false)
		rep := Measure(ctx, m, profiles, 30*time.Second)
		SaveLatency(rep)
		select {
		case d.autoRes <- rep:
		default:
		}
	}()
}

func (d *Daemon) bestOf(rep LatencyReport, cur config.Profile) (string, int64, int64) {
	curMS := int64(-1)
	if r, ok := rep.Results[ProfileKey(cur)]; ok {
		curMS = r.MS
	}
	best, bestMS := "", int64(-1)
	for _, p := range d.cfg.Profiles {
		k := ProfileKey(p)
		if k == ProfileKey(cur) {
			continue
		}
		r, ok := rep.Results[k]
		if !ok || !p.Dashboard || r.MS < 0 || d.loginFailed(k) || p.Expired(time.Now()) {
			continue
		}
		if bestMS < 0 || r.MS < bestMS {
			best, bestMS = k, r.MS
		}
	}
	return best, bestMS, curMS
}

func (d *Daemon) monitor(ctx context.Context, pool *tunnel.Pool) monResult {
	m := d.cfg.Main
	since := time.Now()
	t := time.NewTicker(time.Duration(m.CheckInterval) * time.Second)
	defer t.Stop()
	failed := 0
	host := m.CheckURL
	if u, err := url.Parse(m.CheckURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	stats := time.NewTicker(5 * time.Minute)
	defer stats.Stop()
	auto := time.NewTimer(30 * time.Second)
	defer auto.Stop()
	sched := time.NewTicker(time.Minute)
	defer sched.Stop()
	for {
		select {
		case t := <-d.ipCh:
			if _, tun := d.changeIP(ctx, "Change IP requested", t); tun {
				return monResult{reason: "IP changed"}
			}
		case <-sched.C:
			go d.modemStatus()
			tun := false
			for _, m := range d.cfg.Main.Modems {
				last, ok := d.lastIP[m.ID]
				if !ok {
					last = d.lastIPChange
				}
				if h := m.EveryH; h > 0 && time.Since(last) >= time.Duration(h)*time.Hour {
					_, t := d.changeIP(ctx, fmt.Sprintf("Scheduled Change IP %s (every %d h)", m.Name, h), m.ID)
					tun = tun || t
				}
			}
			if tun {
				return monResult{reason: "IP changed"}
			}
		case <-stats.C:
			d.mu.Lock()
			st := d.st
			d.mu.Unlock()
			d.log.Infof("stats", "Traffic: download %s, upload %s", human(st.RX), human(st.TX))
		case <-ctx.Done():
			return monResult{reason: "stopped"}
		case <-pool.Dead():
			return monResult{reason: "all SSH sessions lost"}
		case id := <-d.switchCh:
			if np, order, err := d.trySwitch(ctx, pool, id, ""); err == nil {
				return monResult{pool: np, order: order}
			}
		case <-d.autoKick:
			if d.policy() == "auto" {
				d.startMeasure(ctx)
			}
		case <-auto.C:
			auto.Reset(AutoInterval)
			if d.policy() == "auto" {
				d.startMeasure(ctx)
			} else if p, ok := d.fallbackHome(pool); ok && time.Since(since) >= fallbackSettle {
				d.measureOf(ctx, []config.Profile{p})
			}
		case rep := <-d.autoRes:
			for k, r := range rep.Results {
				if r.Auth {
					d.markLogin(k, true)
				}
			}
			if p, ok := d.fallbackHome(pool); ok {
				if r, in := rep.Results[ProfileKey(p)]; in && r.MS >= 0 && !r.Auth {
					why := fmt.Sprintf("Fallback: %s works again (%d ms). ", p.Name, r.MS)
					if np, order, err := d.trySwitch(ctx, pool, ProfileKey(p), why); err == nil {
						return monResult{pool: np, order: order}
					}
				}
				continue
			}
			if d.policy() != "auto" {
				continue
			}
			best, bestMS, curMS := d.bestOf(rep, pool.P)
			if best == "" {
				continue
			}
			if curMS >= 0 && bestMS+AutoMargin >= curMS {
				d.log.Infof("auto", "Auto: %s is still the fastest (%d ms)", pool.P.Name, curMS)
				continue
			}
			why := fmt.Sprintf("Auto: faster profile found (%d ms vs %d ms). ", bestMS, curMS)
			if curMS < 0 {
				why = "Auto: current profile failed the latency test. "
			}
			if np, order, err := d.trySwitch(ctx, pool, best, why); err == nil {
				return monResult{pool: np, order: order}
			}
		case <-t.C:
			d.ensureHev()
			ms, err := check(ctx, pool, m.CheckURL, m.UnifiedDelay)
			if ctx.Err() != nil {
				return monResult{reason: "stopped"}
			}
			if err != nil {
				failed++
				if failed >= pauseAfter {

					d.mainBad.Store(true)
				}
				d.log.Warnf("ping", "Ping %s failed: %s (%d/%d)", host, shortErr(err), failed, m.CheckFail)
				if failed >= m.CheckFail {
					return monResult{reason: fmt.Sprintf("check failed %d×", failed)}
				}
				continue
			}
			d.log.Okf("ping", "Ping %s %d ms OK", host, ms)
			failed = 0
			d.mainBad.Store(false)
			d.set(func(s *Status) { s.PingMS = ms })
		}
	}
}

const (
	AutoInterval   = 5 * time.Minute
	AutoMargin     = 20
	fallbackSettle = 3 * time.Minute
)

func (d *Daemon) fallbackHome(cur *tunnel.Pool) (config.Profile, bool) {
	if cur == nil || d.policy() != "fallback" || !d.cfg.Main.FallbackBack {
		return config.Profile{}, false
	}
	c, err := config.Load(d.cfgPath)
	if err != nil {
		return config.Profile{}, false
	}
	o := c.Ordered()
	if len(o) == 0 {
		return config.Profile{}, false
	}
	p := o[0]
	if ProfileKey(p) == ProfileKey(cur.P) || !p.Dashboard || p.Expired(time.Now()) || p.Validate() != nil || d.loginFailed(ProfileKey(p)) {
		return config.Profile{}, false
	}
	return p, true
}

func (d *Daemon) handleCmds(ctx context.Context) {
	work := CmdFile + ".work"
	if os.Rename(CmdFile, work) != nil {
		return
	}
	b, _ := os.ReadFile(work)
	os.Remove(work)
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "switch":
			if len(f) < 2 {
				continue
			}
			select {
			case <-d.switchCh:
			default:
			}
			d.switchCh <- f[1]
		case "policy":
			if len(f) < 2 {
				continue
			}
			switch f[1] {
			case "manual", "fallback", "auto", "balance":
				d.pol.Store(f[1])
				d.set(func(s *Status) { s.Policy = f[1] })
				d.log.Infof("policy", "Profile selection: %s", f[1])
				if f[1] == "auto" {
					select {
					case d.autoKick <- struct{}{}:
					default:
					}
				}
			}
		case "changeip":
			t := ""
			if len(f) > 1 {
				t = f[1]
			}
			select {
			case d.ipCh <- t:
			default:
			}
		case "usage-reset":
			d.use.Reset()
			d.use.ResetDeviceBase()
			d.log.Infof("usage", "Usage statistics reset")
		case "reload-profiles":
			d.reloadProfiles()
		case "reload-settings":
			d.reloadSettings()
		case "cancel":
			d.mu.Lock()
			c := d.swCancel
			d.mu.Unlock()
			if c != nil {
				c()
			}
		}
	}
}

func check(ctx context.Context, pool *tunnel.Pool, url string, unified bool) (int64, error) {
	tr := &http.Transport{
		DialContext:         func(ctx context.Context, _, addr string) (net.Conn, error) { return pool.Dial(addr) },
		DisableKeepAlives:   !unified,
		MaxIdleConnsPerHost: 1,
	}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: pool.CheckTimeout(),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func() (int64, error) {
		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return 0, err
		}
		resp, err := cl.Do(req)
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
	ms, err := get()
	if err != nil || !unified {
		return ms, err
	}
	if ms2, err2 := get(); err2 == nil {
		return ms2, nil
	}
	return ms, nil
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
		"BYPASS="+strings.Join(m.Bypass, " "), "BYPASS_ID="+b2s(m.BypassID), "ROUTER_SELF="+b2s(m.RouterSelf))
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
	bin := "/usr/libexec/tomato-tunnel/hev-socks5-tunnel"
	if st, err := os.Stat(bin); err != nil || st.Mode()&0111 == 0 {
		b, err := exec.LookPath("hev-socks5-tunnel")
		if err != nil {
			return fmt.Errorf("hev-socks5-tunnel is not installed (needed for TUN mode)")
		}
		bin = b
	}
	conf := filepath.Join(RunDir, "hev.yml")
	host, port := d.hevTarget()
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
  tcp-read-write-timeout: %d
`, TunName, port, host, IdleTCP.Milliseconds())
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
	d.hevPort = port
	if port != m.SocksPort {
		trx, ttx := tunBytes()
		d.mu.Lock()
		d.tunRX, d.tunTX = trx, ttx
		d.mu.Unlock()
		d.direct.Store(true)
		d.log.Infof("tun", "TUN goes straight to Xray (port %d)", port)
	} else {
		d.direct.Store(false)
	}
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
	d.usageTick(time.Now(), true)
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
	tick := 0
	var accRX, accTX int64
	d.usageTick(time.Now(), false)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if connsWanted() {
			d.writeConns()
		}
		p := d.pool.Load()
		d.mu.Lock()
		if p != nil {
			rx, tx := p.RX.Load(), p.TX.Load()
			d.st.RateRX, d.st.RateTX = rx-d.rxBase, tx-d.txBase
			if d.direct.Load() {
				trx, ttx := tunBytes()
				if trx >= d.tunRX && ttx >= d.tunTX {
					d.st.RateRX += trx - d.tunRX
					d.st.RateTX += ttx - d.tunTX
				}
				d.tunRX, d.tunTX = trx, ttx
			}
			brx, btx := d.balanceRates(p, d.st.RateRX, d.st.RateTX)
			d.st.RateRX += brx
			d.st.RateTX += btx
			d.st.RX += d.st.RateRX
			d.st.TX += d.st.RateTX
			d.rxBase, d.txBase = rx, tx
			d.st.Sessions = p.Sessions()
			if d.use != nil {
				d.use.AddProfile(time.Now(), ProfileKey(p.P), p.P.Name, d.st.RateRX, d.st.RateTX)
			}
		} else {
			d.st.RateRX, d.st.RateTX, d.st.Sessions = 0, 0, 0
		}
		accRX += d.st.RateRX
		accTX += d.st.RateTX
		if tick%2 == 1 {
			var smp []int64
			if p != nil && d.st.State == "connected" {
				smp = []int64{accRX / 2, accTX / 2}
			}
			d.st.Hist = append(d.st.Hist, smp)
			if len(d.st.Hist) > HistLen {
				d.st.Hist = d.st.Hist[len(d.st.Hist)-HistLen:]
			}
			accRX, accTX = 0, 0
		}
		d.mu.Unlock()
		tick++
		if tick%60 == 0 {
			d.usageTick(time.Now(), false)
		} else if tick%10 == 0 && d.use != nil {
			trx, ttx := d.use.Today(time.Now())
			d.mu.Lock()
			d.st.TodayBytes = trx + ttx
			d.mu.Unlock()
		}
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

func tunBytes() (rx, tx int64) {
	read := func(f string) int64 {
		b, err := os.ReadFile("/sys/class/net/" + TunName + "/statistics/" + f)
		if err != nil {
			return 0
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		return n
	}
	return read("rx_bytes"), read("tx_bytes")
}
