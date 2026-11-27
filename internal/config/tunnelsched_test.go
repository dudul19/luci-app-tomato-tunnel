package config

import (
	"strings"
	"testing"
	"time"
)

func at(day, hhmm string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", day+" "+hhmm, time.Local)
	return t
}

func TestTunnelWindows(t *testing.T) {
	off := Schedule{ID: "a", Action: "tunnel", Value: "off", Time: "01:00", End: "06:00", Days: "daily", Enabled: true}
	c := &Config{Schedules: []Schedule{off}}
	for hhmm, want := range map[string]string{"00:59": "", "01:00": "off", "05:59": "off", "06:00": ""} {
		if got := TunnelWanted(c, at("2026-11-25", hhmm)); got != want {
			t.Errorf("off 01-06 at %s = %q, want %q", hhmm, got, want)
		}
	}
	night := Schedule{ID: "b", Action: "tunnel", Value: "off", Time: "23:00", End: "02:00", Days: "fri", Enabled: true}
	c = &Config{Schedules: []Schedule{night}}

	if TunnelWanted(c, at("2026-11-27", "23:30")) != "off" || TunnelWanted(c, at("2026-11-28", "01:30")) != "off" {
		t.Error("window across midnight from Friday")
	}
	if TunnelWanted(c, at("2026-11-28", "23:30")) != "" || TunnelWanted(c, at("2026-11-27", "01:30")) != "" {
		t.Error("window must only start on Friday")
	}
	on := Schedule{ID: "c", Action: "tunnel", Value: "on", Time: "08:00", End: "17:00", Days: "weekdays", Enabled: true}
	c = &Config{Schedules: []Schedule{on}}
	if TunnelWanted(c, at("2026-11-25", "09:00")) != "on" || TunnelWanted(c, at("2026-11-25", "18:00")) != "off" {
		t.Error("on window")
	}
	lines := TunnelCron(&Config{Schedules: []Schedule{off, night}}, "/ctl")
	want := []string{
		"0 1 * * * /ctl schedule-stop " + CronMark,
		"0 6 * * * /ctl schedule-start " + CronMark,
		"0 23 * * 5 /ctl schedule-stop " + CronMark,
		"0 2 * * 6 /ctl schedule-start " + CronMark,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("cron:\n%s", strings.Join(lines, "\n"))
	}
	bad := Schedule{Action: "tunnel", Value: "off", Time: "01:00", End: "01:00", Enabled: true}
	if bad.IsTunnel() {
		t.Error("empty window accepted")
	}
}
