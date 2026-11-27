package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
)

var CtlPath = "/usr/libexec/tomato-tunnel/ctl"

type schedRun struct {
	Action string `json:"action"`
	Value  string `json:"value"`
	Prev   string `json:"prev"`
}

func schedFile() string { return filepath.Join(RunDir, "schedule-back.json") }

func loadSchedRuns() map[string]schedRun {
	m := map[string]schedRun{}
	if b, err := os.ReadFile(schedFile()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func saveSchedRuns(m map[string]schedRun) {
	if len(m) == 0 {
		os.Remove(schedFile())
		return
	}
	if b, err := json.Marshal(m); err == nil {
		os.WriteFile(schedFile(), b, 0600)
	}
}

func (d *Daemon) scheduleLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	fired := map[string]string{}
	runs := loadSchedRuns()
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
		if d.scheduleBack(ctx, c, runs, now) {
			saveSchedRuns(runs)
		}
		stamp := now.Format("2006-01-02 15:04")
		for _, s := range c.Schedules {
			if !s.Due(now) || fired[s.ID] == stamp {
				continue
			}
			fired[s.ID] = stamp
			if r, ok := d.runSchedule(ctx, c, s); ok && s.HasEnd() {
				runs[s.ID] = r
				saveSchedRuns(runs)
			}
		}
	}
}

func (d *Daemon) scheduleBack(ctx context.Context, c *config.Config, runs map[string]schedRun, now time.Time) bool {
	changed := false
	for id, r := range runs {
		var s *config.Schedule
		for i := range c.Schedules {
			if c.Schedules[i].ID == id {
				s = &c.Schedules[i]
			}
		}
		if s != nil && s.Enabled && s.HasEnd() && s.Action == r.Action && s.Value == r.Value && s.InWindow(now) {
			continue
		}
		delete(runs, id)
		changed = true
		if s == nil || !s.Enabled || !s.HasEnd() || s.Action != r.Action || s.Value != r.Value {
			continue
		}
		switch r.Action {
		case "account":
			if c.Main.Profile != r.Value {
				continue
			}
			p, ok := c.Find(r.Prev)
			if !ok {
				d.log.Warnf("schedule", "Schedule until %s: account to go back to no longer exists", s.End)
				continue
			}
			d.log.Infof("schedule", "Schedule until %s: back to %s", s.End, p.Name)
			exec.CommandContext(ctx, CtlPath, "switch", p.ID).Run()
		case "policy":
			if d.policy() != r.Value {
				continue
			}
			d.log.Infof("schedule", "Schedule until %s: back to %s", s.End, r.Prev)
			exec.CommandContext(ctx, CtlPath, "policy", r.Prev).Run()
		}
	}
	return changed
}

func (d *Daemon) runSchedule(ctx context.Context, c *config.Config, s config.Schedule) (r schedRun, ok bool) {
	switch s.Action {
	case "account":
		p, found := c.Find(s.Value)
		if !found {
			d.log.Warnf("schedule", "Schedule %s: account %s no longer exists", s.Time, s.Value)
			return r, false
		}
		if cur := d.pool.Load(); (cur != nil && ProfileKey(cur.P) == ProfileKey(p)) || c.Main.Profile == p.ID {
			d.log.Infof("schedule", "Schedule %s: already on %s", s.Time, p.Name)
			return r, false
		}
		if s.HasEnd() {
			d.log.Infof("schedule", "Schedule %s: using %s until %s", s.Time, p.Name, s.End)
		} else {
			d.log.Infof("schedule", "Schedule %s: using %s", s.Time, p.Name)
		}
		r = schedRun{Action: "account", Value: p.ID, Prev: c.Main.Profile}
		if cur := d.pool.Load(); r.Prev == "" && cur != nil {
			r.Prev = cur.P.ID
		}
		exec.CommandContext(ctx, CtlPath, "switch", p.ID).Run()
		return r, r.Prev != ""
	case "policy":
		cur := d.policy()
		if s.Value == cur {
			d.log.Infof("schedule", "Schedule %s: already %s", s.Time, s.Value)
			return r, false
		}
		if s.HasEnd() {
			d.log.Infof("schedule", "Schedule %s: profile selection %s until %s", s.Time, s.Value, s.End)
		} else {
			d.log.Infof("schedule", "Schedule %s: profile selection %s", s.Time, s.Value)
		}
		exec.CommandContext(ctx, CtlPath, "policy", s.Value).Run()
		return schedRun{Action: "policy", Value: s.Value, Prev: cur}, cur != ""
	}
	return r, false
}
