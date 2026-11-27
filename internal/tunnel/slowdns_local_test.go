package tunnel

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/logx"
)

func TestSlowDNSLocal(t *testing.T) {
	bin := os.Getenv("TT_DNSTT_SERVER")
	if bin == "" {
		t.Skip("set TT_DNSTT_SERVER to a dnstt-server binary")
	}
	dir := t.TempDir()
	key, pub := filepath.Join(dir, "k"), filepath.Join(dir, "p")
	if out, err := exec.Command(bin, "-gen-key", "-privkey-file", key, "-pubkey-file", pub).CombinedOutput(); err != nil {
		t.Fatalf("gen-key: %v %s", err, out)
	}
	pb, _ := os.ReadFile(pub)
	pubHex := strings.TrimSpace(string(pb))

	const size = 1 << 20
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		blob := make([]byte, size)
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func() { c.Write(blob); c.Close() }()
		}
	}()

	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	addr := pc.LocalAddr().String()
	pc.Close()
	srv := exec.Command(bin, "-udp", addr, "-privkey-file", key, "t.test", up.Addr().String())
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Process.Kill()
	time.Sleep(300 * time.Millisecond)

	{
		for _, rt := range []string{"txt", "aaaa"} {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			s, err := NewSlowDNS(ctx, "t.test", pubHex, addr, rt, 0, SlowDNSTune{}, &net.Dialer{}, logx.New("", 10, false))
			if err != nil {
				cancel()
				t.Fatalf("%s: %v", rt, err)
			}
			t0 := time.Now()
			st, err := s.Open()
			if err != nil {
				t.Fatal(err)
			}
			n, err := io.Copy(io.Discard, st)
			d := time.Since(t0)
			st.Close()
			s.Close()
			cancel()
			if n != size {
				t.Fatalf("%s: got %d bytes (%v)", rt, n, err)
			}
			t.Logf("%-4s: 1 MB in %v", rt, d.Round(time.Millisecond))
		}
	}
}
