package daemon

import (
	"context"
	"errors"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
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

func Measure(ctx context.Context, m config.Main, profiles []config.Profile, timeout time.Duration) LatencyReport {
	rep := LatencyReport{Results: map[string]LatencyResult{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range profiles {
		wg.Add(1)
		go func(p config.Profile) {
			defer wg.Done()
			r := LatencyResult{MS: -1}
			if err := p.Validate(); err != nil {
				r.Error = err.Error()
			} else {
				cctx, cancel := context.WithTimeout(ctx, timeout)
				ms, target, err := tunnel.Probe(cctx, p, m.WanIface, 3)
				cancel()
				r.MS, r.Target = ms, target
				if err != nil {
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

func SaveLatency(rep LatencyReport) {
	os.MkdirAll(RunDir, 0700)
	b, _ := json.Marshal(rep)
	tmp := LatencyFile + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		os.Rename(tmp, LatencyFile)
	}
}
