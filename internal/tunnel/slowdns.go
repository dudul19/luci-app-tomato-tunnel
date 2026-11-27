package tunnel

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/logx"
	"github.com/dudul19/tomato-tunnel/internal/slowdns"
	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"
	"www.bamsoftware.com/git/dnstt.git/dns"
	"www.bamsoftware.com/git/dnstt.git/noise"
	"www.bamsoftware.com/git/dnstt.git/turbotunnel"
)

type SlowDNS struct {
	QType uint16
	pconn net.PacketConn
	kcp   *kcp.UDPSession
	sess  *smux.Session
	once  sync.Once
}

var autoTypes = []uint16{slowdns.RRTypeTXT, slowdns.RRTypeNULL, slowdns.RRTypeCNAME, slowdns.RRTypeHTTPS, slowdns.RRTypeSVCB, slowdns.RRTypeDNSKEY}

type SlowDNSTune struct {
	EDNS   int
	Window int
}

const DefaultKCPWindow = 512

const (
	addrAckHold   = 40
	addrKCPWindow = 4096
	addrEDNS      = 1232
)

func NewSlowDNS(ctx context.Context, domain, pubkeyHex, resolver, rtype string, prefer uint16, tune SlowDNSTune, nd *net.Dialer, log *logx.Logger) (*SlowDNS, error) {
	if t, ok := slowdns.TypeNames[strings.ToLower(rtype)]; ok {
		return newSlowDNS(ctx, domain, pubkeyHex, resolver, t, 30*time.Second, tune, nd, log)
	}
	order := autoTypes
	if prefer != 0 {
		order = append([]uint16{prefer}, autoTypes...)
	}
	var lastErr error
	tried := map[uint16]bool{}
	for _, t := range order {
		if tried[t] {
			continue
		}
		tried[t] = true
		s, err := newSlowDNS(ctx, domain, pubkeyHex, resolver, t, 7*time.Second, tune, nd, log)
		if err == nil {
			return s, nil
		}
		lastErr = err
		if ctx.Err() != nil || !strings.Contains(err.Error(), "no answer") {
			return nil, err
		}
		log.Warnf("slowdns", "No answer with type %s, trying the next type", slowdns.TypeName(t))
	}
	return nil, lastErr
}

func newSlowDNS(ctx context.Context, domain, pubkeyHex, resolver string, qtype uint16, wait time.Duration, tune SlowDNSTune, nd *net.Dialer, log *logx.Logger) (*SlowDNS, error) {
	name, err := dns.ParseName(domain)
	if err != nil {
		return nil, fmt.Errorf("SlowDNS NS %q: %v", domain, err)
	}
	pub, err := hex.DecodeString(strings.TrimSpace(pubkeyHex))
	if err != nil || len(pub) != 32 {
		return nil, fmt.Errorf("SlowDNS public key must be 64 hex characters")
	}
	mtu := dnsNameCapacity(name) - 8 - 1 - 3 - 1
	if mtu < 80 {
		return nil, fmt.Errorf("SlowDNS NS %s is too long (MTU %d)", domain, mtu)
	}

	var remote net.Addr
	var pconn net.PacketConn
	r := strings.TrimSpace(resolver)
	switch {
	case strings.HasPrefix(r, "doh:") || strings.HasPrefix(r, "https://"):
		u := strings.TrimPrefix(r, "doh:")
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		tr.DialContext = nd.DialContext
		remote = turbotunnel.DummyAddr{}
		pconn, err = slowdns.NewHTTPPacketConn(tr, u, 32)
	case strings.HasPrefix(r, "dot:") || strings.HasPrefix(r, "tls://"):
		a := strings.TrimPrefix(strings.TrimPrefix(r, "dot:"), "tls://")
		if _, _, e := net.SplitHostPort(a); e != nil {
			a = net.JoinHostPort(a, "853")
		}
		td := &tls.Dialer{NetDialer: nd}
		remote = turbotunnel.DummyAddr{}
		pconn, err = slowdns.NewTLSPacketConn(a, td.DialContext)
	default:
		a := strings.TrimPrefix(r, "udp://")
		if a == "" {
			a = "1.1.1.1"
		}
		if _, _, e := net.SplitHostPort(a); e != nil {
			a = net.JoinHostPort(a, "53")
		}
		ua, e := net.ResolveUDPAddr("udp", a)
		if e != nil {
			return nil, fmt.Errorf("SlowDNS resolver %q: %v", resolver, e)
		}
		remote = ua
		lc := net.ListenConfig{Control: nd.Control}
		pconn, err = lc.ListenPacket(ctx, "udp", ":0")
	}
	if err != nil {
		return nil, fmt.Errorf("SlowDNS resolver %q: %v", resolver, err)
	}
	addr := qtype == slowdns.RRTypeA || qtype == slowdns.RRTypeAAAA
	edns := tune.EDNS
	if addr && (edns == 0 || edns == slowdns.DefaultEDNS) {
		edns = addrEDNS
	}
	pconn = slowdns.NewDNSPacketConn(pconn, remote, name, qtype, edns)

	conn, err := kcp.NewConn2(remote, nil, 0, 0, pconn)
	if err != nil {
		pconn.Close()
		return nil, fmt.Errorf("SlowDNS KCP: %v", err)
	}
	conn.SetStreamMode(true)

	conn.SetNoDelay(0, 0, 0, 1)
	win := tune.Window
	if win < 32 || win > 4096 {
		win = DefaultKCPWindow
	}
	if addr {

		conn.SetNoDelay(1, 10, 2, 1)
		conn.SetAckHold(addrAckHold)
		if tune.Window == 0 || tune.Window == DefaultKCPWindow {
			win = addrKCPWindow
		}
	}
	conn.SetWindowSize(win, win)
	conn.SetMtu(mtu)

	type res struct {
		rw  any
		err error
	}
	ch := make(chan res, 1)
	go func() {
		rw, err := noise.NewClient(conn, pub)
		ch <- res{rw, err}
	}()
	var rw res
	select {
	case rw = <-ch:
	case <-time.After(wait):
		conn.Close()
		pconn.Close()
		return nil, fmt.Errorf("SlowDNS: no answer from server with type %s", slowdns.TypeName(qtype))
	case <-ctx.Done():
		conn.Close()
		pconn.Close()
		return nil, ctx.Err()
	}
	if rw.err != nil {
		conn.Close()
		pconn.Close()
		return nil, fmt.Errorf("SlowDNS handshake: %v (wrong public key?)", rw.err)
	}
	cfg := smux.DefaultConfig()
	cfg.Version = 2
	cfg.KeepAliveTimeout = 2 * time.Minute
	cfg.MaxStreamBuffer = 1 << 20
	sess, err := smux.Client(rw.rw.(interface {
		Read([]byte) (int, error)
		Write([]byte) (int, error)
		Close() error
	}), cfg)
	if err != nil {
		conn.Close()
		pconn.Close()
		return nil, fmt.Errorf("SlowDNS smux: %v", err)
	}
	log.Okf("slowdns", "SlowDNS session up (NS %s, type %s, resolver %s)", domain, slowdns.TypeName(qtype), r)
	sd := &SlowDNS{QType: qtype, pconn: pconn, kcp: conn, sess: sess}
	if OnSession != nil {
		go OnSession(sd, log)
	}
	return sd, nil
}

func (s *SlowDNS) Open() (net.Conn, error) {
	st, err := s.sess.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("SlowDNS stream: %v", err)
	}
	return st, nil
}

func (s *SlowDNS) Closed() bool { return s.sess.IsClosed() }

func (s *SlowDNS) Close() {
	s.once.Do(func() {
		s.sess.Close()
		s.kcp.Close()
		s.pconn.Close()
	})
}

func dnsNameCapacity(domain dns.Name) int {
	capacity := 255 - 1
	for _, label := range domain {
		capacity -= len(label) + 1
	}
	capacity = capacity * 63 / 64
	return capacity * 5 / 8
}

var OnSession func(*SlowDNS, *logx.Logger)
