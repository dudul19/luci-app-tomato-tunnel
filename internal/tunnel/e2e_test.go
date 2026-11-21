package tunnel

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/logx"
	"github.com/dudul19/tomato-tunnel/internal/uci"
	"golang.org/x/crypto/ssh"
)

func oldSSHServer(t *testing.T, legacyOnly bool) string {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer, _ := ssh.NewSignerFromKey(key)
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "dudul" && string(pass) == "secret" {
				return nil, nil
			}
			return nil, io.EOF
		},
		ServerVersion: "SSH-2.0-dropbear_2013.60",
		BannerCallback: func(ssh.ConnMetadata) string {
			return "<h3><font color='green'>Tomato VPN</font></h3>Expired : 2026-12-01<br>Max login : 2 &amp; no torrent<br>"
		},
	}
	if legacyOnly {
		rsaOnly, err := ssh.NewSignerWithAlgorithms(signer.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoRSA})
		if err != nil {
			t.Fatal(err)
		}
		cfg.AddHostKey(rsaOnly)
		cfg.KeyExchanges = []string{ssh.InsecureKeyExchangeDH1SHA1}
		cfg.Ciphers = []string{ssh.InsecureCipherAES128CBC}
		cfg.MACs = []string{ssh.InsecureHMACSHA196}
	} else {
		cfg.AddHostKey(signer)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "direct-tcpip" {
						nc.Reject(ssh.UnknownChannelType, "")
						continue
					}
					var m struct {
						Host  string
						Port  uint32
						OHost string
						OPort uint32
					}
					ssh.Unmarshal(nc.ExtraData(), &m)
					up, err := net.Dial("tcp", net.JoinHostPort(m.Host, strconv.Itoa(int(m.Port))))
					if err != nil {
						nc.Reject(ssh.ConnectionFailed, err.Error())
						continue
					}
					ch, rq, _ := nc.Accept()
					go ssh.DiscardRequests(rq)
					go func() { io.Copy(ch, up); ch.CloseWrite() }()
					go func() { io.Copy(up, ch); up.Close() }()
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func frontServer(t *testing.T, sshAddr string, useTLS bool, seen chan<- string) string {
	var ln net.Listener
	var err error
	if useTLS {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", selfSigned(t))
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				var all strings.Builder
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					all.WriteString(line)
					if strings.HasPrefix(line, "SSH-") {
						if seen != nil {
							seen <- all.String()
						}
						up, err := net.Dial("tcp", sshAddr)
						if err != nil {
							return
						}
						up.Write([]byte(line))
						go func() { io.Copy(c, up); c.Close() }()
						io.Copy(up, br)
						up.Close()
						return
					}
					switch {
					case strings.HasPrefix(line, "HEAD "):
						skipHeaders(br, &all)
						c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 1234\r\nServer: cloudflare\r\n\r\n"))
					case strings.HasPrefix(line, "PATCH "), strings.HasPrefix(line, "GET "):
						skipHeaders(br, &all)
						c.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func skipHeaders(br *bufio.Reader, all *strings.Builder) {
	for {
		l, err := br.ReadString('\n')
		all.WriteString(l)
		if err != nil || strings.TrimSpace(l) == "" {
			return
		}
	}
}

func selfSigned(t *testing.T) *tls.Config {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "x"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

func echoServer(t *testing.T) string {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func hp(a string) (string, int) {
	h, p, _ := net.SplitHostPort(a)
	n, _ := strconv.Atoi(p)
	return h, n
}

func roundTrip(t *testing.T, pool *Pool, echo string) {
	c, err := pool.Dial(echo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	msg := []byte("hello through the tunnel")
	c.Write(msg)
	got := make([]byte, len(msg))
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, got); err != nil || string(got) != string(msg) {
		t.Fatalf("echo: %q %v", got, err)
	}
}

const enhancedPayload = "HEAD / HTTP/1.1[crlf]Host: [bug][crlf][crlf]PATCH / HTTP/1.1[crlf]Host: ten.duduls.my.id[crlf]Upgrade: websocket[crlf][crlf][split]HTTP/1.1[crlf][crlf]"

func TestEnhancedWSLegacyDropbear(t *testing.T) {
	sshAddr := oldSSHServer(t, true)
	seen := make(chan string, 1)
	front := frontServer(t, sshAddr, false, seen)
	echo := echoServer(t)
	h, port := hp(front)
	p := config.Profile{Name: "t", UsePayload: true, Enhanced: true, Host: h, Port: port, User: "dudul", Pass: "secret",
		Bug: "quiz.int.vidio.com", UA: "ua", Payload: enhancedPayload, SplitDelay: 50, UDPGW: 7300}
	log := logx.New("", 100, testing.Verbose())
	log.SetLevel("debug")
	pool := NewPool(p, "auto", "", log)
	if err := pool.Connect(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if pool.Info.ServerMsg != "Tomato VPN\nExpired : 2026-12-01\nMax login : 2 & no torrent" {
		t.Fatalf("server message %q", pool.Info.ServerMsg)
	}
	if !pool.Info.Legacy || pool.Info.Kex != ssh.InsecureKeyExchangeDH1SHA1 {
		t.Fatalf("expected legacy kex, got %+v", pool.Info)
	}
	req := <-seen
	if !strings.Contains(req, "HEAD / HTTP/1.1\r\nHost: quiz.int.vidio.com\r\n\r\nPATCH / HTTP/1.1\r\nHost: ten.duduls.my.id\r\n") ||
		!strings.Contains(req, "\r\n\r\nHTTP/1.1\r\n\r\nSSH-2.0-") {
		t.Fatalf("front saw %q", req)
	}
	roundTrip(t, pool, echo)
	time.Sleep(300 * time.Millisecond)
	if pool.Sessions() != 2 {
		t.Fatalf("sessions = %d", pool.Sessions())
	}
	for i := 0; i < 4; i++ {
		roundTrip(t, pool, echo)
	}
}

func TestModernOnlyRejectsOldServer(t *testing.T) {
	sshAddr := oldSSHServer(t, true)
	h, port := hp(sshAddr)
	p := config.Profile{Name: "t", Host: h, Port: port, User: "dudul", Pass: "secret"}
	pool := NewPool(p, "modern", "", logx.New("", 10, false))
	err := pool.Connect(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "Legacy") {
		t.Fatalf("want hint to use Legacy, got %v", err)
	}
}

func TestWrongPassword(t *testing.T) {
	sshAddr := oldSSHServer(t, false)
	h, port := hp(sshAddr)
	p := config.Profile{Name: "t", Host: h, Port: port, User: "dudul", Pass: "nope"}
	pool := NewPool(p, "auto", "", logx.New("", 10, false))
	err := pool.Connect(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("got %v", err)
	}
}

func TestTLSCombinations(t *testing.T) {
	sshAddr := oldSSHServer(t, false)
	echo := echoServer(t)
	front := frontServer(t, sshAddr, true, nil)
	fh, fport := hp(front)
	ph, pport := hp(tlsPipe(t, sshAddr))
	cases := []struct {
		name string
		p    config.Profile
	}{
		{"payload+tls (default wss payload)", config.Profile{UsePayload: true, UseTLS: true, Insecure: true, Host: fh, Port: fport}},
		{"payload+tls+enhanced", config.Profile{UsePayload: true, UseTLS: true, Enhanced: true, Insecure: true, Host: fh, Port: fport, Payload: enhancedPayload, SplitDelay: 30}},
		{"tls only", config.Profile{UseTLS: true, Insecure: true, Host: ph, Port: pport}},
	}
	for _, tc := range cases {
		p := tc.p
		p.Name, p.User, p.Pass, p.Bug, p.UA = tc.name, "dudul", "secret", "bug.example.com", "ua"
		pool := NewPool(p, "auto", "", logx.New("", 50, false))
		if err := pool.Connect(context.Background(), 1); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		roundTrip(t, pool, echo)
		pool.Close()
	}

	p := config.Profile{Name: "strict", UseTLS: true, Insecure: false, Host: ph, Port: pport, User: "dudul", Pass: "secret", Bug: "bug.example.com"}
	pool := NewPool(p, "auto", "", logx.New("", 10, false))
	err := pool.Connect(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "Allow insecure") {
		t.Fatalf("strict TLS: got %v", err)
	}
}

func TestLegacyModeMapping(t *testing.T) {
	ss, _ := uciParse("config profile 'a'\n\toption mode 'wss'\n\toption enhanced '1'\nconfig profile 'b'\n\toption use_payload '1'\n\toption use_slowdns '1'\n")
	c, _ := config.FromSections(ss)
	a, b := c.Profiles[0], c.Profiles[1]
	if !a.UseTLS || !a.UsePayload || !a.Enhanced || a.Label() != "SSH + Payload + TLS + Enhanced" || a.Port != 443 {
		t.Fatalf("a %+v %s", a, a.Label())
	}
	if !b.UseDNS || !b.UsePayload || b.Label() != "SSH + Payload + SlowDNS" || !b.Insecure {
		t.Fatalf("b %+v", b)
	}
}

func tlsPipe(t *testing.T, to string) string {
	ln, _ := tls.Listen("tcp", "127.0.0.1:0", selfSigned(t))
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				up, err := net.Dial("tcp", to)
				if err != nil {
					c.Close()
					return
				}
				go func() { io.Copy(up, c); up.Close() }()
				io.Copy(c, up)
				c.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

func TestPayloadRejected(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		bufio.NewReader(c).ReadString('\n')
		c.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
		time.Sleep(200 * time.Millisecond)
		c.Close()
	}()
	h, port := hp(ln.Addr().String())
	p := config.Profile{Name: "t", UsePayload: true, Enhanced: true, Host: h, Port: port, User: "u", Pass: "p", Bug: "b", UA: "x"}
	pool := NewPool(p, "auto", "", logx.New("", 10, false))
	err := pool.Connect(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "403 Forbidden") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildSegments(t *testing.T) {
	segs := Build(enhancedPayload, PayloadVars{Host: "h", Port: 80, Bug: "bug", UA: "ua"}, 700*time.Millisecond)
	if len(segs) != 2 || segs[1].Delay != 700*time.Millisecond || string(segs[1].Data) != "HTTP/1.1\r\n\r\n" {
		t.Fatalf("%+v", segs)
	}
	s2 := Build("A[instant_split]B[delay_split]C[split]", PayloadVars{Host: "h", Port: 1}, time.Second)
	if len(s2) != 3 || s2[1].Delay != 0 || s2[2].Delay != 1500*time.Millisecond {
		t.Fatalf("%+v", s2)
	}
	s3 := Build("CONNECT [host_port] [protocol][crlf]X: [ua][crlf*2]", PayloadVars{Host: "srv", Port: 22, UA: "U"}, 0)
	if string(s3[0].Data) != "CONNECT srv:22 HTTP/1.1\r\nX: U\r\n\r\n" {
		t.Fatalf("%q", s3[0].Data)
	}
}

var _ = binary.BigEndian

func uciParse(s string) ([]*uci.Section, error) { return uci.Parse(strings.NewReader(s)) }
