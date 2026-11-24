package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/changeip"
	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/usage"
)

var (
	UsageFile     = envOr("TT_USAGE", "/etc/tomato-tunnel/usage.json")
	UsageLiveFile = RunDir + "/usage.json"
	LeasesFile    = "/tmp/dhcp.leases"
)

type Logger interface {
	Infof(tag, f string, a ...any)
	Okf(tag, f string, a ...any)
	Warnf(tag, f string, a ...any)
	Errorf(tag, f string, a ...any)
}

func (d *Daemon) usageTick(now time.Time, persist bool) {
	if d.use == nil {
		return
	}
	if d.cfg.Main.NetMode == "tun" && d.netUp {
		if cur, ok := usage.ReadDevices(); ok {
			d.use.AddDevices(now, cur, usage.Leases(LeasesFile))
		}
	}
	d.use.Save(now, persist)
	rx, tx, _ := d.use.Period(now, d.cfg.Main.UsageResetDay)
	trx, ttx := d.use.Today(now)
	d.mu.Lock()
	d.st.PeriodBytes = rx + tx
	d.st.TodayBytes = trx + ttx
	d.st.QuotaGB = d.cfg.Main.UsageQuotaGB
	d.mu.Unlock()
}

func (d *Daemon) skipExpired(profiles []config.Profile, pi int) (int, bool) {
	if d.policy() == "manual" || len(profiles) < 2 {
		return pi, false
	}
	now := time.Now()
	for i := 0; i < len(profiles); i++ {
		p := profiles[(pi+i)%len(profiles)]
		if !p.Expired(now) {
			if i > 0 {
				for j := 0; j < i; j++ {
					x := profiles[(pi+j)%len(profiles)]
					d.log.Warnf("expiry", "Skipping %s: account expired%s", x.Name, onDate(x))
				}
			}
			return pi + i, i > 0
		}
	}
	return pi, false
}

func onDate(p config.Profile) string {
	if t, ok := p.ExpiryTime(); ok {
		return " on " + t.Format("2 Jan")
	}
	return ""
}

func (d *Daemon) reloadSettings() {
	c, err := config.Load(d.cfgPath)
	if err != nil {
		return
	}
	m := &d.cfg.Main
	m.IP, m.ExpiryWarn, m.UsageResetDay, m.UsageQuotaGB = c.Main.IP, c.Main.ExpiryWarn, c.Main.UsageResetDay, c.Main.UsageQuotaGB
	m.IPTarget, m.Modems = c.Main.IPTarget, c.Main.Modems
	m.BalanceSticky = c.Main.BalanceSticky
	m.FallbackBack, m.BlockQUIC = c.Main.FallbackBack, c.Main.BlockQUIC
	go d.modemStatus()
	m.UpdateCheck, m.UpdateRepo, m.UpdateChannel = c.Main.UpdateCheck, c.Main.UpdateRepo, c.Main.UpdateChannel
	if len(c.Profiles) > 0 {
		d.cfg.Profiles = c.Profiles
	}
	d.log.Infof("config", "Settings reloaded")
}

func (d *Daemon) reloadProfiles() {
	if c, err := config.Load(d.cfgPath); err == nil && len(c.Profiles) > 0 {
		d.cfg.Profiles = c.Profiles
	}
}

func (d *Daemon) warnExpiry() {
	now := time.Now()
	for _, p := range d.cfg.Profiles {
		t, ok := p.ExpiryTime()
		if !ok {
			continue
		}
		left := t.Sub(now)
		if left < 0 {
			d.log.Warnf("expiry", "%s expired on %s", p.Name, t.Format("2 Jan"))
		} else if left < time.Duration(d.cfg.Main.ExpiryWarn+1)*24*time.Hour {
			d.log.Warnf("expiry", "%s expires in %s (%s)", p.Name, daysText(left), t.Format("2 Jan 15:04"))
		}
	}
}

func daysText(x time.Duration) string {
	n := int(x.Hours() / 24)
	if n < 1 {
		return fmt.Sprintf("%d hours", int(x.Hours()))
	}
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

// changeIP changes the IP of one modem, of all of them, or (target "" or
// "auto") of the one the tunnel goes out on. It reports whether that modem
// was among them, so the caller knows the tunnel needs a reconnect.
func (d *Daemon) changeIP(ctx context.Context, why, target string) (changeip.Result, bool) {
	if !d.ipBusy.CompareAndSwap(false, true) {
		return changeip.Result{Error: "already changing IP"}, false
	}
	defer d.ipBusy.Store(false)
	c := d.cfg
	if fresh, err := config.Load(d.cfgPath); err == nil {
		c = fresh
		d.cfg.Main.IP, d.cfg.Main.IPTarget, d.cfg.Main.Modems = fresh.Main.IP, fresh.Main.IPTarget, fresh.Main.Modems
	}
	ms := c.Main.Modems
	if len(ms) == 0 {
		return changeip.Result{Error: "no Change IP method is set (Settings › Change IP)"}, false
	}
	if target == "" {
		target = c.Main.IPTarget
	}
	tun := tunnelModem(ms, c.Main.WanIface)
	var sel []config.ChangeIP
	switch target {
	case "all":
		sel = ms
	case "", "auto":
		sel = []config.ChangeIP{tun}
	default:
		m, ok := c.Main.Modem(target)
		if !ok {
			return changeip.Result{Error: "unknown modem " + target}, false
		}
		sel = []config.ChangeIP{m}
	}
	if why != "" {
		d.log.Warnf("changeip", "%s", why)
	}
	var r changeip.Result
	touched := false
	for _, m := range sel {
		if ctx.Err() != nil {
			break
		}
		if m.ID == tun.ID {
			touched = true
		}
		d.setModem(m.ID, func(x *ModemStatus) { x.Changing, x.Msg = true, "" })
		d.set(func(s *Status) { s.ChangingIP = true })
		x := changeip.Run(ctx, m, c.Main.WanIface, d.log)
		d.lastIP[m.ID] = time.Now()
		d.lastIPChange = time.Now()
		d.setModem(m.ID, func(st *ModemStatus) {
			st.Changing, st.Last = false, time.Now().Unix()
			if x.OK {
				st.Msg = "New IP " + x.New
			} else {
				st.Msg = x.Error
			}
		})
		d.set(func(s *Status) {
			s.ChangingIP = false
			s.LastIPChange = time.Now().Unix()
			s.IPChangeOld = x.Old
			if x.OK {
				s.IPChangeMsg = "New IP " + x.New
			} else {
				s.IPChangeMsg = x.Error
			}
			if len(ms) > 1 {
				s.IPChangeModem = m.Name
			} else {
				s.IPChangeModem = ""
			}
		})
		if r.ID == "" || !x.OK {
			r = x
		}
	}
	go d.modemStatus()
	return r, touched
}

// ModemStatus is one modem on the Dashboard.
type ModemStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Method   string `json:"method"`
	IP       string `json:"ip,omitempty"`
	Tunnel   bool   `json:"tunnel,omitempty"`
	Changing bool   `json:"changing,omitempty"`
	Msg      string `json:"msg,omitempty"`
	Last     int64  `json:"last,omitempty"`
}

func (d *Daemon) setModem(id string, f func(*ModemStatus)) {
	d.set(func(s *Status) {
		for i := range s.Modems {
			if s.Modems[i].ID == id {
				f(&s.Modems[i])
			}
		}
	})
}

// modemStatus refreshes the modem list with each modem's interface address.
func (d *Daemon) modemStatus() {
	ms := d.cfg.Main.Modems
	tun := config.ChangeIP{}
	if len(ms) > 0 {
		tun = tunnelModem(ms, d.cfg.Main.WanIface)
	}
	ips := make([]string, len(ms))
	for i, m := range ms {
		ips[i] = changeip.IfaceIP(m.Iface)
	}
	d.set(func(s *Status) {
		old := map[string]ModemStatus{}
		for _, x := range s.Modems {
			old[x.ID] = x
		}
		s.Modems = s.Modems[:0]
		for i, m := range ms {
			x := old[m.ID]
			x.ID, x.Name, x.Method, x.IP, x.Tunnel = m.ID, m.Name, m.Method, ips[i], len(ms) > 1 && m.ID == tun.ID
			s.Modems = append(s.Modems, x)
		}
	})
}

// tunnelModem picks the modem the tunnel goes out on: the one on the WAN
// device set in Network, else the one on the default route with the lowest
// metric, else the first.
func tunnelModem(ms []config.ChangeIP, wan string) config.ChangeIP {
	if len(ms) == 1 {
		return ms[0]
	}
	dev := wan
	if dev == "" {
		dev = defaultDev()
	}
	if dev != "" {
		for _, m := range ms {
			if m.Iface != "" && changeip.IfaceDevice(m.Iface) == dev {
				return m
			}
		}
	}
	return ms[0]
}

func defaultDev() string {
	out, err := exec.Command("ip", "-4", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	best, bestM := "", int(^uint(0)>>1)
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		dev, met := "", 0
		for i := 0; i+1 < len(f); i++ {
			switch f[i] {
			case "dev":
				dev = f[i+1]
			case "metric":
				met, _ = strconv.Atoi(f[i+1])
			}
		}
		if dev == "" || dev == TunName || strings.HasPrefix(dev, "tun") {
			continue
		}
		if met < bestM {
			best, bestM = dev, met
		}
	}
	return best
}

func (d *Daemon) ipEnabled() bool {
	return len(d.cfg.Main.Modems) > 0
}
