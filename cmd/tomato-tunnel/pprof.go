//go:build pprof

package main

// Profiling build (go build -tags pprof): CPU and memory profiles on
// http://127.0.0.1:6060/debug/pprof/, router only. Never in a release.

import (
	"net/http"
	_ "net/http/pprof"
)

func init() {
	go http.ListenAndServe("127.0.0.1:6060", nil)
}
