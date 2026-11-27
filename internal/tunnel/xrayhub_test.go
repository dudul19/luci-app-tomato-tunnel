package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/logx"
)

func TestXrayOutboundShared(t *testing.T) {
	get := func(m map[string]any, keys ...string) any {
		var v any = m
		for _, k := range keys {
			mm, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = mm[k]
		}
		return v
	}
	addr := func(out map[string]any) any {
		s := out["settings"].(map[string]any)
		if vn, ok := s["vnext"]; ok {
			return vn.([]any)[0].(map[string]any)["address"]
		}
		return s["servers"].([]any)[0].(map[string]any)["address"]
	}
	base := config.Profile{Type: "xray", Host: "sg1.example.com", Port: 443, UUID: e2eUUID, FP: "chrome"}

	p := base
	p.XProto, p.XNet, p.XSec, p.XPath = "vless", "ws", "tls", "/v"
	out, hosts, err := xrayOutbound(p, "", "203.0.113.7", true)
	if err != nil {
		t.Fatal(err)
	}
	if addr(out) != "203.0.113.7" || len(hosts) != 0 {
		t.Errorf("ws tls: address %v hosts %v", addr(out), hosts)
	}
	ss := out["streamSettings"].(map[string]any)
	if get(ss, "wsSettings", "host") != "sg1.example.com" || get(ss, "tlsSettings", "serverName") != "sg1.example.com" {
		t.Errorf("ws tls: host %v sni %v", get(ss, "wsSettings", "host"), get(ss, "tlsSettings", "serverName"))
	}

	p = base
	p.XProto, p.XNet, p.XSec, p.SNI = "trojan", "ws", "tls", "bug.example.net"
	out, _, _ = xrayOutbound(p, "", "203.0.113.7", true)
	ss = out["streamSettings"].(map[string]any)
	if get(ss, "wsSettings", "host") != "bug.example.net" {
		t.Errorf("ws sni: host %v", get(ss, "wsSettings", "host"))
	}

	p = base
	p.XProto, p.XNet, p.XSec, p.XService, p.XHost, p.SNI = "vmess", "grpc", "tls", "g", "h.example.org", "s.example.org"
	out, _, _ = xrayOutbound(p, "", "203.0.113.7", true)
	if a := get(out["streamSettings"].(map[string]any), "grpcSettings", "authority"); a != "s.example.org" {
		t.Errorf("grpc authority %v", a)
	}
	p.XSec, p.SNI = "none", ""
	out, _, _ = xrayOutbound(p, "", "203.0.113.7", true)
	if a := get(out["streamSettings"].(map[string]any), "grpcSettings", "authority"); a != "sg1.example.com" {
		t.Errorf("grpc plain authority %v", a)
	}

	p = base
	p.XProto, p.XNet, p.XSec = "vless", "ws", "tls"
	out, hosts, _ = xrayOutbound(p, "", "203.0.113.7", false)
	if addr(out) != "sg1.example.com" || hosts["sg1.example.com"] != "203.0.113.7" {
		t.Errorf("own: address %v hosts %v", addr(out), hosts)
	}

	p = base
	p.Host, p.XProto, p.XNet, p.XSec = "198.51.100.9", "vless", "ws", "none"
	if _, _, err := xrayOutbound(p, "", "", true); err != errNotShared {
		t.Errorf("alias case: %v", err)
	}
}

func TestXrayShared(t *testing.T) {
	dir := os.Getenv("TT_XRAY_E2E")
	if dir == "" {
		t.Skip("set TT_XRAY_E2E to the folder with xray-full and ttxray")
	}
	srvConf := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{"listen": "127.0.0.1", "port": 21101, "protocol": "vless",
				"settings":       map[string]any{"clients": []any{map[string]any{"id": e2eUUID}}, "decryption": "none"},
				"streamSettings": map[string]any{"network": "ws", "wsSettings": map[string]any{"path": "/a"}}},
			map[string]any{"listen": "127.0.0.1", "port": 21102, "protocol": "vmess",
				"settings":       map[string]any{"clients": []any{map[string]any{"id": e2eUUID}}},
				"streamSettings": map[string]any{"network": "grpc", "grpcSettings": map[string]any{"serviceName": "g"}}},
			map[string]any{"listen": "127.0.0.1", "port": 21103, "protocol": "vless",
				"settings":       map[string]any{"clients": []any{map[string]any{"id": e2eUUID}}, "decryption": "none"},
				"streamSettings": map[string]any{"network": "httpupgrade", "httpupgradeSettings": map[string]any{"path": "/h"}}},
		},

		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow"}}}}},
	}
	b, _ := json.Marshal(srvConf)
	sc := filepath.Join(t.TempDir(), "server.json")
	os.WriteFile(sc, b, 0600)
	srv := exec.Command(filepath.Join(dir, "xray-full"), "run", "-c", sc)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Process.Kill()
	time.Sleep(800 * time.Millisecond)
	XrayPaths = []string{filepath.Join(dir, "ttxray")}
	old := hubIdle
	hubIdle = 500 * time.Millisecond
	defer func() { hubIdle = old }()

	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/204" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/slow" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(30 * time.Second):
			}
			return
		}
		io.WriteString(w, "hello-through-xray")
	}))
	defer web.Close()

	base := config.Profile{Type: "xray", UUID: e2eUUID, Host: "127.0.0.1", FP: "chrome", XMode: "auto", Dashboard: true}
	mk := func(name string, mod func(p *config.Profile)) *Pool {
		p := base
		p.Name = name
		mod(&p)
		p.Mode = p.Label()
		pool := NewPool(p, "auto", "", logx.New("", 100, testing.Verbose()))
		pool.X = XrayOpts{Shared: true, CheckURL: web.URL + "/204", RunDir: t.TempDir(), BlockQUIC: true}
		return pool
	}
	connect := func(p *Pool) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return p.Connect(ctx, 1)
	}
	fetch := func(p *Pool) error {
		cl := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, a string) (net.Conn, error) { return p.Dial(a) }}}
		resp, err := cl.Get(web.URL + "/x")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if b, _ := io.ReadAll(resp.Body); string(b) != "hello-through-xray" {
			return fmt.Errorf("body %q", b)
		}
		return nil
	}

	pools := []*Pool{
		mk("ws", func(p *config.Profile) {
			p.XProto, p.XNet, p.XSec, p.Port, p.XPath = "vless", "ws", "none", 21101, "/a"
		}),
		mk("grpc", func(p *config.Profile) {
			p.XProto, p.XNet, p.XSec, p.Port, p.XService = "vmess", "grpc", "none", 21102, "g"
		}),
		mk("hu", func(p *config.Profile) {
			p.XProto, p.XNet, p.XSec, p.Port, p.XPath = "vless", "httpupgrade", "none", 21103, "/h"
		}),
	}
	for _, p := range pools {
		if err := connect(p); err != nil {
			t.Fatalf("%s: connect: %v", p.P.Name, err)
		}
	}
	hub.mu.Lock()
	shared := hub.x
	hub.mu.Unlock()
	if shared == nil {
		t.Fatal("no shared Xray running")
	}
	for _, p := range pools {
		if p.xr.parent != shared {
			t.Fatalf("%s is not in the shared Xray", p.P.Name)
		}
		if err := fetch(p); err != nil {
			t.Fatalf("%s: %v", p.P.Name, err)
		}
	}
	if shared.refs != 3 {
		t.Fatalf("refs %d, want 3", shared.refs)
	}

	bad := mk("bad", func(p *config.Profile) {
		p.XProto, p.XNet, p.XSec, p.Port, p.XPath = "vless", "ws", "none", 21101, "/a"
		p.UUID = "00000000-0000-0000-0000-000000000000"
	})
	if err := connect(bad); err == nil {
		t.Fatal("wrong UUID connected")
	}
	bad.Close()

	cl := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, a string) (net.Conn, error) { return pools[0].Dial(a) }}}
	resp, err := cl.Get(web.URL + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() { io.Copy(io.Discard, resp.Body); close(ended) }()
	pools[0].Close()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("download through the removed account still open")
	}
	for _, p := range pools[1:] {
		if err := fetch(p); err != nil {
			t.Fatalf("%s after removing ws: %v", p.P.Name, err)
		}
	}
	if hub.x != shared || shared.refs != 2 {
		t.Fatalf("shared Xray changed (refs %d)", shared.refs)
	}

	echo, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer echo.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, a, err := echo.ReadFromUDP(b)
			if err != nil {
				return
			}
			echo.WriteToUDP(append([]byte("echo:"), b[:n]...), a)
		}
	}()
	got := make(chan string, 1)
	u, err := pools[2].XrayUDP(func(src *net.UDPAddr, data []byte) { got <- string(data) })
	if err != nil {
		t.Fatal(err)
	}
	u.Send(echo.LocalAddr().(*net.UDPAddr), []byte("ping"))
	select {
	case s := <-got:
		if s != "echo:ping" {
			t.Fatalf("udp %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("udp: no echo")
	}
	u.Close()

	for _, p := range pools[1:] {
		p.Close()
	}
	select {
	case <-shared.done:
	case <-time.After(5 * time.Second):
		t.Fatal("shared Xray still running with no account")
	}
}
