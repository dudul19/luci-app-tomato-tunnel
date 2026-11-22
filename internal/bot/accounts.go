package bot

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/uci"
)

var profileKeys = []string{"name", "type", "use_payload", "use_tls", "use_slowdns", "enhanced", "insecure", "host", "port", "user", "pass", "bug", "ua", "payload", "proxy", "udpgw", "split_delay", "dns_domain", "dns_resolver", "dns_pubkey", "dns_type", "expires", "dashboard",
	"x_proto", "uuid", "x_net", "x_path", "x_host", "x_service", "x_mode", "x_flow", "x_sec", "sni", "fp", "pbk", "sid", "spx", "vcn", "pcs"}

func isProfileKey(k string) bool {
	for _, x := range profileKeys {
		if x == k {
			return true
		}
	}
	return false
}

var xnets = map[string]bool{"tcp": true, "ws": true, "httpupgrade": true, "grpc": true, "xhttp": true}

func b64dec(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("-", "+", "_", "/").Replace(s)
	s = strings.TrimRight(s, "=")
	return base64.RawStdEncoding.DecodeString(s)
}

func normNet(n string) string {
	n = strings.ToLower(n)
	switch n {
	case "raw", "":
		n = "tcp"
	case "splithttp":
		n = "xhttp"
	}
	if !xnets[n] {
		n = "tcp"
	}
	return n
}

func parseXrayLink(link string) map[string]string {
	link = strings.TrimSpace(link)
	i := strings.Index(link, "://")
	if i < 0 {
		return nil
	}
	proto := strings.ToLower(link[:i])
	rest := link[i+3:]
	if proto != "vless" && proto != "vmess" && proto != "trojan" {
		return nil
	}
	o := map[string]string{"type": "xray", "x_proto": proto}
	if proto == "vmess" && !strings.Contains(strings.SplitN(rest, "#", 2)[0], "@") {
		b, err := b64dec(strings.SplitN(rest, "#", 2)[0])
		if err != nil {
			return nil
		}
		var j map[string]any
		if json.Unmarshal(b, &j) != nil {
			return nil
		}
		str := func(k string) string {
			switch v := j[k].(type) {
			case string:
				return v
			case float64:
				return strconv.FormatFloat(v, 'f', -1, 64)
			}
			return ""
		}
		o["host"], o["port"], o["uuid"] = str("add"), str("port"), str("id")
		if o["port"] == "" {
			o["port"] = "443"
		}
		o["name"] = str("ps")
		if o["name"] == "" {
			o["name"] = o["host"]
		}
		o["x_net"] = normNet(str("net"))
		o["x_sec"] = "none"
		if str("tls") == "tls" {
			o["x_sec"] = "tls"
		}
		if o["x_net"] == "grpc" {
			o["x_service"] = str("path")
		} else if p := str("path"); p != "" {
			o["x_path"] = p
		}
		set(o, "x_host", str("host"))
		set(o, "sni", str("sni"))
		set(o, "fp", str("fp"))
		o["insecure"] = "1"
		if o["host"] == "" || o["uuid"] == "" {
			return nil
		}
		return o
	}
	hash := ""
	if i := strings.Index(rest, "#"); i >= 0 {
		hash, rest = rest[i+1:], rest[:i]
	}
	q := ""
	if i := strings.Index(rest, "?"); i >= 0 {
		q, rest = rest[i+1:], rest[:i]
	}
	rest = strings.TrimSuffix(rest, "/")
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return nil
	}
	cred, hp := rest[:at], rest[at+1:]
	if c, err := url.PathUnescape(cred); err == nil {
		cred = c
	}
	host, port := hp, "443"
	if strings.HasPrefix(hp, "[") {
		if j := strings.Index(hp, "]"); j > 0 {
			host = hp[1:j]
			if p, ok := strings.CutPrefix(hp[j+1:], ":"); ok {
				port = p
			}
		}
	} else if h, p, ok := strings.Cut(hp, ":"); ok {
		host, port = h, p
	}
	if _, err := strconv.Atoi(port); err != nil || host == "" || cred == "" {
		return nil
	}
	a, _ := url.ParseQuery(q)
	o["host"], o["port"] = host, port
	if proto == "trojan" {
		o["pass"] = cred
	} else {
		o["uuid"] = cred
	}
	o["x_net"] = normNet(a.Get("type"))
	sec := a.Get("security")
	switch {
	case sec == "reality" || sec == "tls":
		o["x_sec"] = sec
	case proto == "trojan" && sec != "none":
		o["x_sec"] = "tls"
	default:
		o["x_sec"] = "none"
	}
	set(o, "x_path", a.Get("path"))
	set(o, "x_host", a.Get("host"))
	set(o, "x_service", a.Get("serviceName"))
	if m := a.Get("mode"); m == "auto" || m == "packet-up" || m == "stream-up" || m == "stream-one" {
		o["x_mode"] = m
	}
	set(o, "x_flow", a.Get("flow"))
	sni := a.Get("sni")
	if sni == "" {
		sni = a.Get("peer")
	}
	set(o, "sni", sni)
	for _, k := range []string{"fp", "pbk", "sid", "spx", "pcs", "vcn"} {
		set(o, k, a.Get(k))
	}
	o["insecure"] = "1"
	if a.Get("allowInsecure") == "0" || a.Get("insecure") == "0" {
		o["insecure"] = "0"
	}
	if h, err := url.PathUnescape(hash); err == nil {
		hash = h
	}
	o["name"] = hash
	if o["name"] == "" {
		o["name"] = host
	}
	return o
}

func set(o map[string]string, k, v string) {
	if v != "" {
		o[k] = v
	}
}

var (
	reSSHURL = regexp.MustCompile(`(?i)^ssh://([^:@\s]+):([^@\s]+)@([^:\s/]+)(?::(\d+))?`)
	reHPUP   = regexp.MustCompile(`^([^\s:@]+):(\d+)@([^:\s]+):(\S+)$`)
	reUPHP   = regexp.MustCompile(`^([^\s:@]+):([^\s@]+)@([^\s:]+):(\d+)$`)
	reDate   = regexp.MustCompile(`(\d{4})-(\d\d)-(\d\d)`)
	reDMY    = regexp.MustCompile(`(\d{1,2})[ -]([A-Za-z]{3})[a-z]*[ -,]*(\d{4})`)
	reNoise  = regexp.MustCompile("[•➤►▶❖◇◆■□●○✦✧⭐️*`|]")
)

var months = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}

func sshMode(port string) map[string]string {
	if port == "443" {
		return map[string]string{"use_payload": "1", "use_tls": "1"}
	}
	return map[string]string{"use_payload": "1", "use_tls": "0"}
}

func parseSSHText(t string) map[string]string {
	t = strings.TrimSpace(t)
	if m := reSSHURL.FindStringSubmatch(t); m != nil {
		p := m[4]
		if p == "" {
			p = "22"
		}
		return map[string]string{"type": "ssh", "name": m[3], "user": m[1], "pass": m[2], "host": m[3], "port": p, "use_payload": "0", "use_tls": "0"}
	}
	if m := reHPUP.FindStringSubmatch(t); m != nil {
		o := map[string]string{"type": "ssh", "name": m[1], "host": m[1], "port": m[2], "user": m[3], "pass": m[4]}
		for k, v := range sshMode(m[2]) {
			o[k] = v
		}
		return o
	}
	if m := reUPHP.FindStringSubmatch(t); m != nil {
		o := map[string]string{"type": "ssh", "name": m[3], "host": m[3], "port": m[4], "user": m[1], "pass": m[2]}
		for k, v := range sshMode(m[4]) {
			o[k] = v
		}
		return o
	}
	lines := strings.Split(t, "\n")
	get := func(re *regexp.Regexp) string {
		for _, l := range lines {
			l = reNoise.ReplaceAllString(l, " ")
			if m := re.FindStringSubmatch(l); m != nil {
				return strings.TrimSpace(m[1])
			}
		}
		return ""
	}
	host := get(regexp.MustCompile(`(?i)^\s*(?:host|domain|hostname|server|ip)\s*[:=]\s*([^\s,]+)`))
	user := get(regexp.MustCompile(`(?i)^\s*(?:username|user)\s*[:=]\s*(\S+)`))
	pass := get(regexp.MustCompile(`(?i)^\s*(?:password|pass)\s*[:=]\s*(\S+)`))
	if host == "" || user == "" || pass == "" {
		return nil
	}
	ns := get(regexp.MustCompile(`(?i)^\s*(?:ns|ns host|nameserver|ns domain)\s*[:=]\s*(\S+)`))
	pk := get(regexp.MustCompile(`(?i)^\s*(?:pub ?key|public key|key)\s*[:=]\s*([0-9a-f]{64})`))
	ex := get(regexp.MustCompile(`(?i)^\s*(?:expired|expires|exp|expiry|aktif sampai|masa aktif)\s*(?:on)?\s*[:=]\s*(.+)$`))
	wss := get(regexp.MustCompile(`(?i)^\s*port\s*(?:ws\s*)?(?:ssl|tls)[^:=]*[:=]\s*(\d+)`))
	ws := get(regexp.MustCompile(`(?i)^\s*port\s*(?:ssh\s*)?(?:ws|websocket|http)[^:=]*[:=]\s*(\d+)`))
	anyp := get(regexp.MustCompile(`(?i)^\s*port[^:=]*[:=]\s*(\d+)`))
	o := map[string]string{"type": "ssh", "name": strings.ToUpper(strings.Split(host, ".")[0]), "host": host, "user": user, "pass": pass}
	switch {
	case ws != "":
		o["use_payload"], o["use_tls"], o["port"] = "1", "0", ws
	case wss != "":
		o["use_payload"], o["use_tls"], o["port"] = "1", "1", wss
	default:
		o["use_payload"], o["use_tls"], o["port"] = "0", "0", anyp
		if anyp == "" {
			o["port"] = "22"
		}
	}
	if m := reDate.FindString(ex); m != "" {
		o["expires"] = m
	} else if m := reDMY.FindStringSubmatch(ex); m != nil {
		for i, mo := range months {
			if strings.EqualFold(m[2], mo) {
				d, _ := strconv.Atoi(m[1])
				o["expires"] = fmt.Sprintf("%s-%02d-%02d", m[3], i+1, d)
			}
		}
	}
	if ns != "" && pk != "" && ws == "" && wss == "" {
		o["use_payload"], o["use_tls"], o["use_slowdns"] = "0", "0", "1"
		o["dns_domain"], o["dns_pubkey"] = ns, pk
		delete(o, "port")
	}
	return o
}

func parseAccounts(t string) []map[string]string {
	var out []map[string]string
	for _, w := range strings.Fields(t) {
		lw := strings.ToLower(w)
		switch {
		case strings.HasPrefix(lw, "tomatotunnel://"):
			if b, err := b64dec(w[len("tomatotunnel://"):]); err == nil {
				var m map[string]any
				if json.Unmarshal(b, &m) == nil {
					o := map[string]string{}
					for k, v := range m {
						if isProfileKey(k) {
							o[k] = fmt.Sprint(v)
						}
					}
					if o["host"] != "" || o["dns_domain"] != "" {
						out = append(out, o)
					}
				}
			}
		case strings.HasPrefix(lw, "vless://"), strings.HasPrefix(lw, "vmess://"), strings.HasPrefix(lw, "trojan://"):
			if o := parseXrayLink(w); o != nil {
				out = append(out, o)
			}
		}
	}
	if len(out) == 0 {
		if o := parseSSHText(t); o != nil {
			out = append(out, o)
		}
	}
	return out
}

func loadProfiles(path string) ([]config.Profile, error) {
	c, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return c.Profiles, nil
}

func profileKey(p config.Profile) string {
	if p.Alias != "" {
		return p.Alias
	}
	return p.ID
}

func typeLabel(p config.Profile) string {
	switch p.Type {
	case "zivpn":
		return "ZiVPN"
	case "xray":
		n := map[string]string{"vless": "VLESS", "vmess": "VMess", "trojan": "Trojan"}[p.XProto]
		net := map[string]string{"tcp": "TCP", "ws": "WS", "httpupgrade": "HU", "grpc": "gRPC", "xhttp": "XHTTP"}[p.XNet]
		s := n + " " + net
		if p.XSec == "tls" {
			s += " TLS"
		} else if p.XSec == "reality" {
			s += " REALITY"
		}
		return s
	}
	if p.UseDNS {
		return "SlowDNS"
	}
	s := "SSH"
	if p.UsePayload {
		s += " WS"
	}
	if p.UseTLS {
		s += " SSL"
	}
	return s
}

func shortType(p config.Profile) string {
	switch p.Type {
	case "zivpn":
		return "ZiVPN"
	case "xray":
		return map[string]string{"vless": "VLESS", "vmess": "VMess", "trojan": "Trojan"}[p.XProto]
	}
	if p.UseDNS {
		return "SlowDNS"
	}
	if p.UsePayload {
		return "SSH WS"
	}
	return "SSH"
}

func server(p config.Profile) string {
	if p.UseDNS && p.Type == "ssh" {
		return p.DNSDomain
	}
	return p.Host
}

func rawProfiles(path string) []map[string]string {
	ss, err := uci.LoadFile(path)
	if err != nil {
		return nil
	}
	var out []map[string]string
	for _, s := range ss {
		if s.Type != "profile" {
			continue
		}
		o := map[string]string{}
		for _, k := range profileKeys {
			if v, ok := s.Options[k]; ok && v != "" {
				o[k] = v
			}
		}
		if v, ok := s.Options["order"]; ok {
			o["order"] = v
		}
		out = append(out, o)
	}
	return out
}

func backupJSON(path, version string) []byte {
	list := rawProfiles(path)
	for _, o := range list {
		delete(o, "order")
	}
	b, _ := json.MarshalIndent(map[string]any{"app": "tomato-tunnel", "version": version, "exported": time.Now().UTC().Format(time.RFC3339), "profiles": list}, "", "  ")
	return b
}

func parseBackup(b []byte) ([]map[string]string, error) {
	var j struct {
		Profiles []map[string]any `json:"profiles"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		var arr []map[string]any
		if json.Unmarshal(b, &arr) != nil {
			return nil, fmt.Errorf("not a Tomato Tunnel backup")
		}
		j.Profiles = arr
	}
	var out []map[string]string
	for _, p := range j.Profiles {
		o := map[string]string{}
		for k, v := range p {
			if isProfileKey(k) && v != nil {
				o[k] = fmt.Sprint(v)
			}
		}
		if len(o) > 0 {
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no profiles in that file")
	}
	return out, nil
}

func dedupeKey(o map[string]string) string {
	return o["name"] + "|" + o["host"] + "|" + o["user"]
}

func addProfiles(path string, list []map[string]string) ([]string, error) {
	have := map[string]bool{}
	maxOrder := 0
	used := map[string]bool{}
	ss, _ := uci.LoadFile(path)
	for _, s := range ss {
		if s.Type != "profile" {
			continue
		}
		used[s.Name] = true
		o := map[string]string{"name": s.Get("name", ""), "host": s.Get("host", ""), "user": s.Get("user", "")}
		have[dedupeKey(o)] = true
		if n, _ := strconv.Atoi(s.Get("order", "0")); n > maxOrder {
			maxOrder = n
		}
	}
	var ids []string
	for _, v := range list {
		if have[dedupeKey(v)] {
			continue
		}
		n := 1
		for used["p"+strconv.Itoa(n)] {
			n++
		}
		id := "p" + strconv.Itoa(n)
		used[id] = true
		maxOrder++
		defs := map[string]string{"name": "New profile", "use_payload": "1", "use_tls": "0", "use_slowdns": "0", "enhanced": "0", "insecure": "1", "port": "80", "udpgw": "7300", "order": strconv.Itoa(maxOrder)}
		switch v["type"] {
		case "zivpn":
			defs["use_payload"], defs["port"] = "0", "5667"
			delete(defs, "udpgw")
		case "xray":
			defs["use_payload"], defs["port"] = "0", "443"
			delete(defs, "udpgw")
		}
		if err := uciRun("set", pkgName+"."+id+"=profile"); err != nil {
			return ids, err
		}
		for k, d := range defs {
			val := d
			if x, ok := v[k]; ok && x != "" && k != "order" {
				val = x
			}
			uciRun("set", pkgName+"."+id+"."+k+"="+val)
		}
		for k, x := range v {
			if _, ok := defs[k]; ok || !isProfileKey(k) || x == "" {
				continue
			}
			uciRun("set", pkgName+"."+id+"."+k+"="+x)
		}
		ids = append(ids, id)
		have[dedupeKey(v)] = true
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if exec.Command("uci", "-q", "get", pkgName+".main.profile").Run() != nil {
		uciRun("set", pkgName+".main.profile="+ids[0])
	} else if out, _ := exec.Command("uci", "-q", "get", pkgName+".main.profile").Output(); strings.TrimSpace(string(out)) == "" {
		uciRun("set", pkgName+".main.profile="+ids[0])
	}
	return ids, uciCommit()
}

func setProfileOpt(id, k, v string) error {
	if v == "" {
		uciRun("delete", pkgName+"."+id+"."+k)
	} else if err := uciRun("set", pkgName+"."+id+"."+k+"="+v); err != nil {
		return err
	}
	return uciCommit()
}

func deleteProfile(id string) error {
	if err := uciRun("delete", pkgName+"."+id); err != nil {
		return err
	}
	return uciCommit()
}
