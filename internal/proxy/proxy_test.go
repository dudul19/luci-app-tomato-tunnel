package proxy

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/logx"
)

type direct struct{ gw string }

func (d direct) Dial(addr string) (net.Conn, error) {
	if _, p, _ := net.SplitHostPort(addr); p == "7300" {
		return net.Dial("tcp", d.gw)
	}
	return net.Dial("tcp", addr)
}
func (d direct) UDPGWPort() int { return 7300 }

func fakeUDPGW(t *testing.T) string {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				for {
					h := make([]byte, 2)
					if _, err := io.ReadFull(c, h); err != nil {
						return
					}
					p := make([]byte, binary.LittleEndian.Uint16(h))
					if _, err := io.ReadFull(c, p); err != nil {
						return
					}
					if p[0]&flagKeepalive != 0 || len(p) < 9 {
						continue
					}
					conid := p[1:3]
					dst := &net.UDPAddr{IP: net.IP(p[3:7]), Port: int(binary.BigEndian.Uint16(p[7:9]))}
					u, _ := net.DialUDP("udp", nil, dst)
					u.Write(p[9:])
					buf := make([]byte, 2048)
					u.SetReadDeadline(time.Now().Add(2 * time.Second))
					n, err := u.Read(buf)
					u.Close()
					if err != nil {
						continue
					}
					out := []byte{0}
					out = append(out, conid...)
					out = append(out, p[3:9]...)
					out = append(out, buf[:n]...)
					c.Write(append(binary.LittleEndian.AppendUint16(nil, uint16(len(out))), out...))
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func udpEcho(t *testing.T) *net.UDPAddr {
	pc, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	t.Cleanup(func() { pc.Close() })
	go func() {
		b := make([]byte, 2048)
		for {
			n, a, err := pc.ReadFromUDP(b)
			if err != nil {
				return
			}
			pc.WriteToUDP(append([]byte("echo:"), b[:n]...), a)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr)
}

func tcpEcho(t *testing.T) string {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func socksHello(t *testing.T, addr string, cmd byte, host string, port int) (net.Conn, []byte) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte{5, 1, 0})
	b := make([]byte, 2)
	io.ReadFull(c, b)
	req := []byte{5, cmd, 0, 1}
	req = append(req, net.ParseIP(host).To4()...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	c.Write(req)
	rep := make([]byte, 10)
	if _, err := io.ReadFull(c, rep); err != nil {
		t.Fatal(err)
	}
	return c, rep
}

func TestSocksTCPAndUDP(t *testing.T) {
	up := direct{gw: fakeUDPGW(t)}
	s := &Socks{Addr: "127.0.0.1:0", Up: up, Log: logx.New("", 10, false)}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addr := s.ln.Addr().String()

	eh, ep, _ := net.SplitHostPort(tcpEcho(t))
	port, _ := strconv.Atoi(ep)
	c, rep := socksHello(t, addr, 1, eh, port)
	if rep[1] != 0 {
		t.Fatalf("connect reply %v", rep)
	}
	c.Write([]byte("ping"))
	got := make([]byte, 4)
	io.ReadFull(c, got)
	if string(got) != "ping" {
		t.Fatalf("tcp got %q", got)
	}
	c.Close()

	ctrl, rep := socksHello(t, addr, 3, "0.0.0.0", 0)
	defer ctrl.Close()
	if rep[1] != 0 {
		t.Fatalf("associate reply %v", rep)
	}
	relay := &net.UDPAddr{IP: net.IP(rep[4:8]), Port: int(binary.BigEndian.Uint16(rep[8:10]))}
	echo := udpEcho(t)
	u, _ := net.DialUDP("udp", nil, relay)
	defer u.Close()
	pkt := []byte{0, 0, 0, 1}
	pkt = append(pkt, echo.IP.To4()...)
	pkt = binary.BigEndian.AppendUint16(pkt, uint16(echo.Port))
	pkt = append(pkt, []byte("game")...)
	u.Write(pkt)
	buf := make([]byte, 2048)
	u.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := u.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[10:n]) != "echo:game" || int(binary.BigEndian.Uint16(buf[8:10])) != echo.Port {
		t.Fatalf("udp got %q", buf[:n])
	}
}

func TestDNSForward(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			h := make([]byte, 2)
			io.ReadFull(c, h)
			q := make([]byte, binary.BigEndian.Uint16(h))
			io.ReadFull(c, q)
			q[2] |= 0x80
			c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(q))), q...))
			c.Close()
		}
	}()
	d := &DNS{Addr: "127.0.0.1:0", Server: ln.Addr().String(), Up: direct{}, Log: logx.New("", 10, false)}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u, _ := net.DialUDP("udp", nil, d.pc.LocalAddr().(*net.UDPAddr))
	q := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	u.Write(q)
	b := make([]byte, 512)
	u.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := u.Read(b)
	if err != nil || n != len(q) || b[0] != 0x12 || b[2]&0x80 == 0 {
		t.Fatalf("dns %v %x", err, b[:n])
	}
}
