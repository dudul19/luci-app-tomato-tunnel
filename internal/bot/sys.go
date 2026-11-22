package bot

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	RunDir    = envOr("TT_RUNDIR", "/var/run/tomato-tunnel")
	CtlPath   = envOr("TT_CTL", "/usr/libexec/tomato-tunnel/ctl")
	BinPath   = envOr("TT_BIN", "/usr/bin/tomato-tunnel")
	StateFile = envOr("TT_BOTSTATE", "/etc/tomato-tunnel/bot-state.json")
	LeaseFile = "/tmp/dhcp.leases"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type Status struct {
	State        string           `json:"state"`
	Message      string           `json:"message"`
	Profile      string           `json:"profile"`
	ProfileName  string           `json:"profile_name"`
	Since        int64            `json:"since"`
	PublicIP     string           `json:"public_ip"`
	Country      string           `json:"country"`
	ISP          string           `json:"isp"`
	PingMS       int64            `json:"ping_ms"`
	RateRX       int64            `json:"rate_rx"`
	RateTX       int64            `json:"rate_tx"`
	Reconnects   int              `json:"reconnects_today"`
	Policy       string           `json:"policy"`
	Switching    string           `json:"switching"`
	LoginFailed  map[string]int64 `json:"login_failed"`
	TodayBytes   int64            `json:"today_bytes"`
	ChangingIP   bool             `json:"changing_ip"`
	LastIPChange int64            `json:"last_ip_change"`
	IPChangeMsg  string           `json:"ip_change_msg"`
	IPChangeOld  string           `json:"ip_change_old"`
	Version      string           `json:"version"`
	Updated      int64            `json:"updated"`
	PID          int              `json:"pid"`
}

func readStatus() Status {
	b, err := os.ReadFile(RunDir + "/status.json")
	if err != nil {
		return Status{State: "stopped"}
	}
	var s Status
	if json.Unmarshal(b, &s) != nil || s.PID <= 0 || syscall.Kill(s.PID, 0) != nil {
		return Status{State: "stopped"}
	}
	return s
}

func ctl(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, CtlPath, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func tailFile(path string, n int) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

type SysInfo struct {
	Model, Firmware, CPU, RAM, Temp, Storage, Uptime string
	UptimeSec                                        float64
}

func uptimeSec() float64 {
	b, _ := os.ReadFile("/proc/uptime")
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func cpuSample() (idle, total uint64) {
	b, _ := os.ReadFile("/proc/stat")
	line, _, _ := strings.Cut(string(b), "\n")
	f := strings.Fields(line)
	for i, x := range f[1:] {
		v, _ := strconv.ParseUint(x, 10, 64)
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return
}

func readSys() SysInfo {
	s := SysInfo{}
	if b, err := os.ReadFile("/tmp/sysinfo/model"); err == nil {
		s.Model = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile("/etc/openwrt_release"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(l, "DISTRIB_DESCRIPTION="); ok {
				s.Firmware = strings.Trim(v, "'\"")
			}
		}
	}
	i1, t1 := cpuSample()
	time.Sleep(400 * time.Millisecond)
	i2, t2 := cpuSample()
	if t2 > t1 {
		s.CPU = fmt.Sprintf("%d %%", 100-int((i2-i1)*100/(t2-t1)))
	}
	mem := map[string]int64{}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			x := strings.Fields(sc.Text())
			if len(x) >= 2 {
				v, _ := strconv.ParseInt(x[1], 10, 64)
				mem[strings.TrimSuffix(x[0], ":")] = v
			}
		}
		f.Close()
	}
	if t := mem["MemTotal"]; t > 0 {
		s.RAM = fmt.Sprintf("%d / %d MB", (t-mem["MemAvailable"])/1024, t/1024)
	}
	for i := 0; i < 4; i++ {
		if b, err := os.ReadFile(fmt.Sprintf("/sys/class/thermal/thermal_zone%d/temp", i)); err == nil {
			if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && v > 0 {
				s.Temp = fmt.Sprintf("%d °C", v/1000)
				break
			}
		}
	}
	for _, p := range []string{"/overlay", "/"} {
		var st syscall.Statfs_t
		if syscall.Statfs(p, &st) == nil && st.Blocks > 0 {
			tot := int64(st.Blocks) * int64(st.Bsize)
			free := int64(st.Bavail) * int64(st.Bsize)
			s.Storage = human(tot-free) + " / " + human(tot)
			break
		}
	}
	s.UptimeSec = uptimeSec()
	s.Uptime = dur(time.Duration(s.UptimeSec) * time.Second)
	return s
}

type Device struct {
	MAC, IP, Name string
	Online        bool
}

func readDevices() []Device {
	arp := map[string]bool{}
	if b, err := os.ReadFile("/proc/net/arp"); err == nil {
		for _, l := range strings.Split(string(b), "\n")[1:] {
			f := strings.Fields(l)
			if len(f) >= 4 && f[2] != "0x0" {
				arp[strings.ToLower(f[3])] = true
			}
		}
	}
	var out []Device
	f, err := os.Open(LeaseFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		x := strings.Fields(sc.Text())
		if len(x) < 4 {
			continue
		}
		name := x[3]
		if name == "*" {
			name = ""
		}
		mac := strings.ToLower(x[1])
		out = append(out, Device{MAC: mac, IP: x[2], Name: name, Online: arp[mac]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return ipLess(out[i].IP, out[j].IP)
	})
	return out
}

func ipLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return a < b
}

type Modem struct {
	Operator, Tech, Signal, State string
}

func readModem(ctx context.Context) (Modem, bool) {
	if _, err := exec.LookPath("mmcli"); err != nil {
		return Modem{}, false
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "mmcli", "-m", "any", "-J").Output()
	if err != nil {
		return Modem{}, false
	}
	var j struct {
		Modem struct {
			Generic struct {
				State  string   `json:"state"`
				Tech   []string `json:"access-technologies"`
				Signal struct {
					Value string `json:"value"`
				} `json:"signal-quality"`
			} `json:"generic"`
			G3 struct {
				Operator string `json:"operator-name"`
			} `json:"3gpp"`
		} `json:"modem"`
	}
	if json.Unmarshal(out, &j) != nil {
		return Modem{}, false
	}
	m := Modem{Operator: j.Modem.G3.Operator, Tech: strings.ToUpper(strings.Join(j.Modem.Generic.Tech, ", ")), State: j.Modem.Generic.State}
	if v := j.Modem.Generic.Signal.Value; v != "" && v != "--" {
		m.Signal = v + " %"
	}
	return m, true
}

type UsageItem struct {
	Name string `json:"name"`
	RX   int64  `json:"rx"`
	TX   int64  `json:"tx"`
}

type UsageDay struct {
	RX       int64                 `json:"rx"`
	TX       int64                 `json:"tx"`
	Profiles map[string]*UsageItem `json:"profiles"`
	Devices  map[string]*UsageItem `json:"devices"`
}

type Usage struct {
	Days   map[string]*UsageDay  `json:"days"`
	Months map[string]*UsageItem `json:"months"`
}

func readUsage(ctx context.Context) Usage {
	var u Usage
	if s, err := ctl(ctx, "usage"); err == nil {
		json.Unmarshal([]byte(s), &u)
	}
	return u
}

func human(n int64) string {
	f := float64(n)
	for _, u := range []string{"B", "KB", "MB", "GB", "TB"} {
		if f < 1024 || u == "TB" {
			if u == "B" {
				return fmt.Sprintf("%d B", n)
			}
			if f < 10 {
				return fmt.Sprintf("%.2f %s", f, u)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return ""
}

func rate(bps int64) string {
	return human(bps) + "/s"
}

func dur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d.Seconds())
	days, h, m := s/86400, s%86400/3600, s%3600/60
	switch {
	case days > 0:
		return fmt.Sprintf("%d d %d h", days, h)
	case h > 0:
		return fmt.Sprintf("%d h %d m", h, m)
	case m > 0:
		return fmt.Sprintf("%d m %d s", m, s%60)
	}
	return fmt.Sprintf("%d s", s)
}
