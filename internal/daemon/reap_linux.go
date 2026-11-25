package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/logx"
	"github.com/dudul19/tomato-tunnel/internal/tunnel"
)

// reapXrayLoop stops Xray processes that no pool owns any more: children of
// this daemon that are not in the tunnel's list of live Xray, and leftovers of
// an earlier daemon (parent 1) that still run on a config in RunDir. Each one
// must be seen twice in a row (a minute apart) before it is killed, so an Xray
// that is just starting is never touched.
func reapXrayLoop(ctx context.Context, log *logx.Logger) {
	seen := map[int]bool{}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := map[int]bool{}
		for _, pid := range strayXray(os.Getpid()) {
			if !seen[pid] {
				now[pid] = true
				continue
			}
			if err := syscall.Kill(pid, syscall.SIGKILL); err == nil {
				log.Warnf("xray", "Stopped a leftover Xray (pid %d) that no account was using", pid)
			}
		}
		seen = now
	}
}

func strayXray(self int) []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		cmd, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil || len(cmd) == 0 {
			continue
		}
		args := strings.Split(string(bytes.TrimRight(cmd, "\x00")), "\x00")
		if filepath.Base(args[0]) != "xray" || len(args) < 4 || args[1] != "run" ||
			!strings.HasPrefix(args[3], RunDir+"/xray-") {
			continue
		}
		ppid := parentPID(e.Name())
		if (ppid == self && !tunnel.XrayLive(pid)) || ppid == 1 {
			out = append(out, pid)
		}
	}
	return out
}

func parentPID(pid string) int {
	b, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return -1
	}
	// "pid (comm) state ppid ..." where comm may hold spaces or ')'
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return -1
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 2 {
		return -1
	}
	n, _ := strconv.Atoi(f[1])
	return n
}
