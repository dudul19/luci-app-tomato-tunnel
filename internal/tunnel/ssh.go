package tunnel

import (
	"context"
	"fmt"
	"html"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/logx"
	"golang.org/x/crypto/ssh"
)

const clientVersion = "SSH-2.0-OpenSSH_9.6"

func algorithms(compat string) (kex, ciphers, macs, hostkeys []string) {
	sup := ssh.SupportedAlgorithms()
	ins := ssh.InsecureAlgorithms()
	switch compat {
	case "modern":
		return sup.KeyExchanges, sup.Ciphers, sup.MACs, sup.HostKeys
	case "legacy":
		return cat(ins.KeyExchanges, sup.KeyExchanges), cat(ins.Ciphers, sup.Ciphers), cat(sup.MACs, ins.MACs), cat(ins.HostKeys, sup.HostKeys)
	default:
		return cat(sup.KeyExchanges, ins.KeyExchanges), cat(sup.Ciphers, ins.Ciphers), cat(sup.MACs, ins.MACs), cat(sup.HostKeys, ins.HostKeys)
	}
}

func cat(a, b []string) []string {
	out := append([]string{}, a...)
	for _, x := range b {
		dup := false
		for _, y := range out {
			if x == y {
				dup = true
			}
		}
		if !dup {
			out = append(out, x)
		}
	}
	return out
}

type HandshakeInfo struct {
	Banner    string
	ServerMsg string
	Kex       string
	Host      string
	Cipher    string
	Legacy    bool
}

func NewClient(ctx context.Context, conn net.Conn, user, pass, compat string, log *logx.Logger, verbose bool) (*ssh.Client, *HandshakeInfo, error) {
	kex, ciphers, macs, hks := algorithms(compat)
	var serverMsg strings.Builder
	vc := &verConn{Conn: conn}
	conn = vc
	cfg := &ssh.ClientConfig{
		BannerCallback: func(msg string) error {
			if serverMsg.Len() < 8192 {
				serverMsg.WriteString(msg)
			}
			if verbose && log != nil {
				for _, l := range strings.Split(CleanBanner(msg), "\n") {
					if strings.TrimSpace(l) != "" {
						log.Msgf("banner", "%s", l)
					}
				}
			}
			return nil
		},
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				ans := make([]string, len(qs))
				for i := range ans {
					ans[i] = pass
				}
				return ans, nil
			}),
		},

		HostKeyCallback: func(string, net.Addr, ssh.PublicKey) error {
			if verbose && log != nil {
				log.Infof("ssh", "SSH server: %s", vc.version())
				log.Infof("ssh", "Authenticating as %s", user)
			}
			return nil
		},
		HostKeyAlgorithms: hks,
		ClientVersion:     clientVersion,
		Timeout:           20 * time.Second,
		Config:            ssh.Config{KeyExchanges: kex, Ciphers: ciphers, MACs: macs},
	}
	deadline := time.Now().Add(25 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	c, chans, reqs, err := ssh.NewClientConn(conn, "", cfg)
	close(done)
	if err != nil {
		conn.Close()
		return nil, nil, explainSSH(err)
	}
	conn.SetDeadline(time.Time{})
	info := &HandshakeInfo{Banner: string(c.ServerVersion()), ServerMsg: CleanBanner(serverMsg.String())}
	if a, ok := c.(interface {
		Algorithms() ssh.NegotiatedAlgorithms
	}); ok {
		al := a.Algorithms()
		info.Kex, info.Host, info.Cipher = al.KeyExchange, al.HostKey, al.Write.Cipher
		for _, x := range ssh.InsecureAlgorithms().KeyExchanges {
			if x == info.Kex {
				info.Legacy = true
			}
		}
		for _, x := range ssh.InsecureAlgorithms().Ciphers {
			if x == info.Cipher {
				info.Legacy = true
			}
		}
		if info.Host == ssh.KeyAlgoRSA {
			info.Legacy = true
		}
	}
	return ssh.NewClient(c, chans, reqs), info, nil
}

type verConn struct {
	net.Conn
	mu   sync.Mutex
	buf  []byte
	ver  string
	done bool
}

func (c *verConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	if !c.done && n > 0 {
		c.buf = append(c.buf, p[:n]...)
		for {
			i := strings.IndexByte(string(c.buf), '\n')
			if i < 0 {
				break
			}
			line := strings.TrimRight(string(c.buf[:i]), "\r")
			c.buf = c.buf[i+1:]
			if strings.HasPrefix(line, "SSH-") {
				c.ver, c.done = line, true
				break
			}
		}
		if len(c.buf) > 4096 {
			c.done = true
		}
		if c.done {
			c.buf = nil
		}
	}
	c.mu.Unlock()
	return n, err
}

func (c *verConn) version() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ver == "" {
		return "?"
	}
	return c.ver
}

var (
	reBreak = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/h[1-6]|/li)\s*/?>`)
	reTag   = regexp.MustCompile(`<[^>]*>`)
	reBlank = regexp.MustCompile(`\n{3,}`)
)

func CleanBanner(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = reBreak.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, " \t")
		lines = append(lines, l)
	}
	s = strings.Trim(strings.Join(lines, "\n"), "\n ")
	return reBlank.ReplaceAllString(s, "\n\n")
}

func explainSSH(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "unable to authenticate"):
		return fmt.Errorf("login failed: wrong username/password or account expired")
	case strings.Contains(s, "no common algorithm"):
		return fmt.Errorf("%v (try SSH compatibility: Legacy)", err)
	case strings.Contains(s, "i/o timeout"):
		return fmt.Errorf("SSH handshake timed out (server did not answer)")
	}
	return fmt.Errorf("SSH: %v", err)
}
