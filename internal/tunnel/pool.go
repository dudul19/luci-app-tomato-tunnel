package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apernet/hysteria/core/v2/client"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/logx"
	"golang.org/x/crypto/ssh"
)

type Pool struct {
	P      config.Profile
	Compat string
	Log    *logx.Logger
	dialer *Dialer

	mu      sync.Mutex
	slots   []*slot
	dead    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	Info    *HandshakeInfo
	RX, TX  atomic.Int64
	streams atomic.Int64
	zv      client.Client
}

type slot struct {
	c      *ssh.Client
	active atomic.Int64
}

func NewPool(p config.Profile, compat, iface string, log *logx.Logger) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{P: p, Compat: compat, Log: log, dialer: &Dialer{P: p, Iface: iface, Log: log},
		dead: make(chan struct{}), ctx: ctx, cancel: cancel}
}

func (p *Pool) Connect(ctx context.Context, n int) error {
	if p.IsZivpn() {
		return p.connectZivpn(ctx)
	}
	c, info, err := p.open(ctx, 1)
	if err != nil {
		return err
	}
	p.Info = info
	p.mu.Lock()
	p.slots = []*slot{{c: c}}
	p.mu.Unlock()
	p.watch(p.slots[0], 1)
	for i := 2; i <= n; i++ {
		go p.refill(i)
	}
	return nil
}

func (p *Pool) open(ctx context.Context, idx int) (*ssh.Client, *HandshakeInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if idx == 1 {
		p.Log.Infof("connect", "Profile %s: %s", p.P.Name, p.P.Label())
	}
	t0 := time.Now()
	conn, dinfo, err := p.dialer.Dial(cctx)
	if err != nil {
		return nil, nil, err
	}
	c, info, err := NewClient(cctx, conn, p.P.User, p.P.Pass, p.Compat, p.Log, idx == 1)
	if err != nil {
		if dinfo.skip != nil && dinfo.skip.err != nil {
			return nil, nil, dinfo.skip.err
		}
		return nil, nil, err
	}
	leg := ""
	if info.Legacy {
		leg = ", legacy"
	}
	if idx == 1 {
		p.Log.Okf("ssh", "Login successful")
		p.Log.Infof("ssh", "Key exchange %s, cipher %s%s", info.Kex, info.Cipher, leg)
		p.Log.Okf("ssh", "SSH connected in %d ms", time.Since(t0).Milliseconds())
	} else {
		p.Log.Okf("ssh", "Extra session #%d connected in %d ms", idx, time.Since(t0).Milliseconds())
	}
	return c, info, nil
}

func (p *Pool) target() string {
	if p.P.UseDNS {
		return "NS " + p.P.DNSDomain
	}
	if p.P.Proxy != "" {
		return fmt.Sprintf("%s:%d via %s", p.P.Host, p.P.Port, p.P.Proxy)
	}
	return fmt.Sprintf("%s:%d", p.P.Host, p.P.Port)
}

func onoff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (p *Pool) watch(s *slot, idx int) {
	go func() {
		waitErr := make(chan error, 1)
		go func() { waitErr <- s.c.Wait() }()
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-p.ctx.Done():
				s.c.Close()
				return
			case err := <-waitErr:
				p.drop(s, idx, err)
				return
			case <-t.C:
				res := make(chan error, 1)
				go func() {
					_, _, err := s.c.SendRequest("keepalive@openssh.com", true, nil)
					res <- err
				}()
				select {
				case err := <-res:
					if err != nil {
						s.c.Close()
					}
				case <-time.After(12 * time.Second):
					p.Log.Warnf("ssh", "Session #%d: keepalive timeout", idx)
					s.c.Close()
				case <-p.ctx.Done():
				}
			}
		}
	}()
}

func (p *Pool) drop(s *slot, idx int, err error) {
	p.mu.Lock()
	for i, x := range p.slots {
		if x == s {
			p.slots = append(p.slots[:i], p.slots[i+1:]...)
			break
		}
	}
	left := len(p.slots)
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return
	}
	msg := "closed"
	if err != nil && !errors.Is(err, net.ErrClosed) {
		msg = err.Error()
	}
	p.Log.Warnf("ssh", "Session #%d lost (%s), %d left", idx, msg, left)
	if left == 0 {
		p.kill()
		return
	}
	go p.refill(idx)
}

func (p *Pool) refill(idx int) {
	for try := 0; try < 3; try++ {
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(time.Duration(try) * 3 * time.Second):
		}
		c, _, err := p.open(p.ctx, idx)
		if err != nil {
			p.Log.Errorf("ssh", "Session #%d: %v", idx, err)
			continue
		}
		s := &slot{c: c}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			c.Close()
			return
		}
		p.slots = append(p.slots, s)
		p.mu.Unlock()
		p.watch(s, idx)
		return
	}
}

func (p *Pool) kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.dead:
	default:
		close(p.dead)
	}
}

func (p *Pool) Dead() <-chan struct{} { return p.dead }

func (p *Pool) Sessions() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.zv != nil {
		return 1
	}
	return len(p.slots)
}

func (p *Pool) pick() *slot {
	p.mu.Lock()
	defer p.mu.Unlock()
	var best *slot
	for _, s := range p.slots {
		if best == nil || s.active.Load() < best.active.Load() {
			best = s
		}
	}
	return best
}

var ErrNoSession = errors.New("tunnel not connected")

func (p *Pool) Dial(addr string) (net.Conn, error) {
	if p.IsZivpn() {
		return p.dialZivpn(addr)
	}
	s := p.pick()
	if s == nil {
		return nil, ErrNoSession
	}
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := s.c.Dial("tcp", addr)
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			if strings.Contains(r.err.Error(), "administratively prohibited") {
				return nil, fmt.Errorf("server refused %s (port forwarding disabled?)", addr)
			}
			return nil, r.err
		}
		s.active.Add(1)
		p.streams.Add(1)
		return &countConn{Conn: r.c, p: p, s: s}, nil
	case <-time.After(20 * time.Second):
		go func() {
			if r := <-ch; r.c != nil {
				r.c.Close()
			}
		}()
		return nil, fmt.Errorf("open %s: timeout", addr)
	}
}

func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	slots := p.slots
	p.slots = nil
	zv := p.zv
	p.zv = nil
	p.mu.Unlock()
	if zv != nil {
		zv.Close()
	}
	p.cancel()
	for _, s := range slots {
		s.c.Close()
	}
	p.dialer.Close()
	p.kill()
}

type countConn struct {
	net.Conn
	p    *Pool
	s    *slot
	once sync.Once
}

func (c *countConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.p.RX.Add(int64(n))
	return n, err
}

func (c *countConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.p.TX.Add(int64(n))
	return n, err
}

func (c *countConn) Close() error {
	c.once.Do(func() {
		if c.s != nil {
			c.s.active.Add(-1)
		}
		c.p.streams.Add(-1)
	})
	return c.Conn.Close()
}

func (c *countConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
