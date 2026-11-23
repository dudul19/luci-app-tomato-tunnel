package daemon

import (
	"testing"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/tunnel"
)

func spread(b *balancer, ps []*tunnel.Pool, n int) []int {
	got := make([]int, len(ps))
	cnt := map[*tunnel.Pool]int64{}
	for i := 0; i < n; i++ {
		k := b.pick(ps, cnt)
		cnt[ps[k]]++
		got[k]++
	}
	return got
}

func TestBalancePick(t *testing.T) {
	b := newBalancer()
	mk := func(id string, w int) *tunnel.Pool {
		return &tunnel.Pool{P: config.Profile{ID: id, Name: id, Weight: w}}
	}
	modem, hp1, hp2 := mk("modem", 2), mk("hp1", 1), mk("hp2", 1)
	ps := []*tunnel.Pool{modem, hp1, hp2}

	got := spread(b, ps, 40)
	if got[0] != 20 || got[1] != 10 || got[2] != 10 {
		t.Fatalf("weights 2/1/1 over 40: %v", got)
	}

	// hp2 is a slow account (SlowDNS): a tenth of the others' speed.
	b.peak[modem], b.peak[hp1], b.peak[hp2] = 8e6, 8e6, 8e5
	got = spread(b, ps, 46)
	if got[2] >= got[1] || got[2] < 3 {
		t.Fatalf("slow account should get less but not nothing: %v", got)
	}

	// Idle: no speed difference is trusted.
	b.peak[modem], b.peak[hp1], b.peak[hp2] = 1000, 1000, 10
	got = spread(b, ps, 40)
	if got[1] != got[2] {
		t.Fatalf("idle should ignore speed: %v", got)
	}
}
