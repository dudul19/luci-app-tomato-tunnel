package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	dir := t.TempDir()
	p, l := filepath.Join(dir, "p.json"), filepath.Join(dir, "l.json")
	s := Load(p, l)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.Local)
	s.AddProfile(now, "p1", "Hallo", 1000, 100)
	s.AddProfile(now, "p1", "Hallo", 500, 50)
	s.AddProfile(now.AddDate(0, 0, -30), "p2", "Slow", 7, 7)
	s.AddDevices(now, map[string][2]int64{"192.168.1.5": {10, 20}}, map[string][2]string{"192.168.1.5": {"redmi", "aa:bb"}})
	s.AddDevices(now, map[string][2]int64{"192.168.1.5": {15, 30}}, nil)
	s.AddDevices(now, map[string][2]int64{"192.168.1.5": {3, 4}}, nil)
	s.Save(now, true)
	s2 := Load(p, l)
	d := s2.d.Days["2026-11-22"]
	if d.RX != 1500 || d.TX != 150 || d.Profiles["p1"].RX != 1500 {
		t.Fatalf("day %+v", d)
	}
	dv := d.Devices["192.168.1.5"]
	if dv.TX != 18 || dv.RX != 34 || dv.Name != "redmi" {
		t.Fatalf("dev %+v", dv)
	}
	if s2.d.Months["2026-11"].RX != 1500 {
		t.Fatalf("month %+v", s2.d.Months)
	}
	rx, _, st := s2.Period(now, 1)
	if rx != 1500 || st.Day() != 1 {
		t.Fatalf("period %d %v", rx, st)
	}
	if PeriodStart(time.Date(2026, 11, 3, 0, 0, 0, 0, time.Local), 5).Month() != 10 {
		t.Fatal("period start before reset day")
	}
	s2.AddProfile(now.AddDate(0, 0, 100), "p1", "x", 1, 1)
	s2.Save(now.AddDate(0, 0, 100), true)
	if _, ok := Load(p, l).d.Days["2026-11-22"]; ok {
		t.Fatal("old day not pruned")
	}
}
