package daemon

import (
	"net"
	"testing"
)

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

func TestSiteGroupYouTube(t *testing.T) {
	d := &Daemon{}
	d.flows.learnName("rr3---sn-cp1oxu-jb3es.googlevideo.com.", []net.IP{net.ParseIP("202.169.40.206")}, 300)
	for host, want := range map[string]string{
		"202.169.40.206": "youtube", "www.youtube.com": "youtube", "i.ytimg.com": "youtube",
		"youtubei.googleapis.com": "youtube", "8.8.8.8": "", "www.google.com": "", "notyoutube.com": "",
	} {
		if got := d.siteGroup(host); got != want {
			t.Errorf("siteGroup(%q) = %q, want %q", host, got, want)
		}
	}
}
