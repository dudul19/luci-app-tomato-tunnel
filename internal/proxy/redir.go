package proxy

import (
	"net"
	"strconv"

	"github.com/dudul19/tomato-tunnel/internal/logx"
)

type Redir struct {
	Addr string
	Up   Upstream
	Log  *logx.Logger
	ln   net.Listener
}

func (r *Redir) Start() error {
	ln, err := net.Listen("tcp", r.Addr)
	if err != nil {
		return err
	}
	r.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go r.handle(c)
		}
	}()
	return nil
}

func (r *Redir) handle(c net.Conn) {
	ip, port, err := originalDst(c.(*net.TCPConn))
	if err != nil {
		r.Log.Debugf("redir", "original destination: %v", err)
		c.Close()
		return
	}
	dst := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	up, err := r.Up.Dial(dst)
	if err != nil {
		r.Log.Debugf("redir", "%s: %v", dst, err)
		c.Close()
		return
	}
	Relay(c, up)
}

func (r *Redir) Close() {
	if r.ln != nil {
		r.ln.Close()
	}
}
