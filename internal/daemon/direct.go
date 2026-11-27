package daemon

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/proxy"
	"github.com/dudul19/tomato-tunnel/internal/tunnel"
)

func (t *flowTable) nameOf(ip string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.names[ip]; ok && time.Now().Before(e.exp) {
		return e.name
	}
	return ""
}

func directMatch(name string, list []string) bool {
	if name == "" {
		return false
	}
	for _, d := range list {
		d = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), ".")
		if d != "" && (name == d || strings.HasSuffix(name, "."+d)) {
			return true
		}
	}
	return false
}

func (d *Daemon) directFor(host string) bool {
	list := d.cfg.Main.DirectDomains
	if len(list) == 0 {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() {
			return false
		}
		return directMatch(d.flows.nameOf(ip.String()), list)
	}
	return directMatch(strings.ToLower(host), list)
}

func (d *Daemon) directIface() string {
	if p := d.pool.Load(); p != nil {
		return p.Iface()
	}
	return d.cfg.Main.WanIface
}

type directUDP struct {
	pc   *net.UDPConn
	once sync.Once
}

func (d *Daemon) newDirectUDP(onPacket func(src *net.UDPAddr, data []byte)) (proxy.UDPRelay, error) {
	pc, err := tunnel.ListenUDPOn(d.directIface())
	if err != nil {
		return nil, err
	}
	go func() {
		buf := make([]byte, 65535)
		for {
			pc.SetReadDeadline(time.Now().Add(3 * time.Minute))
			n, from, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			onPacket(from, append([]byte{}, buf[:n]...))
		}
	}()
	return &directUDP{pc: pc}, nil
}

func (r *directUDP) Send(dst *net.UDPAddr, data []byte) error {
	_, err := r.pc.WriteToUDP(data, dst)
	return err
}

func (r *directUDP) Close() { r.once.Do(func() { r.pc.Close() }) }
