package changeip

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
)

type Logger interface {
	Infof(tag, f string, a ...any)
	Okf(tag, f string, a ...any)
	Warnf(tag, f string, a ...any)
	Errorf(tag, f string, a ...any)
}

type Result struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Old   string `json:"old"`
	New   string `json:"new"`
	Tries int    `json:"tries"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

var Methods = map[string]string{
	"iface":  "restart interface",
	"at":     "airplane mode (AT command)",
	"mm":     "ModemManager",
	"hilink": "HiLink / Orbit modem",
	"adb":    "Android phone (ADB)",
}

func Run(ctx context.Context, c config.ChangeIP, wan string, log Logger) Result {
	res := Result{ID: c.ID, Name: c.Name}
	if c.Method == "none" || Methods[c.Method] == "" {
		res.Error = "no Change IP method is set (Settings › Change IP)"
		return res
	}
	dev := wan
	if d := IfaceDevice(c.Iface); d != "" {
		dev = d
	}
	who := ""
	if c.Name != "" {
		who = " " + c.Name
	}
	old := probe(ctx, dev, c.Iface)
	res.Old = old.String()
	for res.Tries = 1; res.Tries <= c.MaxTries; res.Tries++ {
		if ctx.Err() != nil {
			res.Error = "cancelled"
			return res
		}
		log.Warnf("changeip", "Change IP%s: %s%s", who, Methods[c.Method], where(c))
		if err := toggle(ctx, c, log); err != nil {
			res.Error = err.Error()
			log.Errorf("changeip", "Change IP%s failed: %v", who, err)
			return res
		}
		log.Infof("changeip", "Radio on, waiting for network")
		got := waitNew(ctx, dev, c.Iface, old, 60*time.Second)
		res.New = got.String()
		if got.empty() {
			res.Error = "no network after the change"
			log.Errorf("changeip", "No network 60 s after the change")
			continue
		}
		if len(c.Prefix) > 0 && !matchPrefix(got.pub, c.Prefix) && !matchPrefix(got.local, c.Prefix) {
			log.Warnf("changeip", "New IP %s (was %s), not in %s · try %d/%d", res.New, orDash(old.String()), strings.Join(c.Prefix, ", "), res.Tries, c.MaxTries)
			old = got
			continue
		}
		if got == old {
			log.Warnf("changeip", "IP is still %s", res.New)
		} else {
			log.Okf("changeip", "New IP %s OK (was %s)", res.New, orDash(old.String()))
		}
		res.OK, res.Error = true, ""
		return res
	}
	res.Tries = c.MaxTries
	if res.Error == "" {
		res.Error = fmt.Sprintf("no IP matching %s after %d tries", strings.Join(c.Prefix, ", "), c.MaxTries)
	}
	return res
}

// addrs is the public IP seen from outside and the address on the modem
// interface. Mobile operators often hand out a private (CGNAT) address
// such as 10.x, so the IP ranges are checked against both.
type addrs struct{ pub, local string }

func (a addrs) empty() bool { return a.pub == "" && a.local == "" }

func (a addrs) String() string {
	switch {
	case a.pub == "":
		return a.local
	case a.local == "" || a.local == a.pub:
		return a.pub
	}
	return a.pub + " (" + a.local + ")"
}

func probe(ctx context.Context, dev, iface string) addrs {
	return addrs{pub: publicIP(ctx, dev), local: IfaceIP(iface)}
}

// IfaceDevice returns the Linux device of an OpenWrt interface (wwan → usb0).
func IfaceDevice(iface string) string {
	if iface == "" {
		return ""
	}
	out, err := exec.Command("ifstatus", iface).Output()
	if err != nil {
		return ""
	}
	var st struct {
		L3  string `json:"l3_device"`
		Dev string `json:"device"`
	}
	if json.Unmarshal(out, &st) != nil {
		return ""
	}
	if st.L3 != "" {
		return st.L3
	}
	return st.Dev
}

// IfaceUp reports whether the OpenWrt interface is up. An interface that
// netifd does not know (a plain device name, no ifstatus) counts as up, so
// only a real "down" stops anything.
func IfaceUp(iface string) bool {
	if iface == "" {
		return true
	}
	out, err := exec.Command("ifstatus", iface).Output()
	if err != nil {
		return true
	}
	var st struct {
		Up *bool `json:"up"`
	}
	if json.Unmarshal(out, &st) != nil || st.Up == nil {
		return true
	}
	return *st.Up
}

func orDash(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func where(c config.ChangeIP) string {
	switch c.Method {
	case "iface":
		return " " + c.Iface
	case "at":
		return " on " + c.ATPort
	case "mm":
		return " modem " + c.MMModem
	case "hilink":
		return " " + c.HilinkAddr
	case "adb":
		if c.ADBSerial != "" {
			return " " + c.ADBSerial
		}
	}
	return ""
}

// partial parses an IPv4 address that may be cut short ("10.19") and fills
// the missing parts with fill: 10.19 is 10.19.0.0 as a start and
// 10.19.255.255 as an end.
func partial(s string, fill int) (netip.Addr, error) {
	if strings.Contains(s, ":") {
		return netip.ParseAddr(s)
	}
	f := strings.Split(strings.TrimSuffix(s, "."), ".")
	if len(f) == 0 || len(f) > 4 {
		return netip.Addr{}, fmt.Errorf("bad address %q", s)
	}
	var b [4]byte
	for i := range b {
		v := fill
		if i < len(f) {
			n, err := strconv.Atoi(f[i])
			if err != nil || n < 0 || n > 255 || f[i] == "" {
				return netip.Addr{}, fmt.Errorf("bad address %q", s)
			}
			v = n
		}
		b[i] = byte(v)
	}
	return netip.AddrFrom4(b), nil
}

// matchPrefix reports whether ip matches one of the entries. An entry is a
// CIDR block (114.12.0.0/14), a dash range (114.12.0.0-114.15.255.255, or
// short: 10.0-10.19) or a
// plain text prefix (114.12.).
func matchPrefix(ip string, pre []string) bool {
	a, aerr := netip.ParseAddr(ip)
	a = a.Unmap()
	for _, p := range pre {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
		case strings.Contains(p, "/"):
			if n, err := netip.ParsePrefix(p); err == nil && aerr == nil && n.Masked().Contains(a) {
				return true
			}
		case strings.Contains(p, "-"):
			lo, hi, _ := strings.Cut(p, "-")
			l, err1 := partial(strings.TrimSpace(lo), 0)
			h, err2 := partial(strings.TrimSpace(hi), 255)
			if err1 == nil && err2 == nil && aerr == nil && a.BitLen() == l.BitLen() && l.Compare(a) <= 0 && a.Compare(h) <= 0 {
				return true
			}
		case strings.HasPrefix(ip, p):
			return true
		}
	}
	return false
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func run(ctx context.Context, name string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, name, args...).CombinedOutput()
	if err != nil {
		s := strings.TrimSpace(string(out))
		if s == "" {
			s = err.Error()
		}
		if len(s) > 200 {
			s = s[:200]
		}
		return fmt.Errorf("%s: %s", name, s)
	}
	return nil
}

func toggle(ctx context.Context, c config.ChangeIP, log Logger) error {
	w := time.Duration(c.Wait) * time.Second
	switch c.Method {
	case "iface":
		if c.Iface == "" {
			return errors.New("interface is empty")
		}
		run(ctx, "ifdown", c.Iface)
		sleep(ctx, w)
		return run(ctx, "ifup", c.Iface)
	case "at":
		if err := SendAT(c.ATPort, c.ATOff); err != nil {
			return err
		}
		sleep(ctx, w)
		if err := SendAT(c.ATPort, c.ATOn); err != nil {
			return err
		}
		if c.Iface != "" {
			sleep(ctx, 3*time.Second)
			run(ctx, "ifup", c.Iface)
		}
		return nil
	case "mm":
		if _, err := exec.LookPath("mmcli"); err != nil {
			return errors.New("mmcli not found (install modemmanager)")
		}
		if err := run(ctx, "mmcli", "-m", c.MMModem, "--disable"); err != nil {
			return err
		}
		sleep(ctx, w)
		if err := run(ctx, "mmcli", "-m", c.MMModem, "--enable"); err != nil {
			return err
		}
		if c.Iface != "" {
			run(ctx, "ifup", c.Iface)
		}
		return nil
	case "hilink":
		h := &Hilink{Addr: c.HilinkAddr, User: c.HilinkUser, Pass: c.HilinkPass}
		if err := h.DataSwitch(ctx, false); err != nil {
			return err
		}
		sleep(ctx, w)
		return h.DataSwitch(ctx, true)
	case "adb":
		if _, err := exec.LookPath("adb"); err != nil {
			return errors.New("adb not found (install the adb package)")
		}
		base := []string{}
		if c.ADBSerial != "" {
			base = append(base, "-s", c.ADBSerial)
		}
		on := append(append([]string{}, base...), "shell", "cmd", "connectivity", "airplane-mode", "enable")
		off := append(append([]string{}, base...), "shell", "cmd", "connectivity", "airplane-mode", "disable")
		if err := run(ctx, "adb", on...); err != nil {
			legacy := append(append([]string{}, base...), "shell", "settings put global airplane_mode_on 1; am broadcast -a android.intent.action.AIRPLANE_MODE --ez state true")
			if err2 := run(ctx, "adb", legacy...); err2 != nil {
				return err
			}
			sleep(ctx, w)
			return run(ctx, "adb", append(append([]string{}, base...), "shell", "settings put global airplane_mode_on 0; am broadcast -a android.intent.action.AIRPLANE_MODE --ez state false")...)
		}
		sleep(ctx, w)
		return run(ctx, "adb", off...)
	}
	return fmt.Errorf("unknown method %q", c.Method)
}

func waitNew(ctx context.Context, dev, iface string, old addrs, max time.Duration) addrs {
	end := time.Now().Add(max)
	var last addrs
	for time.Now().Before(end) && ctx.Err() == nil {
		a := probe(ctx, dev, iface)
		if !a.empty() {
			last = a
			if a != old {
				return a
			}
		}
		sleep(ctx, 3*time.Second)
	}
	return last
}

func directClient(wan string) *http.Client {
	d := &net.Dialer{Timeout: 4 * time.Second}
	if wan != "" {
		d.Control = func(_, _ string, rc syscall.RawConn) error {
			var e error
			rc.Control(func(fd uintptr) {
				e = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, wan)
			})
			return e
		}
	}
	tr := &http.Transport{DialContext: d.DialContext, Proxy: nil, DisableKeepAlives: true}
	return &http.Client{Transport: tr, Timeout: 6 * time.Second}
}

var reIP = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)

func WanIP(ctx context.Context, wan, iface string) string {
	if ip := publicIP(ctx, wan); ip != "" {
		return ip
	}
	return IfaceIP(iface)
}

func publicIP(ctx context.Context, wan string) string {
	cl := directClient(wan)
	for _, u := range []string{"http://ip-api.com/line/?fields=query", "http://api.ipify.org", "http://ifconfig.me/ip"} {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", "curl/8")
		resp, err := cl.Do(req)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
		resp.Body.Close()
		if s := strings.TrimSpace(string(b)); reIP.MatchString(s) {
			return s
		}
	}
	return ""
}

func IfaceIP(iface string) string {
	if iface == "" {
		return ""
	}
	out, err := exec.Command("ifstatus", iface).Output()
	if err != nil {
		return ""
	}
	var st struct {
		V4 []struct {
			Address string `json:"address"`
		} `json:"ipv4-address"`
	}
	if json.Unmarshal(out, &st) == nil && len(st.V4) > 0 {
		return st.V4[0].Address
	}
	return ""
}

func SendAT(port, cmd string) error {
	if port == "" || cmd == "" {
		return errors.New("AT port or command is empty")
	}
	f, err := os.OpenFile(port, os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("open %s: %v", port, err)
	}
	defer f.Close()
	_, err = f.Write([]byte(cmd + "\r"))
	if err != nil {
		return fmt.Errorf("write %s: %v", port, err)
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}

func queryAT(port, cmd string, wait time.Duration) string {
	f, err := os.OpenFile(port, os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 512)
	for {
		f.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		if n, _ := f.Read(buf); n <= 0 {
			break
		}
	}
	if _, err := f.Write([]byte(cmd + "\r")); err != nil {
		return ""
	}
	var out []byte
	end := time.Now().Add(wait)
	for time.Now().Before(end) {
		f.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, _ := f.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if strings.Contains(string(out), "\nOK") || strings.Contains(string(out), "ERROR") {
				break
			}
		}
	}
	return string(out)
}

type Port struct {
	Dev   string `json:"dev"`
	OK    bool   `json:"ok"`
	Model string `json:"model,omitempty"`
}

func DetectAT() []Port {
	var out []Port
	var devs []string
	for _, g := range []string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/wwan0at*"} {
		m, _ := filepath.Glob(g)
		devs = append(devs, m...)
	}
	for _, d := range devs {
		r := queryAT(d, "ATI", 1500*time.Millisecond)
		p := Port{Dev: d, OK: strings.Contains(r, "OK")}
		if p.OK {
			var lines []string
			for _, l := range strings.Split(r, "\n") {
				l = strings.TrimSpace(l)
				if l == "" || l == "OK" || strings.HasPrefix(l, "ATI") {
					continue
				}
				lines = append(lines, l)
			}
			if len(lines) > 3 {
				lines = lines[:3]
			}
			p.Model = strings.Join(lines, " ")
		}
		out = append(out, p)
	}
	return out
}

type Hilink struct {
	Addr, User, Pass string
	cl               *http.Client
	cookie           string
}

var (
	reSes = regexp.MustCompile(`<SesInfo>([^<]*)</SesInfo>`)
	reTok = regexp.MustCompile(`<TokInfo>([^<]*)</TokInfo>`)
	reSt  = regexp.MustCompile(`<State>(-?\d+)</State>`)
	reErr = regexp.MustCompile(`<code>(\d+)</code>`)
)

func (h *Hilink) url(p string) string {
	a := strings.TrimSuffix(h.Addr, "/")
	if !strings.HasPrefix(a, "http") {
		a = "http://" + a
	}
	return a + p
}

func (h *Hilink) get(ctx context.Context, p string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", h.url(p), nil)
	if h.cookie != "" {
		req.Header.Set("Cookie", h.cookie)
	}
	resp, err := h.cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return string(b), nil
}

func (h *Hilink) token(ctx context.Context) (string, error) {
	s, err := h.get(ctx, "/api/webserver/SesTokInfo")
	if err != nil {
		return "", fmt.Errorf("modem %s: %v", h.Addr, err)
	}
	t := reTok.FindStringSubmatch(s)
	if t == nil {
		return "", fmt.Errorf("modem %s is not a HiLink modem", h.Addr)
	}
	if c := reSes.FindStringSubmatch(s); c != nil && h.cookie == "" {
		h.cookie = c[1]
	}
	return t[1], nil
}

func (h *Hilink) post(ctx context.Context, p, body string) (string, error) {
	tok, err := h.token(ctx)
	if err != nil {
		return "", err
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", h.url(p), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("__RequestVerificationToken", tok)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if h.cookie != "" {
		req.Header.Set("Cookie", h.cookie)
	}
	resp, err := h.cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "SessionID" {
			h.cookie = "SessionID=" + c.Value
		}
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	s := string(b)
	if e := reErr.FindStringSubmatch(s); e != nil {
		return s, fmt.Errorf("modem error %s", e[1])
	}
	return s, nil
}

func hexSHA(s string) string {
	x := sha256.Sum256([]byte(s))
	return hex.EncodeToString(x[:])
}

func (h *Hilink) login(ctx context.Context) error {
	s, err := h.get(ctx, "/api/user/state-login")
	if err != nil {
		return err
	}
	if m := reSt.FindStringSubmatch(s); m == nil || m[1] == "0" || h.Pass == "" {
		return nil
	}
	tok, err := h.token(ctx)
	if err != nil {
		return err
	}
	user := h.User
	if user == "" {
		user = "admin"
	}
	pw := base64.StdEncoding.EncodeToString([]byte(hexSHA(user + base64.StdEncoding.EncodeToString([]byte(hexSHA(h.Pass))) + tok)))
	body := "<?xml version=\"1.0\" encoding=\"UTF-8\"?><request><Username>" + user + "</Username><Password>" + pw + "</Password><password_type>4</password_type></request>"
	req, _ := http.NewRequestWithContext(ctx, "POST", h.url("/api/user/login"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("__RequestVerificationToken", tok)
	req.Header.Set("Cookie", h.cookie)
	resp, err := h.cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "SessionID" {
			h.cookie = "SessionID=" + c.Value
		}
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if e := reErr.FindStringSubmatch(string(b)); e != nil {
		return fmt.Errorf("modem login failed (error %s, check the password)", e[1])
	}
	return nil
}

func (h *Hilink) DataSwitch(ctx context.Context, on bool) error {
	if h.cl == nil {
		h.cl = &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil}}
	}
	if h.cookie == "" {
		if _, err := h.token(ctx); err != nil {
			return err
		}
		if err := h.login(ctx); err != nil {
			return err
		}
	}
	v := "0"
	if on {
		v = "1"
	}
	_, err := h.post(ctx, "/api/dialup/mobile-dataswitch", "<?xml version=\"1.0\" encoding=\"UTF-8\"?><request><dataswitch>"+v+"</dataswitch></request>")
	return err
}

type ADBDevice struct {
	Serial string `json:"serial"`
	State  string `json:"state"`
	Model  string `json:"model,omitempty"`
}

func ADBDevices() ([]ADBDevice, error) {
	if _, err := exec.LookPath("adb"); err != nil {
		return nil, errors.New("adb not installed")
	}
	out, err := exec.Command("adb", "devices", "-l").Output()
	if err != nil {
		return nil, err
	}
	var r []ADBDevice
	for _, l := range strings.Split(string(out), "\n")[1:] {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		d := ADBDevice{Serial: f[0], State: f[1]}
		for _, x := range f[2:] {
			if strings.HasPrefix(x, "model:") {
				d.Model = strings.ReplaceAll(strings.TrimPrefix(x, "model:"), "_", " ")
			}
		}
		r = append(r, d)
	}
	return r, nil
}

type MMModem struct {
	Index string `json:"index"`
	Name  string `json:"name"`
}

var reMM = regexp.MustCompile(`/Modem/(\d+)\s+\[([^\]]*)\]\s*(.*)`)

func MMModems() ([]MMModem, error) {
	if _, err := exec.LookPath("mmcli"); err != nil {
		return nil, errors.New("modemmanager not installed")
	}
	out, err := exec.Command("mmcli", "-L").Output()
	if err != nil {
		return nil, err
	}
	var r []MMModem
	for _, m := range reMM.FindAllStringSubmatch(string(out), -1) {
		r = append(r, MMModem{Index: m[1], Name: strings.TrimSpace(m[2] + " " + m[3])})
	}
	return r, nil
}
