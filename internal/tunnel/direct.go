package tunnel

import (
	"context"
	"net"
	"syscall"
)

func (p *Pool) Iface() string { return p.dialer.Iface }

func DirectDialer(iface string) *net.Dialer { return (&Dialer{Iface: iface}).netDialer() }

func ListenUDPOn(iface string) (*net.UDPConn, error) {
	lc := net.ListenConfig{}
	if iface != "" {
		lc.Control = func(network, address string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) { serr = bindToDevice(int(fd), iface) }); err != nil {
				return err
			}
			return serr
		}
	}
	pc, err := lc.ListenPacket(context.Background(), "udp", ":0")
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}
