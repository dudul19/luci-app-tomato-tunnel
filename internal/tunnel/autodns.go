package tunnel

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strings"
)

var resolvFiles = []string{"/tmp/resolv.conf.d/resolv.conf.auto", "/tmp/resolv.conf.auto"}

func IsAutoResolver(r string) bool { return strings.EqualFold(strings.TrimSpace(r), "auto") }

func AutoResolver(wan string) (ip, from string, ok bool) {

	names := []string{"wan", "wan_4", "wan6"}
	if wan != "" {
		names = []string{wan, wan + "_4", wan + "_6"}
	}
	for _, n := range names {
		if ip, ok = ifaceDNS(n); ok {
			return ip, n, true
		}
	}
	for _, f := range resolvFiles {
		if ip, from, ok = autoFrom(f, wan); ok {
			return
		}
	}
	return "", "", false
}

var ifstatusJSON = func(iface string) ([]byte, error) {
	return exec.Command("ifstatus", iface).Output()
}

func ifaceDNS(iface string) (string, bool) {
	out, err := ifstatusJSON(iface)
	if err != nil {
		return "", false
	}
	var st struct {
		Up  bool     `json:"up"`
		DNS []string `json:"dns-server"`
	}
	if json.Unmarshal(out, &st) != nil || !st.Up {
		return "", false
	}
	v6 := ""
	for _, s := range st.DNS {
		ip := net.ParseIP(strings.TrimSpace(s))
		if ip == nil || ip.IsLoopback() {
			continue
		}
		if ip.To4() != nil {
			return ip.String(), true
		}
		if v6 == "" {
			v6 = ip.String()
		}
	}
	return v6, v6 != ""
}

func autoFrom(file, wan string) (string, string, bool) {
	fh, err := os.Open(file)
	if err != nil {
		return "", "", false
	}
	defer fh.Close()
	sec := ""
	var v6, v6from string
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "# Interface ") {
			sec = strings.TrimSpace(strings.TrimPrefix(l, "# Interface "))
			continue
		}
		f := strings.Fields(l)
		if len(f) < 2 || f[0] != "nameserver" || !autoSection(sec, wan) {
			continue
		}
		ip := net.ParseIP(f[1])
		if ip == nil || ip.IsLoopback() {
			continue
		}
		if ip.To4() != nil {
			return ip.String(), sec, true
		}
		if v6 == "" {
			v6, v6from = ip.String(), sec
		}
	}
	return v6, v6from, v6 != ""
}

func autoSection(sec, wan string) bool {
	if sec == "" || sec == "lan" || sec == "loopback" {
		return false
	}
	if wan == "" {
		return true
	}
	return sec == wan || strings.HasPrefix(sec, wan+"_")
}

func (d *Dialer) effectiveResolver() string {
	r := strings.TrimSpace(d.P.DNSResolv)
	if !IsAutoResolver(r) {
		return r
	}
	ip, from, ok := AutoResolver(d.P.Wan)
	if !ok {
		where := "the modem"
		if d.P.Wan != "" {
			where = d.P.Wan
		}
		d.Log.Warnf("slowdns", "Resolver auto: no DNS server known for %s yet, using 1.1.1.1", where)
		return "1.1.1.1"
	}
	d.Log.Infof("slowdns", "Resolver auto: %s (DNS of %s)", ip, from)
	return ip
}
