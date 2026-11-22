package config

import (
	"strings"
	"testing"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/uci"
)

func TestNewOptions(t *testing.T) {
	ss, err := uci.Parse(strings.NewReader(`
config main 'main'
	option restart_modem '1'
	option modem_iface 'wwan0'
	option ip_prefix '114.12., 114.13.'
	option usage_quota_gb '100'

config profile 'p1'
	option name 'old'
	option host 'a'
	option user 'u'
	option expires '2026-11-01'

config profile 'p2'
	option name 'live'
	option host 'b'
	option user 'u'
	option expires '2099-01-01 10:00'
`))
	if err != nil {
		t.Fatal(err)
	}
	c, err := FromSections(ss)
	if err != nil {
		t.Fatal(err)
	}
	ip := c.Main.IP
	if ip.Method != "iface" || !ip.AfterFails || ip.Iface != "wwan0" || len(ip.Prefix) != 2 || c.Main.UsageQuotaGB != 100 {
		t.Fatalf("change ip %+v", ip)
	}
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.Local)
	if !c.Profiles[0].Expired(now) || c.Profiles[1].Expired(now) {
		t.Fatal("expiry")
	}
	if c.Profiles[0].Expired(time.Date(2026, 11, 1, 22, 0, 0, 0, time.Local)) {
		t.Fatal("date-only expiry should last the whole day")
	}
}
