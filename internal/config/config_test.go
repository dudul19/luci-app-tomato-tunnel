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

func TestDashboardAndXray(t *testing.T) {
	ss, err := uci.Parse(strings.NewReader(`
config main 'main'
	option profile 'p3'
	option bypass_id '1'
	list direct_domain '*.KlikBCA.com'

config profile 'p1'
	option name 'a'
	option host 'a'
	option user 'u'
	option order '1'

config profile 'p2'
	option name 'spare'
	option host 'b'
	option user 'u'
	option dashboard '0'
	option order '2'

config profile 'p3'
	option name 'x'
	option type 'xray'
	option host 'sg1.example.com'
	option uuid 'id'
	option x_sec 'reality'
	option x_net 'ws'
	option x_proto 'trojan'
	option pbk 'k'
	option order '3'
`))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := FromSections(ss)
	o := c.Ordered()
	if len(o) != 2 || o[0].Name != "x" || o[1].Name != "a" {
		t.Fatalf("ordered %v", o)
	}
	c.Main.Profile = "p2"
	if o := c.Ordered(); len(o) != 3 || o[0].Name != "spare" {
		t.Fatalf("hidden active profile must still be first: %v", o)
	}
	x := c.Profiles[2]
	if x.XProto != "vless" || x.XNet != "tcp" || x.Port != 443 || x.Label() != "Xray VLESS TCP REALITY" || x.Validate() != nil {
		t.Fatalf("xray %+v %s %v", x, x.Label(), x.Validate())
	}
	if !c.Main.BypassID || len(c.Main.DirectDomains) != 1 || c.Main.DirectDomains[0] != "klikbca.com" {
		t.Fatalf("main %+v", c.Main.DirectDomains)
	}
}
