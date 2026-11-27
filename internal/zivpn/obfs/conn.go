package obfs

import (
	"net"
	"sync"
	"syscall"
	"time"
)

const udpBufferSize = 2048

type obfuscator interface {
	Obfuscate(in, out []byte) int
	Deobfuscate(in, out []byte) int
}

var _ net.PacketConn = (*obfsPacketConn)(nil)

type obfsPacketConn struct {
	Conn net.PacketConn
	Obfs obfuscator

	readBuf    []byte
	readMutex  sync.Mutex
	writeBuf   []byte
	writeMutex sync.Mutex
}

type udpLikePacketConn interface {
	net.PacketConn
	SyscallConn() (syscall.RawConn, error)
	SetReadBuffer(int) error
	SetWriteBuffer(int) error
}

type obfsPacketConnUDP struct {
	*obfsPacketConn
	UDPConn udpLikePacketConn
}

func wrapPacketConn(conn net.PacketConn, ob obfuscator) net.PacketConn {
	opc := &obfsPacketConn{
		Conn:     conn,
		Obfs:     ob,
		readBuf:  make([]byte, udpBufferSize),
		writeBuf: make([]byte, udpBufferSize),
	}
	if udpConn, ok := conn.(udpLikePacketConn); ok {
		return &obfsPacketConnUDP{
			obfsPacketConn: opc,
			UDPConn:        udpConn,
		}
	} else {
		return opc
	}
}

func (c *obfsPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	for {
		c.readMutex.Lock()
		n, addr, err = c.Conn.ReadFrom(c.readBuf)
		if n <= 0 {
			c.readMutex.Unlock()
			return n, addr, err
		}
		n = c.Obfs.Deobfuscate(c.readBuf[:n], p)
		c.readMutex.Unlock()
		if n > 0 || err != nil {
			return n, addr, err
		}

	}
}

func (c *obfsPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	c.writeMutex.Lock()
	nn := c.Obfs.Obfuscate(p, c.writeBuf)
	_, err = c.Conn.WriteTo(c.writeBuf[:nn], addr)
	c.writeMutex.Unlock()
	if err == nil {
		n = len(p)
	}
	return n, err
}

func (c *obfsPacketConn) Close() error {
	return c.Conn.Close()
}

func (c *obfsPacketConn) LocalAddr() net.Addr {
	return c.Conn.LocalAddr()
}

func (c *obfsPacketConn) SetDeadline(t time.Time) error {
	return c.Conn.SetDeadline(t)
}

func (c *obfsPacketConn) SetReadDeadline(t time.Time) error {
	return c.Conn.SetReadDeadline(t)
}

func (c *obfsPacketConn) SetWriteDeadline(t time.Time) error {
	return c.Conn.SetWriteDeadline(t)
}

func (c *obfsPacketConnUDP) SetReadBuffer(bytes int) error {
	return c.UDPConn.SetReadBuffer(bytes)
}

func (c *obfsPacketConnUDP) SetWriteBuffer(bytes int) error {
	return c.UDPConn.SetWriteBuffer(bytes)
}

func (c *obfsPacketConnUDP) SyscallConn() (syscall.RawConn, error) {
	return c.UDPConn.SyscallConn()
}
