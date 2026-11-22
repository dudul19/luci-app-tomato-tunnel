package bot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTG struct {
	mu    sync.Mutex
	calls []map[string]any
	names []string
	id    int64
}

func (f *fakeTG) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	body, _ := io.ReadAll(r.Body)
	var p map[string]any
	json.Unmarshal(body, &p)
	f.calls = append(f.calls, p)
	f.names = append(f.names, m)
	f.id++
	switch m {
	case "getUpdates":
		w.Write([]byte(`{"ok":true,"result":[]}`))
	default:
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1000 + f.id}})
	}
}

func (f *fakeTG) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, x := range f.names {
		if x == method {
			n++
		}
	}
	return n
}

func (f *fakeTG) last(method string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.names) - 1; i >= 0; i-- {
		if f.names[i] == method {
			return f.calls[i]
		}
	}
	return nil
}

const testConf = `config main 'main'
	option profile 'p1'
	option policy 'fallback'
	option expiry_warn '3'

config profile 'p1'
	option name 'SG VIP'
	option host 'sg1.example.com'
	option port '443'
	option user 'budi'
	option pass 'secret'
	option use_payload '1'
	option use_tls '1'
	option expires '2026-12-31'

config profile 'p2'
	option name 'JKT Reality'
	option type 'xray'
	option x_proto 'vless'
	option x_net 'xhttp'
	option x_sec 'reality'
	option host 'jkt.example.com'
	option port '443'
	option uuid '3f1c0000-0000-4000-8000-000000009a2e'

config bot 'bot'
	option enabled '1'
	option token '123:abc'
	list admin '111:Dudul'
`

func setup(t *testing.T) (*Bot, *fakeTG) {
	dir := t.TempDir()
	RunDir = dir
	StateFile = filepath.Join(dir, "state.json")
	LeaseFile = filepath.Join(dir, "leases")
	os.WriteFile(LeaseFile, []byte("1700000000 aa:bb:cc:dd:ee:ff 192.168.1.142 Redmi *\n"), 0644)
	ctlScript := filepath.Join(dir, "ctl")
	os.WriteFile(ctlScript, []byte("#!/bin/sh\ncase \"$1\" in usage) echo '{\"days\":{},\"months\":{}}';; latency-cached) echo '{\"results\":{\"p1\":{\"ms\":48}}}';; *) echo ok;; esac\n"), 0755)
	CtlPath = ctlScript
	conf := filepath.Join(dir, "tomato-tunnel")
	os.WriteFile(conf, []byte(testConf), 0600)
	st, _ := json.Marshal(map[string]any{"state": "connected", "profile": "p1", "profile_name": "SG VIP", "pid": os.Getpid(), "public_ip": "114.79.12.34", "isp": "AS23693 Telkomsel", "ping_ms": 48, "since": time.Now().Add(-time.Hour).Unix(), "policy": "fallback"})
	os.WriteFile(filepath.Join(dir, "status.json"), st, 0644)
	f := &fakeTG{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	apiBase = srv.URL
	cfg, err := loadConf(conf)
	if err != nil {
		t.Fatal(err)
	}
	b := &Bot{path: conf, version: "1.4.0", c: newClient(cfg.Token, "wan", ""), cfg: cfg,
		panels: map[int64]int64{}, pending: map[int64]*pend{}, denied: map[int64]time.Time{}, pairTry: map[int64][]time.Time{}, quiet: map[string]time.Time{}, st: loadState()}
	return b, f
}

func msg(from int64, text string) tgUpdate {
	return tgUpdate{Message: &tgIncoming{MessageID: 5, From: &tgUser{ID: from, FirstName: "X"}, Chat: tgChat{ID: from, Type: "private"}, Text: text}}
}

func cb(from int64, data string) tgUpdate {
	return tgUpdate{Callback: &tgCallback{ID: "q", From: tgUser{ID: from}, Data: data, Message: &tgIncoming{MessageID: 7, Chat: tgChat{ID: from, Type: "private"}}}}
}

func TestMenusRender(t *testing.T) {
	b, f := setup(t)
	ctx := context.Background()
	b.handle(ctx, msg(111, "/start"))
	p := f.last("sendRichMessage")
	if p == nil {
		t.Fatal("no rich message for /start")
	}
	html := p["rich_message"].(map[string]any)["html"].(string)
	for _, want := range []string{"Tomato Tunnel", "SG VIP", "114.79.12.34", "<table bordered compact>"} {
		if !strings.Contains(html, want) {
			t.Errorf("main menu misses %q: %s", want, html)
		}
	}
	for _, d := range []string{"st", "pf", "us:today", "us:7d", "us:month", "ac", "a:p1", "a:p2", "ax:p2", "rt", "dv", "tl", "lg", "nt", "sp", "ip", "a+", "rsf", "m"} {
		before := f.count("sendRichMessage")
		b.handle(ctx, cb(111, d))
		if f.count("sendRichMessage") <= before {
			t.Errorf("callback %s sent nothing", d)
		}
	}
	if f.count("answerCallbackQuery") < 19 {
		t.Errorf("callbacks not answered")
	}
}

func TestStrangerDenied(t *testing.T) {
	b, f := setup(t)
	ctx := context.Background()
	b.handle(ctx, msg(999, "/start"))
	p := f.last("sendRichMessage")
	if p == nil || !strings.Contains(p["rich_message"].(map[string]any)["html"].(string), "999") {
		t.Fatal("stranger did not get the denied card with the ID")
	}
	n := f.count("sendRichMessage")
	b.handle(ctx, cb(999, "sp"))
	if f.count("sendRichMessage") != n {
		t.Fatal("stranger callback did something")
	}
	b.handle(ctx, msg(999, "/pair 123456"))
	if !strings.Contains(f.last("sendRichMessage")["rich_message"].(map[string]any)["html"].(string), "Pairing failed") {
		t.Fatal("wrong pairing code accepted")
	}
	for i := 0; i < 6; i++ {
		b.handle(ctx, msg(999, "/pair 000000"))
	}
	if !strings.Contains(f.last("sendRichMessage")["rich_message"].(map[string]any)["html"].(string), "Too many") {
		t.Fatal("pairing not rate limited")
	}
}

func TestLinkIsDeletedAndParsed(t *testing.T) {
	b, f := setup(t)
	ctx := context.Background()
	b.handle(ctx, cb(111, "a+"))
	b.handle(ctx, msg(111, "vless://3f1c0000-0000-4000-8000-000000009a2e@jkt.example.com:443?security=reality&type=xhttp&pbk=abc&sni=www.example.com#rina"))
	del := false
	f.mu.Lock()
	for i, n := range f.names {
		if n == "deleteMessage" && f.calls[i]["message_id"] == float64(5) {
			del = true
		}
	}
	f.mu.Unlock()
	if !del {
		t.Fatal("message with the link was not deleted")
	}
	html := f.last("sendRichMessage")["rich_message"].(map[string]any)["html"].(string)
	if !strings.Contains(html, "rina") || strings.Contains(html, "3f1c0000") {
		t.Fatalf("bad add card: %s", html)
	}
}

func TestParseAccounts(t *testing.T) {
	l := parseAccounts("sg1.example.com:80@budi:secret")
	if len(l) != 1 || l[0]["user"] != "budi" || l[0]["use_payload"] != "1" {
		t.Fatalf("ssh short: %v", l)
	}
	l = parseAccounts("Host : sg1.example.com\nUsername : budi\nPassword : secret\nNS : ns.example.com\nPub key : " + strings.Repeat("a", 64) + "\nExpired : 2026-12-20")
	if len(l) != 1 || l[0]["use_slowdns"] != "1" || l[0]["expires"] != "2026-12-20" {
		t.Fatalf("account text: %v", l)
	}
	l = parseAccounts("trojan://pw@t.example.com:443?type=ws&path=%2Ftrojan#T1 vmess://eyJhZGQiOiJ2LmV4YW1wbGUuY29tIiwicG9ydCI6IjQ0MyIsImlkIjoiYWJjIiwibmV0Ijoid3MiLCJ0bHMiOiJ0bHMiLCJwcyI6IlZNIn0=")
	if len(l) != 2 || l[0]["pass"] != "pw" || l[0]["x_sec"] != "tls" || l[1]["name"] != "VM" || l[1]["x_net"] != "ws" {
		t.Fatalf("links: %v", l)
	}
}
