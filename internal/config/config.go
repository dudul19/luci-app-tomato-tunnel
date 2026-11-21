package config

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dudul19/tomato-tunnel/internal/uci"
)

const DefaultPath = "/etc/config/tomato-tunnel"

type Main struct {
	Enabled       bool
	Profile       string
	NetMode       string
	SocksListen   string
	SocksPort     int
	RedirPort     int
	DNSPort       int
	LanIface      string
	WanIface      string
	Sessions      int
	CheckURL      string
	CheckInterval int
	CheckFail     int
	Failover      bool
	RestartModem  bool
	ModemIface    string
	SSHCompat     string
	DNSMode       string
	DNSServer     string
	ManageDnsmasq bool
	Bypass        []string
	LogLevel      string
	UpdateRepo    string
	UpdateChannel string
	UpdateCheck   bool
}

type Profile struct {
	ID      string
	Alias   string
	Name    string
	fileIdx int

	UsePayload bool
	UseTLS     bool
	UseDNS     bool
	Enhanced   bool
	Insecure   bool
	Mode       string
	Host       string
	Port       int
	User       string
	Pass       string
	Bug        string
	UA         string
	Payload    string
	Proxy      string
	UDPGW      int
	SplitDelay int
	DNSDomain  string
	DNSResolv  string
	DNSPubkey  string
	DNSType    string
	Order      int
}

type Config struct {
	Main     Main
	Profiles []Profile
}

func Load(path string) (*Config, error) {
	ss, err := uci.LoadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := FromSections(ss)
	if err != nil {
		return nil, err
	}
	c.aliasAnonymous(path)
	return c, nil
}

// LuCI refers to unnamed sections by uci's generated ids (cfgXXXXXX);
// "uci -X" lists them in file order.
func (c *Config) aliasAnonymous(path string) {
	if !filepath.IsAbs(path) || filepath.Dir(path) != "/etc/config" {
		return
	}
	out, err := exec.Command("uci", "-X", "show", filepath.Base(path)).Output()
	if err != nil {
		return
	}
	var ids []string
	for _, l := range strings.Split(string(out), "\n") {
		if i := strings.Index(l, "=profile"); i > 0 && i == len(l)-len("=profile") && !strings.Contains(l[:i], "=") {
			parts := strings.SplitN(l[:i], ".", 2)
			if len(parts) == 2 && !strings.Contains(parts[1], ".") {
				ids = append(ids, parts[1])
			}
		}
	}
	for i := range c.Profiles {
		if k := c.Profiles[i].fileIdx; k < len(ids) {
			c.Profiles[i].Alias = ids[k]
		}
	}
}

func FromSections(ss []*uci.Section) (*Config, error) {
	c := &Config{Main: defaults()}
	idx := 0
	for _, s := range ss {
		switch s.Type {
		case "main":
			readMain(&c.Main, s)
		case "profile":
			p := readProfile(s)
			if p.Order == 0 {
				p.Order = 1000 + idx
			}
			p.fileIdx = idx
			idx++
			c.Profiles = append(c.Profiles, p)
		}
	}
	sort.SliceStable(c.Profiles, func(i, j int) bool { return c.Profiles[i].Order < c.Profiles[j].Order })
	return c, nil
}

func defaults() Main {
	return Main{
		NetMode: "tun", SocksListen: "127.0.0.1", SocksPort: 1080, RedirPort: 12345, DNSPort: 5453,
		LanIface: "br-lan", Sessions: 1,
		CheckURL: "http://cp.cloudflare.com/generate_204", CheckInterval: 5, CheckFail: 3,
		Failover: true, ModemIface: "wwan", SSHCompat: "auto", DNSMode: "tunnel", DNSServer: "1.1.1.1",
		ManageDnsmasq: true, LogLevel: "info", UpdateRepo: "dudul19/tomato-tunnel", UpdateChannel: "stable", UpdateCheck: true,
	}
}

func readMain(m *Main, s *uci.Section) {
	m.Enabled = b(s, "enabled", m.Enabled)
	m.Profile = s.Get("profile", m.Profile)
	m.NetMode = oneOf(s.Get("net_mode", m.NetMode), m.NetMode, "tun", "redirect", "socks")
	m.SocksListen = s.Get("socks_listen", m.SocksListen)
	m.SocksPort = n(s, "socks_port", m.SocksPort, 1, 65535)
	m.RedirPort = n(s, "redir_port", m.RedirPort, 1, 65535)
	m.DNSPort = n(s, "dns_port", m.DNSPort, 1, 65535)
	m.LanIface = s.Get("lan_iface", m.LanIface)
	m.WanIface = s.Get("wan_iface", m.WanIface)
	m.Sessions = n(s, "sessions", m.Sessions, 1, 4)
	m.CheckURL = s.Get("check_url", m.CheckURL)
	m.CheckInterval = n(s, "check_interval", m.CheckInterval, 2, 600)
	m.CheckFail = n(s, "check_fail", m.CheckFail, 1, 20)
	m.Failover = b(s, "failover", m.Failover)
	m.RestartModem = b(s, "restart_modem", m.RestartModem)
	m.ModemIface = s.Get("modem_iface", m.ModemIface)
	m.SSHCompat = oneOf(s.Get("ssh_compat", m.SSHCompat), m.SSHCompat, "auto", "legacy", "modern")
	m.DNSMode = oneOf(s.Get("dns_mode", m.DNSMode), m.DNSMode, "tunnel", "off")
	m.DNSServer = s.Get("dns_server", m.DNSServer)
	m.ManageDnsmasq = b(s, "manage_dnsmasq", m.ManageDnsmasq)
	m.LogLevel = oneOf(s.Get("log_level", m.LogLevel), m.LogLevel, "info", "debug", "error")
	m.UpdateRepo = s.Get("update_repo", m.UpdateRepo)
	m.UpdateChannel = oneOf(s.Get("update_channel", m.UpdateChannel), m.UpdateChannel, "stable", "beta")
	m.UpdateCheck = b(s, "update_check", m.UpdateCheck)
	for _, v := range s.Lists["bypass"] {
		v = strings.TrimSpace(strings.SplitN(v, "#", 2)[0])
		if v != "" {
			m.Bypass = append(m.Bypass, v)
		}
	}
}

func readProfile(s *uci.Section) Profile {
	p := Profile{
		ID:         s.Name,
		Name:       s.Get("name", s.Name),
		Mode:       oneOf(s.Get("mode", ""), "", "direct", "payload", "ssl", "ws", "wss", "dns"),
		Enhanced:   b(s, "enhanced", false),
		Insecure:   b(s, "insecure", true),
		Host:       strings.TrimSpace(s.Get("host", "")),
		User:       s.Get("user", ""),
		Pass:       s.Get("pass", ""),
		Bug:        strings.TrimSpace(s.Get("bug", "")),
		UA:         s.Get("ua", ""),
		Payload:    s.Get("payload", ""),
		Proxy:      strings.TrimSpace(s.Get("proxy", "")),
		UDPGW:      n(s, "udpgw", 7300, 0, 65535),
		SplitDelay: n(s, "split_delay", 1000, 0, 10000),
		DNSDomain:  strings.TrimSpace(s.Get("dns_domain", "")),
		DNSResolv:  strings.TrimSpace(s.Get("dns_resolver", "1.1.1.1")),
		DNSPubkey:  strings.TrimSpace(s.Get("dns_pubkey", "")),
		DNSType:    oneOf(strings.ToLower(s.Get("dns_type", "auto")), "auto", "auto", "txt", "null", "cname", "ns", "mx", "srv"),
		Order:      n(s, "order", 0, 0, 100000),
	}

	switch p.Mode {
	case "payload", "ws":
		p.UsePayload = true
	case "ssl":
		p.UseTLS = true
	case "wss":
		p.UseTLS, p.UsePayload = true, true
	case "dns":
		p.UseDNS = true
	}
	p.UsePayload = b(s, "use_payload", p.UsePayload)
	p.UseTLS = b(s, "use_tls", p.UseTLS)
	p.UseDNS = b(s, "use_slowdns", p.UseDNS)
	if !p.UsePayload {
		p.Enhanced = false
	}
	p.Mode = p.Label()
	p.Port = n(s, "port", DefaultPort(p.UseTLS, p.UsePayload), 1, 65535)
	if p.UA == "" {
		p.UA = "Dalvik/2.1.0 (Linux; U; Android 13)"
	}
	return p
}

func DefaultPort(useTLS, usePayload bool) int {
	switch {
	case useTLS:
		return 443
	case usePayload:
		return 80
	}
	return 22
}

func (p Profile) Label() string {
	l := "SSH"
	if !p.UsePayload && !p.UseTLS && !p.UseDNS {
		return "SSH Direct"
	}
	if p.UsePayload {
		l += " + Payload"
	}
	if p.UseTLS {
		l += " + TLS"
	}
	if p.Enhanced && p.UsePayload {
		l += " + Enhanced"
	}
	if p.UseDNS {
		l += " + SlowDNS"
	}
	return l
}

func (p Profile) Validate() error {
	if p.Host == "" && !p.UseDNS {
		return fmt.Errorf("profile %q: server host is empty", p.Name)
	}
	if p.User == "" {
		return fmt.Errorf("profile %q: username is empty", p.Name)
	}
	if p.UseDNS && (p.DNSDomain == "" || len(p.DNSPubkey) != 64) {
		return fmt.Errorf("profile %q: SlowDNS needs NS domain and a 64-hex public key", p.Name)
	}
	if p.Proxy != "" {
		if _, _, err := net.SplitHostPort(p.Proxy); err != nil {
			return fmt.Errorf("profile %q: remote proxy must be host:port", p.Name)
		}
	}
	return nil
}

func (c *Config) Ordered() []Profile {
	if len(c.Profiles) == 0 {
		return nil
	}
	start := 0
	for i, p := range c.Profiles {
		if p.ID == c.Main.Profile || (p.Alias != "" && p.Alias == c.Main.Profile) {
			start = i
			break
		}
	}
	out := append([]Profile{}, c.Profiles[start:]...)
	return append(out, c.Profiles[:start]...)
}

func (c *Config) Find(id string) (Profile, bool) {
	for _, p := range c.Profiles {
		if p.ID == id || p.Name == id || (p.Alias != "" && p.Alias == id) {
			return p, true
		}
	}
	return Profile{}, false
}

func b(s *uci.Section, k string, def bool) bool {
	v, ok := s.Options[k]
	if !ok {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "on", "yes", "true", "enabled":
		return true
	}
	return false
}

func n(s *uci.Section, k string, def, lo, hi int) int {
	v, ok := s.Options[k]
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	x, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || x < lo || x > hi {
		return def
	}
	return x
}

func oneOf(v, def string, allowed ...string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return def
}
