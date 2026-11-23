package changeip

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHilink(t *testing.T) {
	var sw []string
	logged := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/webserver/SesTokInfo":
			io.WriteString(w, "<response><SesInfo>SessionID=abc</SesInfo><TokInfo>tok123</TokInfo></response>")
		case "/api/user/state-login":
			st := "-1"
			if logged {
				st = "0"
			}
			io.WriteString(w, "<response><State>"+st+"</State><password_type>4</password_type></response>")
		case "/api/user/login":
			b, _ := io.ReadAll(r.Body)
			want := "<Password>"
			if r.Header.Get("__RequestVerificationToken") != "tok123" || !strings.Contains(string(b), want) {
				io.WriteString(w, "<error><code>108006</code></error>")
				return
			}
			logged = true
			http.SetCookie(w, &http.Cookie{Name: "SessionID", Value: "logged"})
			io.WriteString(w, "<response>OK</response>")
		case "/api/dialup/mobile-dataswitch":
			if r.Header.Get("Cookie") != "SessionID=logged" {
				io.WriteString(w, "<error><code>125002</code></error>")
				return
			}
			b, _ := io.ReadAll(r.Body)
			sw = append(sw, string(b))
			io.WriteString(w, "<response>OK</response>")
		}
	}))
	defer srv.Close()
	h := &Hilink{Addr: srv.URL, User: "admin", Pass: "secret"}
	if err := h.DataSwitch(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := h.DataSwitch(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if len(sw) != 2 || !strings.Contains(sw[0], ">0<") || !strings.Contains(sw[1], ">1<") {
		t.Fatalf("switch calls %v", sw)
	}
	if !matchPrefix("114.12.3.4", []string{"10.", "114.12."}) || matchPrefix("114.10.1.1", []string{"114.12."}) {
		t.Fatal("prefix")
	}
	for _, c := range []struct {
		ip   string
		pre  string
		want bool
	}{
		{"114.13.9.9", "114.12.0.0/14", true},
		{"114.16.0.1", "114.12.0.0/14", false},
		{"114.12.200.1", "114.12.5.6/16", true},
		{"36.80.1.1", "10.0.0.0/8", false},
		{"112.215.100.7", "112.215.0.0-112.215.127.255", true},
		{"112.215.128.0", "112.215.0.0-112.215.127.255", false},
		{"112.215.0.0", "112.215.0.0 - 112.215.0.0", true},
		{"114.1.2.3", "114.1.", true},
		{"114.10.2.3", "114.1.", false},
		{"114.12.0.1", "bad/99", false},
		{"2001:db8::1", "2001:db8::/32", true},
		{"2001:db8::1", "0.0.0.0-255.255.255.255", false},
		{"10.0.0.1", "10.0-10.19", true},
		{"10.19.255.255", "10.0-10.19", true},
		{"10.20.0.0", "10.0-10.19", false},
		{"10.145.3.3", "10.130 - 10.159", true},
		{"10.160.0.1", "10.130-10.159", false},
		{"10.5.1.1", "10.5.-10.6.", true},
		{"10.1.1.1", "10.x-10.9", false},
		{"", "10.0-10.19", false},
	} {
		if got := matchPrefix(c.ip, []string{c.pre}); got != c.want {
			t.Errorf("matchPrefix(%s, %s) = %v", c.ip, c.pre, got)
		}
	}
}
