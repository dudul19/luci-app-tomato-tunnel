package proxy

import (
	"encoding/binary"
	"io"
	"net"
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
}

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
	c, err := d.Up.Dial(d.Server)
	if err != nil {
		d.Log.Debugf("dns", "%v", err)
		return servfail(q)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(q))), q...)); err != nil {
		return servfail(q)
	}
	h := make([]byte, 2)
	if _, err := io.ReadFull(c, h); err != nil {
		return servfail(q)
	}
	ans := make([]byte, binary.BigEndian.Uint16(h))
	if _, err := io.ReadFull(c, ans); err != nil {
		return servfail(q)
	}
	if d.OnAnswer != nil {
		d.report(ans)
	}
	return ans
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
	if d.pc != nil {
		d.pc.Close()
	}
	if d.ln != nil {
		d.ln.Close()
	}
}
