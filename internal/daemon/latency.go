package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/logx"
	"github.com/dudul19/tomato-tunnel/internal/tunnel"
)

var LatencyFile = RunDir + "/latency.json"

type LatencyResult struct {
	MS     int64  `json:"ms"`
	Target string `json:"target,omitempty"`
	Auth   bool   `json:"login_failed,omitempty"`
	Error  string `json:"error,omitempty"`
}

type LatencyReport struct {
	At      int64                    `json:"at"`
	Results map[string]LatencyResult `json:"results"`
}

func ProfileKey(p config.Profile) string {
	if p.Alias != "" {
		return p.Alias
	}
	return p.ID
}

// Measure gives every profile a real delay: it logs in with the account
// (SSH, Xray, ZiVPN, SlowDNS…) and times a request to the check URL through it,
// like the "real delay" test in v2rayN. A dead, locked or expired account fails
// here even when its server still answers TCP. Accounts that are running right
// now keep their live ping, so no second login is opened for them.
func Measure(ctx context.Context, m config.Main, profiles []config.Profile, timeout time.Duration) LatencyReport {
	rep := LatencyReport{Results: map[string]LatencyResult{}}
	live := livePings()
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3) // Xray accounts start a core each; keep the router light
	for _, p := range profiles {
		wg.Add(1)
		go func(p config.Profile) {
			defer wg.Done()
			r := LatencyResult{MS: -1}
			if ms, ok := livePing(live, p); ok {
				r.MS, r.Target = ms, "live tunnel"
			} else if err := p.Validate(); err != nil {
				r.Error = err.Error()
			} else {
				sem <- struct{}{}
				cctx, cancel := context.WithTimeout(ctx, timeout)
				ms, err := realDelay(cctx, m, p)
				cancel()
				<-sem
				r.MS, r.Target = ms, m.CheckURL
				if err != nil {
					r.MS = -1
					r.Error = err.Error()
					r.Auth = errors.Is(err, tunnel.ErrAuth)
				}
			}
			mu.Lock()
			rep.Results[ProfileKey(p)] = r
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	rep.At = time.Now().Unix()
	return rep
}

// realDelay connects the account, then times the check URL through it (unified
// delay: the second request on a warm connection; otherwise the faster of two).
func realDelay(ctx context.Context, m config.Main, p config.Profile) (int64, error) {
	pool := tunnel.NewPool(p, m.SSHCompat, m.WanIface, logx.New("", 10, false))
	pool.X = XrayOpts(m)
	defer pool.Close()
	if err := pool.Connect(ctx, 1); err != nil {
		return -1, err
	}
	best := int64(-1)
	var last error
	tries := 2
	if m.UnifiedDelay {
		tries = 1 // check already warms the connection and times the second request
	}
	for i := 0; i < tries && ctx.Err() == nil; i++ {
		ms, err := check(pool, m.CheckURL, m.UnifiedDelay)
		if err != nil {
			last = err
			continue
		}
		if best < 0 || ms < best {
			best = ms
		}
	}
	if best < 0 {
		if last == nil {
			last = ctx.Err()
		}
		return -1, fmt.Errorf("connected, but %s failed through it: %s", m.CheckURL, shortErr(last))
	}
	return best, nil
}

// livePings reads the running tunnel's own pings (main account and Balance accounts).
func livePings() map[string]int64 {
	out := map[string]int64{}
	b, err := os.ReadFile(StatusFile)
	if err != nil {
		return out
	}
	var st Status
	if json.Unmarshal(b, &st) != nil || st.State != "connected" {
		return out
	}
	if st.Profile != "" && st.PingMS > 0 {
		out[st.Profile] = st.PingMS
	}
	for _, x := range st.Balance {
		if x.State == "connected" && !x.Paused && x.PingMS > 0 && x.Key != "" {
			out[x.Key] = x.PingMS
		}
	}
	return out
}

func livePing(live map[string]int64, p config.Profile) (int64, bool) {
	for _, k := range []string{ProfileKey(p), p.ID, p.Alias} {
		if k == "" {
			continue
		}
		if ms, ok := live[k]; ok {
			return ms, true
		}
	}
	return 0, false
}

func SaveLatency(rep LatencyReport) {
	os.MkdirAll(RunDir, 0700)
	b, _ := json.Marshal(rep)
	tmp := LatencyFile + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		os.Rename(tmp, LatencyFile)
	}
}
