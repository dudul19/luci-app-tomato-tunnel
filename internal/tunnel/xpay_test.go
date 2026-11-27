package tunnel

import (
	"bufio"
	"context"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/logx"
)

func TestPayloadShape(t *testing.T) {
	rs := payloadShape([]byte("HEAD / HTTP/1.1\r\nHost: a\r\n\r\nPATCH / HTTP/1.1\r\nHost: b\r\nUpgrade: websocket\r\n\r\nHTTP/1.1\r\n\r\n"))
	if len(rs) != 2 || rs[0] != (payReq{"HEAD", false}) || rs[1] != (payReq{"PATCH", true}) {
		t.Fatalf("got %+v", rs)
	}
}

func fakePayloadServer(t *testing.T) (int, chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan string, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				var seen []string
				for {
					b, err := br.Peek(1)
					if err != nil {
						return
					}
					if b[0] < 'A' || b[0] > 'Z' {
						got <- strings.Join(seen, "|")
						io.Copy(c, br)
						return
					}
					var head []string
					for {
						l, err := br.ReadString('\n')
						if err != nil {
							return
						}
						l = strings.TrimRight(l, "\r\n")
						if l == "" {
							break
						}
						head = append(head, l)
					}
					req := head[0]
					seen = append(seen, req)
					up := false
					for _, h := range head[1:] {
						if strings.HasPrefix(strings.ToLower(h), "upgrade:") {
							up = true
						}
					}
					switch {
					case strings.HasPrefix(req, "GET /vless"):
						got <- strings.Join(seen, "|")
						io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\n")
						io.Copy(c, br)
						return
					case up && strings.HasPrefix(req, "PATCH /no101"):
						io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
					case up:
						io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\n\r\n")
					case strings.HasPrefix(req, "HEAD"):
						io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\n")
					default:
						io.WriteString(c, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n")
					}
				}
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestXrayPayloadInjector(t *testing.T) {
	log := logx.New(filepath.Join(t.TempDir(), "log"), 1<<20, false)
	cases := []struct {
		name, net, payload string
		enh                bool
		send, want         string
		seen               string
	}{
		{"ws-enhanced-upgrade-in-payload", "ws",
			"HEAD / HTTP/1.1[crlf]Host: [bug][crlf][crlf][split]PATCH / HTTP/1.1[crlf]Host: [bug][crlf]Upgrade: websocket[crlf][crlf]", true,
			"GET /vless HTTP/1.1\r\nHost: sg1.test\r\nUpgrade: websocket\r\n\r\nPING",
			"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\nPING",
			"HEAD / HTTP/1.1|PATCH / HTTP/1.1|GET /vless HTTP/1.1"},
		{"ws-enhanced-upgrade-refused-no-wait", "ws",
			"HEAD / HTTP/1.1[crlf]Host: [bug][crlf][crlf]PATCH /no101 HTTP/1.1[crlf]Host: [bug][crlf]Upgrade: websocket[crlf][crlf]", true,
			"GET /vless HTTP/1.1\r\nHost: sg1.test\r\nUpgrade: websocket\r\n\r\nPING",
			"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\nPING",
			"HEAD / HTTP/1.1|PATCH /no101 HTTP/1.1|GET /vless HTTP/1.1"},
		{"httpupgrade-enhanced-get-chunked", "httpupgrade", "", true,
			"GET /vless HTTP/1.1\r\nHost: sg1.test\r\nUpgrade: websocket\r\n\r\nPING",
			"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\nPING",
			"GET / HTTP/1.1|GET /vless HTTP/1.1"},
		{"tcp-enhanced", "tcp", "GET / HTTP/1.1[crlf]Host: [bug][crlf][crlf]HEAD / HTTP/1.1[crlf]Host: [bug][crlf][crlf]", true,
			"\x00VLESS-DATA", "\x00VLESS-DATA", "GET / HTTP/1.1|HEAD / HTTP/1.1"},
		{"ws-plain-no-enhanced", "ws", "", false,
			"GET /vless HTTP/1.1\r\nHost: sg1.test\r\n\r\nPING",
			"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6\r\n world\r\n0\r\n\r\nHTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\nPING",
			"GET / HTTP/1.1|GET /vless HTTP/1.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port, got := fakePayloadServer(t)
			pr := config.Profile{Name: "x", Type: "xray", XProto: "vless", XNet: tc.net, XSec: "none", Host: "127.0.0.1", Port: port,
				XPay: true, Enhanced: tc.enh, Bug: "bug.test", Payload: tc.payload, SplitDelay: 50, UA: "ua"}
			p := NewPool(pr, "", "", log)
			defer p.Close()
			xr, err := p.payloadForward(context.Background(), pr)
			if err != nil {
				t.Fatal(err)
			}
			if xr.Host != "127.0.0.1" || xr.XSec != "none" || xr.XPay {
				t.Fatalf("xray profile %+v", xr)
			}
			c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(xr.Port)))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			t0 := time.Now()
			io.WriteString(c, tc.send)
			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, len(tc.want))
			if _, err := io.ReadFull(c, buf); err != nil {
				t.Fatalf("read: %v (got %q)", err, buf)
			}
			if string(buf) != tc.want {
				t.Fatalf("got %q\nwant %q", buf, tc.want)
			}
			if el := time.Since(t0); el > 500*time.Millisecond {
				t.Fatalf("took %v: the filter waited", el)
			}
			select {
			case s := <-got:
				if s != tc.seen {
					t.Fatalf("server saw %q, want %q", s, tc.seen)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("server saw nothing")
			}
		})
	}
}

func TestXpayProfilesTLS(t *testing.T) {
	pr := config.Profile{Type: "xray", XProto: "vless", XNet: "ws", XSec: "tls", Host: "sg1.test", Port: 443, Bug: "bug.test", XPay: true, XPayPos: "inside"}
	inj, xr := xpayProfiles(pr, 1234)
	if !inj.UseTLS || xr.XSec != "none" || xr.XHost != "sg1.test" || xr.Port != 1234 || inj.Payload != XrayDefaultPayload {
		t.Fatalf("inside: %+v / %+v", inj, xr)
	}
	pr.XPayPos = "before"
	inj, xr = xpayProfiles(pr, 1234)
	if inj.UseTLS || xr.XSec != "tls" || xr.SNI != "bug.test" {
		t.Fatalf("before: %+v / %+v", inj, xr)
	}
	if xpayUntil101(pr) {
		t.Fatal("before TLS: the upgrade is inside Xray's TLS")
	}
}
