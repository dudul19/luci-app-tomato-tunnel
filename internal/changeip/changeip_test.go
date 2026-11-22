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
}
