package daemon

import (
	"testing"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/tunnel"
)

func spread(b *balancer, ps []*tunnel.Pool, n int) []int {
	got := make([]int, len(ps))
	for i := 0; i < n; i++ {
		k := b.pick(ps, b.rrT)
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

	// The speed a pool carries must not change its share: that fed on itself.
	b.peak[modem], b.peak[hp1], b.peak[hp2] = 8e6, 8e6, 8e5
	got = spread(b, ps, 40)
	if got[0] != 20 || got[1] != 10 || got[2] != 10 {
		t.Fatalf("carried speed changed the shares: %v", got)
	}
}

// Idle connections must not steer new ones: with weights 1 and 1 the turns
// alternate however many connections each account already has open.
func TestBalancePickIgnoresOpenConnections(t *testing.T) {
	b := newBalancer()
	a := &tunnel.Pool{P: config.Profile{ID: "a", Name: "a", Weight: 1}}
	c := &tunnel.Pool{P: config.Profile{ID: "c", Name: "c", Weight: 1}}
	b.tcp[a] = 30 // idle push and chat connections
	got := spread(b, []*tunnel.Pool{a, c}, 8)
	if got[0] != 4 || got[1] != 4 {
		t.Fatalf("1:1 over 8 new connections: %v", got)
	}
	b2 := newBalancer()
	got = spread(b2, []*tunnel.Pool{&tunnel.Pool{P: config.Profile{ID: "x", Weight: 3}}, &tunnel.Pool{P: config.Profile{ID: "y", Weight: 1}}}, 8)
	if got[0] != 6 || got[1] != 2 {
		t.Fatalf("3:1 over 8: %v", got)
	}
}
