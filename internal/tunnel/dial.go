package tunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/logx"
)

type Dialer struct {
	P       config.Profile
	Iface   string
	Log     *logx.Logger
	mu      sync.Mutex
	dns     *SlowDNS
	dnsType uint16
	lastIP  map[string]string
}

func (d *Dialer) netDialer() *net.Dialer {
	nd := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	if d.Iface != "" {
		iface := d.Iface
		nd.Control = func(network, address string, c syscall.RawConn) error {
			var serr error
			err := c.Control(func(fd uintptr) {
				serr = bindToDevice(int(fd), iface)
			})
			if err != nil {
				return err
			}
			return serr
		}
	}
	return nd
}

func (d *Dialer) resolve(ctx context.Context, host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}
	ips, err := UpstreamResolver(d.netDialer()).LookupHost(ctx, host)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastIP == nil {
		d.lastIP = map[string]string{}
	}
	if err != nil || len(ips) == 0 {
		if ip, ok := d.lastIP[host]; ok {
			return ip, nil
		}
		if err == nil {
			err = fmt.Errorf("no address for %s", host)
		}
		return "", err
	}
	pick := ips[0]
	for _, ip := range ips {
		if strings.Contains(ip, ".") {
			pick = ip
			break
		}
	}
	d.lastIP[host] = pick
	return pick, nil
}

func UpstreamResolver(nd *net.Dialer) *net.Resolver {
	servers := upstreamServers()
	var i int
	var mu sync.Mutex
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			mu.Lock()
			s := servers[i%len(servers)]
			i++
			mu.Unlock()
			return nd.DialContext(ctx, network, net.JoinHostPort(s, "53"))
		},
	}
}

func upstreamServers() []string {
	var out []string
	for _, f := range []string{"/tmp/resolv.conf.d/resolv.conf.auto", "/tmp/resolv.conf.auto"} {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) >= 2 && fs[0] == "nameserver" {
				ip := net.ParseIP(fs[1])
				if ip != nil && !ip.IsLoopback() {
					out = append(out, fs[1])
				}
			}
		}
		fh.Close()
		if len(out) > 0 {
			break
		}
	}
	return append(out, "1.1.1.1", "8.8.8.8")
}

type Info struct {
	Target string
	skip   *skipConn
}

func (i *Info) Replies() []string {
	if i == nil || i.skip == nil {
		return nil
	}
	return i.skip.replies
}

func (d *Dialer) Dial(ctx context.Context) (net.Conn, *Info, error) {
	p := d.P
	key := p.ID + "\x00" + p.Name
	p.Host, p.Bug, p.Proxy = PickValue(p.Host, key+"h"), PickValue(p.Bug, key+"b"), PickValue(p.Proxy, key+"p")
	serverIP := ""
	var c net.Conn
	info := &Info{}

	if p.UseDNS {
		s, err := d.slowDNS(ctx)
		if err != nil {
			return nil, nil, err
		}
		st, err := s.Open()
		if err != nil {
			d.mu.Lock()
			if d.dns == s {
				d.dns = nil
			}
			d.mu.Unlock()
			s.Close()
			return nil, nil, err
		}
		c = st
		info.Target = "slowdns " + p.DNSDomain
		d.Log.Infof("dns", "SlowDNS stream opened (NS %s)", p.DNSDomain)
	} else {
		host, port := p.Host, p.Port
		if p.Proxy != "" {
			h, ps, _ := net.SplitHostPort(p.Proxy)
			pp, _ := strconv.Atoi(ps)
			host, port = h, pp
		}
		ip, err := d.resolve(ctx, host)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve %s: %v", host, err)
		}
		if p.Proxy == "" {
			serverIP = ip
		}
		info.Target = net.JoinHostPort(host, strconv.Itoa(port))
		d.Log.Infof("tcp", "Connecting to %s", info.Target)
		t0 := time.Now()
		raw, err := d.netDialer().DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
		if err != nil {
			return nil, nil, fmt.Errorf("TCP %s (%s): %v", info.Target, ip, err)
		}
		d.Log.Infof("tcp", "Connected to %s (%s) in %d ms", host, ip, time.Since(t0).Milliseconds())
		if tc, ok := raw.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}
		c = raw
	}

	if p.UseTLS {
		sni := p.Bug
		if sni == "" {
			sni = p.Host
		}
		t0 := time.Now()
		tc := tls.Client(c, &tls.Config{ServerName: sni, InsecureSkipVerify: p.Insecure, NextProtos: []string{"http/1.1"}})
		hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			c.Close()
			if !p.Insecure && strings.Contains(err.Error(), "certificate") {
				return nil, nil, fmt.Errorf("TLS (SNI %s): %v — enable Allow insecure for bug hosts", sni, err)
			}
			return nil, nil, fmt.Errorf("TLS handshake (SNI %s): %v", sni, err)
		}
		d.Log.Infof("tls", "TLS handshake OK, SNI %s (%s, %d ms)", sni, tls.VersionName(tc.ConnectionState().Version), time.Since(t0).Milliseconds())
		c = tc
	}

	if !p.UsePayload {
		return c, info, nil
	}
	payload := p.Payload
	if strings.TrimSpace(payload) == "" {
		if p.UseTLS {
			payload = DefaultWSS
		} else {
			payload = DefaultWS
		}
	}
	if serverIP == "" && strings.Contains(strings.ToLower(payload), "[ip") {
		if ip, err := d.resolve(ctx, p.Host); err == nil {
			serverIP = ip
		}
	}
	segs := Build(payload, PayloadVars{Host: p.Host, Port: p.Port, Bug: p.Bug, UA: p.UA, IP: serverIP, Proxy: p.Proxy, Key: key},
		time.Duration(p.SplitDelay)*time.Millisecond)
	for i, s := range segs {
		if s.Delay > 0 {
			select {
			case <-time.After(s.Delay):
			case <-ctx.Done():
				c.Close()
				return nil, nil, ctx.Err()
			}
		}
		c.SetWriteDeadline(time.Now().Add(15 * time.Second))
		if _, err := c.Write(s.Data); err != nil {
			c.Close()
			return nil, nil, fmt.Errorf("send payload: %v", err)
		}
		d.Log.Debugf("payload", "Payload part %d/%d sent (%d bytes)", i+1, len(segs), len(s.Data))
	}
	c.SetWriteDeadline(time.Time{})
	total := 0
	for _, s := range segs {
		total += len(s.Data)
	}
	if len(segs) > 1 {
		d.Log.Infof("payload", "Payload sent in %d parts (%d bytes)", len(segs), total)
	} else {
		d.Log.Infof("payload", "Payload sent (%d bytes)", total)
	}

	sk := newSkipConn(c, p.Enhanced)
	sk.log = d.Log
	info.skip = sk
	return sk, info, nil
}

func (d *Dialer) slowDNS(ctx context.Context) (*SlowDNS, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dns != nil && !d.dns.Closed() {
		return d.dns, nil
	}
	s, err := NewSlowDNS(ctx, d.P.DNSDomain, d.P.DNSPubkey, d.P.DNSResolv, d.P.DNSType, d.dnsType, d.netDialer(), d.Log)
	if err != nil {
		return nil, err
	}
	d.dns = s
	d.dnsType = s.QType
	return s, nil
}

func (d *Dialer) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dns != nil {
		d.dns.Close()
		d.dns = nil
	}
}
