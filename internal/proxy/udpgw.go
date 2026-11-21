package proxy

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/logx"
)

type UDPGW struct {
	Up       Upstream
	Log      *logx.Logger
	OnPacket func(src *net.UDPAddr, data []byte)

	mu     sync.Mutex
	conn   net.Conn
	ids    map[string]uint16
	next   uint16
	closed bool
	wmu    sync.Mutex
}

const (
	flagKeepalive = 1 << 0
	flagIPv6      = 1 << 3
)

func (g *UDPGW) ensure() (net.Conn, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, errors.New("udpgw closed")
	}
	if g.conn != nil {
		return g.conn, nil
	}
	port := g.Up.UDPGWPort()
	if port == 0 {
		return nil, errors.New("UDPGW disabled for this profile")
	}
	c, err := g.Up.Dial(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	g.conn = c
	g.ids = map[string]uint16{}
	go g.readLoop(c)
	go g.keepalive(c)
	return c, nil
}

func (g *UDPGW) keepalive(c net.Conn) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for range t.C {
		g.mu.Lock()
		same := g.conn == c && !g.closed
		g.mu.Unlock()
		if !same {
			return
		}
		if err := g.write(c, []byte{flagKeepalive, 0, 0}); err != nil {
			return
		}
	}
}

func (g *UDPGW) write(c net.Conn, pkt []byte) error {
	frame := binary.LittleEndian.AppendUint16(nil, uint16(len(pkt)))
	frame = append(frame, pkt...)
	g.wmu.Lock()
	defer g.wmu.Unlock()
	c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.Write(frame)
	return err
}

func (g *UDPGW) Send(dst *net.UDPAddr, data []byte) error {
	c, err := g.ensure()
	if err != nil {
		return err
	}
	key := dst.String()
	g.mu.Lock()
	id, ok := g.ids[key]
	if !ok {
		g.next++
		id = g.next
		g.ids[key] = id
	}
	g.mu.Unlock()
	flags := byte(0)
	var addr []byte
	if ip4 := dst.IP.To4(); ip4 != nil {
		addr = append(addr, ip4...)
	} else {
		flags |= flagIPv6
		addr = append(addr, dst.IP.To16()...)
	}
	addr = binary.BigEndian.AppendUint16(addr, uint16(dst.Port))
	pkt := []byte{flags}
	pkt = binary.LittleEndian.AppendUint16(pkt, id)
	pkt = append(pkt, addr...)
	pkt = append(pkt, data...)
	if len(pkt) > 65535 {
		return errors.New("datagram too large")
	}
	if err := g.write(c, pkt); err != nil {
		g.reset(c)
		return err
	}
	return nil
}

func (g *UDPGW) readLoop(c net.Conn) {
	defer g.reset(c)
	hdr := make([]byte, 2)
	buf := make([]byte, 65535)
	for {
		if _, err := io.ReadFull(c, hdr); err != nil {
			return
		}
		n := int(binary.LittleEndian.Uint16(hdr))
		if _, err := io.ReadFull(c, buf[:n]); err != nil {
			return
		}
		p := buf[:n]
		if len(p) < 3 {
			continue
		}
		flags := p[0]
		p = p[3:]
		var src *net.UDPAddr
		if flags&flagIPv6 != 0 {
			if len(p) < 18 {
				continue
			}
			src = &net.UDPAddr{IP: net.IP(append([]byte{}, p[:16]...)), Port: int(binary.BigEndian.Uint16(p[16:18]))}
			p = p[18:]
		} else {
			if len(p) < 6 {
				continue
			}
			src = &net.UDPAddr{IP: net.IP(append([]byte{}, p[:4]...)), Port: int(binary.BigEndian.Uint16(p[4:6]))}
			p = p[6:]
		}
		if g.OnPacket != nil {
			g.OnPacket(src, append([]byte{}, p...))
		}
	}
}

func (g *UDPGW) reset(c net.Conn) {
	g.mu.Lock()
	if g.conn == c {
		g.conn = nil
	}
	g.mu.Unlock()
	c.Close()
}

func (g *UDPGW) Close() {
	g.mu.Lock()
	g.closed = true
	c := g.conn
	g.conn = nil
	g.mu.Unlock()
	if c != nil {
		c.Close()
	}
}
