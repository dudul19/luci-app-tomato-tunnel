package bot

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/uci"
)

const pkgName = "tomato-tunnel"

type Admin struct {
	ID   int64
	Name string
}

type Conf struct {
	Enabled    bool
	Token      string
	Lang       string
	Route      string
	DownAfter  int
	ReportHour int
	Admins     []Admin
	Notify     map[string]bool
	Allow      map[string]bool
}

var notifyKeys = []string{"down", "switch", "login", "expiry", "ip", "boot", "device", "update", "report"}
var notifyDef = map[string]bool{"down": true, "switch": true, "login": true, "expiry": true, "ip": true, "boot": true, "device": false, "update": true, "report": true}
var allowKeys = []string{"control", "changeip", "accounts", "update", "restore", "reboot"}

func loadConf(path string) (Conf, error) {
	c := Conf{Lang: "en", Route: "auto", DownAfter: 30, ReportHour: 0, Notify: map[string]bool{}, Allow: map[string]bool{}}
	for k, v := range notifyDef {
		c.Notify[k] = v
	}
	for _, k := range allowKeys {
		c.Allow[k] = true
	}
	ss, err := uci.LoadFile(path)
	if err != nil {
		return c, err
	}
	for _, s := range ss {
		if s.Type != "bot" {
			continue
		}
		c.Enabled = s.Get("enabled", "0") == "1"
		c.Token = strings.TrimSpace(s.Get("token", ""))
		if l := s.Get("lang", "en"); l == "id" {
			c.Lang = "id"
		}
		switch r := s.Get("route", "auto"); r {
		case "wan", "tunnel":
			c.Route = r
		}
		if n, err := strconv.Atoi(s.Get("down_after", "30")); err == nil && n >= 0 && n <= 3600 {
			c.DownAfter = n
		}
		if n, err := strconv.Atoi(s.Get("report_hour", "0")); err == nil && n >= -1 && n <= 23 {
			c.ReportHour = n
		}
		for _, k := range notifyKeys {
			if v, ok := s.Options["n_"+k]; ok {
				c.Notify[k] = v == "1"
			}
		}
		for _, k := range allowKeys {
			if v, ok := s.Options["a_"+k]; ok {
				c.Allow[k] = v == "1"
			}
		}
		for _, a := range s.Lists["admin"] {
			id, name, _ := strings.Cut(a, ":")
			if n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64); err == nil && n != 0 {
				c.Admins = append(c.Admins, Admin{n, name})
			}
		}
	}
	return c, nil
}

func (c Conf) isAdmin(id int64) bool {
	for _, a := range c.Admins {
		if a.ID == id {
			return true
		}
	}
	return false
}

func uciRun(args ...string) error {
	out, err := exec.Command("uci", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("uci %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

func uciCommit() error { return uciRun("commit", pkgName) }

func ensureBotSection() {
	if exec.Command("uci", "-q", "get", pkgName+".bot").Run() != nil {
		uciRun("set", pkgName+".bot=bot")
	}
}

func setBotOpt(k, v string) error {
	ensureBotSection()
	if err := uciRun("set", pkgName+".bot."+k+"="+v); err != nil {
		return err
	}
	return uciCommit()
}

func addAdmin(id int64, name string) error {
	ensureBotSection()
	name = strings.Map(func(r rune) rune {
		if r == '\'' || r == '"' || r == '\n' || r == '\\' || r == ':' {
			return -1
		}
		return r
	}, name)
	if len([]rune(name)) > 32 {
		name = string([]rune(name)[:32])
	}
	if err := uciRun("add_list", fmt.Sprintf("%s.bot.admin=%d:%s", pkgName, id, name)); err != nil {
		return err
	}
	return uciCommit()
}

func NewPairCode() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
	code := fmt.Sprintf("%06d", n)
	os.MkdirAll(RunDir, 0700)
	if err := os.WriteFile(RunDir+"/bot-pair", []byte(fmt.Sprintf("%s %d\n", code, time.Now().Add(5*time.Minute).Unix())), 0600); err != nil {
		return "", err
	}
	return code, nil
}
