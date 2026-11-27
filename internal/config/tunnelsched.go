package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func hm(s string) (int, bool) {
	p := strings.Split(strings.TrimSpace(s), ":")
	if len(p) != 2 {
		return 0, false
	}
	h, e1 := strconv.Atoi(p[0])
	m, e2 := strconv.Atoi(p[1])
	if e1 != nil || e2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func (s Schedule) IsTunnel() bool {
	if !s.Enabled || s.Action != "tunnel" || (s.Value != "on" && s.Value != "off") {
		return false
	}
	a, ok1 := hm(s.Time)
	b, ok2 := hm(s.End)
	return ok1 && ok2 && a != b
}

func (s Schedule) HasEnd() bool {
	if s.Action == "tunnel" {
		return false
	}
	a, ok1 := hm(s.Time)
	b, ok2 := hm(s.End)
	return ok1 && ok2 && a != b
}

func (s Schedule) dayMatch(wd time.Weekday) bool {
	switch s.Days {
	case "", "daily":
		return true
	case "weekdays":
		return wd >= time.Monday && wd <= time.Friday
	case "weekend":
		return wd == time.Saturday || wd == time.Sunday
	}
	short := strings.ToLower(wd.String()[:3])
	for _, d := range strings.Split(s.Days, ",") {
		if strings.TrimSpace(strings.ToLower(d)) == short {
			return true
		}
	}
	return false
}

func (s Schedule) InWindow(t time.Time) bool {
	a, _ := hm(s.Time)
	b, _ := hm(s.End)
	now := t.Hour()*60 + t.Minute()
	if a < b {
		return s.dayMatch(t.Weekday()) && now >= a && now < b
	}

	if s.dayMatch(t.Weekday()) && now >= a {
		return true
	}
	return s.dayMatch(t.AddDate(0, 0, -1).Weekday()) && now < b
}

func TunnelWanted(c *Config, t time.Time) string {
	hasOn, inOn := false, false
	for _, s := range c.Schedules {
		if !s.IsTunnel() {
			continue
		}
		if s.Value == "off" && s.InWindow(t) {
			return "off"
		}
		if s.Value == "on" {
			hasOn = true
			inOn = inOn || s.InWindow(t)
		}
	}
	switch {
	case inOn:
		return "on"
	case hasOn:
		return "off"
	}
	return ""
}

var dowNum = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

func (s Schedule) cronDays(shift int) string {
	var ds []int
	switch s.Days {
	case "", "daily":
		return "*"
	case "weekdays":
		ds = []int{1, 2, 3, 4, 5}
	case "weekend":
		ds = []int{6, 0}
	default:
		for _, d := range strings.Split(s.Days, ",") {
			if n, ok := dowNum[strings.TrimSpace(strings.ToLower(d))]; ok {
				ds = append(ds, n)
			}
		}
	}
	if len(ds) == 0 {
		return "*"
	}
	var out []string
	for _, d := range ds {
		out = append(out, strconv.Itoa((d+shift)%7))
	}
	return strings.Join(out, ",")
}

const CronMark = "# tomato-tunnel schedule"

func TunnelCron(c *Config, ctl string) []string {
	var out []string
	for _, s := range c.Schedules {
		if !s.IsTunnel() {
			continue
		}
		a, _ := hm(s.Time)
		b, _ := hm(s.End)
		first, last := "stop", "start"
		if s.Value == "on" {
			first, last = "start", "stop"
		}
		shift := 0
		if b <= a {
			shift = 1
		}
		out = append(out,
			fmt.Sprintf("%d %d * * %s %s schedule-%s %s", a%60, a/60, s.cronDays(0), ctl, first, CronMark),
			fmt.Sprintf("%d %d * * %s %s schedule-%s %s", b%60, b/60, s.cronDays(shift), ctl, last, CronMark))
	}
	return out
}
