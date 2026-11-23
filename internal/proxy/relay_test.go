package proxy

import (
	"io"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type cnt struct {
	net.Conn
	rx, tx *atomic.Int64
}

func (c *cnt) Raw() net.Conn      { return c.Conn }
func (c *cnt) Counted(r, w int64) { c.rx.Add(r); c.tx.Add(w) }
func (c *cnt) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.rx.Add(int64(n))
	return n, err
}

type plain struct{ net.Conn }

func pair(t testing.TB) (net.Conn, net.Conn) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	ch := make(chan net.Conn)
	go func() { c, _ := ln.Accept(); ch <- c }()
	a, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return a, <-ch
}

func cpuTime() time.Duration {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func runRelay(t *testing.T, wrap func(net.Conn, *atomic.Int64, *atomic.Int64) net.Conn, size int64) (time.Duration, int64, int64) {
	cli, relayIn := pair(t)
	relayOut, srv := pair(t)
	var rx, tx atomic.Int64
	done := make(chan struct{})
	go func() { Relay(relayIn, wrap(relayOut, &rx, &tx)); close(done) }()
	go func() {
		io.CopyN(srv, zero{}, size)
		srv.(*net.TCPConn).CloseWrite()
		io.Copy(io.Discard, srv)
		srv.Close()
	}()
	t0 := cpuTime()
	go func() { io.CopyN(cli, zero{}, size/4); cli.(*net.TCPConn).CloseWrite() }()
	n, _ := io.Copy(io.Discard, cli)
	<-done
	cli.Close()
	if n != size {
		t.Fatalf("got %d want %d", n, size)
	}
	return cpuTime() - t0, rx.Load(), tx.Load()
}

type zero struct{}

func (zero) Read(b []byte) (int, error) { return len(b), nil }

func TestRelaySpliceCounts(t *testing.T) {
	const size = 256 << 20
	spl, rx, tx := runRelay(t, func(c net.Conn, rx, tx *atomic.Int64) net.Conn { return &cnt{Conn: c, rx: rx, tx: tx} }, size)
	if rx != size || tx != size/4 {
		t.Fatalf("counted rx=%d tx=%d", rx, tx)
	}
	cp, _, _ := runRelay(t, func(c net.Conn, rx, tx *atomic.Int64) net.Conn { return plain{c} }, size)
	t.Logf("cpu for 256 MiB down + 64 MiB up (incl. test endpoints): splice %v, copy %v", spl, cp)
}
