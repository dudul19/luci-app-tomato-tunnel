package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/logx"
)

func Probe(ctx context.Context, p config.Profile, iface string, tries int) (int64, string, error) {
	d := &Dialer{P: p, Iface: iface, Log: logx.New("", 10, false)}
	if p.Type == "zivpn" {
		return d.probeZivpn(ctx)
	}
	if p.UseDNS {
		return d.probeDNS(ctx, tries)
	}
	host, port := PickValue(p.Host, "probe"), p.Port
	if px := PickValue(p.Proxy, "probe"); px != "" {
		h, ps, _ := net.SplitHostPort(px)
		pp, _ := strconv.Atoi(ps)
		host, port = h, pp
	}
	return d.probeTCP(ctx, host, port, tries)
}

func (d *Dialer) probeTCP(ctx context.Context, host string, port, tries int) (int64, string, error) {
	target := net.JoinHostPort(host, strconv.Itoa(port))
	ip, err := d.resolve(ctx, host)
	if err != nil {
		return -1, target, fmt.Errorf("resolve %s: %v", host, err)
	}
	nd := d.netDialer()
	nd.Timeout = 5 * time.Second
	best := int64(-1)
	var last error
	for i := 0; i < tries && ctx.Err() == nil; i++ {
		t0 := time.Now()
		c, err := nd.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
		if err != nil {
			last = err
			continue
		}
		ms := time.Since(t0).Milliseconds()
		c.Close()
		if best < 0 || ms < best {
			best = ms
		}
	}
	if best < 0 {
		if last == nil {
			last = ctx.Err()
		}
		return -1, target, fmt.Errorf("TCP %s: %v", target, last)
	}
	return best, target, nil
}

func (d *Dialer) probeDNS(ctx context.Context, tries int) (int64, string, error) {
	r := strings.TrimSpace(d.P.DNSResolv)
	switch {
	case strings.HasPrefix(r, "doh:") || strings.HasPrefix(r, "https://"):
		u, err := url.Parse(strings.TrimPrefix(r, "doh:"))
		if err != nil || u.Hostname() == "" {
			return -1, r, fmt.Errorf("bad DoH resolver %q", r)
		}
		port, _ := strconv.Atoi(u.Port())
		if port == 0 {
			port = 443
		}
		return d.probeTCP(ctx, u.Hostname(), port, tries)
	case strings.HasPrefix(r, "dot:") || strings.HasPrefix(r, "tls://"):
		a := strings.TrimPrefix(strings.TrimPrefix(r, "dot:"), "tls://")
		h, ps, err := net.SplitHostPort(a)
		if err != nil {
			h, ps = a, "853"
		}
		port, _ := strconv.Atoi(ps)
		return d.probeTCP(ctx, h, port, tries)
	}
	a := strings.TrimPrefix(r, "udp://")
	if a == "" {
		a = "1.1.1.1"
	}
	if _, _, e := net.SplitHostPort(a); e != nil {
		a = net.JoinHostPort(a, "53")
	}
	nd := d.netDialer()
	nd.Timeout = 5 * time.Second
	res := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return nd.DialContext(ctx, "udp", a)
	}}
	target := "NS " + d.P.DNSDomain + " via " + a
	best := int64(-1)
	var last error
	for i := 0; i < tries && ctx.Err() == nil; i++ {
		b := make([]byte, 4)
		rand.Read(b)
		name := "tt" + hex.EncodeToString(b) + "." + strings.TrimSuffix(d.P.DNSDomain, ".")
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		t0 := time.Now()
		_, err := res.LookupTXT(qctx, name)
		ms := time.Since(t0).Milliseconds()
		cancel()
		var de *net.DNSError
		if err != nil && !(errors.As(err, &de) && !de.IsTimeout && (de.IsNotFound || strings.Contains(de.Err, "server misbehaving") || strings.Contains(de.Err, "no such host"))) {
			last = err
			continue
		}
		if best < 0 || ms < best {
			best = ms
		}
	}
	if best < 0 {
		if last == nil {
			last = ctx.Err()
		}
		return -1, target, fmt.Errorf("DNS %s: %v", a, last)
	}
	return best, target, nil
}
