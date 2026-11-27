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

type Upstream interface {
	Dial(addr string) (net.Conn, error)
	UDPGWPort() int
}

type UDPRelay interface {
	Send(dst *net.UDPAddr, data []byte) error
	Close()
}

type NativeUDPTo interface {
	NativeUDPTo(dst *net.UDPAddr, onPacket func(src *net.UDPAddr, data []byte)) (UDPRelay, bool, error)
}

type QUICBlocker interface {
	BlockQUIC() bool
}

type NativeUDP interface {
	NativeUDP(onPacket func(src *net.UDPAddr, data []byte)) (UDPRelay, bool, error)
}

type Socks struct {
	Addr string
	Up   Upstream
	Log  *logx.Logger
	ln   net.Listener
}

func (s *Socks) Start() error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	return nil
}

func (s *Socks) Close() {
	if s.ln != nil {
		s.ln.Close()
	}
}

func (s *Socks) handle(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	buf := make([]byte, 262)
	if _, err := io.ReadFull(c, buf[:2]); err != nil || buf[0] != 5 {
		return
	}
	if _, err := io.ReadFull(c, buf[:buf[1]]); err != nil {
		return
	}
	c.Write([]byte{5, 0})
	if _, err := io.ReadFull(c, buf[:4]); err != nil || buf[0] != 5 {
		return
	}
	cmd := buf[1]
	host, port, err := readAddr(c, buf[3])
	if err != nil {
		return
	}
	switch cmd {
	case 1:
		dst := net.JoinHostPort(host, strconv.Itoa(port))
		r, err := s.Up.Dial(dst)
		if err != nil {
			s.Log.Debugf("socks", "connect %s: %v", dst, err)
			c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		c.SetDeadline(time.Time{})
		Relay(c, r)
	case 3:
		s.udpAssociate(c)
	default:
		c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
	}
}

func readAddr(r io.Reader, atyp byte) (string, int, error) {
	var host string
	switch atyp {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case 3:
		l := make([]byte, 1)
		if _, err := io.ReadFull(r, l); err != nil {
			return "", 0, err
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	default:
		return "", 0, errors.New("bad address type")
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(r, pb); err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(pb)), nil
}

type Counted interface {
	Raw() net.Conn
	Counted(read, written int64)
}

var bufPool = sync.Pool{New: func() any { b := make([]byte, 32*1024); return &b }}

func copyConn(dst, src net.Conn) {
	dc, _ := dst.(Counted)
	sc, _ := src.(Counted)
	rd, rs := dst, src
	if dc != nil {
		rd = dc.Raw()
	}
	if sc != nil {
		rs = sc.Raw()
	}
	count := func(n int64) {
		if sc != nil {
			sc.Counted(n, 0)
		}
		if dc != nil {
			dc.Counted(0, n)
		}
	}
	dt, ok1 := rd.(*net.TCPConn)
	st, ok2 := rs.(*net.TCPConn)
	if ok1 && ok2 {
		for {
			n, err := dt.ReadFrom(&io.LimitedReader{R: st, N: 256 * 1024})
			if n > 0 {
				count(n)
			}
			if n == 0 || err != nil {
				return
			}
		}
	}
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	buf := *bp
	for {
		n, err := rs.Read(buf)
		if n > 0 {
			w, werr := rd.Write(buf[:n])
			if w > 0 {
				count(int64(w))
			}
			if werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func Relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		copyConn(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			dst.Close()
		}
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	a.Close()
	b.Close()
}

func (s *Socks) udpAssociate(ctrl net.Conn) {
	la := ctrl.LocalAddr().(*net.TCPAddr)
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: la.IP, Port: 0})
	if err != nil {
		ctrl.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer pc.Close()
	bound := pc.LocalAddr().(*net.UDPAddr)
	rep := []byte{5, 0, 0, 1}
	rep = append(rep, bound.IP.To4()...)
	rep = binary.BigEndian.AppendUint16(rep, uint16(bound.Port))
	ctrl.Write(rep)
	ctrl.SetDeadline(time.Time{})

	var client *net.UDPAddr
	var cmu sync.Mutex
	onPacket := func(src *net.UDPAddr, data []byte) {
		cmu.Lock()
		dst := client
		cmu.Unlock()
		if dst == nil {
			return
		}
		h := []byte{0, 0, 0}
		if ip4 := src.IP.To4(); ip4 != nil {
			h = append(h, 1)
			h = append(h, ip4...)
		} else {
			h = append(h, 4)
			h = append(h, src.IP.To16()...)
		}
		h = binary.BigEndian.AppendUint16(h, uint16(src.Port))
		pc.WriteToUDP(append(h, data...), dst)
	}
	var gw UDPRelay
	open := func(dst *net.UDPAddr) bool {
		var r UDPRelay
		var native bool
		var err error
		if nt, ok := s.Up.(NativeUDPTo); ok {
			r, native, err = nt.NativeUDPTo(dst, onPacket)
		} else if nu, ok := s.Up.(NativeUDP); ok {
			r, native, err = nu.NativeUDP(onPacket)
		}
		if native {
			if err != nil {
				s.Log.Debugf("udp", "%v", err)
				return false
			}
			gw = r
		}
		if gw == nil {
			gw = &UDPGW{Up: s.Up, Log: s.Log, OnPacket: onPacket}
		}
		return true
	}
	defer func() {
		if gw != nil {
			gw.Close()
		}
	}()

	go func() {
		io.Copy(io.Discard, ctrl)
		pc.Close()
	}()
	buf := make([]byte, 65535)
	for {
		n, from, err := pc.ReadFromUDP(buf)
		if err != nil {
			return
		}
		cmu.Lock()
		client = from
		cmu.Unlock()
		if n < 4 || buf[2] != 0 {
			continue
		}
		r := &sliceReader{b: buf[4:n]}
		host, port, err := readAddr(r, buf[3])
		if err != nil {
			continue
		}
		if port == 443 {
			if qb, ok := s.Up.(QUICBlocker); ok && qb.BlockQUIC() {
				continue
			}
		}
		ip := net.ParseIP(host)
		if ip == nil {
			ips, err := net.LookupIP(host)
			if err != nil || len(ips) == 0 {
				continue
			}
			ip = ips[0]
		}
		dst := &net.UDPAddr{IP: ip, Port: port}
		if gw == nil && !open(dst) {
			return
		}
		if err := gw.Send(dst, r.b); err != nil {
			s.Log.Debugf("udpgw", "%v", err)
		}
	}
}

type sliceReader struct{ b []byte }

func (r *sliceReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}
