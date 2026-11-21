package proxy

import (
	"encoding/binary"
	"net"
	"syscall"
	"unsafe"
)

const soOriginalDst = 80

func originalDst(c *net.TCPConn) (net.IP, int, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return nil, 0, err
	}
	var addr syscall.RawSockaddrInet4
	var serr error
	err = rc.Control(func(fd uintptr) {
		size := uint32(unsafe.Sizeof(addr))
		_, _, e := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, syscall.IPPROTO_IP, soOriginalDst,
			uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&size)), 0)
		if e != 0 {
			serr = e
		}
	})
	if err != nil {
		return nil, 0, err
	}
	if serr != nil {
		return nil, 0, serr
	}
	port := int(binary.BigEndian.Uint16((*[2]byte)(unsafe.Pointer(&addr.Port))[:]))
	return net.IP(addr.Addr[:]).To4(), port, nil
}
