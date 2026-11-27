package tunnel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dudul19/tomato-tunnel/internal/config"
)

func TestXrayConfigKeepsIdleConnections(t *testing.T) {
	b, err := XrayConfig(config.Profile{Type: "xray", XProto: "trojan", Host: "1.2.3.4", Port: 443, Pass: "x", XNet: "ws", XSec: "tls"}, 10808, "", "", XrayOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"connIdle": 3600`) {
		t.Fatalf("connIdle 3600 missing:\n%s", b)
	}
	if !strings.Contains(string(b), `"heartbeatPeriod": 30`) {
		t.Fatalf("WebSocket heartbeat missing:\n%s", b)
	}
}

func TestXrayOverDNSOutbound(t *testing.T) {
	p := config.Profile{Type: "xray", XProto: "vless", UUID: "id", XNet: "ws", XSec: "none", XPath: "/vless", Host: "ath.example.com", Port: 80, UseDNS: true, DNSDomain: "ns.example.com"}
	q := xrayOverDNS(p, 4321)
	for _, shared := range []bool{false, true} {
		out, hosts, err := xrayOutbound(q, "", "", shared)
		if err != nil {
			t.Fatal(err)
		}
		if len(hosts) != 0 {
			t.Fatalf("hosts %v", hosts)
		}
		b, _ := json.Marshal(out)
		s := string(b)
		for _, w := range []string{`"address":"127.0.0.1"`, `"port":4321`, `"host":"ath.example.com"`, `"path":"/vless"`, `"security":"none"`} {
			if !strings.Contains(s, w) {
				t.Fatalf("shared=%v missing %s in %s", shared, w, s)
			}
		}
		if strings.Contains(s, "interface") {
			t.Fatalf("loopback must not bind the modem: %s", s)
		}
	}
}
