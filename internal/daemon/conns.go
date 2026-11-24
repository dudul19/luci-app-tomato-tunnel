package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/proxy"
)

// The Connections tab: every TCP connection and UDP flow the tunnel carries,
// with the account it went out on, its traffic, the LAN device that opened it
// (from conntrack) and the site name (from the tunnel DNS). It costs nothing
// while nobody looks: the list file is only written for a few seconds after
// `ctl conns` asked for it.

var (
	ConnsFile = RunDir + "/conns.json"
	ConnsWant = RunDir + "/conns.want"
)

type flow struct {
	id      uint64
	proto   string
	dst     atomic.Value // string, host:port
	account string
	start   time.Time
	rx, tx  atomic.Int64
	lrx     int64 // at the last snapshot
	ltx     int64
}

type flowTable struct {
	mu    sync.Mutex
	next  uint64
	m     map[uint64]*flow
	names map[string]nameEntry // IP → DNS name
	last  time.Time
}

type nameEntry struct {
	name string
	exp  time.Time
}

func (t *flowTable) add(proto, dst, account string) *flow {
	f := &flow{proto: proto, account: account, start: time.Now()}
	f.dst.Store(dst)
	t.mu.Lock()
	if t.m == nil {
		t.m = map[uint64]*flow{}
	}
	t.next++
	f.id = t.next
	t.m[f.id] = f
	t.mu.Unlock()
	return f
}

func (t *flowTable) remove(f *flow) {
	t.mu.Lock()
	delete(t.m, f.id)
	t.mu.Unlock()
}

// learnName keeps IP → name from the tunnel DNS answers (display only).
func (t *flowTable) learnName(name string, ips []net.IP, ttl uint32) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	d := time.Duration(ttl) * time.Second
	if d < 30*time.Minute {
		d = 30 * time.Minute // dnsmasq keeps answers in its cache for a while
	}
	exp := time.Now().Add(d)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.names == nil {
		t.names = map[string]nameEntry{}
	}
	if len(t.names) > 20000 {
		now := time.Now()
		for k, e := range t.names {
			if now.After(e.exp) {
				delete(t.names, k)
			}
		}
	}
	for _, ip := range ips {
		t.names[ip.String()] = nameEntry{name, exp}
	}
}

// trackConn counts a TCP connection's bytes; it keeps Raw/Counted so the
// fast copy path stays as it was.
type trackConn struct {
	net.Conn
	f    *flow
	t    *flowTable
	once sync.Once
}

func (c *trackConn) Close() error {
	c.once.Do(func() { c.t.remove(c.f) })
	return c.Conn.Close()
}

func (c *trackConn) Raw() net.Conn {
	if r, ok := c.Conn.(proxy.Counted); ok {
		return r.Raw()
	}
	return c.Conn
}

func (c *trackConn) Counted(read, written int64) {
	c.f.rx.Add(read)
	c.f.tx.Add(written)
	if r, ok := c.Conn.(proxy.Counted); ok {
		r.Counted(read, written)
	}
}

// trackRelay counts a UDP flow; the destination is the first one it sends to.
type trackRelay struct {
	proxy.UDPRelay
	f    *flow
	t    *flowTable
	once sync.Once
}

func (r *trackRelay) Send(dst *net.UDPAddr, data []byte) error {
	if dst != nil && r.f.dst.Load().(string) == "" {
		r.f.dst.Store(dst.String())
	}
	r.f.tx.Add(int64(len(data)))
	return r.UDPRelay.Send(dst, data)
}

func (r *trackRelay) Close() {
	r.once.Do(func() { r.t.remove(r.f) })
	r.UDPRelay.Close()
}

// connsWanted: the page asked for the list in the last 10 seconds.
func connsWanted() bool {
	st, err := os.Stat(ConnsWant)
	return err == nil && time.Since(st.ModTime()) < 10*time.Second
}

type connRow struct {
	Proto   string   `json:"proto"`
	Dst     string   `json:"dst"`
	Host    string   `json:"host,omitempty"`
	Account string   `json:"account"`
	RX      int64    `json:"rx"`
	TX      int64    `json:"tx"`
	RateRX  int64    `json:"rate_rx"`
	RateTX  int64    `json:"rate_tx"`
	Age     int64    `json:"age"`
	Devices []string `json:"devices,omitempty"`
	Seen    bool     `json:"-"`
}

type connsFile struct {
	At     int64     `json:"at"`
	Direct bool      `json:"direct,omitempty"` // TUN goes straight to Xray: no per-connection traffic
	Conns  []connRow `json:"conns"`
}

// writeConns writes the list. Called from statusLoop about once a second.
func (d *Daemon) writeConns() {
	t := &d.flows
	now := time.Now()
	t.mu.Lock()
	secs := now.Sub(t.last).Seconds()
	if t.last.IsZero() || secs <= 0 {
		secs = 1
	}
	t.last = now
	rows := make([]connRow, 0, len(t.m))
	for _, f := range t.m {
		rx, tx := f.rx.Load(), f.tx.Load()
		dst, _ := f.dst.Load().(string)
		r := connRow{Proto: f.proto, Dst: dst, Account: f.account, RX: rx, TX: tx,
			RateRX: int64(float64(rx-f.lrx) / secs), RateTX: int64(float64(tx-f.ltx) / secs), Age: int64(now.Sub(f.start).Seconds())}
		f.lrx, f.ltx = rx, tx
		if h, _, err := net.SplitHostPort(dst); err == nil {
			if e, ok := t.names[h]; ok && now.Before(e.exp) {
				r.Host = e.name
			}
		}
		rows = append(rows, r)
	}
	names := map[string]string{}
	for k, e := range t.names {
		if now.Before(e.exp) {
			names[k] = e.name
		}
	}
	t.mu.Unlock()

	ct := conntrack()
	leases := leaseNames()
	dev := func(ip string) string {
		if n := leases[ip]; n != "" {
			return n + " (" + ip + ")"
		}
		return ip
	}
	direct := d.direct.Load()
	used := map[string]bool{}
	for i := range rows {
		k := rows[i].Proto + " " + rows[i].Dst
		for _, src := range ct[k] {
			rows[i].Devices = appendUniq(rows[i].Devices, dev(src))
		}
		used[k] = true
	}
	if direct {
		// TUN goes straight to Xray, so the tunnel never sees these one by one:
		// list what conntrack knows, on the main account, without traffic numbers.
		acct := ""
		if p := d.pool.Load(); p != nil {
			acct = p.P.Name
		}
		for k, srcs := range ct {
			if used[k] {
				continue
			}
			f := strings.SplitN(k, " ", 2)
			r := connRow{Proto: f[0], Dst: f[1], Account: acct, RX: -1, TX: -1}
			if h, _, err := net.SplitHostPort(f[1]); err == nil {
				r.Host = names[h]
			}
			for _, s := range srcs {
				r.Devices = appendUniq(r.Devices, dev(s))
			}
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].RateRX+rows[i].RateTX != rows[j].RateRX+rows[j].RateTX {
			return rows[i].RateRX+rows[i].RateTX > rows[j].RateRX+rows[j].RateTX
		}
		return rows[i].RX+rows[i].TX > rows[j].RX+rows[j].TX
	})
	if len(rows) > 300 {
		rows = rows[:300]
	}
	b, _ := json.Marshal(connsFile{At: now.Unix(), Direct: direct, Conns: rows})
	tmp := ConnsFile + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		os.Rename(tmp, ConnsFile)
	}
}

func appendUniq(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

// conntrack maps "proto dstip:port" to the LAN sources with an open entry.
func conntrack() map[string][]string {
	out := map[string][]string{}
	f, err := os.Open("/proc/net/nf_conntrack")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 6 {
			continue
		}
		proto := fs[2]
		if proto != "tcp" && proto != "udp" {
			continue
		}
		var src, dst, dport string
		for _, x := range fs[3:] {
			switch {
			case src == "" && strings.HasPrefix(x, "src="):
				src = x[4:]
			case dst == "" && strings.HasPrefix(x, "dst="):
				dst = x[4:]
			case dport == "" && strings.HasPrefix(x, "dport="):
				dport = x[6:]
			}
			if src != "" && dst != "" && dport != "" {
				break
			}
		}
		if strings.Contains(sc.Text(), "TIME_WAIT") || strings.Contains(sc.Text(), " CLOSE ") {
			continue
		}
		si, di := net.ParseIP(src), net.ParseIP(dst)
		if si == nil || di == nil || !si.IsPrivate() || di.IsPrivate() || di.IsLoopback() || di.IsMulticast() || dport == "" {
			continue
		}
		k := proto + " " + net.JoinHostPort(dst, dport)
		out[k] = appendUniq(out[k], src)
	}
	return out
}

// leaseNames: IP → host name from the DHCP leases.
func leaseNames() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(LeasesFile)
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 4 && f[3] != "*" {
			out[f[2]] = f[3]
		}
	}
	return out
}
