package uci

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	src := `
config main 'main'
	option enabled '1'
	option payload 'GET / HTTP/1.1[crlf]Host: [bug][crlf][crlf]'
	list bypass '192.168.1.50'
	list bypass "10.0.0.0/8"

config profile 'sg1'
	option name 'it'\''s mine' # comment
	option pass "a\"b"

config profile
	option name 'anon'
`
	ss, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 3 {
		t.Fatalf("got %d sections", len(ss))
	}
	if ss[0].Options["payload"] != "GET / HTTP/1.1[crlf]Host: [bug][crlf][crlf]" {
		t.Fatalf("payload %q", ss[0].Options["payload"])
	}
	if len(ss[0].Lists["bypass"]) != 2 {
		t.Fatal("lists")
	}
	if ss[1].Options["name"] != "it's mine" || ss[1].Options["pass"] != `a"b` {
		t.Fatalf("quotes %q %q", ss[1].Options["name"], ss[1].Options["pass"])
	}
	if ss[2].Name != "@profile[1]" {
		t.Fatalf("anon name %q", ss[2].Name)
	}
}
