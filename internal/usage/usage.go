package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Item struct {
	Name string `json:"name,omitempty"`
	MAC  string `json:"mac,omitempty"`
	RX   int64  `json:"rx"`
	TX   int64  `json:"tx"`
}

type Day struct {
	RX       int64            `json:"rx"`
	TX       int64            `json:"tx"`
	Profiles map[string]*Item `json:"profiles,omitempty"`
	Devices  map[string]*Item `json:"devices,omitempty"`
}

type data struct {
	Days    map[string]*Day  `json:"days"`
	Months  map[string]*Item `json:"months"`
	Updated int64            `json:"updated"`
}

type Store struct {
	mu        sync.Mutex
	d         data
	path      string
	live      string
	lastFlush time.Time
	last      map[string][2]int64
}

const (
	KeepDays   = 90
	KeepMonths = 24
)

func readFile(p string) (data, bool) {
	var d data
	b, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(b, &d) != nil {
		return d, false
	}
	return d, true
}

func Load(path, live string) *Store {
	s := &Store{path: path, live: live, lastFlush: time.Now()}
	p, okP := readFile(path)
	l, okL := readFile(live)
	switch {
	case okL && (!okP || l.Updated >= p.Updated):
		s.d = l
	case okP:
		s.d = p
	}
	if s.d.Days == nil {
		s.d.Days = map[string]*Day{}
	}
	if s.d.Months == nil {
		s.d.Months = map[string]*Item{}
	}
	return s
}

func (s *Store) day(now time.Time) *Day {
	k := now.Format("2006-01-02")
	d := s.d.Days[k]
	if d == nil {
		d = &Day{}
		s.d.Days[k] = d
	}
	return d
}

func (s *Store) AddProfile(now time.Time, id, name string, rx, tx int64) {
	if rx <= 0 && tx <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.day(now)
	d.RX += rx
	d.TX += tx
	if d.Profiles == nil {
		d.Profiles = map[string]*Item{}
	}
	it := d.Profiles[id]
	if it == nil {
		it = &Item{}
		d.Profiles[id] = it
	}
	it.Name = name
	it.RX += rx
	it.TX += tx
	mk := now.Format("2006-01")
	m := s.d.Months[mk]
	if m == nil {
		m = &Item{}
		s.d.Months[mk] = m
	}
	m.RX += rx
	m.TX += tx
}

func (s *Store) AddDevices(now time.Time, cur map[string][2]int64, names map[string][2]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = map[string][2]int64{}
	}
	d := s.day(now)
	for ip, c := range cur {
		prev, seen := s.last[ip]
		up, dn := c[0]-prev[0], c[1]-prev[1]
		if !seen || up < 0 || dn < 0 {
			up, dn = c[0], c[1]
		}
		s.last[ip] = c
		if up <= 0 && dn <= 0 {
			continue
		}
		if d.Devices == nil {
			d.Devices = map[string]*Item{}
		}
		it := d.Devices[ip]
		if it == nil {
			it = &Item{}
			d.Devices[ip] = it
		}
		if nm, ok := names[ip]; ok {
			if nm[0] != "" && nm[0] != "*" {
				it.Name = nm[0]
			}
			it.MAC = nm[1]
		}
		it.TX += up
		it.RX += dn
	}
}

func (s *Store) ResetDeviceBase() {
	s.mu.Lock()
	s.last = nil
	s.mu.Unlock()
}

func (s *Store) prune(now time.Time) {
	cut := now.AddDate(0, 0, -KeepDays).Format("2006-01-02")
	for k := range s.d.Days {
		if k < cut {
			delete(s.d.Days, k)
		}
	}
	var ms []string
	for k := range s.d.Months {
		ms = append(ms, k)
	}
	sort.Strings(ms)
	for len(ms) > KeepMonths {
		delete(s.d.Months, ms[0])
		ms = ms[1:]
	}
}

func write(p string, b []byte) error {
	os.MkdirAll(filepath.Dir(p), 0755)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s *Store) Save(now time.Time, persist bool) {
	s.mu.Lock()
	s.prune(now)
	s.d.Updated = now.Unix()
	b, _ := json.Marshal(s.d)
	s.mu.Unlock()
	write(s.live, b)
	if persist || now.Sub(s.lastFlush) >= 10*time.Minute {
		write(s.path, b)
		s.lastFlush = now
	}
}

func (s *Store) Reset() {
	s.mu.Lock()
	s.d = data{Days: map[string]*Day{}, Months: map[string]*Item{}}
	s.mu.Unlock()
	os.Remove(s.path)
	os.Remove(s.live)
}

func (s *Store) Period(now time.Time, resetDay int) (rx, tx int64, start time.Time) {
	start = PeriodStart(now, resetDay)
	k := start.Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	for dk, d := range s.d.Days {
		if dk >= k {
			rx += d.RX
			tx += d.TX
		}
	}
	return
}

func (s *Store) Today(now time.Time) (rx, tx int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d := s.d.Days[now.Format("2006-01-02")]; d != nil {
		return d.RX, d.TX
	}
	return 0, 0
}

func PeriodStart(now time.Time, resetDay int) time.Time {
	if resetDay < 1 || resetDay > 28 {
		resetDay = 1
	}
	t := time.Date(now.Year(), now.Month(), resetDay, 0, 0, 0, 0, now.Location())
	if now.Before(t) {
		t = t.AddDate(0, -1, 0)
	}
	return t
}

var reElem = regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+)\s+counter packets \d+ bytes (\d+)`)

func nftSet(name string) map[string]int64 {
	out, err := exec.Command("nft", "list", "set", "inet", "tomato_tunnel_acct", name).Output()
	if err != nil {
		return nil
	}
	m := map[string]int64{}
	for _, x := range reElem.FindAllStringSubmatch(string(out), -1) {
		var n int64
		for _, c := range x[2] {
			n = n*10 + int64(c-'0')
		}
		m[x[1]] += n
	}
	return m
}

func ReadDevices() (map[string][2]int64, bool) {
	up := nftSet("lan_up")
	dn := nftSet("lan_dn")
	if up == nil && dn == nil {
		return nil, false
	}
	cur := map[string][2]int64{}
	for ip, v := range up {
		c := cur[ip]
		c[0] = v
		cur[ip] = c
	}
	for ip, v := range dn {
		c := cur[ip]
		c[1] = v
		cur[ip] = c
	}
	return cur, true
}

func Leases(path string) map[string][2]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	m := map[string][2]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		x := strings.Fields(sc.Text())
		if len(x) >= 4 {
			m[x[2]] = [2]string{x[3], x[1]}
		}
	}
	return m
}
