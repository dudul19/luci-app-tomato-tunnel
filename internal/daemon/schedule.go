package daemon

import (
	"context"
	"os/exec"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
)

// CtlPath is the control script; the schedule uses it so a scheduled change
// is the same as a click on the page (written to the config, logged, applied).
var CtlPath = "/usr/libexec/tomato-tunnel/ctl"

// scheduleLoop runs the schedules from Settings › Schedule while the tunnel runs.
// The config is read fresh every time, so edits count without a restart.
func (d *Daemon) scheduleLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	fired := map[string]string{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		c, err := config.Load(d.cfgPath)
		if err != nil {
			continue
		}
		now := time.Now()
		stamp := now.Format("2006-01-02 15:04")
		for _, s := range c.Schedules {
			if !s.Due(now) || fired[s.ID] == stamp {
				continue
			}
			fired[s.ID] = stamp
			d.runSchedule(ctx, c, s)
		}
	}
}

func (d *Daemon) runSchedule(ctx context.Context, c *config.Config, s config.Schedule) {
	switch s.Action {
	case "account":
		p, ok := c.Find(s.Value)
		if !ok {
			d.log.Warnf("schedule", "Schedule %s: account %s no longer exists", s.Time, s.Value)
			return
		}
		if cur := d.pool.Load(); cur != nil && ProfileKey(cur.P) == ProfileKey(p) {
			d.log.Infof("schedule", "Schedule %s: already on %s", s.Time, p.Name)
			return
		}
		d.log.Infof("schedule", "Schedule %s: using %s", s.Time, p.Name)
		exec.CommandContext(ctx, CtlPath, "switch", p.ID).Run()
	case "policy":
		if s.Value == d.policy() {
			d.log.Infof("schedule", "Schedule %s: already %s", s.Time, s.Value)
			return
		}
		d.log.Infof("schedule", "Schedule %s: profile selection %s", s.Time, s.Value)
		exec.CommandContext(ctx, CtlPath, "policy", s.Value).Run()
	}
}
