package tunnel

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func joined(segs []Segment) string {
	var b strings.Builder
	for i, s := range segs {
		if i > 0 {
			b.WriteString("|")
		}
		b.Write(s.Data)
	}
	return b.String()
}

func TestPayloadTokens(t *testing.T) {
	v := PayloadVars{Host: "srv.example.com", Port: 443, Bug: "bug.example", UA: "UA", IP: "1.2.3.4", Proxy: "10.1.2.3:8080", Key: "k"}
	cases := map[string]string{
		"[host] [server] [ssh_host] [port] [ssh_port] [sni_port]": "srv.example.com srv.example.com srv.example.com 443 443 443",
		"[host_port] [ssh] [ip] [ip_port]":                        "srv.example.com:443 srv.example.com:443 1.2.3.4 1.2.3.4:443",
		"[bug] [sni] [sni_host]":                                  "bug.example bug.example bug.example",
		"[proxy] [proxy_host] [proxy_port]":                       "10.1.2.3:8080 10.1.2.3 8080",
		"[CRLF][Crlf*2][lf*3]x":                                   "\r\n\r\n\r\n\n\n\nx",
		`a\r\nb`:                                                  "a\r\nb",
		"[unknown] [method] [protocol]":                           "[unknown] CONNECT HTTP/1.1",
		"[netData][crlf][realData]":                               "CONNECT srv.example.com:443 HTTP/1.0\r\nCONNECT srv.example.com:443 HTTP/1.0",
	}
	for in, want := range cases {
		if got := joined(Build(in, v, 0)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	segs := Build("Sec-WebSocket-Key: [ws_key]", v, 0)
	k := strings.TrimPrefix(string(segs[0].Data), "Sec-WebSocket-Key: ")
	if b, err := base64.StdEncoding.DecodeString(k); err != nil || len(b) != 16 {
		t.Errorf("ws_key %q", k)
	}
}

func TestPayloadSplits(t *testing.T) {
	v := PayloadVars{Host: "h", Port: 80}
	segs := Build("a[split]b[splitNoDelay]c[delay]d[INSTANT_SPLIT]e[split_delay]f", v, time.Second)
	want := []struct {
		s string
		d time.Duration
	}{{"a", 0}, {"b", time.Second}, {"c", 0}, {"d", 1500 * time.Millisecond}, {"e", 0}, {"f", 1500 * time.Millisecond}}
	if len(segs) != len(want) {
		t.Fatalf("segments %d: %q", len(segs), joined(segs))
	}
	for i, w := range want {
		if string(segs[i].Data) != w.s || segs[i].Delay != w.d {
			t.Errorf("seg %d: %q %v, want %q %v", i, segs[i].Data, segs[i].Delay, w.s, w.d)
		}
	}
}

func TestRotateRandom(t *testing.T) {
	var got []string
	for i := 0; i < 4; i++ {
		got = append(got, PickValue("x.[rotate=a;b;c].com", "rot"))
	}
	if strings.Join(got, ",") != "x.a.com,x.b.com,x.c.com,x.a.com" {
		t.Errorf("rotate value: %v", got)
	}
	for i := 0; i < 20; i++ {
		if v := PickValue("[RANDOM=a;b]", "r"); v != "a" && v != "b" {
			t.Fatalf("random value %q", v)
		}
	}
	v := PayloadVars{Host: "h", Port: 80, Key: "variants"}
	var ps []string
	for i := 0; i < 3; i++ {
		ps = append(ps, joined(Build("GET /1[rotate]GET /2", v, 0)))
	}
	if strings.Join(ps, ",") != "GET /1,GET /2,GET /1" {
		t.Errorf("rotate payload: %v", ps)
	}
	for i := 0; i < 10; i++ {
		if p := joined(Build("A[random]B[random]C", v, 0)); p != "A" && p != "B" && p != "C" {
			t.Fatalf("random payload %q", p)
		}
	}
	if HasPick("plain.host") || !HasPick("[rotate=a;b]") {
		t.Error("HasPick")
	}
}
