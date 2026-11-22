package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/apernet/hysteria/core/v2/client"
	coreErrs "github.com/apernet/hysteria/core/v2/errors"

	"github.com/dudul19/tomato-tunnel/internal/zivpn/obfs"
)

// Same fixed Salamander PSK as the ZiVPN app and the Tomato ZiVPN server.
const zivpnPSK = "hu``hqb`c"

func init() { client.UseZivpnHeaders(true) }

type zivpnFactory struct{ d *Dialer }

func (f zivpnFactory) New(net.Addr) (net.PacketConn, error) {
	lc := net.ListenConfig{Control: f.d.netDialer().Control}
	pc, err := lc.ListenPacket(context.Background(), "udp", ":0")
	if err != nil {
		return nil, err
	}
	return obfs.WrapPacketConnSalamander(pc, []byte(zivpnPSK))
}

func (d *Dialer) zivpn(ctx context.Context) (client.Client, string, error) {
	p := d.P
	ip, err := d.resolve(ctx, p.Host)
	if err != nil {
		return nil, "", fmt.Errorf("resolve %s: %v", p.Host, err)
	}
	target := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	ua, err := net.ResolveUDPAddr("udp", net.JoinHostPort(ip, strconv.Itoa(p.Port)))
	if err != nil {
		return nil, target, err
	}
	sni := p.Host
	if net.ParseIP(sni) != nil {
		sni = ""
	}
	type res struct {
		c   client.Client
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, _, err := client.NewClient(&client.Config{
			ConnFactory: zivpnFactory{d},
			ServerAddr:  ua,
			Auth:        p.Pass,
			TLSConfig:   client.TLSConfig{ServerName: sni, InsecureSkipVerify: true},
			QUICConfig:  client.QUICConfig{MaxIdleTimeout: 20 * time.Second, KeepAlivePeriod: 8 * time.Second},
		})
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, target, zivpnErr(r.err)
		}
		return r.c, target, nil
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.c != nil {
				r.c.Close()
			}
		}()
		return nil, target, fmt.Errorf("ZiVPN %s: timeout", target)
	}
}

func zivpnErr(err error) error {
	var ae coreErrs.AuthError
	if errors.As(err, &ae) {
		return ErrAuth
	}
	var ce coreErrs.ConnectError
	if errors.As(err, &ce) {
		return fmt.Errorf("ZiVPN: no answer from the server (%v)", shortQUIC(ce.Err))
	}
	return fmt.Errorf("ZiVPN: %v", err)
}

func shortQUIC(err error) string {
	if err == nil {
		return "unknown"
	}
	s := err.Error()
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func (p *Pool) IsZivpn() bool { return p.P.Type == "zivpn" }

func (p *Pool) connectZivpn(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	p.Log.Infof("connect", "Profile %s: %s", p.P.Name, p.P.Label())
	p.Log.Infof("udp", "Connecting to %s (UDP)", net.JoinHostPort(p.P.Host, strconv.Itoa(p.P.Port)))
	t0 := time.Now()
	c, _, err := p.dialer.zivpn(cctx)
	if err != nil {
		return err
	}
	p.Log.Okf("zivpn", "Login successful")
	p.Log.Okf("zivpn", "ZiVPN connected in %d ms", time.Since(t0).Milliseconds())
	p.mu.Lock()
	p.zv = c
	p.mu.Unlock()
	p.Info = &HandshakeInfo{Banner: "ZiVPN (Hysteria2)", Kex: "QUIC"}
	return nil
}

func (p *Pool) dialZivpn(addr string) (net.Conn, error) {
	p.mu.Lock()
	c := p.zv
	p.mu.Unlock()
	if c == nil {
		return nil, ErrNoSession
	}
	conn, err := c.TCP(addr)
	if err != nil {
		var ce coreErrs.ClosedError
		if errors.As(err, &ce) {
			p.Log.Warnf("zivpn", "ZiVPN session closed: %v", shortQUIC(ce.Err))
			p.kill()
		}
		return nil, err
	}
	p.streams.Add(1)
	return &countConn{Conn: conn, p: p}, nil
}

type ZivpnUDP struct {
	u client.HyUDPConn
	p *Pool
}

func (p *Pool) ZivpnUDP(onPacket func(src *net.UDPAddr, data []byte)) (*ZivpnUDP, error) {
	p.mu.Lock()
	c := p.zv
	p.mu.Unlock()
	if c == nil {
		return nil, ErrNoSession
	}
	u, err := c.UDP()
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			data, from, err := u.Receive()
			if err != nil {
				return
			}
			p.RX.Add(int64(len(data)))
			if ua, err := net.ResolveUDPAddr("udp", from); err == nil {
				onPacket(ua, data)
			}
		}
	}()
	return &ZivpnUDP{u: u, p: p}, nil
}

func (z *ZivpnUDP) Send(dst *net.UDPAddr, data []byte) error {
	z.p.TX.Add(int64(len(data)))
	return z.u.Send(data, dst.String())
}

func (z *ZivpnUDP) Close() { z.u.Close() }

func (d *Dialer) probeZivpn(ctx context.Context) (int64, string, error) {
	t0 := time.Now()
	c, target, err := d.zivpn(ctx)
	if err != nil {
		return -1, target, err
	}
	ms := time.Since(t0).Milliseconds()
	c.Close()
	return ms, target, nil
}
