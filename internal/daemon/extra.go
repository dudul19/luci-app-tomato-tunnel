package daemon

import (
	"context"
	"fmt"
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

func (d *Daemon) changeIP(ctx context.Context, why string) changeip.Result {
	if !d.ipBusy.CompareAndSwap(false, true) {
		return changeip.Result{Error: "already changing IP"}
	}
	defer d.ipBusy.Store(false)
	c := d.cfg
	if fresh, err := config.Load(d.cfgPath); err == nil {
		c = fresh
		d.cfg.Main.IP = fresh.Main.IP
	}
	if why != "" {
		d.log.Warnf("changeip", "%s", why)
	}
	d.set(func(s *Status) { s.ChangingIP = true })
	r := changeip.Run(ctx, c.Main.IP, c.Main.WanIface, d.log)
	d.lastIPChange = time.Now()
	d.set(func(s *Status) {
		s.ChangingIP = false
		s.LastIPChange = time.Now().Unix()
		s.IPChangeOld = r.Old
		if r.OK {
			s.IPChangeMsg = "New IP " + r.New
		} else {
			s.IPChangeMsg = r.Error
		}
	})
	return r
}

func (d *Daemon) ipEnabled() bool {
	return d.cfg.Main.IP.Method != "" && d.cfg.Main.IP.Method != "none"
}
