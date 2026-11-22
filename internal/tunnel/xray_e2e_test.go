package tunnel

import (
	"context"
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

const e2eUUID = "79b42a47-e3e1-4d47-9bf6-a5730199c27c"

func TestXrayE2E(t *testing.T) {
	dir := os.Getenv("TT_XRAY_E2E")
	if dir == "" {
		t.Skip("set TT_XRAY_E2E to the folder with xray-full, ttxray and server.json")
	}
	srv := exec.Command(filepath.Join(dir, "xray-full"), "run", "-c", filepath.Join(dir, "server.json"))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Process.Kill()
	time.Sleep(800 * time.Millisecond)
	XrayPaths = []string{filepath.Join(dir, "ttxray")}

	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/204" {
			w.WriteHeader(204)
			return
		}
		io.WriteString(w, "hello-through-xray")
	}))
	defer web.Close()

	echo, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
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

	base := config.Profile{Type: "xray", UUID: e2eUUID, Host: "127.0.0.1", FP: "chrome", Insecure: true, XMode: "auto", Dashboard: true}
	cases := []struct {
		name string
		mod  func(p *config.Profile)
		udp  bool
	}{
		{"vless-ws-tls", func(p *config.Profile) {
			p.XProto, p.XNet, p.XSec, p.Port, p.XPath, p.SNI = "vless", "ws", "tls", 20001, "/vless", "sg1.test"
		}, true},
		{"vless-httpupgrade-plain", func(p *config.Profile) {
			p.XProto, p.XNet, p.XSec, p.Port, p.XPath, p.XHost = "vless", "httpupgrade", "none", 20002, "/hu", "sg1.test"
		}, true},
		{"vmess-grpc-tls", func(p *config.Profile) {
			p.XProto, p.XNet, p.XSec, p.Port, p.XService, p.SNI = "vmess", "grpc", "tls", 20003, "vmess-grpc", "sg1.test"
		}, true},
		{"trojan-tcp-tls", func(p *config.Profile) {
			p.XProto, p.XNet, p.XSec, p.Port, p.Pass, p.SNI = "trojan", "tcp", "tls", 20004, "trojanpw", "sg1.test"
		}, true},
		{"vless-xhttp-reality", func(p *config.Profile) {
			p.XProto, p.XNet, p.XSec, p.Port, p.XPath, p.SNI = "vless", "xhttp", "reality", 20005, "/xhttp", "www.microsoft.com"
			p.PBK, p.SID = "FtKc2POzfshf2bSCLvgvoGyA6k2QMrbr9fYkvt97Ty8", "6ba85179e30d4fc2"
		}, true},
		{"vless-tcp-reality-vision", func(p *config.Profile) {
			p.XProto, p.XNet, p.XSec, p.Port, p.SNI, p.XFlow = "vless", "tcp", "reality", 20006, "www.microsoft.com", "xtls-rprx-vision"
			p.PBK, p.SID = "FtKc2POzfshf2bSCLvgvoGyA6k2QMrbr9fYkvt97Ty8", "6ba85179e30d4fc2"
		}, true},
		{"vless-xhttp-tls", func(p *config.Profile) {
			p.XProto, p.XNet, p.XSec, p.Port, p.XPath, p.SNI = "vless", "xhttp", "tls", 20007, "/xh", "sg1.test"
		}, false},
		{"vless-ws-plain-domain", func(p *config.Profile) {
			p.Host = "localhost"
			p.XProto, p.XNet, p.XSec, p.Port, p.XPath = "vless", "ws", "none", 20008, "/plain"
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := base
			p.Name = c.name
			c.mod(&p)
			p.Mode = p.Label()
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			pool := NewPool(p, "auto", "", logx.New("", 100, testing.Verbose()))
			pool.X = XrayOpts{CheckURL: web.URL + "/204", RunDir: t.TempDir(), DirectDomains: []string{"example.org"}, BlockAds: true}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			if err := pool.Connect(ctx, 1); err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer pool.Close()
			cl := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, a string) (net.Conn, error) { return pool.Dial(a) }}}
			resp, err := cl.Get(web.URL + "/x")
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(b) != "hello-through-xray" {
				t.Fatalf("body %q", b)
			}
			if !c.udp {
				return
			}
			got := make(chan string, 1)
			u, err := pool.XrayUDP(func(src *net.UDPAddr, data []byte) { got <- string(data) })
			if err != nil {
				t.Fatalf("udp: %v", err)
			}
			defer u.Close()
			if err := u.Send(echo.LocalAddr().(*net.UDPAddr), []byte("ping")); err != nil {
				t.Fatal(err)
			}
			select {
			case s := <-got:
				if s != "echo:ping" {
					t.Fatalf("udp got %q", s)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("udp: no echo")
			}
		})
	}

	t.Run("wrong-uuid-fails", func(t *testing.T) {
		p := base
		p.Name = "bad"
		p.XProto, p.XNet, p.XSec, p.Port, p.XPath, p.SNI = "vless", "ws", "tls", 20001, "/vless", "sg1.test"
		p.UUID = "00000000-0000-0000-0000-000000000000"
		pool := NewPool(p, "auto", "", logx.New("", 100, testing.Verbose()))
		pool.X = XrayOpts{CheckURL: web.URL + "/204", RunDir: t.TempDir()}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		err := pool.Connect(ctx, 1)
		pool.Close()
		if err == nil {
			t.Fatal("expected failure with wrong UUID")
		}
		t.Logf("error: %v", err)
	})
}
