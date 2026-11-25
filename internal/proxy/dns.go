package proxy

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/logx"
	"golang.org/x/net/dns/dnsmessage"
)

type DNS struct {
	Addr   string
	Server string
	Up     Upstream
	Log    *logx.Logger
	// OnAnswer, when set, gets the name asked for and the addresses in the
	// answer, so the Connections list can show names instead of bare IPs.
	OnAnswer func(name string, ips []net.IP, ttl uint32)
	pc       net.PacketConn
	ln       net.Listener

	mu    sync.Mutex
	slots [dnsConns]*dnsConn
	turn  int
}

// dnsConns persistent DNS-over-TCP connections are kept to the server. A new
// connection per query costs a full tunnel handshake (TLS and WebSocket for
// Xray), about 200 ms; a kept one answers in one round trip. Queries are
// pipelined on it (RFC 7766) and matched by a DNS ID of our own.
const dnsConns = 2

type dnsConn struct {
	c       net.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[uint16]dnsWait
	next    uint16
	dead    bool
}

type dnsWait struct {
	id uint16 // the client's own ID, put back into the answer
	ch chan []byte
}

var errDNSConn = errors.New("dns connection closed")

func (d *DNS) Start() error {
	pc, err := net.ListenPacket("udp", d.Addr)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", d.Addr)
	if err != nil {
		pc.Close()
		return err
	}
	d.pc, d.ln = pc, ln
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			q := append([]byte{}, buf[:n]...)
			go func() {
				if ans := d.ask(q); ans != nil {
					pc.WriteTo(ans, from)
				}
			}()
		}
	}()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serveTCP(c)
		}
	}()
	return nil
}

func (d *DNS) serveTCP(c net.Conn) {
	defer c.Close()
	for {
		c.SetDeadline(time.Now().Add(30 * time.Second))
		h := make([]byte, 2)
		if _, err := io.ReadFull(c, h); err != nil {
			return
		}
		q := make([]byte, binary.BigEndian.Uint16(h))
		if _, err := io.ReadFull(c, q); err != nil {
			return
		}
		ans := d.ask(q)
		if ans == nil {
			return
		}
		c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(ans))), ans...))
	}
}

func (d *DNS) ask(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	ans, err := d.askKept(q)
	if errors.Is(err, errDNSConn) {
		ans, err = d.askKept(q) // the kept connection had died: once more on a fresh one
	}
	if err != nil {
		d.Log.Debugf("dns", "%v", err)
		return servfail(q)
	}
	if d.OnAnswer != nil {
		d.report(ans)
	}
	return ans
}

// conn returns a live kept connection, dialing one for an empty slot.
func (d *DNS) conn() (*dnsConn, error) {
	d.mu.Lock()
	d.turn++
	i := d.turn % dnsConns
	dc := d.slots[i]
	if dc != nil {
		dc.mu.Lock()
		dead := dc.dead
		dc.mu.Unlock()
		if !dead {
			d.mu.Unlock()
			return dc, nil
		}
	}
	d.mu.Unlock()
	c, err := d.Up.Dial(d.Server)
	if err != nil {
		return nil, err
	}
	nc := &dnsConn{c: c, pending: map[uint16]dnsWait{}, next: uint16(time.Now().UnixNano())}
	go nc.read()
	d.mu.Lock()
	if old := d.slots[i]; old != nil && old != dc {
		// another query filled the slot meanwhile: use that one, drop ours
		old.mu.Lock()
		dead := old.dead
		old.mu.Unlock()
		if !dead {
			d.mu.Unlock()
			nc.close()
			return old, nil
		}
	}
	d.slots[i] = nc
	d.mu.Unlock()
	return nc, nil
}

func (d *DNS) askKept(q []byte) ([]byte, error) {
	dc, err := d.conn()
	if err != nil {
		return nil, err
	}
	ch := make(chan []byte, 1)
	dc.mu.Lock()
	if dc.dead {
		dc.mu.Unlock()
		return nil, errDNSConn
	}
	dc.next++
	for _, busy := dc.pending[dc.next]; busy; _, busy = dc.pending[dc.next] {
		dc.next++
	}
	id := dc.next
	dc.pending[id] = dnsWait{id: binary.BigEndian.Uint16(q), ch: ch}
	dc.mu.Unlock()
	m := make([]byte, 2+len(q))
	binary.BigEndian.PutUint16(m, uint16(len(q)))
	copy(m[2:], q)
	binary.BigEndian.PutUint16(m[2:], id)
	dc.wmu.Lock()
	dc.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = dc.c.Write(m)
	dc.wmu.Unlock()
	if err != nil {
		dc.close()
		return nil, errDNSConn
	}
	select {
	case ans, ok := <-ch:
		if !ok {
			return nil, errDNSConn
		}
		return ans, nil
	case <-time.After(5 * time.Second):
		// no answer: the connection may be half dead, so start over on a new one
		dc.close()
		return nil, errors.New("dns: no answer in 5 s")
	}
}

func (dc *dnsConn) read() {
	defer dc.close()
	h := make([]byte, 2)
	for {
		if _, err := io.ReadFull(dc.c, h); err != nil {
			return
		}
		ans := make([]byte, binary.BigEndian.Uint16(h))
		if _, err := io.ReadFull(dc.c, ans); err != nil || len(ans) < 12 {
			return
		}
		id := binary.BigEndian.Uint16(ans)
		dc.mu.Lock()
		w, ok := dc.pending[id]
		delete(dc.pending, id)
		dc.mu.Unlock()
		if ok {
			binary.BigEndian.PutUint16(ans, w.id)
			w.ch <- ans
		}
	}
}

// close ends the connection and fails every query still waiting on it.
func (dc *dnsConn) close() {
	dc.mu.Lock()
	if dc.dead {
		dc.mu.Unlock()
		return
	}
	dc.dead = true
	p := dc.pending
	dc.pending = map[uint16]dnsWait{}
	dc.mu.Unlock()
	dc.c.Close()
	for _, w := range p {
		close(w.ch)
	}
}

func (d *DNS) report(ans []byte) {
	var p dnsmessage.Parser
	if _, err := p.Start(ans); err != nil {
		return
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) == 0 {
		return
	}
	rs, err := p.AllAnswers()
	if err != nil {
		return
	}
	var ips []net.IP
	ttl := uint32(0)
	for _, r := range rs {
		switch b := r.Body.(type) {
		case *dnsmessage.AResource:
			ips = append(ips, net.IP(b.A[:]))
		case *dnsmessage.AAAAResource:
			ips = append(ips, net.IP(b.AAAA[:]))
		default:
			continue
		}
		if ttl == 0 || r.Header.TTL < ttl {
			ttl = r.Header.TTL
		}
	}
	if len(ips) > 0 {
		d.OnAnswer(qs[0].Name.String(), ips, ttl)
	}
}

func servfail(q []byte) []byte {
	r := append([]byte{}, q...)
	r[2] |= 0x80
	r[3] = (r[3] & 0xF0) | 0x2
	r[3] |= 0x80

	for i := 6; i < 12; i++ {
		r[i] = 0
	}
	return r
}

func (d *DNS) Close() {
	d.mu.Lock()
	for _, dc := range d.slots {
		if dc != nil {
			go dc.close()
		}
	}
	d.mu.Unlock()
	if d.pc != nil {
		d.pc.Close()
	}
	if d.ln != nil {
		d.ln.Close()
	}
}
