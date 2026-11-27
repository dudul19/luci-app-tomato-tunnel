package tunnel

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAutoResolver(t *testing.T) {
	f := filepath.Join(t.TempDir(), "resolv.conf.auto")
	os.WriteFile(f, []byte("# Interface lan\nnameserver 192.168.1.1\n# Interface wan_4\nnameserver fd00::1\nnameserver 192.168.136.28\n# Interface hp\nnameserver 10.48.11.201\n"), 0644)
	old, oldIf := resolvFiles, ifstatusJSON
	resolvFiles = []string{f}
	ifstatusJSON = func(string) ([]byte, error) { return nil, errors.New("no netifd") }
	defer func() { resolvFiles, ifstatusJSON = old, oldIf }()
	for wan, want := range map[string]string{"hp": "10.48.11.201", "wan": "192.168.136.28", "": "192.168.136.28"} {
		if ip, _, ok := AutoResolver(wan); !ok || ip != want {
			t.Errorf("AutoResolver(%q) = %q %v, want %q", wan, ip, ok, want)
		}
	}
	if _, _, ok := AutoResolver("wwan2"); ok {
		t.Error("unknown interface should not resolve")
	}
	if !IsAutoResolver(" Auto ") || IsAutoResolver("1.1.1.1") {
		t.Error("IsAutoResolver")
	}
}

func TestAutoResolverIfstatus(t *testing.T) {
	f := filepath.Join(t.TempDir(), "resolv.conf.auto")
	os.WriteFile(f, []byte("# Interface lan\nnameserver 192.168.136.28\n"), 0644)
	old, oldIf := resolvFiles, ifstatusJSON
	resolvFiles = []string{f}
	ifstatusJSON = func(i string) ([]byte, error) {
		switch i {
		case "wan":
			return []byte(`{"up":true,"proto":"modemmanager","dns-server":["192.168.136.28"]}`), nil
		case "hp":
			return []byte(`{"up":false,"dns-server":[]}`), nil
		}
		return nil, errors.New("not found")
	}
	defer func() { resolvFiles, ifstatusJSON = old, oldIf }()
	if ip, from, ok := AutoResolver("wan"); !ok || ip != "192.168.136.28" || from != "wan" {
		t.Errorf("wan = %q %q %v", ip, from, ok)
	}
	if ip, _, ok := AutoResolver(""); !ok || ip != "192.168.136.28" {
		t.Errorf("default = %q %v", ip, ok)
	}
	if _, _, ok := AutoResolver("hp"); ok {
		t.Error("hp is down and has no DNS: should not resolve")
	}
}
