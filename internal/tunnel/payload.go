package tunnel

import (
	"strconv"
	"strings"
	"time"
)

const (
	DefaultWS      = "GET / HTTP/1.1[crlf]Host: [bug][crlf]Connection: Upgrade[crlf]User-Agent: [ua][crlf]Upgrade: websocket[crlf][crlf]"
	DefaultWSS     = "GET wss://[bug]/ HTTP/1.1[crlf]Host: [bug][crlf]Connection: Upgrade[crlf]User-Agent: [ua][crlf]Upgrade: websocket[crlf][crlf]"
	DefaultPayload = "CONNECT [host_port] HTTP/1.1[crlf]Host: [bug][crlf][crlf]"
)

type PayloadVars struct {
	Host string
	Port int
	Bug  string
	UA   string
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

func Build(payload string, v PayloadVars, splitDelay time.Duration) []Segment {
	r := strings.NewReplacer(
		"[delay_split]", sepDelay, "[split_delay]", sepDelay,
		"[instant_split]", sepInstant, "[split_instant]", sepInstant,
		"[split]", sepSplit,
	)
	p := r.Replace(payload)
	hp := v.Host + ":" + strconv.Itoa(v.Port)
	bug := v.Bug
	if bug == "" {
		bug = v.Host
	}
	exp := strings.NewReplacer(
		"[crlf*2]", "\r\n\r\n", "[crlf]", "\r\n", "[lfcr]", "\n\r", "[cr]", "\r", "[lf]", "\n",
		"[host_port]", hp, "[ssh]", hp, "[host]", v.Host, "[port]", strconv.Itoa(v.Port),
		"[bug]", bug, "[sni]", bug, "[ua]", v.UA, "[protocol]", "HTTP/1.1", "[method]", "CONNECT",
		"[raw]", "CONNECT "+hp+" HTTP/1.0\r\n\r\n", "[real_raw]", "CONNECT "+hp+" HTTP/1.0\r\n\r\n",
		"[netData]", "CONNECT "+hp+" HTTP/1.0", "[realData]", "CONNECT "+hp+" HTTP/1.0",
		"[auth]", "",
	)
	p = exp.Replace(p)

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
