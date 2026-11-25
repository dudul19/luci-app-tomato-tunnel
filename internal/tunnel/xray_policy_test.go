package tunnel

import (
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
