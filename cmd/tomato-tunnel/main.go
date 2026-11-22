package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/changeip"
	"github.com/dudul19/tomato-tunnel/internal/config"
	"github.com/dudul19/tomato-tunnel/internal/daemon"
	"github.com/dudul19/tomato-tunnel/internal/logx"
	"github.com/dudul19/tomato-tunnel/internal/tunnel"
	"github.com/dudul19/tomato-tunnel/internal/update"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `Tomato Tunnel %s — SSH and ZiVPN tunnel for OpenWrt · made by @dudulrealnofek

usage:
  tomato-tunnel run [-c config] [-f]     run the tunnel (procd starts this)
  tomato-tunnel test <profile> [-c ..]   connect once, print JSON, disconnect
  tomato-tunnel latency [-c ..]          test every profile, print JSON
  tomato-tunnel changeip                 change the WAN IP now (Settings › Change IP)
  tomato-tunnel changeip-detect          list AT ports, ModemManager modems, ADB phones
  tomato-tunnel update check             check GitHub for a newer release (JSON)
  tomato-tunnel update install           download, verify and install it
  tomato-tunnel version
`, version)
	os.Exit(2)
}

func main() {
	daemon.Version = version
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		cfg := fs.String("c", config.DefaultPath, "config file")
		fg := fs.Bool("f", false, "also print the log to stderr")
		fs.Parse(os.Args[2:])
		if err := daemon.Run(*cfg, *fg); err != nil {
			os.Exit(1)
		}
	case "test":
		fs := flag.NewFlagSet("test", flag.ExitOnError)
		cfg := fs.String("c", config.DefaultPath, "config file")
		if len(os.Args) < 3 {
			usage()
		}
		id := os.Args[2]
		fs.Parse(os.Args[3:])
		os.Exit(test(*cfg, id))
	case "latency":
		fs := flag.NewFlagSet("latency", flag.ExitOnError)
		cfg := fs.String("c", config.DefaultPath, "config file")
		fs.Parse(os.Args[2:])
		c, err := config.Load(*cfg)
		if err != nil {
			fmt.Printf("{\"error\":%q}\n", err.Error())
			os.Exit(1)
		}
		rep := daemon.Measure(context.Background(), c.Main, c.Profiles, 15*time.Second)
		daemon.SaveLatency(rep)
		b, _ := json.Marshal(rep)
		fmt.Println(string(b))
	case "changeip":
		cfg, err := config.Load(config.DefaultPath)
		if err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
		log := logx.New(daemon.LogFile, 1000, true)
		r := changeip.Run(context.Background(), cfg.Main.IP, cfg.Main.WanIface, log)
		b, _ := json.Marshal(r)
		fmt.Println(string(b))
		if !r.OK {
			os.Exit(1)
		}
	case "changeip-detect":
		out := map[string]any{"at": changeip.DetectAT()}
		if m, err := changeip.MMModems(); err == nil {
			out["mm"] = m
		} else {
			out["mm_error"] = err.Error()
		}
		if a, err := changeip.ADBDevices(); err == nil {
			out["adb"] = a
		} else {
			out["adb_error"] = err.Error()
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
	case "update":
		if len(os.Args) < 3 {
			usage()
		}
		os.Exit(doUpdate(os.Args[2]))
	case "version", "-v", "--version":
		fmt.Println(version)
	default:
		usage()
	}
}

type testResult struct {
	OK      bool     `json:"ok"`
	Error   string   `json:"error,omitempty"`
	Banner  string   `json:"banner,omitempty"`
	Kex     string   `json:"kex,omitempty"`
	Cipher  string   `json:"cipher,omitempty"`
	Legacy  bool     `json:"legacy"`
	MS      int64    `json:"ms"`
	CheckMS int64    `json:"check_ms,omitempty"`
	Log     []string `json:"log"`
}

func test(cfgPath, id string) int {
	res := testResult{}
	out := func() int {
		b, _ := json.Marshal(res)
		fmt.Println(string(b))
		if res.OK {
			return 0
		}
		return 1
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		res.Error = err.Error()
		return out()
	}
	p, ok := cfg.Find(id)
	if !ok {
		res.Error = "profile not found: " + id
		return out()
	}
	if err := p.Validate(); err != nil {
		res.Error = err.Error()
		return out()
	}
	logPath := daemon.RunDir + "/test.log"
	os.MkdirAll(daemon.RunDir, 0700)
	os.Remove(logPath)
	log := logx.New(logPath, 100, false)
	log.SetLevel("debug")
	start := time.Now()
	pool := tunnel.NewPool(p, cfg.Main.SSHCompat, cfg.Main.WanIface, log)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	err = pool.Connect(ctx, 1)
	cancel()
	res.MS = time.Since(start).Milliseconds()
	if err == nil {
		res.OK = true
		res.Banner, res.Kex, res.Cipher, res.Legacy = pool.Info.Banner, pool.Info.Kex, pool.Info.Cipher, pool.Info.Legacy
		t := time.Now()
		tr := &http.Transport{DialContext: func(ctx context.Context, _, a string) (net.Conn, error) { return pool.Dial(a) }, DisableKeepAlives: true}
		cl := &http.Client{Transport: tr, Timeout: 10 * time.Second}
		if resp, err := cl.Get(cfg.Main.CheckURL); err == nil {
			resp.Body.Close()
			res.CheckMS = time.Since(t).Milliseconds()
		} else {
			res.Error = "connected, but the check URL failed through the tunnel: " + err.Error()
		}
	} else {
		res.Error = err.Error()
	}
	pool.Close()
	if b, err := os.ReadFile(logPath); err == nil {
		for _, l := range splitLines(string(b)) {
			res.Log = append(res.Log, l)
		}
	}
	return out()
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func updateClient(cfg *config.Config) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	if b, err := os.ReadFile(daemon.StatusFile); err == nil {
		var st daemon.Status
		if json.Unmarshal(b, &st) == nil && st.State == "connected" && time.Now().Unix()-st.Updated < 10 {
			host := cfg.Main.SocksListen
			if host == "0.0.0.0" || host == "" {
				host = "127.0.0.1"
			}
			u, _ := url.Parse("socks5://" + net.JoinHostPort(host, strconv.Itoa(cfg.Main.SocksPort)))
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Minute}
}

func doUpdate(cmd string) int {
	cfg, err := config.Load(config.DefaultPath)
	if err != nil {
		cfg = &config.Config{}
		cfg.Main.UpdateRepo = "dudul19/tomato-tunnel"
		cfg.Main.UpdateChannel = "stable"
		cfg.Main.SocksPort = 1080
	}
	cl := updateClient(cfg)
	r, err := update.Check(cl, cfg.Main.UpdateRepo, cfg.Main.UpdateChannel, version)
	if err != nil {
		r = &update.Result{Current: version, Error: err.Error(), Checked: time.Now().Unix()}
	}
	switch cmd {
	case "check":
		update.Save(r)
		b, _ := json.Marshal(r)
		fmt.Println(string(b))
		if r.Error != "" {
			return 1
		}
		return 0
	case "install":
		if r.Error != "" {
			fmt.Println("error:", r.Error)
			return 1
		}
		if !r.Available {
			fmt.Printf("already up to date (v%s)\n", version)
			return 0
		}
		if err := update.Install(cl, r, os.Stdout); err != nil {
			fmt.Println("error:", err)
			return 1
		}
		return 0
	}
	usage()
	return 2
}
