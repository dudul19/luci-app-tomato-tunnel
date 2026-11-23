package bot

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/config"
)

type pend struct {
	kind string
	id   string
	list []map[string]string
	at   time.Time
}

type Bot struct {
	path    string
	version string
	c       *client

	mu      sync.Mutex
	cfg     Conf
	panels  map[int64]int64
	pending map[int64]*pend
	denied  map[int64]time.Time
	pairTry map[int64][]time.Time
	quiet   map[string]time.Time
	st      *state
}

type tgUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type tgDoc struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	Size     int64  `json:"file_size"`
}

type tgIncoming struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      tgChat  `json:"chat"`
	Text      string  `json:"text"`
	Caption   string  `json:"caption"`
	Document  *tgDoc  `json:"document"`
}

type tgCallback struct {
	ID      string      `json:"id"`
	From    tgUser      `json:"from"`
	Message *tgIncoming `json:"message"`
	Data    string      `json:"data"`
}

type tgUpdate struct {
	UpdateID int64       `json:"update_id"`
	Message  *tgIncoming `json:"message"`
	Callback *tgCallback `json:"callback_query"`
}

func Run(path, version string) error {
	cfg, err := loadConf(path)
	if err != nil {
		return err
	}
	if !cfg.Enabled || cfg.Token == "" {
		log.Printf("bot: disabled or no token, exiting")
		return nil
	}
	ctl(context.Background(), "normalize")
	socks := ""
	if c, err := config.Load(path); err == nil {
		h := c.Main.SocksListen
		if h == "" || h == "0.0.0.0" {
			h = "127.0.0.1"
		}
		socks = net.JoinHostPort(h, strconv.Itoa(c.Main.SocksPort))
	}
	b := &Bot{path: path, version: version, c: newClient(cfg.Token, cfg.Route, socks), cfg: cfg,
		panels: map[int64]int64{}, pending: map[int64]*pend{}, denied: map[int64]time.Time{}, pairTry: map[int64][]time.Time{}, quiet: map[string]time.Time{},
		st: loadState()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGUSR1)
	go func() {
		for s := range sig {
			if s == syscall.SIGHUP {
				b.reload()
				continue
			}
			if s == syscall.SIGUSR1 {
				b.reload()
				b.goSafe(func() { b.testMessage(ctx) })
				continue
			}
			cancel()
			return
		}
	}()
	var me struct {
		Username string `json:"username"`
	}
	for ctx.Err() == nil {
		if err := b.c.call(ctx, "getMe", map[string]any{}, &me); err == nil {
			break
		} else {
			var te *tgError
			if asTg(err, &te) && te.code == 401 {
				log.Printf("bot: the token was rejected by Telegram")
				writeBotStatus("bad_token", "")
				sleepCtx(ctx, 10*time.Minute)
				continue
			}
			writeBotStatus("offline", "")
			sleepCtx(ctx, 15*time.Second)
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	writeBotStatus("online", me.Username)
	log.Printf("bot: online as @%s, %d admin(s)", me.Username, len(cfg.Admins))
	b.c.call(ctx, "setMyCommands", map[string]any{"commands": []map[string]string{
		{"command": "start", "description": "Main menu"},
		{"command": "status", "description": "Tunnel status"},
		{"command": "profiles", "description": "Switch profile"},
		{"command": "usage", "description": "Data usage"},
	}}, nil)
	go b.notifier(ctx)
	b.afterUpdate(ctx)
	var offset int64
	fails := 0
	for ctx.Err() == nil {
		var ups []tgUpdate
		pctx, pcancel := context.WithTimeout(ctx, 65*time.Second)
		err := b.c.call(pctx, "getUpdates", map[string]any{"offset": offset, "timeout": 50, "allowed_updates": []string{"message", "callback_query"}}, &ups)
		pcancel()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			fails++
			if fails == 3 {
				writeBotStatus("offline", me.Username)
			}
			sleepCtx(ctx, time.Duration(min(fails, 6))*5*time.Second)
			continue
		}
		if fails >= 3 {
			writeBotStatus("online", me.Username)
		}
		fails = 0
		for _, u := range ups {
			offset = u.UpdateID + 1
			b.safe(func() { b.handle(ctx, u) })
		}
	}
	writeBotStatus("stopped", "")
	return nil
}

func asTg(err error, te **tgError) bool {
	x, ok := err.(*tgError)
	if ok {
		*te = x
	}
	return ok
}

func writeBotStatus(state, user string) {
	os.MkdirAll(RunDir, 0700)
	b, _ := json.Marshal(map[string]any{"state": state, "username": user, "pid": os.Getpid(), "updated": time.Now().Unix()})
	os.WriteFile(RunDir+"/bot.json.tmp", b, 0644)
	os.Rename(RunDir+"/bot.json.tmp", RunDir+"/bot.json")
}

func (b *Bot) safe(f func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("bot: panic: %v", r)
		}
	}()
	f()
}

func (b *Bot) goSafe(f func()) {
	go b.safe(f)
}

func (b *Bot) reload() {
	if c, err := loadConf(b.path); err == nil {
		b.mu.Lock()
		b.cfg = c
		b.mu.Unlock()
	}
}

func (b *Bot) conf() Conf {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cfg
}

func (b *Bot) t(en, id string) string {
	if b.conf().Lang == "id" {
		return id
	}
	return en
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (b *Bot) panelID(chat int64) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.panels[chat]
}

func (b *Bot) show(ctx context.Context, chat int64, m tmsg, kb Keyboard, drop ...int64) {
	old := b.panelID(chat)
	id, err := b.c.sendCard(ctx, chat, m, kb)
	if err != nil {
		log.Printf("bot: send failed: %v", err)
		return
	}
	b.mu.Lock()
	b.panels[chat] = id
	b.mu.Unlock()
	for _, x := range append([]int64{old}, drop...) {
		if x != id {
			b.c.deleteMsg(ctx, chat, x)
		}
	}
}

func (b *Bot) result(ctx context.Context, chat int64, m tmsg, kb Keyboard) {
	if _, err := b.c.sendCard(ctx, chat, m, kb); err != nil {
		log.Printf("bot: send failed: %v", err)
		return
	}
	b.mu.Lock()
	old := b.panels[chat]
	delete(b.panels, chat)
	b.mu.Unlock()
	b.c.deleteMsg(ctx, chat, old)
}

func (b *Bot) setPending(chat int64, p *pend) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p == nil {
		delete(b.pending, chat)
		return
	}
	p.at = time.Now()
	b.pending[chat] = p
}

func (b *Bot) getPending(chat int64) *pend {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.pending[chat]
	if p != nil && time.Since(p.at) > 15*time.Minute {
		delete(b.pending, chat)
		return nil
	}
	return p
}

func (b *Bot) handle(ctx context.Context, u tgUpdate) {
	if cb := u.Callback; cb != nil {
		if cb.Message == nil {
			return
		}
		if !b.conf().isAdmin(cb.From.ID) {
			b.c.answer(ctx, cb.ID, b.t("Not allowed", "Tidak diizinkan"))
			return
		}
		b.onCallback(ctx, cb)
		return
	}
	m := u.Message
	if m == nil || m.From == nil || m.Chat.Type != "private" {
		return
	}
	text := strings.TrimSpace(m.Text)
	cmd := strings.ToLower(strings.SplitN(strings.Fields(text + " ")[0], "@", 2)[0])
	if !b.conf().isAdmin(m.From.ID) {
		if cmd == "/pair" {
			b.pair(ctx, m, strings.TrimSpace(strings.TrimPrefix(text, strings.Fields(text)[0])))
			return
		}
		b.deny(ctx, m)
		return
	}
	chat := m.Chat.ID
	if m.Document != nil {
		b.onDocument(ctx, m)
		return
	}
	switch cmd {
	case "/start", "/menu":
		b.setPending(chat, nil)
		b.mainMenu(ctx, chat, m.MessageID)
		return
	case "/status":
		b.setPending(chat, nil)
		b.status(ctx, chat, m.MessageID)
		return
	case "/profiles":
		b.setPending(chat, nil)
		b.profiles(ctx, chat, m.MessageID)
		return
	case "/usage":
		b.setPending(chat, nil)
		b.usage(ctx, chat, m.MessageID, "today")
		return
	case "/pair":
		b.show(ctx, chat, newCard(b.t("Already paired", "Sudah terhubung")).text(b.t("You are already an admin.", "Kamu sudah admin.")).msg(), Keyboard{{b.mainBtn()}}, m.MessageID)
		return
	}
	if p := b.getPending(chat); p != nil {
		switch p.kind {
		case "link":
			b.onLink(ctx, chat, m)
			return
		case "expiry":
			b.onExpiry(ctx, chat, m, p.id)
			return
		case "restore":
			b.show(ctx, chat, newCard(b.t("Restore", "Pulihkan")).text(b.t("Send the backup as a .json file.", "Kirim backup sebagai file .json.")).msg(), Keyboard{{b.cancelBtn()}}, m.MessageID)
			return
		}
	}
	if strings.Contains(text, "://") || strings.Contains(strings.ToLower(text), "password") {
		if b.conf().Allow["accounts"] {
			b.onLink(ctx, chat, m)
			return
		}
	}
	b.mainMenu(ctx, chat, m.MessageID)
}

func (b *Bot) deny(ctx context.Context, m *tgIncoming) {
	b.mu.Lock()
	last := b.denied[m.From.ID]
	if time.Since(last) < 30*time.Second {
		b.mu.Unlock()
		return
	}
	b.denied[m.From.ID] = time.Now()
	b.mu.Unlock()
	c := newCard("Tomato Tunnel").
		kv("Telegram ID", strconv.FormatInt(m.From.ID, 10)).
		kv(b.t("Access", "Akses"), b.t("denied", "ditolak")).
		text(b.t("Ask the router owner for a pairing code,", "Minta kode pairing ke pemilik router,")).
		text(b.t("then send /pair <code>.", "lalu kirim /pair <kode>."))
	b.c.sendCard(ctx, m.Chat.ID, c.msg(), nil)
}

func (b *Bot) pair(ctx context.Context, m *tgIncoming, code string) {
	id := m.From.ID
	b.mu.Lock()
	var recent []time.Time
	for _, t := range b.pairTry[id] {
		if time.Since(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 5 {
		b.pairTry[id] = recent
		b.mu.Unlock()
		b.c.sendCard(ctx, m.Chat.ID, newCard(b.t("Too many tries", "Terlalu banyak percobaan")).text(b.t("Wait 10 minutes and try again.", "Tunggu 10 menit lalu coba lagi.")).msg(), nil)
		return
	}
	b.pairTry[id] = append(recent, time.Now())
	b.mu.Unlock()
	raw, err := os.ReadFile(RunDir + "/bot-pair")
	f := strings.Fields(string(raw))
	ok := err == nil && len(f) == 2 && len(code) == 6
	if ok {
		exp, _ := strconv.ParseInt(f[1], 10, 64)
		ok = time.Now().Unix() <= exp && subtle.ConstantTimeCompare([]byte(f[0]), []byte(code)) == 1
	}
	if !ok {
		b.c.sendCard(ctx, m.Chat.ID, newCard(b.t("Pairing failed", "Pairing gagal")).text(b.t("The code is wrong or expired.", "Kode salah atau sudah kedaluwarsa.")).text(b.t("Make a new one in LuCI › Settings › Telegram.", "Buat kode baru di LuCI › Settings › Telegram.")).msg(), nil)
		return
	}
	os.Remove(RunDir + "/bot-pair")
	name := strings.TrimSpace(m.From.FirstName)
	if name == "" {
		name = m.From.Username
	}
	if err := addAdmin(id, name); err != nil {
		b.c.sendCard(ctx, m.Chat.ID, newCard(b.t("Pairing failed", "Pairing gagal")).text(err.Error()).msg(), nil)
		return
	}
	b.reload()
	log.Printf("bot: paired admin %d", id)
	c := newCard(b.t("Paired", "Terhubung")).
		kv("Telegram ID", strconv.FormatInt(id, 10)).
		kv(b.t("Name", "Nama"), name).
		kv(b.t("Role", "Peran"), "Admin").
		text(b.t("Send /start to open the menu.", "Kirim /start untuk membuka menu."))
	b.c.sendCard(ctx, m.Chat.ID, c.msg(), Keyboard{{b.mainBtn()}})
}

func (b *Bot) mainBtn() Button {
	return Button{Text: b.t("Main menu", "Menu utama"), CallbackData: "m"}
}

func (b *Bot) cancelBtn() Button {
	return Button{Text: b.t("Cancel", "Batal"), CallbackData: "cx", Style: "danger"}
}

func (b *Bot) backBtn(to string) Button {
	return Button{Text: b.t("Back", "Kembali"), CallbackData: to}
}

func (b *Bot) denyAction(ctx context.Context, cb *tgCallback, key string) bool {
	if b.conf().Allow[key] {
		return false
	}
	b.c.answer(ctx, cb.ID, b.t("Turned off in LuCI › Settings › Telegram", "Dimatikan di LuCI › Settings › Telegram"))
	return true
}

func (b *Bot) onCallback(ctx context.Context, cb *tgCallback) {
	chat := cb.Message.Chat.ID
	msg := cb.Message.MessageID
	d := cb.Data
	arg := ""
	if i := strings.Index(d, ":"); i > 0 {
		d, arg = d[:i], d[i+1:]
	}
	answered := false
	ans := func(t string) {
		if !answered {
			b.c.answer(ctx, cb.ID, t)
			answered = true
		}
	}
	defer ans("")
	switch d {
	case "m":
		b.setPending(chat, nil)
		b.mainMenu(ctx, chat, msg)
	case "cx":
		b.setPending(chat, nil)
		b.mainMenu(ctx, chat, msg)
	case "st":
		b.status(ctx, chat, msg)
	case "pf":
		b.profiles(ctx, chat, msg)
	case "sw":
		if b.denyAction(ctx, cb, "control") {
			return
		}
		ans(b.t("Switching…", "Memindahkan…"))
		b.switchTo(ctx, chat, msg, arg)
	case "swc":
		ctl(ctx, "cancel-switch")
		ans(b.t("Cancelled", "Dibatalkan"))
	case "po":
		if b.denyAction(ctx, cb, "control") {
			return
		}
		ctl(ctx, "policy", arg)
		b.profiles(ctx, chat, msg)
	case "lt":
		ans(b.t("Testing, about 20 s…", "Mengetes, sekitar 20 detik…"))
		b.latency(ctx, chat, msg)
	case "ip":
		if b.denyAction(ctx, cb, "changeip") {
			return
		}
		b.changeIPAsk(ctx, chat, msg, arg)
	case "ipy":
		if b.denyAction(ctx, cb, "changeip") {
			return
		}
		b.changeIP(ctx, chat, msg, arg)
	case "rs":
		if b.denyAction(ctx, cb, "control") {
			return
		}
		ans(b.t("Restarting…", "Memulai ulang…"))
		b.startStop(ctx, chat, msg, "restart")
	case "sa":
		if b.denyAction(ctx, cb, "control") {
			return
		}
		ans(b.t("Starting…", "Menyalakan…"))
		b.startStop(ctx, chat, msg, "start")
	case "sp":
		if b.denyAction(ctx, cb, "control") {
			return
		}
		b.show(ctx, chat, newCard(b.t("Stop tunnel?", "Matikan tunnel?")).text(b.t("Devices will use the normal line.", "Perangkat kembali memakai jalur biasa.")).msg(),
			Keyboard{{{Text: b.t("Yes, stop", "Ya, matikan"), CallbackData: "spy", Style: "danger"}, {Text: b.t("Cancel", "Batal"), CallbackData: "m"}}}, msg)
	case "spy":
		if b.denyAction(ctx, cb, "control") {
			return
		}
		b.startStop(ctx, chat, msg, "stop")
	case "us":
		b.usage(ctx, chat, msg, arg)
	case "ac":
		b.accounts(ctx, chat, msg)
	case "a":
		b.account(ctx, chat, msg, arg)
	case "aco":
		if b.denyAction(ctx, cb, "control") {
			return
		}
		ans(b.t("Connecting…", "Menyambungkan…"))
		b.switchTo(ctx, chat, msg, arg)
	case "ad":
		if b.denyAction(ctx, cb, "accounts") {
			return
		}
		b.toggleDash(ctx, chat, msg, arg)
	case "ae":
		if b.denyAction(ctx, cb, "accounts") {
			return
		}
		b.setPending(chat, &pend{kind: "expiry", id: arg})
		c := newCard(b.t("Set expiry", "Atur masa aktif")).
			text(b.t("Send the date, e.g. 2026-12-31,", "Kirim tanggal, misal 2026-12-31,")).
			text(b.t("or days from today, e.g. 30.", "atau jumlah hari dari sekarang, misal 30.")).
			text(b.t("Send 0 to remove the date.", "Kirim 0 untuk menghapus tanggal."))
		b.show(ctx, chat, c.msg(), Keyboard{{b.cancelBtn()}}, msg)
	case "ax":
		if b.denyAction(ctx, cb, "accounts") {
			return
		}
		b.deleteAsk(ctx, chat, msg, arg)
	case "axy":
		if b.denyAction(ctx, cb, "accounts") {
			return
		}
		b.deleteDo(ctx, chat, msg, arg)
	case "a+":
		if b.denyAction(ctx, cb, "accounts") {
			return
		}
		b.setPending(chat, &pend{kind: "link"})
		c := newCard(b.t("Add account", "Tambah akun")).
			text(b.t("Send a link: vless://, vmess://, trojan://", "Kirim link: vless://, vmess://, trojan://")).
			text(b.t("or SSH: host:port@user:pass", "atau SSH: host:port@user:pass")).
			text(b.t("or paste the account message.", "atau tempel pesan akunnya.")).
			note(b.t("Your message is deleted after reading.", "Pesanmu dihapus setelah dibaca."))
		b.show(ctx, chat, c.msg(), Keyboard{{b.cancelBtn()}}, msg)
	case "asv", "asc":
		if b.denyAction(ctx, cb, "accounts") {
			return
		}
		b.saveAccounts(ctx, chat, msg, d == "asc")
	case "rt":
		ans(b.t("Reading…", "Membaca…"))
		b.router(ctx, chat, msg)
	case "dv":
		b.devices(ctx, chat, msg)
	case "rb":
		if b.denyAction(ctx, cb, "reboot") {
			return
		}
		b.show(ctx, chat, newCard(b.t("Reboot router?", "Reboot router?")).text(b.t("The bot is back in about 1 minute.", "Bot kembali sekitar 1 menit lagi.")).msg(),
			Keyboard{{{Text: b.t("Yes, reboot", "Ya, reboot"), CallbackData: "rby", Style: "danger"}, {Text: b.t("Cancel", "Batal"), CallbackData: "rt"}}}, msg)
	case "rby":
		if b.denyAction(ctx, cb, "reboot") {
			return
		}
		b.reboot(ctx, chat, msg)
	case "tl":
		b.tools(ctx, chat, msg)
	case "lg":
		b.logView(ctx, chat, msg)
	case "lgf":
		ans(b.t("Sending…", "Mengirim…"))
		b.logFile(ctx, chat)
	case "uc":
		ans(b.t("Checking GitHub…", "Mengecek GitHub…"))
		b.updateCheck(ctx, chat, msg)
	case "ui":
		if b.denyAction(ctx, cb, "update") {
			return
		}
		b.updateInstall(ctx, chat, msg)
	case "bk":
		ans(b.t("Sending…", "Mengirim…"))
		b.backup(ctx, chat)
	case "rsf":
		if b.denyAction(ctx, cb, "restore") {
			return
		}
		b.setPending(chat, &pend{kind: "restore"})
		b.show(ctx, chat, newCard(b.t("Restore", "Pulihkan")).text(b.t("Send the backup .json file.", "Kirim file backup .json.")).note(b.t("Accounts already here are skipped.", "Akun yang sudah ada dilewati.")).msg(), Keyboard{{b.cancelBtn()}}, msg)
	case "rsy":
		if b.denyAction(ctx, cb, "restore") {
			return
		}
		b.restoreDo(ctx, chat, msg)
	case "nt":
		if arg != "" {
			cfg := b.conf()
			v := "1"
			if cfg.Notify[arg] {
				v = "0"
			}
			if err := setBotOpt("n_"+arg, v); err == nil {
				b.reload()
			}
		}
		b.notifyMenu(ctx, chat, msg)
	}
}

func (b *Bot) profileName(id string) string {
	ps, _ := loadProfiles(b.path)
	for _, p := range ps {
		if profileKey(p) == id || p.ID == id {
			return p.Name
		}
	}
	return id
}

func (b *Bot) findProfile(id string) (config.Profile, bool) {
	ps, _ := loadProfiles(b.path)
	for _, p := range ps {
		if profileKey(p) == id || p.ID == id {
			return p, true
		}
	}
	return config.Profile{}, false
}

func (b *Bot) stateText(s string) string {
	switch s {
	case "connected":
		return b.t("Connected", "Tersambung")
	case "connecting", "starting":
		return b.t("Connecting", "Menyambung")
	case "reconnecting":
		return b.t("Reconnecting", "Menyambung ulang")
	case "failed":
		return b.t("Failed", "Gagal")
	}
	return b.t("Stopped", "Mati")
}

func (b *Bot) policyText(p string) string {
	switch p {
	case "manual":
		return "Manual"
	case "auto":
		return "Auto"
	case "balance":
		return "Balance"
	}
	return "Fallback"
}

func (b *Bot) mainMenu(ctx context.Context, chat, drop int64) {
	s := readStatus()
	sys := readSys()
	c := newCard("Tomato Tunnel")
	sub := "v" + b.version
	if sys.Model != "" {
		sub = sys.Model + " · " + sub
	}
	c.note(sub)
	c.kv("Status", b.stateText(s.State))
	cc, _ := config.Load(b.path)
	if s.State == "stopped" {
		if cc != nil {
			name := ""
			for _, p := range cc.Ordered() {
				name = p.Name
				break
			}
			c.kv(b.t("Profile", "Profil"), name)
			c.kv(b.t("Mode", "Mode"), b.policyText(cc.Main.Policy))
		}
	} else {
		c.kv(b.t("Profile", "Profil"), s.ProfileName)
		c.kv(b.t("Mode", "Mode"), b.policyText(s.Policy))
		c.kv("Public IP", s.PublicIP)
		c.kv("ISP", s.ISP)
		if s.PingMS > 0 {
			c.kv("Ping", fmt.Sprintf("%d ms", s.PingMS))
		}
		if s.State == "connected" && s.Since > 0 {
			c.kv("Uptime", dur(time.Since(time.Unix(s.Since, 0))))
		}
		if s.Message != "" && s.State != "connected" {
			c.kv(b.t("Reason", "Alasan"), s.Message)
		}
	}
	u := readUsage(ctx)
	if d := u.Days[time.Now().Format("2006-01-02")]; d != nil {
		c.head(b.t("Today", "Hari ini"), b.t("Data", "Data"))
		c.kv("Download", human(d.RX)).kv("Upload", human(d.TX)).kv("Total", human(d.RX+d.TX))
	}
	kb := Keyboard{
		{{Text: "Status", CallbackData: "st", Style: "primary"}, {Text: b.t("Profiles", "Profil"), CallbackData: "pf", Style: "success"}},
		{{Text: "Change IP", CallbackData: "ip", Style: "success"}, {Text: "Restart", CallbackData: "rs", Style: "primary"}},
		{{Text: "Usage", CallbackData: "us:today", Style: "primary"}, {Text: b.t("Accounts", "Akun"), CallbackData: "ac", Style: "primary"}},
		{{Text: "Router", CallbackData: "rt", Style: "primary"}, {Text: "Tools", CallbackData: "tl"}},
	}
	if s.State == "stopped" {
		kb[1][1] = Button{Text: b.t("Start tunnel", "Nyalakan tunnel"), CallbackData: "sa", Style: "success"}
	} else {
		kb = append(kb, []Button{{Text: b.t("Stop tunnel", "Matikan tunnel"), CallbackData: "sp", Style: "danger"}})
	}
	b.show(ctx, chat, c.msg(), kb, drop)
}

func (b *Bot) status(ctx context.Context, chat, drop int64) {
	s := readStatus()
	c := newCard("Status")
	c.kv("Tunnel", b.stateText(s.State))
	if s.State != "stopped" {
		c.kv(b.t("Profile", "Profil"), s.ProfileName)
		if p, ok := b.findProfile(s.Profile); ok {
			c.kv(b.t("Type", "Tipe"), typeLabel(p))
			c.kv("Server", server(p))
		}
		if s.Since > 0 && s.State == "connected" {
			c.kv(b.t("Since", "Sejak"), time.Unix(s.Since, 0).Format("15:04")+" ("+dur(time.Since(time.Unix(s.Since, 0)))+")")
		}
		c.kv(b.t("Reconnects", "Sambung ulang"), fmt.Sprintf("%d %s", s.Reconnects, b.t("today", "hari ini")))
		c.kv("Public IP", s.PublicIP)
		c.kv("ISP", s.ISP)
		if s.Switching != "" {
			c.kv(b.t("Switching to", "Pindah ke"), s.Switching)
		}
		if s.Message != "" && s.State != "connected" {
			c.kv(b.t("Reason", "Alasan"), s.Message)
		}
		c.head(b.t("Now", "Sekarang"), b.t("Speed", "Kecepatan"))
		c.kv("Download", rate(s.RateRX)).kv("Upload", rate(s.RateTX))
		if s.PingMS > 0 {
			c.kv("Ping", fmt.Sprintf("%d ms", s.PingMS))
		}
	}
	b.show(ctx, chat, c.msg(), Keyboard{
		{{Text: "Refresh", CallbackData: "st", Style: "primary"}, {Text: b.t("Test latency", "Tes latency"), CallbackData: "lt", Style: "primary"}},
		{b.mainBtn()},
	}, drop)
}

type latRes struct {
	MS    int64  `json:"ms"`
	Auth  bool   `json:"login_failed"`
	Error string `json:"error"`
}

func readLatency(ctx context.Context) map[string]latRes {
	var r struct {
		Results map[string]latRes `json:"results"`
	}
	if s, err := ctl(ctx, "latency-cached"); err == nil {
		json.Unmarshal([]byte(s), &r)
	}
	return r.Results
}

func (b *Bot) pingCell(p config.Profile, lat map[string]latRes, s Status) string {
	k := profileKey(p)
	if _, ok := s.LoginFailed[k]; ok {
		return b.t("login failed", "login gagal")
	}
	if p.Expired(time.Now()) {
		return b.t("expired", "habis")
	}
	if r, ok := lat[k]; ok {
		if r.Auth {
			return b.t("login failed", "login gagal")
		}
		if r.MS >= 0 && r.Error == "" {
			return fmt.Sprintf("%d ms", r.MS)
		}
		return b.t("failed", "gagal")
	}
	return "-"
}

func (b *Bot) profiles(ctx context.Context, chat, drop int64) {
	s := readStatus()
	ps, _ := loadProfiles(b.path)
	cc, _ := config.Load(b.path)
	pol := s.Policy
	active := s.Profile
	if s.State == "stopped" && cc != nil {
		pol = cc.Main.Policy
		if o := cc.Ordered(); len(o) > 0 {
			active = profileKey(o[0])
		}
	}
	lat := readLatency(ctx)
	c := newCard(b.t("Profiles", "Profil"))
	c.note(b.t("Mode: ", "Mode: ") + b.policyText(pol) + b.t(" · tap a number to switch", " · ketuk nomor untuk pindah"))
	var rows [][]cell
	var kb Keyboard
	var row []Button
	for i, p := range ps {
		name := p.Name
		if profileKey(p) == active {
			name += " ●"
		}
		if !p.Dashboard {
			name += b.t(" (hidden)", " (disembunyikan)")
		}
		rows = append(rows, []cell{ct(strconv.Itoa(i + 1)), ct(name), ct(shortType(p)), cv(b.pingCell(p, lat, s))})
		st := "success"
		if profileKey(p) == active {
			st = "primary"
		}
		row = append(row, Button{Text: strconv.Itoa(i + 1), CallbackData: "sw:" + profileKey(p), Style: st})
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
	c.table([]string{"No", b.t("Profile", "Profil"), b.t("Type", "Tipe"), "Ping"}, rows)
	c.note(b.t("● = active profile.", "● = profil aktif."))
	mode := []Button{}
	for _, x := range []string{"manual", "fallback", "auto", "balance"} {
		st := ""
		if x == pol {
			st = "success"
		}
		mode = append(mode, Button{Text: b.policyText(x), CallbackData: "po:" + x, Style: st})
	}
	kb = append(kb, mode, []Button{{Text: b.t("Test latency", "Tes latency"), CallbackData: "lt", Style: "primary"}}, []Button{b.mainBtn()})
	b.show(ctx, chat, c.msg(), kb, drop)
}

func (b *Bot) latency(ctx context.Context, chat, drop int64) {
	b.show(ctx, chat, newCard(b.t("Testing latency…", "Mengetes latency…")).text(b.t("About 20 seconds.", "Sekitar 20 detik.")).msg(), nil, drop)
	b.goSafe(func() {
		cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		ctl(cctx, "latency")
		cancel()
		b.profiles(ctx, chat, 0)
	})
}

func (b *Bot) quietFor(kind string, d time.Duration) {
	b.mu.Lock()
	b.quiet[kind] = time.Now().Add(d)
	b.mu.Unlock()
}

func (b *Bot) isQuiet(kind string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().Before(b.quiet[kind])
}

func (b *Bot) switchTo(ctx context.Context, chat, drop int64, id string) {
	p, ok := b.findProfile(id)
	if !ok {
		b.show(ctx, chat, newCard(b.t("Not found", "Tidak ditemukan")).text(b.t("That profile no longer exists.", "Profil itu sudah tidak ada.")).msg(), Keyboard{{b.backBtn("pf")}}, drop)
		return
	}
	s := readStatus()
	if s.State != "stopped" && s.Profile == profileKey(p) && s.State == "connected" {
		b.show(ctx, chat, newCard(p.Name).text(b.t("This profile is already active.", "Profil ini sudah aktif.")).msg(), Keyboard{{b.backBtn("pf")}}, drop)
		return
	}
	out, _ := ctl(ctx, "switch", profileKey(p))
	if s.State == "stopped" {
		ctl(ctx, "start")
		b.show(ctx, chat, newCard(b.t("Starting…", "Menyalakan…")).kv(b.t("Profile", "Profil"), p.Name).msg(), nil, drop)
		b.goSafe(func() { b.waitConnected(ctx, chat, p.Name, time.Now()) })
		return
	}
	if !strings.Contains(out, "switch") {
		b.show(ctx, chat, newCard(b.t("Switch failed", "Gagal pindah")).text(out).msg(), Keyboard{{b.backBtn("pf")}}, drop)
		return
	}
	b.quietFor("switch", 90*time.Second)
	from := s.ProfileName
	c := newCard(b.t("Switching…", "Memindahkan…")).kv(b.t("From", "Dari"), from).kv(b.t("To", "Ke"), p.Name).
		note(b.t("The old connection stays up until the new one is ready.", "Koneksi lama tetap jalan sampai yang baru siap."))
	b.show(ctx, chat, c.msg(), Keyboard{{{Text: b.t("Cancel", "Batal"), CallbackData: "swc", Style: "danger"}}}, drop)
	t0 := time.Now()
	key := profileKey(p)
	b.goSafe(func() {
		seen := false
		for time.Since(t0) < 80*time.Second && ctx.Err() == nil {
			time.Sleep(time.Second)
			st := readStatus()
			if st.Switching != "" {
				seen = true
			}
			if st.Profile == key && st.State == "connected" && st.Switching == "" {
				time.Sleep(2 * time.Second)
				st = readStatus()
				r := newCard(b.t("Switched", "Berhasil pindah")).kv(b.t("From", "Dari"), from).kv(b.t("To", "Ke"), p.Name).
					kv(b.t("Took", "Waktu"), fmt.Sprintf("%.1f s", time.Since(t0).Seconds())).kv("Public IP", st.PublicIP)
				if st.PingMS > 0 {
					r.kv("Ping", fmt.Sprintf("%d ms", st.PingMS))
				}
				b.result(ctx, chat, r.msg(), Keyboard{{b.mainBtn()}})
				return
			}
			if (seen || time.Since(t0) > 8*time.Second) && st.Switching == "" && st.Profile != key {
				r := newCard(b.t("Switch failed", "Gagal pindah")).kv(b.t("Wanted", "Tujuan"), p.Name).kv(b.t("Still on", "Tetap di"), st.ProfileName)
				if _, bad := st.LoginFailed[key]; bad {
					r.kv(b.t("Reason", "Alasan"), b.t("login failed", "login gagal"))
				}
				r.note(b.t("Nothing was cut.", "Koneksi tidak terputus."))
				b.result(ctx, chat, r.msg(), Keyboard{{{Text: b.t("Profiles", "Profil"), CallbackData: "pf", Style: "success"}, b.mainBtn()}})
				return
			}
		}
		b.result(ctx, chat, newCard(b.t("Still switching", "Masih memindahkan")).text(b.t("Check Status in a moment.", "Cek Status sebentar lagi.")).msg(), Keyboard{{{Text: "Status", CallbackData: "st", Style: "primary"}}})
	})
}

func (b *Bot) waitConnected(ctx context.Context, chat int64, name string, t0 time.Time) {
	b.quietFor("down", 90*time.Second)
	for time.Since(t0) < 75*time.Second && ctx.Err() == nil {
		time.Sleep(time.Second)
		st := readStatus()
		if st.State == "connected" {
			time.Sleep(2 * time.Second)
			st = readStatus()
			r := newCard(b.t("Connected", "Tersambung")).kv(b.t("Profile", "Profil"), st.ProfileName).
				kv(b.t("Took", "Waktu"), fmt.Sprintf("%.1f s", time.Since(t0).Seconds())).kv("Public IP", st.PublicIP)
			b.result(ctx, chat, r.msg(), Keyboard{{b.mainBtn()}})
			return
		}
	}
	st := readStatus()
	r := newCard(b.t("Not connected yet", "Belum tersambung")).kv("Status", b.stateText(st.State)).kv(b.t("Profile", "Profil"), st.ProfileName)
	if st.Message != "" {
		r.kv(b.t("Reason", "Alasan"), st.Message)
	}
	b.result(ctx, chat, r.msg(), Keyboard{{{Text: "Log", CallbackData: "lg", Style: "primary"}, b.mainBtn()}})
}

func (b *Bot) startStop(ctx context.Context, chat, drop int64, act string) {
	switch act {
	case "stop":
		b.quietFor("down", 30*time.Second)
		ctl(ctx, "stop")
		time.Sleep(time.Second)
		b.result(ctx, chat, newCard(b.t("Tunnel stopped", "Tunnel dimatikan")).text(b.t("Devices use the normal line now.", "Perangkat memakai jalur biasa.")).msg(), Keyboard{{{Text: b.t("Start tunnel", "Nyalakan tunnel"), CallbackData: "sa", Style: "success"}, b.mainBtn()}})
		b.c.deleteMsg(ctx, chat, drop)
	default:
		ctl(ctx, act)
		b.show(ctx, chat, newCard(b.t("Connecting…", "Menyambung…")).text(b.t("Usually a few seconds.", "Biasanya beberapa detik.")).msg(), nil, drop)
		t0 := time.Now()
		b.goSafe(func() {
			time.Sleep(2 * time.Second)
			b.waitConnected(ctx, chat, "", t0)
		})
	}
}

func (b *Bot) ipMethod(c config.ChangeIP) string {
	switch c.Method {
	case "iface":
		return b.t("Restart interface", "Restart interface") + " (" + c.Iface + ")"
	case "at":
		return "AT (" + c.ATPort + ")"
	case "mm":
		return "ModemManager"
	case "hilink":
		return "HiLink / Orbit"
	case "adb":
		return "Android (ADB)"
	}
	return b.t("Off", "Mati")
}

func (b *Bot) changeIPAsk(ctx context.Context, chat, drop int64, id string) {
	cc, err := config.Load(b.path)
	if err != nil || len(cc.Main.Modems) == 0 {
		b.show(ctx, chat, newCard("Change IP").text(b.t("Change IP is off.", "Change IP belum aktif.")).text(b.t("Set it up in LuCI › Settings › Change IP.", "Atur di LuCI › Settings › Change IP.")).msg(), Keyboard{{b.mainBtn()}}, drop)
		return
	}
	ms := cc.Main.Modems
	if len(ms) > 1 && id == "" {
		st := readStatus()
		c := newCard("Change IP").text(b.t("Which modem?", "Modem yang mana?"))
		kb := Keyboard{}
		for _, m := range ms {
			ip := ""
			for _, x := range st.Modems {
				if x.ID == m.ID {
					ip = x.IP
					if x.Tunnel {
						ip += " · tunnel"
					}
				}
			}
			c.kv(m.Name, b.ipMethod(m)+orEmpty(" · ", ip))
			kb = append(kb, []Button{{Text: m.Name, CallbackData: "ip:" + m.ID, Style: "success"}})
		}
		kb = append(kb, []Button{{Text: b.t("All modems", "Semua modem"), CallbackData: "ip:all"}, b.mainBtn()})
		b.show(ctx, chat, c.msg(), kb, drop)
		return
	}
	c := newCard("Change IP")
	switch {
	case id == "all":
		c.kv("Modem", b.t("All, one after another", "Semua, bergantian"))
	default:
		m := ms[0]
		if x, ok := cc.Main.Modem(id); ok {
			m = x
		}
		id = m.ID
		if len(ms) > 1 {
			c.kv("Modem", m.Name)
		}
		c.kv(b.t("Method", "Metode"), b.ipMethod(m))
	}
	c.text(b.t("Internet drops for about 10–30 s.", "Internet putus sekitar 10–30 detik."))
	b.show(ctx, chat, c.msg(), Keyboard{{{Text: b.t("Yes, change", "Ya, ganti"), CallbackData: "ipy:" + id, Style: "success"}, {Text: b.t("Cancel", "Batal"), CallbackData: "m", Style: "danger"}}}, drop)
}

func orEmpty(sep, s string) string {
	if s == "" {
		return ""
	}
	return sep + s
}

func (b *Bot) changeIP(ctx context.Context, chat, drop int64, id string) {
	s := readStatus()
	cc, _ := config.Load(b.path)
	method := ""
	if cc != nil && len(cc.Main.Modems) > 0 {
		m := cc.Main.Modems[0]
		if x, ok := cc.Main.Modem(id); ok {
			m = x
		}
		method = b.ipMethod(m)
		if len(cc.Main.Modems) > 1 {
			method = m.Name + " · " + method
		}
		if id == "all" {
			method = b.t("All modems", "Semua modem")
		}
	}
	b.quietFor("ip", 4*time.Minute)
	b.quietFor("down", 4*time.Minute)
	b.show(ctx, chat, newCard(b.t("Changing IP…", "Mengganti IP…")).kv(b.t("Method", "Metode"), method).text(b.t("This can take up to 2 minutes.", "Bisa sampai 2 menit.")).msg(), nil, drop)
	t0 := time.Now()
	b.goSafe(func() {
		done := func(old, nw, errText string) {
			r := newCard(b.t("IP changed", "IP berganti"))
			if errText != "" {
				r = newCard(b.t("Change IP failed", "Change IP gagal")).kv(b.t("Reason", "Alasan"), errText)
			} else {
				r.kv(b.t("Old IP", "IP lama"), old).kv(b.t("New IP", "IP baru"), nw)
			}
			r.kv(b.t("Took", "Waktu"), fmt.Sprintf("%d s", int(time.Since(t0).Seconds())))
			if st := readStatus(); st.State != "stopped" {
				r.kv("Tunnel", b.stateText(st.State))
			}
			b.result(ctx, chat, r.msg(), Keyboard{{{Text: b.t("Change again", "Ganti lagi"), CallbackData: "ipy:" + id, Style: "success"}, b.mainBtn()}})
		}
		if s.State == "stopped" {
			out, _ := runBin(ctx, 4*time.Minute, "changeip", id)
			var r struct {
				Old, New, Error string
				OK              bool
			}
			if i := strings.LastIndex(out, "{"); i >= 0 {
				json.Unmarshal([]byte(out[i:]), &r)
			}
			if !r.OK && r.Error == "" {
				r.Error = b.t("no answer", "tidak ada jawaban")
			}
			done(r.Old, r.New, r.Error)
			return
		}
		before := s.LastIPChange
		ctl(ctx, "changeip", id)
		for time.Since(t0) < 4*time.Minute && ctx.Err() == nil {
			time.Sleep(2 * time.Second)
			st := readStatus()
			if st.LastIPChange > before {
				if strings.HasPrefix(st.IPChangeMsg, "New IP ") {
					done(st.IPChangeOld, strings.TrimPrefix(st.IPChangeMsg, "New IP "), "")
				} else {
					done("", "", st.IPChangeMsg)
				}
				return
			}
		}
		done("", "", b.t("timed out", "waktu habis"))
	})
}

func (b *Bot) usage(ctx context.Context, chat, drop int64, period string) {
	u := readUsage(ctx)
	now := time.Now()
	sum := func(days int) (rx, tx int64, prof, dev map[string]*UsageItem) {
		prof, dev = map[string]*UsageItem{}, map[string]*UsageItem{}
		for i := 0; i < days; i++ {
			d := u.Days[now.AddDate(0, 0, -i).Format("2006-01-02")]
			if d == nil {
				continue
			}
			rx += d.RX
			tx += d.TX
			for k, it := range d.Profiles {
				x := prof[k]
				if x == nil {
					x = &UsageItem{Name: it.Name}
					prof[k] = x
				}
				x.RX += it.RX
				x.TX += it.TX
			}
			for k, it := range d.Devices {
				x := dev[k]
				if x == nil {
					x = &UsageItem{Name: it.Name}
					dev[k] = x
				}
				if it.Name != "" {
					x.Name = it.Name
				}
				x.RX += it.RX
				x.TX += it.TX
			}
		}
		return
	}
	trx, ttx, _, _ := sum(1)
	wrx, wtx, _, _ := sum(7)
	var mrx, mtx int64
	if m := u.Months[now.Format("2006-01")]; m != nil {
		mrx, mtx = m.RX, m.TX
	}
	c := newCard("Usage")
	c.table([]string{b.t("Period", "Periode"), "Down", "Up", "Total"}, [][]cell{
		{ct(b.t("Today", "Hari ini")), cv(human(trx)), cv(human(ttx)), cv(human(trx + ttx))},
		{ct(b.t("7 days", "7 hari")), cv(human(wrx)), cv(human(wtx)), cv(human(wrx + wtx))},
		{ct(b.t("Month", "Bulan")), cv(human(mrx)), cv(human(mtx)), cv(human(mrx + mtx))},
	})
	days := map[string]int{"today": 1, "7d": 7, "month": now.Day()}[period]
	if days == 0 {
		period, days = "today", 1
	}
	label := map[string]string{"today": b.t("Today", "Hari ini"), "7d": b.t("7 days", "7 hari"), "month": b.t("Month", "Bulan")}[period]
	_, _, prof, dev := sum(days)
	top := func(m map[string]*UsageItem, n int) [][]cell {
		type kv struct {
			k string
			v *UsageItem
		}
		var l []kv
		for k, v := range m {
			l = append(l, kv{k, v})
		}
		for i := 0; i < len(l); i++ {
			for j := i + 1; j < len(l); j++ {
				if l[j].v.RX+l[j].v.TX > l[i].v.RX+l[i].v.TX {
					l[i], l[j] = l[j], l[i]
				}
			}
		}
		var rows [][]cell
		var rest int64
		for i, x := range l {
			if i >= n {
				rest += x.v.RX + x.v.TX
				continue
			}
			name := x.v.Name
			if name == "" {
				name = x.k
			}
			rows = append(rows, []cell{ct(name), cv(human(x.v.RX + x.v.TX))})
		}
		if rest > 0 {
			rows = append(rows, []cell{ct(fmt.Sprintf(b.t("Others (%d)", "Lainnya (%d)"), len(l)-n)), cv(human(rest))})
		}
		return rows
	}
	c.table([]string{b.t("Profile", "Profil"), label}, top(prof, 6))
	c.table([]string{b.t("Device", "Perangkat"), label}, top(dev, 6))
	kb := Keyboard{{}}
	for _, x := range [][2]string{{"today", b.t("Today", "Hari ini")}, {"7d", b.t("7 days", "7 hari")}, {"month", b.t("Month", "Bulan")}} {
		st := ""
		if x[0] == period {
			st = "primary"
		}
		kb[0] = append(kb[0], Button{Text: x[1], CallbackData: "us:" + x[0], Style: st})
	}
	kb = append(kb, []Button{b.mainBtn()})
	b.show(ctx, chat, c.msg(), kb, drop)
}

func runBin(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := execCmd(cctx, BinPath, args...)
	return out, err
}

func (b *Bot) testMessage(ctx context.Context) {
	st := readStatus()
	c := newCard(b.t("Test message", "Pesan tes")).
		text(b.t("The bot can reach you.", "Bot bisa mengirim pesan ke kamu.")).
		kv("Tunnel", b.stateText(st.State)).
		kv(b.t("Time", "Waktu"), time.Now().Format("02.01.2006 15:04:05"))
	for _, a := range b.conf().Admins {
		b.c.sendCard(ctx, a.ID, c.msg(), Keyboard{{b.mainBtn()}})
	}
}
