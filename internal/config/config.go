package config

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
	Policy        string
	RestartModem  bool
	ModemIface    string
	SSHCompat     string
	DNSMode       string
	DNSServer     string
	ManageDnsmasq bool
	Bypass        []string
	BypassID      bool
	BalanceSticky bool // Balance: keep each destination on one account
	UnifiedDelay  bool // ping = second request on a warm connection, handshakes left out (like Clash unified-delay)
	FallbackBack  bool // Fallback: go back to the chosen account once it works again
	BlockQUIC     bool // drop UDP 443 so browsers and apps use TCP
	BlockAds      bool
	DirectDomains []string
	LogLevel      string
	UpdateRepo    string
	UpdateChannel string
	UpdateCheck   bool
	ExpiryWarn    int
	UsageResetDay int
	UsageQuotaGB  int
	IP            ChangeIP   // first modem, plus the global triggers
	IPTarget      string     // auto, all or a modem ID
	Modems        []ChangeIP // every modem with a method, IP first
}

type ChangeIP struct {
	ID         string
	Name       string
	Method     string
	Iface      string
	ATPort     string
	ATOff      string
	ATOn       string
	MMModem    string
	HilinkAddr string
	HilinkUser string
	HilinkPass string
	ADBSerial  string
	Wait       int
	AfterFails bool
	AfterN     int
	EveryH     int
	Button     bool
	Prefix     []string
	MaxTries   int
}

type Profile struct {
	ID      string
	Alias   string
	Name    string
	Type    string
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
	Expires    string
	Dashboard  bool
	Wan        string // OpenWrt interface this account goes out on ("" = system route)
	Balance    bool   // joins the Balance mode
	Weight     int    // Balance: share of new connections, 1–5

	XProto   string
	UUID     string
	XNet     string
	XPath    string
	XHost    string
	XService string
	XMode    string
	XFlow    string
	XSec     string
	SNI      string
	FP       string
	PBK      string
	SID      string
	SpiderX  string
	VCN      string
	PCS      string
}

func (p Profile) ExpiryTime() (time.Time, bool) {
	if p.Expires == "" {
		return time.Time{}, false
	}
	for _, f := range []string{"2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(f, p.Expires, time.Local); err == nil {
			if f == "2006-01-02" {
				t = t.Add(24*time.Hour - time.Second)
			}
			return t, true
		}
	}
	return time.Time{}, false
}

func (p Profile) Expired(now time.Time) bool {
	t, ok := p.ExpiryTime()
	return ok && now.After(t)
}

type Config struct {
	Main      Main
	Profiles  []Profile
	Schedules []Schedule
}

// Schedule: at Time (HH:MM) on Days, use an account (Action "account",
// Value = profile ID), a profile selection mode (Action "policy"), or keep
// the tunnel off or on from Time to End (Action "tunnel", Value "off"/"on").
type Schedule struct {
	ID      string
	Time    string
	End     string // Action "tunnel": end of the window (HH:MM)
	Days    string // daily, weekdays, weekend, or mon,tue,…
	Action  string
	Value   string
	Enabled bool
}

// Due says whether the schedule fires at t (same minute, matching day).
func (s Schedule) Due(t time.Time) bool {
	if !s.Enabled || s.Value == "" || s.Action == "tunnel" || t.Format("15:04") != s.Time {
		return false
	}
	wd := t.Weekday()
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
	var extra []ChangeIP
	for _, s := range ss {
		switch s.Type {
		case "main":
			readMain(&c.Main, s)
		case "modem":
			ip := modemDefaults()
			readModem(&ip, s)
			ip.ID, ip.Name = s.Name, s.Get("name", s.Name)
			if ip.ID == "" || ip.ID == "main" {
				ip.ID = fmt.Sprintf("modem%d", len(extra)+2)
			}
			if ip.Method != "none" {
				extra = append(extra, ip)
			}
		case "schedule":
			c.Schedules = append(c.Schedules, Schedule{ID: s.Name, Time: strings.TrimSpace(s.Get("time", "")), Days: s.Get("days", "daily"),
				Action: s.Get("action", "account"), Value: s.Get("value", ""), End: strings.TrimSpace(s.Get("end", "")), Enabled: s.Get("enabled", "1") != "0"})
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
	if c.Main.IP.Method != "none" {
		c.Main.Modems = append(c.Main.Modems, c.Main.IP)
	}
	c.Main.Modems = append(c.Main.Modems, extra...)
	return c, nil
}

func modemDefaults() ChangeIP {
	return ChangeIP{Method: "none", Iface: "wwan", ATPort: "/dev/ttyUSB2", ATOff: "AT+CFUN=4", ATOn: "AT+CFUN=1", MMModem: "0",
		HilinkAddr: "192.168.8.1", HilinkUser: "admin", Wait: 5, MaxTries: 5}
}

// Modem returns the modem with this ID.
func (m *Main) Modem(id string) (ChangeIP, bool) {
	for _, x := range m.Modems {
		if x.ID == id {
			return x, true
		}
	}
	return ChangeIP{}, false
}

func defaults() Main {
	return Main{
		NetMode: "tun", SocksListen: "127.0.0.1", SocksPort: 1080, RedirPort: 12345, DNSPort: 5453,
		LanIface: "br-lan", Sessions: 1,
		CheckURL: "http://cp.cloudflare.com/generate_204", CheckInterval: 5, CheckFail: 3,
		Failover: true, Policy: "fallback", ModemIface: "wwan", SSHCompat: "auto", DNSMode: "tunnel", DNSServer: "1.1.1.1",
		ManageDnsmasq: true, LogLevel: "info", UpdateRepo: "dudul19/tomato-tunnel", UpdateChannel: "stable", UpdateCheck: true,
		ExpiryWarn: 3, UsageResetDay: 1,
		IP: ChangeIP{Method: "none", Iface: "wwan", ATPort: "/dev/ttyUSB2", ATOff: "AT+CFUN=4", ATOn: "AT+CFUN=1", MMModem: "0",
			HilinkAddr: "192.168.8.1", HilinkUser: "admin", Wait: 5, AfterN: 3, Button: true, MaxTries: 5},
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
	pol := "manual"
	if m.Failover {
		pol = "fallback"
	}
	m.Policy = oneOf(s.Get("policy", pol), pol, "manual", "fallback", "auto", "balance")
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
	m.ExpiryWarn = n(s, "expiry_warn", m.ExpiryWarn, 0, 60)
	m.UsageResetDay = n(s, "usage_reset_day", m.UsageResetDay, 1, 28)
	m.UsageQuotaGB = n(s, "usage_quota_gb", m.UsageQuotaGB, 0, 1000000)
	ip := &m.IP
	if m.RestartModem {
		ip.Method, ip.AfterFails = "iface", true
		if m.ModemIface != "" {
			ip.Iface = m.ModemIface
		}
	}
	readModem(ip, s)
	ip.ID, ip.Name = "main", s.Get("ip_name", "Modem 1")
	ip.AfterFails = b(s, "ip_after_fails", ip.AfterFails)
	ip.AfterN = n(s, "ip_after_n", ip.AfterN, 1, 50)
	ip.Button = b(s, "ip_button", ip.Button)
	m.IPTarget = s.Get("ip_target", "auto")
	m.BypassID = b(s, "bypass_id", m.BypassID)
	m.BalanceSticky = b(s, "balance_sticky", false)
	m.UnifiedDelay = b(s, "unified_delay", true)
	m.FallbackBack = b(s, "fallback_return", true)
	m.BlockQUIC = b(s, "block_quic", false)
	m.BlockAds = b(s, "block_ads", m.BlockAds)
	for _, v := range s.Lists["direct_domain"] {
		v = strings.ToLower(strings.TrimSpace(strings.SplitN(v, "#", 2)[0]))
		v = strings.TrimPrefix(strings.TrimPrefix(v, "*."), ".")
		if v != "" && !strings.ContainsAny(v, " /\\\"") {
			m.DirectDomains = append(m.DirectDomains, v)
		}
	}
	for _, v := range s.Lists["bypass"] {
		if v = bypassEntry(v); v != "" {
			m.Bypass = append(m.Bypass, v)
		}
	}
}

// bypassEntry keeps one "direct" line only when it is an IP or CIDR. The list goes
// into ip rules and an nft set, where a single typo would make the whole set fail to
// load (redirect mode then cannot start), so anything else is dropped.
func bypassEntry(v string) string {
	v = strings.TrimSpace(strings.SplitN(v, "#", 2)[0])
	if v == "" {
		return ""
	}
	if _, n, err := net.ParseCIDR(v); err == nil {
		return n.String()
	}
	if ip := net.ParseIP(v); ip != nil {
		return ip.String()
	}
	return ""
}

var reDash = regexp.MustCompile(`\s*-\s*`)

// readModem reads the per-modem Change IP options. The main section holds
// the first modem, every "modem" section one more, with the same names.
func readModem(ip *ChangeIP, s *uci.Section) {
	ip.Method = oneOf(s.Get("ip_method", ip.Method), ip.Method, "none", "iface", "at", "mm", "hilink", "adb")
	ip.Iface = s.Get("ip_iface", ip.Iface)
	ip.ATPort = s.Get("at_port", ip.ATPort)
	ip.ATOff = s.Get("at_off", ip.ATOff)
	ip.ATOn = s.Get("at_on", ip.ATOn)
	ip.MMModem = s.Get("mm_modem", ip.MMModem)
	ip.HilinkAddr = s.Get("hilink_addr", ip.HilinkAddr)
	ip.HilinkUser = s.Get("hilink_user", ip.HilinkUser)
	ip.HilinkPass = s.Get("hilink_pass", ip.HilinkPass)
	ip.ADBSerial = s.Get("adb_serial", ip.ADBSerial)
	if _, set := s.Options["ip_iface"]; !set && (ip.Method == "hilink" || ip.Method == "adb") {
		ip.Iface = "" // only used to check the IP; "wwan" may be another modem
	}
	ip.Wait = n(s, "ip_wait", ip.Wait, 1, 120)
	ip.EveryH = n(s, "ip_every_h", ip.EveryH, 0, 168)
	ip.MaxTries = n(s, "ip_max_tries", ip.MaxTries, 1, 20)
	ip.Prefix = nil
	pre := reDash.ReplaceAllString(s.Get("ip_prefix", ""), "-")
	for _, v := range strings.FieldsFunc(pre, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		ip.Prefix = append(ip.Prefix, v)
	}
}

func readProfile(s *uci.Section) Profile {
	p := Profile{
		ID:         s.Name,
		Name:       s.Get("name", s.Name),
		Type:       oneOf(s.Get("type", "ssh"), "ssh", "ssh", "zivpn", "xray"),
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
		Expires:    strings.TrimSpace(s.Get("expires", "")),
		Dashboard:  b(s, "dashboard", true),
		Wan:        s.Get("wan", ""),
		Balance:    b(s, "balance", true),
		Weight:     n(s, "balance_weight", 1, 1, 5),
		XProto:     oneOf(strings.ToLower(s.Get("x_proto", "vless")), "vless", "vless", "vmess", "trojan"),
		UUID:       strings.TrimSpace(s.Get("uuid", "")),
		XNet:       oneOf(strings.ToLower(s.Get("x_net", "tcp")), "tcp", "tcp", "ws", "httpupgrade", "grpc", "xhttp"),
		XPath:      strings.TrimSpace(s.Get("x_path", "")),
		XHost:      strings.TrimSpace(s.Get("x_host", "")),
		XService:   strings.TrimSpace(s.Get("x_service", "")),
		XMode:      oneOf(s.Get("x_mode", "auto"), "auto", "auto", "packet-up", "stream-up", "stream-one"),
		XFlow:      oneOf(s.Get("x_flow", ""), "", "", "xtls-rprx-vision"),
		XSec:       oneOf(strings.ToLower(s.Get("x_sec", "none")), "none", "none", "tls", "reality"),
		SNI:        strings.TrimSpace(s.Get("sni", "")),
		FP:         oneOf(strings.ToLower(s.Get("fp", "chrome")), "chrome", "chrome", "firefox", "safari", "ios", "android", "edge", "random", "randomized"),
		PBK:        strings.TrimSpace(s.Get("pbk", "")),
		SID:        strings.TrimSpace(s.Get("sid", "")),
		SpiderX:    strings.TrimSpace(s.Get("spx", "")),
		VCN:        strings.TrimSpace(s.Get("vcn", "")),
		PCS:        strings.TrimSpace(s.Get("pcs", "")),
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
	if p.Type == "zivpn" || p.Type == "xray" {
		p.UsePayload, p.UseTLS, p.UseDNS, p.Enhanced, p.Proxy, p.UDPGW = false, false, false, false, "", 0
	}
	if p.Type == "xray" {
		p.normalizeXray()
	}
	p.Mode = p.Label()
	def := DefaultPort(p.UseTLS, p.UsePayload)
	switch p.Type {
	case "zivpn":
		def = 5667
	case "xray":
		def = 443
		if p.XSec == "none" {
			def = 80
		}
	}
	p.Port = n(s, "port", def, 1, 65535)
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

func (p *Profile) normalizeXray() {
	if p.XSec == "reality" {
		p.XProto = "vless"
		if p.XNet == "ws" || p.XNet == "httpupgrade" {
			p.XNet = "tcp"
		}
	}
	if p.XProto != "vless" || p.XNet != "tcp" || p.XSec == "none" {
		p.XFlow = ""
	}
}

var xNetName = map[string]string{"tcp": "TCP", "ws": "WS", "httpupgrade": "HTTPUpgrade", "grpc": "gRPC", "xhttp": "XHTTP"}
var xProtoName = map[string]string{"vless": "VLESS", "vmess": "VMess", "trojan": "Trojan"}

func (p Profile) XrayLabel() string {
	l := xProtoName[p.XProto] + " " + xNetName[p.XNet]
	switch p.XSec {
	case "tls":
		l += " TLS"
	case "reality":
		l += " REALITY"
	}
	return l
}

func (p Profile) Label() string {
	if p.Type == "zivpn" {
		return "ZiVPN (UDP)"
	}
	if p.Type == "xray" {
		return "Xray " + p.XrayLabel()
	}
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
	if p.Type == "xray" {
		if p.Host == "" {
			return fmt.Errorf("profile %q: server address is empty", p.Name)
		}
		if p.XProto == "trojan" && p.Pass == "" {
			return fmt.Errorf("profile %q: Trojan password is empty", p.Name)
		}
		if p.XProto != "trojan" && p.UUID == "" {
			return fmt.Errorf("profile %q: UUID is empty", p.Name)
		}
		if p.XSec == "reality" && p.PBK == "" {
			return fmt.Errorf("profile %q: REALITY needs the public key (pbk)", p.Name)
		}
		return nil
	}
	if p.Type == "zivpn" {
		if p.Host == "" {
			return fmt.Errorf("profile %q: server host is empty", p.Name)
		}
		if p.Pass == "" {
			return fmt.Errorf("profile %q: ZiVPN password is empty", p.Name)
		}
		return nil
	}
	if p.Host == "" && !p.UseDNS {
		return fmt.Errorf("profile %q: server host is empty", p.Name)
	}
	if p.User == "" {
		return fmt.Errorf("profile %q: username is empty", p.Name)
	}
	if p.UseDNS && (p.DNSDomain == "" || len(p.DNSPubkey) != 64) {
		return fmt.Errorf("profile %q: SlowDNS needs NS domain and a 64-hex public key", p.Name)
	}
	if p.Proxy != "" && !strings.Contains(strings.ToLower(p.Proxy), "[rotate=") && !strings.Contains(strings.ToLower(p.Proxy), "[random=") {
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
	out := []Profile{c.Profiles[start]}
	for _, p := range append(append([]Profile{}, c.Profiles[start+1:]...), c.Profiles[:start]...) {
		if p.Dashboard {
			out = append(out, p)
		}
	}
	return out
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
