package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/logx"
)

// fakeCtl replaces the ctl script with one that writes its arguments to a file.
func fakeCtl(t *testing.T) func() string {
	dir := t.TempDir()
	out := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "ctl")
	os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" >> "+out+"\n"), 0755)
	old := CtlPath
	CtlPath = script
	t.Cleanup(func() { CtlPath = old })
	return func() string {
		b, _ := os.ReadFile(out)
		os.Remove(out)
		return strings.TrimSpace(string(b))
	}
}

func TestScheduleUntil(t *testing.T) {
	calls := fakeCtl(t)
	old := RunDir
	RunDir = t.TempDir()
	defer func() { RunDir = old }()
	d := &Daemon{log: logx.New("", 100, false), cfg: &config.Config{}}
	ctx := context.Background()
	at := func(hhmm string) time.Time {
		tm, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-25 "+hhmm, time.Local)
		return tm
	}
	night := config.Schedule{ID: "s1", Time: "00:00", End: "06:00", Days: "daily", Action: "account", Value: "night", Enabled: true}
	c := &config.Config{
		Main:      config.Main{Profile: "main", Policy: "manual"},
		Profiles:  []config.Profile{{ID: "main", Name: "Main"}, {ID: "night", Name: "Night"}},
		Schedules: []config.Schedule{night},
	}

	d.cfg = c
	r, ok := d.runSchedule(ctx, c, night)
	if !ok || r.Prev != "main" || calls() != "switch night" {
		t.Fatalf("start: %+v %v", r, ok)
	}
	runs := map[string]schedRun{"s1": r}
	saveSchedRuns(runs)
	if got := loadSchedRuns(); got["s1"] != r {
		t.Fatalf("not kept across restarts: %+v", got)
	}

	c.Main.Profile = "night" // ctl switch wrote it
	if d.scheduleBack(ctx, c, runs, at("03:00")); len(runs) != 1 || calls() != "" {
		t.Fatal("switched back inside the window")
	}
	if d.scheduleBack(ctx, c, runs, at("06:00")); len(runs) != 0 || calls() != "switch main" {
		t.Fatal("did not switch back at the end")
	}

	// changed by hand inside the window: that choice stays
	runs = map[string]schedRun{"s1": r}
	c.Main.Profile = "main"
	if d.scheduleBack(ctx, c, runs, at("06:01")); len(runs) != 0 || calls() != "" {
		t.Fatal("overrode a choice made by hand")
	}

	// schedule removed meanwhile: forgotten, no switch
	runs = map[string]schedRun{"s1": r}
	c.Main.Profile, c.Schedules = "night", nil
	if d.scheduleBack(ctx, c, runs, at("07:00")); len(runs) != 0 || calls() != "" {
		t.Fatal("acted on a removed schedule")
	}

	// mode with an end, across midnight
	eve := config.Schedule{ID: "s2", Time: "19:00", End: "01:00", Days: "daily", Action: "policy", Value: "balance", Enabled: true}
	c.Schedules = []config.Schedule{eve}
	r, ok = d.runSchedule(ctx, c, eve)
	if !ok || r.Prev != "manual" || calls() != "policy balance" {
		t.Fatalf("mode start: %+v %v", r, ok)
	}
	d.pol.Store("balance")
	runs = map[string]schedRun{"s2": r}
	if d.scheduleBack(ctx, c, runs, at("23:30")); len(runs) != 1 {
		t.Fatal("mode switched back before midnight")
	}
	if d.scheduleBack(ctx, c, runs, at("01:00")); len(runs) != 0 || calls() != "policy manual" {
		t.Fatal("mode did not switch back")
	}

	// no end: the change stays, nothing to remember
	plain := config.Schedule{ID: "s3", Time: "08:00", Days: "daily", Action: "policy", Value: "auto", Enabled: true}
	if plain.HasEnd() {
		t.Fatal("schedule without end has one")
	}
}
