package daemon

import "testing"

func TestDirectMatch(t *testing.T) {
	list := []string{"bca.co.id", ".mobile-legends.com", " Klikbca.com "}
	for n, want := range map[string]bool{
		"m.bca.co.id": true, "bca.co.id": true, "notbca.co.id": false,
		"api.mobile-legends.com": true, "klikbca.com": true, "": false, "google.com": false,
	} {
		if got := directMatch(n, list); got != want {
			t.Errorf("directMatch(%q) = %v, want %v", n, got, want)
		}
	}
}
