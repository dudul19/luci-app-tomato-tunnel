package daemon

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/changeip"
	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/proxy"
	"github.com/dudul19/tomato-tunnel/internal/tunnel"
)

// Balance mode: the main profile runs as usual (supervise), and every other
// ticked profile runs next to it as an extra pool. New connections from the
// LAN go to the pool with the fewest open connections, so the many
// connections of one download or speed test spread over every modem. With
// BalanceSticky one destination stays on one pool for a while instead, for
// banks and games that dislike a changing IP (no speed is added up then).

const (
	stickyFor     = 10 * time.Minute
	mainEvery     = 30 * time.Second // how often Balance looks for a faster main account
	mainMinPeriod = 3 * time.Minute  // and the least time between two changes
)

type balancer struct {
	mu       sync.Mutex
	pools    map[string]*tunnel.Pool // extra pools by profile key
	next     map[string]time.Time    // earliest reconnect per profile key
	fails    map[string]int
	busy     map[string]bool
	sticky   map[string]stick
	base     map[*tunnel.Pool][2]int64
	bad      map[string]int           // failed checks in a row per extra pool
	ping     map[string]int64         // last check in ms
	weights  map[string]int           // Balance weight per profile key
	miss     map[string]int           // ticks in a row a running pool was not wanted
	tcp      map[*tunnel.Pool]int64   // connections handed out and still open
	udp      map[*tunnel.Pool]int64   // UDP flows handed out and still open
	peak     map[*tunnel.Pool]float64 // decaying peak download rate, bytes/s
	rr       int
	lastMain time.Time // last automatic change of the main account
}

type stick struct {
	key string
	exp time.Time
}

// weight of a pool: its Balance weight, 1 to 5, as last read from the
// config, so a change works without a reconnect. Call with b.mu held.
func (b *balancer) weight(p *tunnel.Pool) int64 {
	w, ok := b.weights[ProfileKey(p.P)]
	if !ok {
		w = p.P.Weight
	}
	if w > 1 {
		return int64(w)
	}
	return 1
}

// share is the effective weight: the Balance weight, scaled down for an
// account that is much slower than the fastest one (SlowDNS next to a 4G+
// modem, say), but never below 30 % so it keeps proving its speed.
// Call with b.mu held.
func (b *balancer) share(p *tunnel.Pool) float64 {
	w := float64(b.weight(p))
	var top float64
	for _, v := range b.peak {
		if v > top {
			top = v
		}
	}
	if top < 512*1024 {
		return w // not busy enough to tell fast from slow
	}
	pk, ok := b.peak[p]
	if !ok {
		return w
	}
	f := pk / top
	if f < 0.3 {
		f = 0.3
	}
	return w * f
}

// pick returns the index of the candidate with the fewest handed-out flows
// per unit of share. Call with b.mu held.
func (b *balancer) pick(cs []*tunnel.Pool, count map[*tunnel.Pool]int64) int {
	b.rr++
	best, bestS := -1, 0.0
	for j := range cs {
		i := (j + b.rr) % len(cs)
		sc := float64(count[cs[i]]+1) / b.share(cs[i])
		if best < 0 || sc < bestS {
			best, bestS = i, sc
		}
	}
	return best
}

// balConn gives a connection back to the balancer when it closes. It keeps
// the zero-copy path of the proxy (Raw / Counted) working.
type balConn struct {
	net.Conn
	pool *tunnel.Pool
	once sync.Once
	done func()
}

func (c *balConn) Close() error {
	c.once.Do(c.done)
	return c.Conn.Close()
}

func (c *balConn) Raw() net.Conn {
	if r, ok := c.Conn.(proxy.Counted); ok {
		return r.Raw()
	}
	return c.Conn
}

func (c *balConn) Counted(read, written int64) {
	if r, ok := c.Conn.(proxy.Counted); ok {
		r.Counted(read, written)
	}
}

type balRelay struct {
	proxy.UDPRelay
	pool *tunnel.Pool
	once sync.Once
	done func()
}

func (r *balRelay) Close() {
	r.once.Do(r.done)
	r.UDPRelay.Close()
}

// poolUp lets the UDPGW client talk to one pool.
type poolUp struct{ p *tunnel.Pool }

func (u poolUp) Dial(addr string) (net.Conn, error) { return u.p.Dial(addr) }
func (u poolUp) UDPGWPort() int                     { return u.p.P.UDPGW }

// BalanceStatus is one account in Balance mode.
type BalanceStatus struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Wan     string `json:"wan,omitempty"`
	State   string `json:"state"`
	RateRX  int64  `json:"rate_rx"`
	RateTX  int64  `json:"rate_tx"`
	Streams int64  `json:"streams"`
	UDP     int64  `json:"udp,omitempty"`
	Main    bool   `json:"main,omitempty"`
	Weight  int    `json:"weight,omitempty"`
	Paused  bool   `json:"paused,omitempty"`
	PingMS  int64  `json:"ping_ms,omitempty"`
	Error   string `json:"error,omitempty"`
}

func newBalancer() *balancer {
	return &balancer{pools: map[string]*tunnel.Pool{}, next: map[string]time.Time{}, fails: map[string]int{},
		busy: map[string]bool{}, sticky: map[string]stick{}, base: map[*tunnel.Pool][2]int64{},
		bad: map[string]int{}, ping: map[string]int64{}, weights: map[string]int{},
		tcp: map[*tunnel.Pool]int64{}, miss: map[string]int{}, udp: map[*tunnel.Pool]int64{}, peak: map[*tunnel.Pool]float64{}}
}

// ifaceDev turns an OpenWrt interface (wwan) into its device (wwan0). If the
// interface is down the name is kept, so the bind fails instead of quietly
// leaving through another modem.
func ifaceDev(wan string) string {
	if wan == "" {
		return ""
	}
	if d := changeip.IfaceDevice(wan); d != "" {
		return d
	}
	return wan
}

func (d *Daemon) balancing() bool { return d.policy() == "balance" }

// balanceSet is every profile that takes part, the main one excluded.
func (d *Daemon) balanceSet(c *config.Config) []config.Profile {
	mainKey := ""
	if p := d.pool.Load(); p != nil {
		mainKey = ProfileKey(p.P)
	} else if o := c.Ordered(); len(o) > 0 {
		mainKey = ProfileKey(o[0])
	}
	var out []config.Profile
	now := time.Now()
	for _, p := range c.Profiles {
		if !p.Dashboard || !p.Balance || p.Expired(now) || ProfileKey(p) == mainKey || p.Validate() != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (d *Daemon) balanceLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	was := false
	tick := 0
	for {
		select {
		case <-ctx.Done():
			d.balanceStop()
			return
		case <-t.C:
		}
		on := d.balancing()
		if on != was {
			was = on
			d.retargetHev()
			if on {
				d.log.Infof("balance", "Balance on: the ticked accounts share the traffic")
			} else {
				d.balanceStop()
				d.log.Infof("balance", "Balance off")
			}
		}
		if !on {
			continue
		}
		if tick++; tick%2 == 0 {
			d.balanceCheck()
		}
		if tick%int(mainEvery/(5*time.Second)) == 0 {
			d.balanceMain()
		}
		c, err := config.Load(d.cfgPath)
		if err != nil {
			continue
		}
		want := map[string]config.Profile{}
		for _, p := range d.balanceSet(c) {
			want[ProfileKey(p)] = p
		}
		b := d.bal
		b.mu.Lock()
		for _, p := range c.Profiles {
			b.weights[ProfileKey(p)] = p.Weight
		}
		for k, p := range b.pools {
			_, ok := want[k]
			if ok {
				b.miss[k] = 0
			} else if b.miss[k]++; b.miss[k] < 2 {
				continue // must be missing twice in a row before it leaves
			}
			if !ok || !p.Alive() {
				if ok {
					d.log.Warnf("balance", "%s dropped, reconnecting", p.P.Name)
					b.fails[k]++
					b.next[k] = time.Now().Add(backoff(b.fails[k]))
				} else {
					d.log.Infof("balance", "%s left Balance (unticked, expired or now the main account)", p.P.Name)
				}
				delete(b.miss, k)
				delete(b.pools, k)
				delete(b.base, p)
				go p.Close()
			}
		}
		for k, p := range want {
			if _, ok := b.pools[k]; ok || b.busy[k] || time.Now().Before(b.next[k]) {
				continue
			}
			b.busy[k] = true
			go d.balanceConnect(ctx, p)
		}
		b.mu.Unlock()
	}
}

func backoff(n int) time.Duration {
	s := 10 * time.Duration(n) * time.Second
	if s > time.Minute {
		s = time.Minute
	}
	return s
}

func (d *Daemon) balanceConnect(ctx context.Context, p config.Profile) {
	k := ProfileKey(p)
	np := d.newPool(p)
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	err := np.Connect(cctx, d.cfg.Main.Sessions)
	cancel()
	b := d.bal
	if err != nil {
		np.Close()
		b.mu.Lock()
		b.busy[k] = false
		b.fails[k]++
		b.next[k] = time.Now().Add(backoff(b.fails[k]))
		b.mu.Unlock()
		d.log.Errorf("balance", "%s: %v", p.Name, err)
		d.balErr(k, p, err.Error()) // takes d.mu: never while holding b.mu
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.busy[k] = false
	if !d.balancing() || ctx.Err() != nil {
		np.Close()
		return
	}
	b.fails[k] = 0
	b.pools[k] = np
	b.base[np] = [2]int64{np.RX.Load(), np.TX.Load()}
	via := "system route"
	if p.Wan != "" {
		via = p.Wan
	}
	d.log.Okf("balance", "%s connected via %s, sharing the traffic", p.Name, via)
}

func (d *Daemon) balErr(k string, p config.Profile, msg string) {
	d.set(func(s *Status) {
		for i := range s.Balance {
			if s.Balance[i].Key == k {
				s.Balance[i].Error = msg
				return
			}
		}
		s.Balance = append(s.Balance, BalanceStatus{Key: k, Name: p.Name, Wan: p.Wan, State: "connecting", Error: msg})
	})
}

func (d *Daemon) balanceStop() {
	b := d.bal
	b.mu.Lock()
	ps := b.pools
	b.pools = map[string]*tunnel.Pool{}
	b.base = map[*tunnel.Pool][2]int64{}
	b.sticky = map[string]stick{}
	b.next = map[string]time.Time{}
	b.fails = map[string]int{}
	b.mu.Unlock()
	for _, p := range ps {
		p.Close()
	}
	d.set(func(s *Status) { s.Balance = nil })
}

// balTake removes the running Balance session of a profile so it can become
// the main one without a new connection.
func (d *Daemon) balTake(key string) *tunnel.Pool {
	b := d.bal
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.pools[key]
	if !ok || !p.Alive() || b.bad[key] > 0 {
		return nil
	}
	delete(b.pools, key)
	delete(b.base, p)
	delete(b.miss, key)
	return p
}

// balGive keeps the old main session running as a Balance account.
func (d *Daemon) balGive(p *tunnel.Pool) bool {
	if !d.balancing() || p == nil || !p.Alive() || !p.P.Balance || !p.P.Dashboard {
		return false
	}
	b := d.bal
	b.mu.Lock()
	defer b.mu.Unlock()
	k := ProfileKey(p.P)
	if _, dup := b.pools[k]; dup {
		return false
	}
	b.pools[k] = p
	b.base[p] = [2]int64{p.RX.Load(), p.TX.Load()}
	b.bad[k], b.fails[k] = 0, 0
	return true
}

// balanceMain makes the fastest account the main one. Busy: the one with
// the highest download rate, if it beats the main one by half. Quiet: the
// lowest ping, if 150 ms better. A main account that fails its checks is
// replaced right away by the best healthy one.
func (d *Daemon) balanceMain() {
	main := d.pool.Load()
	if main == nil {
		return
	}
	d.mu.Lock()
	switching, mainPing := d.st.Switching != "", d.st.PingMS
	d.mu.Unlock()
	if switching {
		return
	}
	bad := d.mainBad.Load() || !main.Alive()
	b := d.bal
	b.mu.Lock()
	if !bad && time.Since(b.lastMain) < mainMinPeriod {
		b.mu.Unlock()
		return
	}
	var top float64
	for _, v := range b.peak {
		top = max(top, v)
	}
	busy := top >= 1024*1024
	best, why := "", ""
	var bestPeak float64
	bestPing := int64(-1)
	for k, p := range b.pools {
		if !p.Alive() || b.bad[k] > 0 {
			continue
		}
		if busy {
			if pk := b.peak[p]; pk > bestPeak {
				best, bestPeak = k, pk
			}
		} else if ms := b.ping[k]; ms > 0 && (bestPing < 0 || ms < bestPing) {
			best, bestPing = k, ms
		}
	}
	name := func(k string) string {
		if p := b.pools[k]; p != nil {
			return p.P.Name
		}
		return k
	}
	switch {
	case best == "":
	case bad:
		why = fmt.Sprintf("Balance: %s fails its checks, %s takes over as main. ", main.P.Name, name(best))
	case busy && bestPeak > 1.5*b.peak[main]:
		why = fmt.Sprintf("Balance: %s is faster (%.1f vs %.1f Mbps), making it the main account. ", name(best), bestPeak*8/1e6, b.peak[main]*8/1e6)
	case !busy && bestPing > 0 && (mainPing <= 0 || bestPing+150 < mainPing):
		why = fmt.Sprintf("Balance: %s answers faster (%d vs %d ms), making it the main account. ", name(best), bestPing, mainPing)
	}
	if why == "" {
		b.mu.Unlock()
		return
	}
	b.lastMain = time.Now()
	b.mu.Unlock()
	d.log.Infof("balance", "%s", strings.TrimSpace(why))
	select {
	case d.switchCh <- best:
	default:
	}
}

// balanceCheck pings through every extra pool, like the monitor does for
// the main one, and drops a pool that fails CheckFail times in a row.
func (d *Daemon) balanceCheck() {
	b := d.bal
	b.mu.Lock()
	ps := map[string]*tunnel.Pool{}
	for k, p := range b.pools {
		ps[k] = p
	}
	b.mu.Unlock()
	url, limit := d.cfg.Main.CheckURL, d.cfg.Main.CheckFail
	for k, p := range ps {
		go func(k string, p *tunnel.Pool) {
			ms, err := check(p, url, d.cfg.Main.UnifiedDelay)
			b.mu.Lock()
			defer b.mu.Unlock()
			if err == nil {
				if b.bad[k] > 0 {
					d.log.Okf("balance", "%s answers again, back in Balance", p.P.Name)
				}
				b.bad[k], b.ping[k] = 0, ms
				return
			}
			b.bad[k]++
			if b.bad[k] == 1 {
				d.log.Warnf("balance", "%s: no new connections until it answers again", p.P.Name)
			}
			b.ping[k] = -1
			d.log.Warnf("balance", "%s: ping failed: %s (%d/%d)", p.P.Name, shortErr(err), b.bad[k], limit)
			if b.bad[k] >= limit && b.pools[k] == p {
				b.bad[k] = 0
				p.Close()
			}
		}(k, p)
	}
}

// candidates lists the pools that may take new flows: alive and not
// paused, or, when every one is paused, the paused ones. Call with b.mu held.
func (d *Daemon) candidates(main *tunnel.Pool) []*tunnel.Pool {
	b := d.bal
	var cs, paused []*tunnel.Pool
	if main != nil && main.Alive() {
		if d.mainBad.Load() {
			paused = append(paused, main)
		} else {
			cs = append(cs, main)
		}
	}
	keys := make([]string, 0, len(b.pools))
	for k := range b.pools {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if p := b.pools[k]; p.Alive() {
			if b.bad[k] > 0 {
				paused = append(paused, p)
			} else {
				cs = append(cs, p)
			}
		}
	}
	if len(cs) == 0 {
		return paused
	}
	return cs
}

// balanceDial picks a pool for addr. ok is false when Balance has nothing
// extra to offer and the caller should use the main pool.
func (d *Daemon) balanceDial(main *tunnel.Pool, addr string) (net.Conn, bool, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil, false, nil // the server's own UDPGW: only the main pool knows it
	}
	b := d.bal
	b.mu.Lock()
	if len(b.pools) == 0 {
		b.mu.Unlock()
		return nil, false, nil
	}
	cs := d.candidates(main)
	if len(cs) == 0 {
		b.mu.Unlock()
		return nil, true, tunnel.ErrNoSession
	}
	sticky := d.cfg.Main.BalanceSticky
	now := time.Now()
	first := -1
	if st, ok := b.sticky[host]; sticky && ok && now.Before(st.exp) {
		for i, p := range cs {
			if ProfileKey(p.P) == st.key {
				first = i
			}
		}
	}
	if first < 0 {
		first = b.pick(cs, b.tcp)
	}
	if len(b.sticky) > 4096 {
		for h, st := range b.sticky {
			if now.After(st.exp) {
				delete(b.sticky, h)
			}
		}
	}
	order := append([]*tunnel.Pool{cs[first]}, append(append([]*tunnel.Pool{}, cs[:first]...), cs[first+1:]...)...)
	b.tcp[order[0]]++ // counted before the dial, so parallel dials spread out
	b.mu.Unlock()
	var last error
	for i, p := range order {
		if i > 0 {
			b.mu.Lock()
			b.tcp[order[i-1]]--
			b.tcp[p]++
			b.mu.Unlock()
		}
		conn, err := p.Dial(addr)
		if err == nil {
			if sticky {
				b.mu.Lock()
				b.sticky[host] = stick{key: ProfileKey(p.P), exp: now.Add(stickyFor)}
				b.mu.Unlock()
			}
			pp := p
			return &balConn{Conn: conn, pool: pp, done: func() { b.mu.Lock(); b.tcp[pp]--; b.mu.Unlock() }}, true, nil
		}
		last = err
	}
	b.mu.Lock()
	b.tcp[order[len(order)-1]]--
	b.mu.Unlock()
	return nil, true, last
}

func udpCapable(p *tunnel.Pool) bool {
	return p.IsXray() || p.IsZivpn() || p.P.UDPGW > 0
}

// balanceUDP opens a UDP flow on the pool with the fewest flows per share.
// ok is false when Balance has nothing to offer.
func (d *Daemon) balanceUDP(main *tunnel.Pool, onPacket func(src *net.UDPAddr, data []byte)) (proxy.UDPRelay, bool) {
	b := d.bal
	b.mu.Lock()
	if len(b.pools) == 0 {
		b.mu.Unlock()
		return nil, false
	}
	var cs []*tunnel.Pool
	for _, p := range d.candidates(main) {
		if udpCapable(p) {
			cs = append(cs, p)
		}
	}
	if len(cs) == 0 {
		b.mu.Unlock()
		return nil, false
	}
	first := b.pick(cs, b.udp)
	order := append([]*tunnel.Pool{cs[first]}, append(append([]*tunnel.Pool{}, cs[:first]...), cs[first+1:]...)...)
	b.mu.Unlock()
	for _, p := range order {
		var r proxy.UDPRelay
		switch {
		case p.IsXray():
			if u, err := p.XrayUDP(onPacket); err == nil {
				r = u
			}
		case p.IsZivpn():
			if u, err := p.ZivpnUDP(onPacket); err == nil {
				r = u
			}
		default:
			r = &proxy.UDPGW{Up: poolUp{p}, Log: d.log, OnPacket: onPacket}
		}
		if r == nil {
			continue
		}
		pp := p
		b.mu.Lock()
		b.udp[pp]++
		b.mu.Unlock()
		return &balRelay{UDPRelay: r, pool: pp, done: func() { b.mu.Lock(); b.udp[pp]--; b.mu.Unlock() }}, true
	}
	return nil, false
}

// balanceRates adds the extra pools to the traffic counters and fills the
// per-account list. Called from statusLoop with d.mu held.
func (d *Daemon) balanceRates(main *tunnel.Pool, mainRX, mainTX int64) (rx, tx int64) {
	b := d.bal
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pools) == 0 && !d.balancing() {
		d.st.Balance = nil
		return 0, 0
	}
	decay := func(p *tunnel.Pool, rate int64) {
		pk, ok := b.peak[p]
		if !ok {
			for _, v := range b.peak {
				pk = max(pk, v) // a new account starts as fast as the best
			}
		}
		b.peak[p] = max(float64(rate), pk*0.97)
	}
	live := map[*tunnel.Pool]bool{}
	var list []BalanceStatus
	if main != nil {
		decay(main, mainRX)
		live[main] = true
		list = append(list, BalanceStatus{Key: ProfileKey(main.P), Name: main.P.Name, Wan: main.P.Wan, State: "connected",
			RateRX: mainRX, RateTX: mainTX, Streams: b.tcp[main], UDP: b.udp[main], Main: true, PingMS: d.st.PingMS,
			Weight: int(b.weight(main)), Paused: d.mainBad.Load()})
	}
	keys := make([]string, 0, len(b.pools))
	for k := range b.pools {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	for _, k := range keys {
		p := b.pools[k]
		cur := [2]int64{p.RX.Load(), p.TX.Load()}
		old := b.base[p]
		r, t := cur[0]-old[0], cur[1]-old[1]
		if r < 0 || t < 0 {
			r, t = 0, 0
		}
		b.base[p] = cur
		decay(p, r)
		live[p] = true
		rx += r
		tx += t
		seen[k] = true
		if d.use != nil {
			d.use.AddProfile(time.Now(), k, p.P.Name, r, t)
		}
		list = append(list, BalanceStatus{Key: k, Name: p.P.Name, Wan: p.P.Wan, State: "connected", RateRX: r, RateTX: t, Streams: b.tcp[p], UDP: b.udp[p], PingMS: b.ping[k],
			Weight: int(b.weight(p)), Paused: b.bad[k] > 0})
	}
	for _, x := range d.st.Balance {
		if !x.Main && !seen[x.Key] && x.State == "connecting" {
			list = append(list, x)
		}
	}
	for k := range b.busy {
		if b.busy[k] && !seen[k] {
			found := false
			for _, x := range list {
				found = found || x.Key == k
			}
			if !found {
				list = append(list, BalanceStatus{Key: k, Name: k, State: "connecting"})
			}
		}
	}
	for p := range b.peak {
		if !live[p] {
			delete(b.peak, p)
		}
	}
	for p, n := range b.tcp {
		if n <= 0 && !live[p] {
			delete(b.tcp, p)
		}
	}
	for p, n := range b.udp {
		if n <= 0 && !live[p] {
			delete(b.udp, p)
		}
	}
	d.st.Balance = list
	return rx, tx
}
