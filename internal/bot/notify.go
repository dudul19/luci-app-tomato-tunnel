package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
)

type stateData struct {
	Report     string            `json:"report"`
	Expiry     map[string]string `json:"expiry"`
	Update     string            `json:"update"`
	Devices    map[string]int64  `json:"devices"`
	Drops      map[string]int    `json:"drops"`
	UpdateChat int64             `json:"update_chat"`
	UpdateFrom string            `json:"update_from"`
	UpdateAt   int64             `json:"update_at"`
	RebootBy   int64             `json:"reboot_by"`
	BootID     string            `json:"boot_id"`
}

type state struct {
	mu sync.Mutex
	d  stateData
}

func loadState() *state {
	s := &state{}
	if b, err := os.ReadFile(StateFile); err == nil {
		json.Unmarshal(b, &s.d)
	}
	if s.d.Expiry == nil {
		s.d.Expiry = map[string]string{}
	}
	if s.d.Devices == nil {
		s.d.Devices = map[string]int64{}
	}
	if s.d.Drops == nil {
		s.d.Drops = map[string]int{}
	}
	return s
}

func (s *state) get(f func(*stateData)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.d)
}

func (s *state) set(f func(*stateData)) {
	s.mu.Lock()
	f(&s.d)
	b, _ := json.Marshal(s.d)
	s.mu.Unlock()
	os.MkdirAll(filepath.Dir(StateFile), 0700)
	tmp := StateFile + ".tmp"
	if os.WriteFile(tmp, b, 0600) == nil {
		os.Rename(tmp, StateFile)
	}
}

func (b *Bot) broadcast(ctx context.Context, key string, m tmsg, kb Keyboard) {
	cfg := b.conf()
	if key != "" && !cfg.Notify[key] {
		return
	}
	for _, a := range cfg.Admins {
		if _, err := b.c.sendCard(ctx, a.ID, m, kb); err != nil {
			time.Sleep(5 * time.Second)
			b.c.sendCard(ctx, a.ID, m, kb)
		}
	}
}

func bootID() string {
	b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return string(b)
}

func (b *Bot) notifier(ctx context.Context) {
	prev := readStatus()
	lastProfile, lastName := "", ""
	if prev.State == "connected" {
		lastProfile, lastName = prev.Profile, prev.ProfileName
	}
	var downSince time.Time
	downSent := false
	downProfile := ""
	knownFail := map[string]bool{}
	for k := range prev.LoginFailed {
		knownFail[k] = true
	}
	lastIP := prev.LastIPChange
	bootPending := false
	bid := bootID()
	b.st.get(func(s *stateData) { bootPending = bid != "" && s.BootID != bid })
	if bootPending {
		b.st.set(func(s *stateData) { s.BootID = bid })
		bootPending = uptimeSec() < 900
	}
	started := time.Now()
	devInit := false
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	slow := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s := readStatus()
		now := time.Now()
		today := now.Format("2006-01-02")

		if bootPending && (s.State == "connected" || time.Since(started) > 2*time.Minute) {
			bootPending = false
			var by int64
			b.st.get(func(x *stateData) { by = x.RebootBy })
			b.st.set(func(x *stateData) { x.RebootBy = 0 })
			c := newCard(b.t("Router started", "Router menyala")).
				kv(b.t("Boot", "Nyala"), now.Add(-time.Duration(uptimeSec())*time.Second).Format("15:04:05")).
				kv("Tunnel", b.stateText(s.State))
			if s.State != "stopped" {
				c.kv(b.t("Profile", "Profil"), s.ProfileName)
			}
			if by != 0 {
				c.note(b.t("Rebooted from the bot.", "Di-reboot dari bot."))
			}
			b.broadcast(ctx, "boot", c.msg(), Keyboard{{b.mainBtn()}})
		}

		switch {
		case s.State == "connected":
			if downSent {
				c := newCard(b.t("Tunnel back up", "Tunnel nyambung lagi")).kv(b.t("Profile", "Profil"), s.ProfileName).
					kv(b.t("Down for", "Putus selama"), dur(now.Sub(downSince)))
				if downProfile != "" && downProfile != s.Profile {
					c.kv(b.t("Was on", "Sebelumnya"), b.profileName(downProfile))
				}
				c.kv("Public IP", s.PublicIP)
				b.broadcast(ctx, "down", c.msg(), nil)
				lastProfile, lastName = s.Profile, s.ProfileName
			} else if lastProfile != "" && s.Profile != lastProfile && !b.isQuiet("switch") {
				c := newCard(b.t("Profile switched", "Profil berpindah")).kv(b.t("Mode", "Mode"), b.policyText(s.Policy)).
					kv(b.t("From", "Dari"), lastName)
				reason := b.t("connection failed", "koneksi gagal")
				if _, bad := s.LoginFailed[lastProfile]; bad {
					reason = b.t("login failed (locked?)", "login gagal (dikunci?)")
				} else if s.Policy == "auto" {
					reason = b.t("faster profile found", "ada profil lebih cepat")
				}
				c.kv(b.t("Reason", "Alasan"), reason).kv(b.t("To", "Ke"), s.ProfileName)
				b.broadcast(ctx, "switch", c.msg(), Keyboard{{{Text: b.t("Profiles", "Profil"), CallbackData: "pf", Style: "success"}}})
			}
			downSince, downSent, downProfile = time.Time{}, false, ""
			if s.Profile != "" {
				lastProfile, lastName = s.Profile, s.ProfileName
			}
		case s.State == "stopped":
			downSince, downSent, downProfile = time.Time{}, false, ""
			lastProfile, lastName = "", ""
		default:
			if prev.State == "connected" && downSince.IsZero() {
				downSince, downProfile = now, prev.Profile
				b.st.set(func(x *stateData) { x.Drops[today]++ })
			}
			if downSince.IsZero() && prev.State != s.State && (s.State == "reconnecting" || s.State == "failed") {
				downSince, downProfile = now, s.Profile
			}
			if !downSince.IsZero() && !downSent && now.Sub(downSince) >= time.Duration(b.conf().DownAfter)*time.Second && !b.isQuiet("down") && !s.ChangingIP {
				downSent = true
				c := newCard(b.t("Tunnel down", "Tunnel putus")).kv(b.t("Profile", "Profil"), s.ProfileName)
				if s.Message != "" {
					c.kv(b.t("Reason", "Alasan"), s.Message)
				}
				c.kv(b.t("Since", "Sejak"), downSince.Format("15:04:05")).kv("Status", b.stateText(s.State))
				b.broadcast(ctx, "down", c.msg(), Keyboard{{{Text: "Status", CallbackData: "st", Style: "primary"}, {Text: b.t("Profiles", "Profil"), CallbackData: "pf", Style: "success"}}})
			}
		}

		for k := range s.LoginFailed {
			if knownFail[k] {
				continue
			}
			knownFail[k] = true
			c := newCard(b.t("Login failed", "Login gagal")).kv(b.t("Account", "Akun"), b.profileName(k)).
				text(b.t("Wrong password or the account is locked.", "Password salah atau akun dikunci.")).
				text(b.t("Check it with the seller.", "Cek ke penjual akun."))
			b.broadcast(ctx, "login", c.msg(), Keyboard{{{Text: b.t("Accounts", "Akun"), CallbackData: "ac", Style: "primary"}}})
		}
		for k := range knownFail {
			if _, ok := s.LoginFailed[k]; !ok && s.State != "stopped" {
				delete(knownFail, k)
			}
		}

		if s.LastIPChange > lastIP {
			lastIP = s.LastIPChange
			if !b.isQuiet("ip") {
				c := newCard(b.t("IP changed", "IP berganti"))
				if len(s.IPChangeMsg) > 7 && s.IPChangeMsg[:7] == "New IP " {
					c.kv(b.t("Old IP", "IP lama"), s.IPChangeOld).kv(b.t("New IP", "IP baru"), s.IPChangeMsg[7:])
				} else {
					c = newCard(b.t("Change IP failed", "Change IP gagal")).kv(b.t("Reason", "Alasan"), s.IPChangeMsg)
				}
				if s.IPChangeModem != "" {
					c.kv("Modem", s.IPChangeModem)
				}
				b.broadcast(ctx, "ip", c.msg(), nil)
			}
		}
		prev = s

		slow++
		if slow%10 != 1 {
			continue
		}
		b.checkDevices(ctx, &devInit)
		b.checkUpdate(ctx)
		b.checkExpiry(ctx, now)
		b.checkReport(ctx, now)
	}
}

func (b *Bot) checkDevices(ctx context.Context, inited *bool) {
	devs := readDevices()
	if devs == nil {
		return
	}
	var fresh []Device
	b.st.set(func(s *stateData) {
		first := len(s.Devices) == 0
		for _, d := range devs {
			if _, ok := s.Devices[d.MAC]; !ok && !first && *inited {
				fresh = append(fresh, d)
			}
			s.Devices[d.MAC] = time.Now().Unix()
		}
		if len(s.Devices) > 500 {
			for k, v := range s.Devices {
				if time.Since(time.Unix(v, 0)) > 60*24*time.Hour {
					delete(s.Devices, k)
				}
			}
		}
	})
	*inited = true
	for _, d := range fresh {
		name := d.Name
		if name == "" {
			name = "-"
		}
		c := newCard(b.t("New device", "Perangkat baru")).kv(b.t("Name", "Nama"), name).kv("IP", d.IP).kv("MAC", d.MAC)
		b.broadcast(ctx, "device", c.msg(), nil)
	}
}

func (b *Bot) checkUpdate(ctx context.Context) {
	raw, err := os.ReadFile(RunDir + "/update.json")
	if err != nil {
		return
	}
	var u updInfo
	if json.Unmarshal(raw, &u) != nil || !u.Available || u.Latest == "" {
		return
	}
	seen := ""
	b.st.get(func(s *stateData) { seen = s.Update })
	if seen == u.Latest {
		return
	}
	b.st.set(func(s *stateData) { s.Update = u.Latest })
	m, kb := b.updateCard(u)
	c := newCard(b.t("Update available", "Update tersedia"))
	_ = m
	c.kv(b.t("Installed", "Terpasang"), b.version).kv(b.t("New", "Baru"), u.Latest)
	if u.Notes != "" {
		n := u.Notes
		if len(n) > 500 {
			n = n[:500] + "…"
		}
		c.pre("notes", n)
	}
	if len(kb) > 1 {
		kb = Keyboard{{{Text: b.t("Install", "Pasang"), CallbackData: "ui", Style: "success"}, {Text: b.t("Later", "Nanti"), CallbackData: "m"}}}
	} else {
		kb = nil
	}
	b.broadcast(ctx, "update", c.msg(), kb)
}

func (b *Bot) checkExpiry(ctx context.Context, now time.Time) {
	if now.Hour() < 7 {
		return
	}
	today := now.Format("2006-01-02")
	cc, err := config.Load(b.path)
	if err != nil {
		return
	}
	warn := cc.Main.ExpiryWarn
	var rows [][]cell
	var keys []string
	b.st.get(func(s *stateData) {
		for _, p := range cc.Profiles {
			t, ok := p.ExpiryTime()
			if !ok {
				continue
			}
			left := t.Sub(now)
			if left < -24*time.Hour || left > time.Duration(warn)*24*time.Hour+time.Hour {
				continue
			}
			k := profileKey(p) + "|" + p.Expires
			if s.Expiry[k] == today {
				continue
			}
			keys = append(keys, k)
			lt := b.t("expired", "habis")
			if left > 0 {
				d := int(left.Hours() / 24)
				if d < 1 {
					lt = fmt.Sprintf(b.t("%d hours", "%d jam"), int(left.Hours()))
				} else {
					lt = fmt.Sprintf(b.t("%d days", "%d hari"), d)
				}
			}
			rows = append(rows, []cell{ct(p.Name), cv(t.Format("02.01.2006")), ct(lt)})
		}
	})
	if len(rows) == 0 {
		return
	}
	b.st.set(func(s *stateData) {
		for _, k := range keys {
			s.Expiry[k] = today
		}
		if len(s.Expiry) > 200 {
			for k, v := range s.Expiry {
				if v < now.AddDate(0, 0, -30).Format("2006-01-02") {
					delete(s.Expiry, k)
				}
			}
		}
	})
	c := newCard(b.t("Account expires soon", "Akun segera habis")).
		table([]string{b.t("Account", "Akun"), b.t("Expires", "Habis"), b.t("Left", "Sisa")}, rows).
		note(b.t("Expired accounts are skipped by Fallback and Auto.", "Akun habis dilewati oleh Fallback dan Auto."))
	b.broadcast(ctx, "expiry", c.msg(), Keyboard{{{Text: b.t("Accounts", "Akun"), CallbackData: "ac", Style: "primary"}}})
}

func (b *Bot) checkReport(ctx context.Context, now time.Time) {
	h := b.conf().ReportHour
	if h < 0 || now.Hour() != h {
		return
	}
	day := now.AddDate(0, 0, -1).Format("2006-01-02")
	done := ""
	b.st.get(func(s *stateData) { done = s.Report })
	if done == day {
		return
	}
	drops := 0
	b.st.set(func(s *stateData) {
		s.Report = day
		drops = s.Drops[day]
		for k := range s.Drops {
			if k < now.AddDate(0, 0, -14).Format("2006-01-02") {
				delete(s.Drops, k)
			}
		}
	})
	u := readUsage(ctx)
	d := u.Days[day]
	if d == nil {
		d = &UsageDay{}
	}
	dt, _ := time.Parse("2006-01-02", day)
	c := newCard(b.t("Daily report", "Laporan harian") + " · " + dt.Format("02.01.2006"))
	c.head("Data", "Total").kv("Download", human(d.RX)).kv("Upload", human(d.TX)).kv("Total", human(d.RX+d.TX))
	type it struct {
		n string
		v int64
	}
	var pl []it
	for k, x := range d.Profiles {
		n := x.Name
		if n == "" {
			n = k
		}
		pl = append(pl, it{n, x.RX + x.TX})
	}
	sort.Slice(pl, func(i, j int) bool { return pl[i].v > pl[j].v })
	var rows [][]cell
	for i, x := range pl {
		if i >= 5 {
			break
		}
		rows = append(rows, []cell{ct(x.n), cv(human(x.v))})
	}
	c.table([]string{b.t("Profile", "Profil"), b.t("Used", "Dipakai")}, rows)
	c.head(b.t("Summary", "Ringkasan"), "")
	c.kv(b.t("Drops", "Putus"), strconv.Itoa(drops))
	var top it
	for k, x := range d.Devices {
		if x.RX+x.TX > top.v {
			n := x.Name
			if n == "" {
				n = k
			}
			top = it{n, x.RX + x.TX}
		}
	}
	if top.v > 0 {
		c.kv(b.t("Top device", "Perangkat teratas"), top.n+" ("+human(top.v)+")")
	}
	b.broadcast(ctx, "report", c.msg(), nil)
}
