package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
)

func execCmd(ctx context.Context, bin string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (b *Bot) expiryText(p config.Profile) string {
	t, ok := p.ExpiryTime()
	if !ok {
		return "-"
	}
	if p.Expired(time.Now()) {
		return t.Format("02.01.2006") + b.t(" (expired)", " (habis)")
	}
	return t.Format("02.01.2006")
}

func (b *Bot) accounts(ctx context.Context, chat, drop int64) {
	ps, _ := loadProfiles(b.path)
	s := readStatus()
	c := newCard(b.t("Accounts", "Akun"))
	var rows [][]cell
	var kb Keyboard
	var row []Button
	for i, p := range ps {
		exp := b.expiryText(p)
		if _, bad := s.LoginFailed[profileKey(p)]; bad {
			exp = b.t("login failed", "login gagal")
		}
		rows = append(rows, []cell{ct(strconv.Itoa(i + 1)), ct(p.Name), ct(shortType(p)), cv(exp)})
		row = append(row, Button{Text: strconv.Itoa(i + 1), CallbackData: "a:" + profileKey(p), Style: "primary"})
		if len(row) == 5 {
			kb = append(kb, row)
			row = nil
		}
	}
	if len(row) > 0 {
		kb = append(kb, row)
	}
	if len(rows) == 0 {
		c.text(b.t("No accounts yet.", "Belum ada akun."))
	}
	c.table([]string{"No", b.t("Name", "Nama"), b.t("Type", "Tipe"), b.t("Expires", "Habis")}, rows)
	c.note(b.t("Tap a number to manage, or send a link to add.", "Ketuk nomor untuk mengatur, atau kirim link untuk menambah."))
	if b.conf().Allow["accounts"] {
		kb = append(kb, []Button{{Text: b.t("Add account", "Tambah akun"), CallbackData: "a+", Style: "success"}})
	}
	kb = append(kb, []Button{b.mainBtn()})
	b.show(ctx, chat, c.msg(), kb, drop)
}

func (b *Bot) account(ctx context.Context, chat, drop int64, id string) {
	p, ok := b.findProfile(id)
	if !ok {
		b.accounts(ctx, chat, drop)
		return
	}
	s := readStatus()
	lat := readLatency(ctx)
	c := newCard(p.Name)
	c.kv(b.t("Type", "Tipe"), typeLabel(p))
	c.kv("Server", server(p))
	if p.Port > 0 && !p.UseDNS {
		c.kv("Port", strconv.Itoa(p.Port))
	}
	if p.Type == "ssh" && p.User != "" {
		c.kv("Username", p.User)
	}
	c.kv(b.t("Expires", "Habis"), b.expiryText(p))
	dash := b.t("shown", "tampil")
	if !p.Dashboard {
		dash = b.t("hidden", "disembunyikan")
	}
	c.kv("Dashboard", dash)
	c.kv(b.t("Last ping", "Ping terakhir"), b.pingCell(p, lat, s))
	if s.State != "stopped" && s.Profile == profileKey(p) {
		c.kv("Status", b.t("active", "aktif"))
	}
	k := profileKey(p)
	dl := b.t("Hide from Dashboard", "Sembunyikan")
	if !p.Dashboard {
		dl = b.t("Show on Dashboard", "Tampilkan")
	}
	kb := Keyboard{{{Text: b.t("Connect", "Sambungkan"), CallbackData: "aco:" + k, Style: "success"}}}
	if b.conf().Allow["accounts"] {
		kb = append(kb,
			[]Button{{Text: b.t("Set expiry", "Atur masa aktif"), CallbackData: "ae:" + k, Style: "primary"}, {Text: dl, CallbackData: "ad:" + k, Style: "primary"}},
			[]Button{{Text: b.t("Delete", "Hapus"), CallbackData: "ax:" + k, Style: "danger"}})
	}
	kb = append(kb, []Button{b.backBtn("ac")})
	b.show(ctx, chat, c.msg(), kb, drop)
}

func (b *Bot) toggleDash(ctx context.Context, chat, drop int64, id string) {
	p, ok := b.findProfile(id)
	if !ok {
		b.accounts(ctx, chat, drop)
		return
	}
	v := "0"
	if !p.Dashboard {
		v = "1"
	}
	setProfileOpt(id, "dashboard", v)
	ctl(ctx, "reload")
	b.account(ctx, chat, drop, id)
}

func (b *Bot) deleteAsk(ctx context.Context, chat, drop int64, id string) {
	p, ok := b.findProfile(id)
	if !ok {
		b.accounts(ctx, chat, drop)
		return
	}
	c := newCard(b.t("Delete account?", "Hapus akun?")).kv(b.t("Name", "Nama"), p.Name).kv(b.t("Type", "Tipe"), typeLabel(p)).text(b.t("This cannot be undone.", "Tidak bisa dibatalkan."))
	b.show(ctx, chat, c.msg(), Keyboard{{{Text: b.t("Yes, delete", "Ya, hapus"), CallbackData: "axy:" + id, Style: "danger"}, {Text: b.t("Cancel", "Batal"), CallbackData: "a:" + id}}}, drop)
}

func (b *Bot) deleteDo(ctx context.Context, chat, drop int64, id string) {
	p, ok := b.findProfile(id)
	if !ok {
		b.accounts(ctx, chat, drop)
		return
	}
	s := readStatus()
	if s.State != "stopped" && s.Profile == profileKey(p) {
		b.show(ctx, chat, newCard(b.t("Cannot delete", "Tidak bisa dihapus")).text(b.t("This profile is in use.", "Profil ini sedang dipakai.")).text(b.t("Switch to another profile first.", "Pindah ke profil lain dulu.")).msg(), Keyboard{{b.backBtn("a:" + id)}}, drop)
		return
	}
	if err := deleteProfile(id); err != nil {
		b.show(ctx, chat, newCard(b.t("Delete failed", "Gagal menghapus")).text(err.Error()).msg(), Keyboard{{b.backBtn("ac")}}, drop)
		return
	}
	if out, _ := exec.Command("uci", "-q", "get", pkgName+".main.profile").Output(); strings.TrimSpace(string(out)) == id {
		uciRun("delete", pkgName+".main.profile")
		uciCommit()
	}
	ctl(ctx, "reload")
	b.accounts(ctx, chat, drop)
}

var reISO = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})$`)
var reDMYNum = regexp.MustCompile(`^(\d{1,2})[./-](\d{1,2})[./-](\d{4})$`)

func (b *Bot) onExpiry(ctx context.Context, chat int64, m *tgIncoming, id string) {
	t := strings.TrimSpace(m.Text)
	b.c.deleteMsg(ctx, chat, m.MessageID)
	val := ""
	switch {
	case t == "0" || t == "-":
	case reISO.MatchString(t):
		x := reISO.FindStringSubmatch(t)
		y, _ := strconv.Atoi(x[1])
		mo, _ := strconv.Atoi(x[2])
		d, _ := strconv.Atoi(x[3])
		val = fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
	case reDMYNum.MatchString(t):
		x := reDMYNum.FindStringSubmatch(t)
		d, _ := strconv.Atoi(x[1])
		mo, _ := strconv.Atoi(x[2])
		y, _ := strconv.Atoi(x[3])
		val = fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
	default:
		n, err := strconv.Atoi(t)
		if err != nil || n < 1 || n > 3650 {
			b.show(ctx, chat, newCard(b.t("Set expiry", "Atur masa aktif")).text(b.t("Not a date. Try 2026-12-31 or 30.", "Bukan tanggal. Coba 2026-12-31 atau 30.")).msg(), Keyboard{{b.cancelBtn()}})
			return
		}
		val = time.Now().AddDate(0, 0, n).Format("2006-01-02")
	}
	if val != "" {
		if _, err := time.Parse("2006-01-02", val); err != nil {
			b.show(ctx, chat, newCard(b.t("Set expiry", "Atur masa aktif")).text(b.t("That date does not exist.", "Tanggal itu tidak ada.")).msg(), Keyboard{{b.cancelBtn()}})
			return
		}
	}
	b.setPending(chat, nil)
	setProfileOpt(id, "expires", val)
	ctl(ctx, "reload")
	b.account(ctx, chat, 0, id)
}

func (b *Bot) onLink(ctx context.Context, chat int64, m *tgIncoming) {
	b.c.deleteMsg(ctx, chat, m.MessageID)
	list := parseAccounts(m.Text)
	if len(list) == 0 {
		b.setPending(chat, &pend{kind: "link"})
		b.show(ctx, chat, newCard(b.t("Add account", "Tambah akun")).text(b.t("Could not read an account from that.", "Tidak ada akun yang terbaca.")).text(b.t("Send a vless://, vmess://, trojan:// link", "Kirim link vless://, vmess://, trojan://")).text(b.t("or host:port@user:pass.", "atau host:port@user:pass.")).msg(), Keyboard{{b.cancelBtn()}})
		return
	}
	b.setPending(chat, &pend{kind: "save", list: list})
	c := newCard(b.t("Add account", "Tambah akun"))
	if len(list) > 1 {
		c.kv(b.t("Accounts found", "Akun terbaca"), strconv.Itoa(len(list)))
		var rows [][]cell
		for i, o := range list {
			rows = append(rows, []cell{ct(strconv.Itoa(i + 1)), ct(o["name"]), ct(kindOf(o))})
		}
		c.table([]string{"No", b.t("Name", "Nama"), b.t("Type", "Tipe")}, rows)
	} else {
		o := list[0]
		c.kv(b.t("Name", "Nama"), o["name"]).kv(b.t("Type", "Tipe"), kindOf(o))
		srv := o["host"]
		if o["use_slowdns"] == "1" {
			srv = o["dns_domain"]
		}
		c.kv("Server", srv).kv("Port", o["port"])
		if o["type"] == "xray" {
			c.kv("Transport", strings.ToUpper(o["x_net"])).kv("Security", strings.ToUpper(o["x_sec"]))
		}
		if o["user"] != "" {
			c.kv("Username", o["user"])
		}
		if o["pass"] != "" || o["uuid"] != "" {
			c.kv("Password", "••••••")
		}
		c.kv(b.t("Expires", "Habis"), o["expires"])
	}
	kb := Keyboard{{{Text: b.t("Save", "Simpan"), CallbackData: "asv", Style: "success"}}}
	if len(list) == 1 && b.conf().Allow["control"] {
		kb[0] = append(kb[0], Button{Text: b.t("Save & connect", "Simpan & sambung"), CallbackData: "asc", Style: "success"})
	}
	kb = append(kb, []Button{b.cancelBtn()})
	b.show(ctx, chat, c.msg(), kb)
}

func kindOf(o map[string]string) string {
	switch o["type"] {
	case "xray":
		return map[string]string{"vless": "VLESS", "vmess": "VMess", "trojan": "Trojan"}[o["x_proto"]]
	case "zivpn":
		return "ZiVPN"
	}
	if o["use_slowdns"] == "1" {
		return "SlowDNS"
	}
	if o["use_tls"] == "1" {
		return "SSH WS SSL"
	}
	if o["use_payload"] == "1" {
		return "SSH WS"
	}
	return "SSH"
}

func (b *Bot) saveAccounts(ctx context.Context, chat, drop int64, connect bool) {
	p := b.getPending(chat)
	if p == nil || (p.kind != "save" && p.kind != "restore-ok") {
		b.show(ctx, chat, newCard(b.t("Expired", "Kedaluwarsa")).text(b.t("Send the link again.", "Kirim link lagi.")).msg(), Keyboard{{b.mainBtn()}}, drop)
		return
	}
	b.setPending(chat, nil)
	ids, err := addProfiles(b.path, p.list)
	if err != nil {
		b.show(ctx, chat, newCard(b.t("Save failed", "Gagal menyimpan")).text(err.Error()).msg(), Keyboard{{b.mainBtn()}}, drop)
		return
	}
	ctl(ctx, "reload")
	if len(ids) == 0 {
		b.show(ctx, chat, newCard(b.t("Nothing new", "Tidak ada yang baru")).text(b.t("That account is already here.", "Akun itu sudah ada.")).msg(), Keyboard{{{Text: b.t("Accounts", "Akun"), CallbackData: "ac", Style: "primary"}, b.mainBtn()}}, drop)
		return
	}
	if connect && len(ids) == 1 {
		b.switchTo(ctx, chat, drop, ids[0])
		return
	}
	c := newCard(b.t("Saved", "Tersimpan")).kv(b.t("Accounts added", "Akun ditambah"), strconv.Itoa(len(ids)))
	b.result(ctx, chat, c.msg(), Keyboard{{{Text: b.t("Accounts", "Akun"), CallbackData: "ac", Style: "primary"}, b.mainBtn()}})
	b.c.deleteMsg(ctx, chat, drop)
}

func (b *Bot) onDocument(ctx context.Context, m *tgIncoming) {
	chat := m.Chat.ID
	if !b.conf().Allow["restore"] {
		b.show(ctx, chat, newCard(b.t("Restore", "Pulihkan")).text(b.t("Restore is turned off in LuCI › Settings › Telegram.", "Restore dimatikan di LuCI › Settings › Telegram.")).msg(), Keyboard{{b.mainBtn()}})
		return
	}
	if !strings.HasSuffix(strings.ToLower(m.Document.FileName), ".json") {
		b.show(ctx, chat, newCard(b.t("Restore", "Pulihkan")).text(b.t("Send a Tomato Tunnel backup (.json).", "Kirim backup Tomato Tunnel (.json).")).msg(), Keyboard{{b.mainBtn()}})
		return
	}
	data, err := b.c.download(ctx, m.Document.FileID, 2<<20)
	b.c.deleteMsg(ctx, chat, m.MessageID)
	if err != nil {
		b.show(ctx, chat, newCard(b.t("Restore failed", "Gagal memulihkan")).text(err.Error()).msg(), Keyboard{{b.mainBtn()}})
		return
	}
	list, err := parseBackup(data)
	if err != nil {
		b.show(ctx, chat, newCard(b.t("Restore failed", "Gagal memulihkan")).text(err.Error()).msg(), Keyboard{{b.mainBtn()}})
		return
	}
	have := map[string]bool{}
	for _, o := range rawProfiles(b.path) {
		have[dedupeKey(o)] = true
	}
	n := 0
	for _, o := range list {
		if !have[dedupeKey(o)] {
			n++
		}
	}
	b.setPending(chat, &pend{kind: "restore-ok", list: list})
	c := newCard(b.t("Restore backup?", "Pulihkan backup?")).kv(b.t("File", "File"), m.Document.FileName).
		kv(b.t("Accounts in file", "Akun di file"), strconv.Itoa(len(list))).kv(b.t("New accounts", "Akun baru"), strconv.Itoa(n)).
		note(b.t("Accounts already here are skipped.", "Akun yang sudah ada dilewati."))
	kb := Keyboard{{{Text: b.t("Yes, restore", "Ya, pulihkan"), CallbackData: "rsy", Style: "success"}, {Text: b.t("Cancel", "Batal"), CallbackData: "cx", Style: "danger"}}}
	if n == 0 {
		kb = Keyboard{{b.mainBtn()}}
	}
	b.show(ctx, chat, c.msg(), kb)
}

func (b *Bot) restoreDo(ctx context.Context, chat, drop int64) {
	b.saveAccounts(ctx, chat, drop, false)
}

func (b *Bot) router(ctx context.Context, chat, drop int64) {
	s := readSys()
	c := newCard("Router")
	c.kv(b.t("Model", "Model"), s.Model).kv("Firmware", s.Firmware).kv("CPU", s.CPU).kv("RAM", s.RAM).kv(b.t("Temp", "Suhu"), s.Temp).kv("Storage", s.Storage).kv("Uptime", s.Uptime)
	if m, ok := readModem(ctx); ok {
		c.head("Modem", b.t("Value", "Nilai"))
		c.kv("Operator", m.Operator).kv(b.t("Network", "Jaringan"), m.Tech).kv(b.t("Signal", "Sinyal"), m.Signal).kv("State", m.State)
	}
	devs := readDevices()
	var rows [][]cell
	on := 0
	for _, d := range devs {
		if !d.Online {
			continue
		}
		on++
		if len(rows) < 6 {
			name := d.Name
			if name == "" {
				name = d.MAC
			}
			rows = append(rows, []cell{ct(strconv.Itoa(len(rows) + 1)), ct(name), cv(d.IP)})
		}
	}
	c.table([]string{"No", b.t("Device", "Perangkat"), "IP"}, rows)
	if on > len(rows) {
		c.note(fmt.Sprintf(b.t("%d devices online.", "%d perangkat online."), on))
	}
	kb := Keyboard{{{Text: b.t("All devices", "Semua perangkat"), CallbackData: "dv", Style: "primary"}, {Text: "Refresh", CallbackData: "rt", Style: "primary"}}}
	if b.conf().Allow["reboot"] {
		kb = append(kb, []Button{{Text: b.t("Reboot router", "Reboot router"), CallbackData: "rb", Style: "danger"}})
	}
	kb = append(kb, []Button{b.mainBtn()})
	b.show(ctx, chat, c.msg(), kb, drop)
}

func (b *Bot) devices(ctx context.Context, chat, drop int64) {
	devs := readDevices()
	c := newCard(b.t("Devices", "Perangkat"))
	var rows [][]cell
	for i, d := range devs {
		if i >= 30 {
			break
		}
		name := d.Name
		if name == "" {
			name = "-"
		}
		st := b.t("off", "mati")
		if d.Online {
			st = "online"
		}
		rows = append(rows, []cell{ct(name), cv(d.IP), ct(st)})
	}
	if len(rows) == 0 {
		c.text(b.t("No DHCP leases.", "Tidak ada DHCP lease."))
	}
	c.table([]string{b.t("Device", "Perangkat"), "IP", "Status"}, rows)
	b.show(ctx, chat, c.msg(), Keyboard{{b.backBtn("rt")}}, drop)
}

func (b *Bot) reboot(ctx context.Context, chat, drop int64) {
	b.st.set(func(s *stateData) { s.RebootBy = chat })
	b.result(ctx, chat, newCard(b.t("Rebooting…", "Reboot…")).text(b.t("The bot is back in about 1 minute.", "Bot kembali sekitar 1 menit lagi.")).msg(), nil)
	b.c.deleteMsg(ctx, chat, drop)
	b.goSafe(func() {
		time.Sleep(2 * time.Second)
		exec.Command("reboot").Run()
	})
}

func (b *Bot) tools(ctx context.Context, chat, drop int64) {
	c := newCard("Tools")
	c.kv(b.t("Version", "Versi"), b.version)
	if x, _ := ctl(ctx, "xray-version"); x != "" {
		f := strings.Fields(x)
		if len(f) >= 2 {
			x = f[1]
		}
		c.kv(b.t("Xray add-on", "Add-on Xray"), x)
	}
	var u struct {
		Available bool   `json:"available"`
		Latest    string `json:"latest"`
		Checked   int64  `json:"checked"`
	}
	if s, err := ctl(ctx, "update-cached"); err == nil {
		json.Unmarshal([]byte(s), &u)
	}
	switch {
	case u.Available:
		c.kv("Update", "v"+u.Latest+b.t(" available", " tersedia"))
	case u.Checked > 0:
		c.kv("Update", b.t("up to date", "terbaru"))
	}
	if st, err := os.ReadFile(RunDir + "/bot.json"); err == nil {
		var j struct{ Username string }
		json.Unmarshal(st, &j)
		if j.Username != "" {
			c.kv("Bot", "@"+j.Username)
		}
	}
	kb := Keyboard{
		{{Text: "Log", CallbackData: "lg", Style: "primary"}, {Text: b.t("Check update", "Cek update"), CallbackData: "uc", Style: "primary"}},
		{{Text: "Backup", CallbackData: "bk", Style: "success"}},
		{{Text: b.t("Notifications", "Notifikasi"), CallbackData: "nt", Style: "primary"}},
		{b.mainBtn()},
	}
	if b.conf().Allow["restore"] {
		kb[1] = append(kb[1], Button{Text: "Restore", CallbackData: "rsf", Style: "primary"})
	}
	b.show(ctx, chat, c.msg(), kb, drop)
}

func (b *Bot) logView(ctx context.Context, chat, drop int64) {
	lines := tailFile(RunDir+"/tunnel.log", 25)
	var keep []string
	for _, l := range lines {
		if len(l) > 110 {
			l = l[:110] + "…"
		}
		keep = append(keep, l)
	}
	c := newCard("Log")
	if len(keep) == 0 {
		c.text(b.t("The log is empty.", "Log kosong."))
	} else {
		c.pre("log", strings.Join(keep, "\n"))
	}
	b.show(ctx, chat, c.msg(), Keyboard{
		{{Text: "Refresh", CallbackData: "lg", Style: "primary"}, {Text: b.t("Full log (file)", "Log lengkap (file)"), CallbackData: "lgf", Style: "primary"}},
		{b.backBtn("tl")},
	}, drop)
}

func (b *Bot) logFile(ctx context.Context, chat int64) {
	data, err := os.ReadFile(RunDir + "/tunnel.log")
	if err != nil || len(data) == 0 {
		b.c.sendText(ctx, chat, b.t("The log is empty.", "Log kosong."), nil)
		return
	}
	b.c.typing(ctx, chat, "upload_document")
	b.c.sendDocument(ctx, chat, "tomato-tunnel-"+time.Now().Format("20060102-1504")+".log", data, "")
}

func (b *Bot) backup(ctx context.Context, chat int64) {
	b.c.typing(ctx, chat, "upload_document")
	data := backupJSON(b.path, b.version)
	cap := b.t("Every account with its password. Keep it private.", "Semua akun beserta password. Jangan dibagikan.")
	if err := b.c.sendDocument(ctx, chat, "tomato-tunnel-backup-"+time.Now().Format("20060102")+".json", data, htmlEscape(cap)); err != nil {
		b.c.sendText(ctx, chat, htmlEscape(err.Error()), nil)
	}
}

type updInfo struct {
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Notes     string `json:"notes"`
	Error     string `json:"error"`
}

func (b *Bot) updateCard(u updInfo) (tmsg, Keyboard) {
	c := newCard("Update")
	if u.Error != "" {
		c.kv(b.t("Error", "Error"), u.Error)
		return c.msg(), Keyboard{{b.backBtn("tl")}}
	}
	c.kv(b.t("Installed", "Terpasang"), b.version)
	c.kv(b.t("Latest", "Terbaru"), u.Latest)
	kb := Keyboard{}
	if u.Available {
		notes := strings.TrimSpace(u.Notes)
		if len(notes) > 700 {
			notes = notes[:700] + "…"
		}
		if notes != "" {
			c.pre("notes", notes)
		}
		c.note(b.t("SHA256 is checked before install.", "SHA256 dicek sebelum dipasang."))
		if b.conf().Allow["update"] {
			kb = append(kb, []Button{{Text: b.t("Install", "Pasang"), CallbackData: "ui", Style: "success"}})
		}
	} else {
		c.text(b.t("You have the latest version.", "Sudah versi terbaru."))
	}
	kb = append(kb, []Button{b.backBtn("tl")})
	return c.msg(), kb
}

func (b *Bot) updateCheck(ctx context.Context, chat, drop int64) {
	b.show(ctx, chat, newCard(b.t("Checking…", "Mengecek…")).text("GitHub").msg(), nil, drop)
	b.goSafe(func() {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		out, _ := ctl(cctx, "update-check")
		cancel()
		var u updInfo
		if i := strings.Index(out, "{"); i >= 0 {
			json.Unmarshal([]byte(out[i:]), &u)
		}
		if u.Latest == "" && u.Error == "" {
			u.Error = b.t("no answer from GitHub", "GitHub tidak menjawab")
		}
		m, kb := b.updateCard(u)
		b.show(ctx, chat, m, kb)
	})
}

func (b *Bot) updateInstall(ctx context.Context, chat, drop int64) {
	b.st.set(func(s *stateData) {
		s.UpdateChat = chat
		s.UpdateFrom = b.version
		s.UpdateAt = time.Now().Unix()
	})
	ctl(ctx, "update-install")
	b.show(ctx, chat, newCard(b.t("Installing update…", "Memasang update…")).text(b.t("The bot restarts when it is done.", "Bot akan restart setelah selesai.")).msg(), nil, drop)
	t0 := time.Now()
	b.goSafe(func() {
		for time.Since(t0) < 6*time.Minute && ctx.Err() == nil {
			time.Sleep(3 * time.Second)
			raw, _ := os.ReadFile(RunDir + "/update.log")
			s := string(raw)
			if strings.Contains(s, "error:") {
				i := strings.LastIndex(s, "error:")
				b.st.set(func(x *stateData) { x.UpdateChat = 0 })
				b.result(ctx, chat, newCard(b.t("Update failed", "Update gagal")).kv(b.t("Reason", "Alasan"), strings.TrimSpace(s[i+6:])).msg(), Keyboard{{b.backBtn("tl")}})
				return
			}
			if strings.Contains(s, "already up to date") {
				b.st.set(func(x *stateData) { x.UpdateChat = 0 })
				b.result(ctx, chat, newCard("Update").text(b.t("You have the latest version.", "Sudah versi terbaru.")).msg(), Keyboard{{b.mainBtn()}})
				return
			}
		}
	})
}

func (b *Bot) afterUpdate(ctx context.Context) {
	var chat int64
	var from string
	var at int64
	b.st.get(func(s *stateData) { chat, from, at = s.UpdateChat, s.UpdateFrom, s.UpdateAt })
	if chat == 0 {
		return
	}
	b.st.set(func(s *stateData) { s.UpdateChat = 0 })
	if time.Since(time.Unix(at, 0)) > 30*time.Minute || from == b.version {
		return
	}
	time.Sleep(3 * time.Second)
	st := readStatus()
	c := newCard(b.t("Updated", "Terupdate")).kv(b.t("From", "Dari"), from).kv(b.t("Version", "Versi"), b.version).
		kv(b.t("Took", "Waktu"), fmt.Sprintf("%d s", time.Now().Unix()-at))
	if st.State != "stopped" {
		c.kv("Tunnel", b.stateText(st.State))
	}
	b.c.sendCard(ctx, chat, c.msg(), Keyboard{{b.mainBtn()}})
}

func (b *Bot) notifyMenu(ctx context.Context, chat, drop int64) {
	cfg := b.conf()
	labels := map[string]string{
		"down": b.t("Down / up", "Putus / nyambung"), "switch": b.t("Switched", "Pindah profil"), "login": b.t("Login failed", "Login gagal"),
		"expiry": b.t("Expiry", "Masa aktif"), "ip": b.t("IP changed", "IP berubah"), "boot": b.t("Router started", "Router nyala"),
		"device": b.t("New device", "Perangkat baru"), "update": "Update", "report": b.t("Daily report", "Laporan harian"),
	}
	c := newCard(b.t("Notifications", "Notifikasi")).note(b.t("Green = on, red = off. Tap to change.", "Hijau = nyala, merah = mati. Ketuk untuk ubah."))
	var kb Keyboard
	var row []Button
	for _, k := range notifyKeys {
		st, mark := "danger", b.t("off", "mati")
		if cfg.Notify[k] {
			st, mark = "success", b.t("on", "nyala")
		}
		row = append(row, Button{Text: labels[k] + ": " + mark, CallbackData: "nt:" + k, Style: st})
		if len(row) == 2 {
			kb = append(kb, row)
			row = nil
		}
	}
	if len(row) > 0 {
		kb = append(kb, row)
	}
	kb = append(kb, []Button{b.backBtn("tl")})
	b.show(ctx, chat, c.msg(), kb, drop)
}
