//go:build !linux

package proxy

import (
	"errors"
	"net"
)

func originalDst(c *net.TCPConn) (net.IP, int, error) { return nil, 0, errors.New("not supported") }
