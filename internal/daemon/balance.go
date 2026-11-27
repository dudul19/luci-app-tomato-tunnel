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

const (
	stickyFor     = 10 * time.Minute
	mainEvery     = 30 * time.Second
	mainMinPeriod = 10 * time.Minute
	mainWins      = 3
	pauseAfter    = 2
)

type balancer struct {
	mu       sync.Mutex
	pools    map[string]*tunnel.Pool
	next     map[string]time.Time
	fails    map[string]int
	busy     map[string]bool
	sticky   map[string]stick
	base     map[*tunnel.Pool][2]int64
	bad      map[string]int
	ping     map[string]int64
	weights  map[string]int
	miss     map[string]int
	tcp      map[*tunnel.Pool]int64
	udp      map[*tunnel.Pool]int64
	peak     map[*tunnel.Pool]float64
	rrT, rrU map[*tunnel.Pool]float64
	ifDown   map[string]bool
	rr       int
	lastMain time.Time
	winKey   string
	winRuns  int
}

type stick struct {
	key string
	exp time.Time
}

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

func (b *balancer) share(p *tunnel.Pool) float64 { return float64(b.weight(p)) }

func (b *balancer) pick(cs []*tunnel.Pool, rr map[*tunnel.Pool]float64) int {
	best, total := -1, 0.0
	for i, p := range cs {
		w := b.share(p)
		rr[p] += w
		total += w
		if best < 0 || rr[p] > rr[cs[best]] {
			best = i
		}
	}
	rr[cs[best]] -= total
	return best
}

var keepTogether = map[string][]string{
	"youtube": {"youtube.com", "youtu.be", "youtube-nocookie.com", "googlevideo.com", "ytimg.com", "ggpht.com",
		"youtubei.googleapis.com", "youtube.googleapis.com", "yt3.googleusercontent.com"},
}

func (d *Daemon) siteGroup(host string) string {
	name := strings.ToLower(host)
	if ip := net.ParseIP(host); ip != nil {
		name = d.flows.nameOf(ip.String())
	}
	if name == "" {
		return ""
	}
	for g, list := range keepTogether {
		if directMatch(name, list) {
			return g
		}
	}
	return ""
}

func (b *balancer) stuck(cs []*tunnel.Pool, key string, now time.Time) int {
	st, ok := b.sticky[key]
	if !ok || !now.Before(st.exp) {
		return -1
	}
	for i, p := range cs {
		if ProfileKey(p.P) == st.key {
			return i
		}
	}
	return -1
}

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

type poolUp struct{ p *tunnel.Pool }

func (u poolUp) Dial(addr string) (net.Conn, error) { return u.p.Dial(addr) }
func (u poolUp) UDPGWPort() int                     { return u.p.P.UDPGW }

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
		tcp: map[*tunnel.Pool]int64{}, miss: map[string]int{}, udp: map[*tunnel.Pool]int64{}, peak: map[*tunnel.Pool]float64{},
		rrT: map[*tunnel.Pool]float64{}, rrU: map[*tunnel.Pool]float64{}, ifDown: map[string]bool{}}
}

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
		d.balanceIfaces()
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
				continue
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
			if _, ok := b.pools[k]; ok || b.busy[k] || time.Now().Before(b.next[k]) || b.ifDown[k] {
				continue
			}
			b.busy[k] = true
			go d.balanceConnect(ctx, p)
		}
		b.mu.Unlock()
	}
}

func (d *Daemon) balanceIfaces() {
	b := d.bal
	b.mu.Lock()
	type acct struct {
		key, name, wan string
		pool           *tunnel.Pool
	}
	var list []acct
	for k, p := range b.pools {
		if p.P.Wan != "" {
			list = append(list, acct{k, p.P.Name, p.P.Wan, p})
		}
	}
	wanted, names := map[string]string{}, map[string]string{}
	for _, p := range d.cfg.Profiles {
		if p.Wan != "" {
			wanted[ProfileKey(p)], names[ProfileKey(p)] = p.Wan, p.Name
		}
	}
	b.mu.Unlock()
	if main := d.pool.Load(); main != nil && main.P.Wan != "" && !changeip.IfaceUp(main.P.Wan) {
		if !d.mainBad.Swap(true) {
			d.log.Warnf("balance", "%s: interface %s is down, moving new connections to the other accounts", main.P.Name, main.P.Wan)
			go d.balanceMain()
		}
	}
	down := map[string]bool{}
	for k, wan := range wanted {
		if !changeip.IfaceUp(wan) {
			down[k] = true
		}
	}
	b.mu.Lock()
	var stop []*tunnel.Pool
	for _, a := range list {
		if down[a.key] && b.pools[a.key] == a.pool {
			delete(b.pools, a.key)
			delete(b.base, a.pool)
			delete(b.miss, a.key)
			stop = append(stop, a.pool)
			d.log.Warnf("balance", "%s: interface %s is down, closing its connections", a.name, a.wan)
		}
	}
	for k := range b.ifDown {
		if !down[k] {
			d.log.Infof("balance", "%s: interface %s is up again, reconnecting", names[k], wanted[k])
			b.next[k], b.fails[k] = time.Time{}, 0
		}
	}
	b.ifDown = down
	b.mu.Unlock()
	for _, p := range stop {
		go p.Close()
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
		d.balErr(k, p, err.Error())
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.busy[k] = false
	if !d.balancing() || ctx.Err() != nil {
		np.Close()
		return
	}
	if _, dup := b.pools[k]; dup {

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
	if busy && !bad {
		b.mu.Unlock()
		return
	}
	best, why := "", ""
	bestPing := int64(-1)
	for k, p := range b.pools {
		if !p.Alive() || b.bad[k] > 0 {
			continue
		}
		if ms := b.ping[k]; ms > 0 && (bestPing < 0 || ms < bestPing) {
			best, bestPing = k, ms
		}
	}
	if best == "" && bad {
		for k, p := range b.pools {
			if p.Alive() && b.bad[k] == 0 {
				best = k
				break
			}
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
	case bestPing > 0 && (mainPing <= 0 || bestPing+150 < mainPing):

		if b.winKey == best {
			b.winRuns++
		} else {
			b.winKey, b.winRuns = best, 1
		}
		if b.winRuns >= mainWins {
			why = fmt.Sprintf("Balance: %s answers faster (%d vs %d ms), making it the main account. ", name(best), bestPing, mainPing)
		}
	default:
		b.winKey, b.winRuns = "", 0
	}
	if why == "" {
		b.mu.Unlock()
		return
	}
	b.lastMain = time.Now()
	b.winKey, b.winRuns = "", 0
	b.mu.Unlock()
	d.log.Infof("balance", "%s", strings.TrimSpace(why))
	select {
	case d.switchCh <- best:
	default:
	}
}

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
			ms, err := check(context.Background(), p, url, d.cfg.Main.UnifiedDelay)
			b.mu.Lock()
			defer b.mu.Unlock()
			if err == nil {
				if b.bad[k] >= pauseAfter {
					d.log.Okf("balance", "%s answers again, back in Balance", p.P.Name)
				}
				b.bad[k], b.ping[k] = 0, ms
				return
			}
			b.bad[k]++
			if b.bad[k] == pauseAfter {
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
			if b.bad[k] >= pauseAfter {
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

func (d *Daemon) balanceDial(main *tunnel.Pool, addr string) (net.Conn, bool, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil, false, nil
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
	skey := host
	if g := d.siteGroup(host); g != "" {
		sticky, skey = true, "@"+g
	}
	now := time.Now()
	first := -1
	if sticky {
		first = b.stuck(cs, skey, now)
	}
	if first < 0 {
		first = b.pick(cs, b.rrT)
	}
	if len(b.sticky) > 4096 {
		for h, st := range b.sticky {
			if now.After(st.exp) {
				delete(b.sticky, h)
			}
		}
	}
	order := append([]*tunnel.Pool{cs[first]}, append(append([]*tunnel.Pool{}, cs[:first]...), cs[first+1:]...)...)
	b.tcp[order[0]]++
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
				b.sticky[skey] = stick{key: ProfileKey(p.P), exp: now.Add(stickyFor)}
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

func (d *Daemon) balanceUDP(main *tunnel.Pool, dst *net.UDPAddr, onPacket func(src *net.UDPAddr, data []byte)) (proxy.UDPRelay, bool) {
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
	skey, now := "", time.Now()
	if dst != nil {
		if g := d.siteGroup(dst.IP.String()); g != "" {
			skey = "@" + g
		}
	}
	first := -1
	if skey != "" {
		first = b.stuck(cs, skey, now)
	}
	if first < 0 {
		first = b.pick(cs, b.rrU)
	}
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
		if skey != "" {
			b.sticky[skey] = stick{key: ProfileKey(pp.P), exp: now.Add(stickyFor)}
		}
		b.mu.Unlock()
		return &balRelay{UDPRelay: r, pool: pp, done: func() { b.mu.Lock(); b.udp[pp]--; b.mu.Unlock() }}, true
	}
	return nil, false
}

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
				pk = max(pk, v)
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
			Weight: int(b.weight(p)), Paused: b.bad[k] >= pauseAfter})
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
	for _, rr := range []map[*tunnel.Pool]float64{b.rrT, b.rrU} {
		for p := range rr {
			if !live[p] {
				delete(rr, p)
			}
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
