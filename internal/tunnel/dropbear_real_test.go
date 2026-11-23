package tunnel

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/logx"
)

func realDropbear(t *testing.T, args ...string) (string, int, *exec.Cmd) {
	bin := os.Getenv("DROPBEAR_BIN")
	if bin == "" {
		t.Skip("DROPBEAR_BIN not set")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "rsa")
	if out, err := exec.Command(filepath.Join(filepath.Dir(bin), "dropbearkey"), "-t", "rsa", "-s", "2048", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("dropbearkey: %v %s", err, out)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cmd := exec.Command(bin, append([]string{"-F", "-r", key, "-p", "127.0.0.1:" + strconv.Itoa(port)}, args...)...)
	if testing.Verbose() {
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "127.0.0.1", port, cmd
}

func TestRealDropbearKeepalive(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{{"default", nil}, {"no-remote-fwd -k", []string{"-k"}}} {
		t.Run(tc.name, func(t *testing.T) {
			h, port, _ := realDropbear(t, tc.args...)
			oe, ow := keepaliveEvery, keepaliveWait
			keepaliveEvery, keepaliveWait = 500*time.Millisecond, 700*time.Millisecond
			defer func() { keepaliveEvery, keepaliveWait = oe, ow }()
			p := config.Profile{Name: "t", Host: h, Port: port, User: os.Getenv("USER"), Pass: "test"}
			log := logx.New("", 200, testing.Verbose())
			pool := NewPool(p, "auto", "", log)
			if err := pool.Connect(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			echo := echoServer(t)
			roundTrip(t, pool, echo)
			select {
			case <-pool.Dead():
				t.Fatal("session dropped by keepalive")
			case <-time.After(4 * time.Second):
			}
			if pool.Sessions() != 1 {
				t.Fatalf("sessions = %d", pool.Sessions())
			}
			roundTrip(t, pool, echo)
		})
	}
}

func TestRealDropbearHungServerDetected(t *testing.T) {
	h, port, cmd := realDropbear(t, "-k")
	oe, ow := keepaliveEvery, keepaliveWait
	keepaliveEvery, keepaliveWait = 500*time.Millisecond, 700*time.Millisecond
	defer func() { keepaliveEvery, keepaliveWait = oe, ow }()
	p := config.Profile{Name: "t", Host: h, Port: port, User: os.Getenv("USER"), Pass: "test"}
	pool := NewPool(p, "auto", "", logx.New("", 200, testing.Verbose()))
	if err := pool.Connect(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	time.Sleep(2 * time.Second)
	exec.Command("pkill", "-STOP", "-P", strconv.Itoa(cmd.Process.Pid)).Run()
	defer exec.Command("pkill", "-CONT", "-P", strconv.Itoa(cmd.Process.Pid)).Run()
	select {
	case <-pool.Dead():
	case <-time.After(5 * time.Second):
		t.Fatal("hung server not detected")
	}
}
