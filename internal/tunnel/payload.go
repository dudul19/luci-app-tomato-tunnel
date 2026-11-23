package tunnel

import (
	"crypto/rand"
	"encoding/base64"
	mrand "math/rand/v2"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultWS      = "GET / HTTP/1.1[crlf]Host: [bug][crlf]Connection: Upgrade[crlf]User-Agent: [ua][crlf]Upgrade: websocket[crlf][crlf]"
	DefaultWSS     = "GET wss://[bug]/ HTTP/1.1[crlf]Host: [bug][crlf]Connection: Upgrade[crlf]User-Agent: [ua][crlf]Upgrade: websocket[crlf][crlf]"
	DefaultPayload = "CONNECT [host_port] HTTP/1.1[crlf]Host: [bug][crlf][crlf]"
)

type PayloadVars struct {
	Host  string
	Port  int
	Bug   string
	UA    string
	IP    string // resolved server IP, for [ip] and [ip_port]
	Proxy string // remote proxy host:port, for [proxy]
	Key   string // keeps [rotate] turns apart per account
}

type Segment struct {
	Data  []byte
	Delay time.Duration
}

const (
	sepSplit   = "\x00S\x00"
	sepDelay   = "\x00D\x00"
	sepInstant = "\x00I\x00"
)

var (
	reToken   = regexp.MustCompile(`\[([A-Za-z_]+)(?:\*(\d{1,2}))?\]`)
	reValue   = regexp.MustCompile(`(?i)\[(rotate|random)=([^\]]*)\]`)
	reVariant = regexp.MustCompile(`(?i)\[(rotate|random)\]`)

	turnMu sync.Mutex
	turns  = map[string]int{}
)

// turn returns 0, 1, 2, ... for each call with the same key.
func turn(key string) int {
	turnMu.Lock()
	defer turnMu.Unlock()
	n := turns[key]
	turns[key] = n + 1
	if len(turns) > 1000 {
		turns = map[string]int{key: n + 1}
	}
	return n
}

// PickValue resolves [rotate=a;b;c] (next value on every connect) and
// [random=a;b;c] (any value) in a field such as the bug host, the remote
// proxy or the payload.
func PickValue(s, key string) string {
	if !strings.Contains(s, "=") {
		return s
	}
	return reValue.ReplaceAllStringFunc(s, func(m string) string {
		g := reValue.FindStringSubmatch(m)
		var vals []string
		for _, v := range strings.Split(g[2], ";") {
			if v = strings.TrimSpace(v); v != "" {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			return ""
		}
		if strings.EqualFold(g[1], "random") {
			return vals[mrand.IntN(len(vals))]
		}
		return vals[turn(key+"\x00"+g[2])%len(vals)]
	})
}

// HasPick reports whether a field uses [rotate=…] or [random=…].
func HasPick(s string) bool { return reValue.MatchString(s) }

// variant picks one payload from a payload cut by [rotate] (in turn) or
// [random] (any of them).
func variant(p, key string) string {
	loc := reVariant.FindAllStringSubmatchIndex(p, -1)
	if len(loc) == 0 {
		return p
	}
	random := strings.EqualFold(p[loc[0][2]:loc[0][3]], "random")
	var parts []string
	last := 0
	for _, l := range loc {
		parts = append(parts, p[last:l[0]])
		last = l[1]
	}
	parts = append(parts, p[last:])
	var vs []string
	for _, x := range parts {
		if strings.TrimSpace(x) != "" {
			vs = append(vs, x)
		}
	}
	if len(vs) == 0 {
		return ""
	}
	if random {
		return vs[mrand.IntN(len(vs))]
	}
	return vs[turn(key+"\x00payload\x00"+p)%len(vs)]
}

func wsKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func Build(payload string, v PayloadVars, splitDelay time.Duration) []Segment {
	p := variant(payload, v.Key)
	p = PickValue(p, v.Key)
	p = strings.NewReplacer(`\r`, "\r", `\n`, "\n").Replace(p)

	port := strconv.Itoa(v.Port)
	hp := net.JoinHostPort(v.Host, port)
	bug := v.Bug
	if bug == "" {
		bug = v.Host
	}
	ip := v.IP
	if ip == "" {
		ip = v.Host
	}
	ph, pp, _ := net.SplitHostPort(v.Proxy)
	connect := "CONNECT " + hp + " HTTP/1.0"
	val := map[string]string{
		"crlf": "\r\n", "lfcr": "\n\r", "cr": "\r", "lf": "\n",
		"host": v.Host, "server": v.Host, "ssh_host": v.Host,
		"port": port, "ssh_port": port, "sni_port": port,
		"host_port": hp, "ssh": hp,
		"ip": ip, "ip_port": net.JoinHostPort(ip, port),
		"bug": bug, "sni": bug, "sni_host": bug,
		"proxy": v.Proxy, "proxy_host": ph, "proxy_port": pp,
		"ua": v.UA, "protocol": "HTTP/1.1", "method": "CONNECT",
		"raw": connect + "\r\n\r\n", "real_raw": connect + "\r\n\r\n",
		"netdata": connect, "realdata": connect,
		"auth":  "",
		"split": sepSplit,
		"delay": sepDelay, "delay_split": sepDelay, "split_delay": sepDelay,
		"instant_split": sepInstant, "split_instant": sepInstant, "splitnodelay": sepInstant,
	}
	p = reToken.ReplaceAllStringFunc(p, func(m string) string {
		g := reToken.FindStringSubmatch(m)
		name := strings.ToLower(g[1])
		var s string
		if name == "ws_key" {
			s = wsKey()
		} else if x, ok := val[name]; ok {
			s = x
		} else {
			return m // unknown token: sent as written
		}
		if n, _ := strconv.Atoi(g[2]); n > 1 {
			s = strings.Repeat(s, n)
		}
		return s
	})

	var segs []Segment
	delay := time.Duration(0)
	for {
		i, kind := nextSep(p)
		if i < 0 {
			if p != "" {
				segs = append(segs, Segment{Data: []byte(p), Delay: delay})
			}
			return segs
		}
		if i > 0 {
			segs = append(segs, Segment{Data: []byte(p[:i]), Delay: delay})
			delay = 0
		}
		switch kind {
		case sepSplit:
			delay += splitDelay
		case sepDelay:
			delay += 1500 * time.Millisecond
		}
		p = p[i+len(kind):]
	}
}

func nextSep(p string) (int, string) {
	best, kind := -1, ""
	for _, s := range []string{sepSplit, sepDelay, sepInstant} {
		if i := strings.Index(p, s); i >= 0 && (best < 0 || i < best) {
			best, kind = i, s
		}
	}
	return best, kind
}
