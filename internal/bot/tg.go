package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var apiBase = "https://api.telegram.org"

type Button struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
	Style        string `json:"style,omitempty"`
}

type Keyboard [][]Button

type tgError struct {
	code int
	desc string
}

func (e *tgError) Error() string { return fmt.Sprintf("telegram %d: %s", e.code, e.desc) }

type client struct {
	token  string
	route  string
	socks  string
	direct *http.Client
	proxy  *http.Client
	mu     sync.Mutex
	viaTun bool
}

func newClient(token, route, socks string) *client {
	d := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 15 * time.Second}).DialContext, TLSHandshakeTimeout: 15 * time.Second, IdleConnTimeout: 90 * time.Second}
	c := &client{token: token, route: route, socks: socks, direct: &http.Client{Transport: d, Timeout: 75 * time.Second}}
	if u, err := url.Parse("socks5://" + socks); err == nil && socks != "" {
		p := &http.Transport{Proxy: http.ProxyURL(u), TLSHandshakeTimeout: 20 * time.Second, IdleConnTimeout: 90 * time.Second}
		c.proxy = &http.Client{Transport: p, Timeout: 75 * time.Second}
	}
	return c
}

func (c *client) clients() []*http.Client {
	switch {
	case c.route == "tunnel" && c.proxy != nil:
		return []*http.Client{c.proxy}
	case c.route == "wan" || c.proxy == nil:
		return []*http.Client{c.direct}
	}
	c.mu.Lock()
	tun := c.viaTun
	c.mu.Unlock()
	if tun {
		return []*http.Client{c.proxy, c.direct}
	}
	return []*http.Client{c.direct, c.proxy}
}

func (c *client) do(build func() (*http.Request, error), out any) error {
	var last error
	for i, hc := range c.clients() {
		req, err := build()
		if err != nil {
			return err
		}
		resp, err := hc.Do(req)
		if err != nil {
			if req.Context().Err() != nil {
				return req.Context().Err()
			}
			var ue *url.Error
			if errors.As(err, &ue) {
				last = fmt.Errorf("telegram request failed: %v", ue.Err)
			} else {
				last = errors.New("telegram request failed")
			}
			continue
		}
		if c.route == "auto" && c.proxy != nil {
			c.mu.Lock()
			c.viaTun = hc == c.proxy && i >= 0
			c.mu.Unlock()
		}
		return decode(resp, out)
	}
	return last
}

func decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if ro, ok := out.(rawOut); ok {
		if resp.StatusCode != 200 {
			return fmt.Errorf("download failed (HTTP %d)", resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, ro.max+1))
		if err != nil {
			return err
		}
		if int64(len(b)) > ro.max {
			return fmt.Errorf("file is too big")
		}
		*ro.b = b
		return nil
	}
	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r); err != nil {
		return fmt.Errorf("telegram: bad response (HTTP %d)", resp.StatusCode)
	}
	if !r.OK {
		return &tgError{r.ErrorCode, r.Description}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (c *client) call(ctx context.Context, method string, payload any, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.do(func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", apiBase+"/bot"+c.token+"/"+method, bytes.NewReader(b))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return req, err
	}, out)
}

func (k Keyboard) withoutStyle() Keyboard {
	out := make(Keyboard, len(k))
	for i, row := range k {
		out[i] = make([]Button, len(row))
		for j, b := range row {
			b.Style = ""
			out[i][j] = b
		}
	}
	return out
}

func styleRejected(err error) bool {
	var te *tgError
	if !errors.As(err, &te) || te.code != 400 {
		return false
	}
	d := strings.ToLower(te.desc)
	return strings.Contains(d, "style") || strings.Contains(d, "inline keyboard") || strings.Contains(d, "button")
}

type tgMessage struct {
	MessageID int64 `json:"message_id"`
}

func markup(kb Keyboard) map[string]any {
	return map[string]any{"inline_keyboard": kb}
}

func (c *client) sendText(ctx context.Context, chat int64, text string, kb Keyboard) (int64, error) {
	p := map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML", "link_preview_options": map[string]any{"is_disabled": true}}
	if kb != nil {
		p["reply_markup"] = markup(kb)
	}
	var m tgMessage
	err := c.call(ctx, "sendMessage", p, &m)
	if err != nil && kb != nil && styleRejected(err) {
		p["reply_markup"] = markup(kb.withoutStyle())
		err = c.call(ctx, "sendMessage", p, &m)
	}
	return m.MessageID, err
}

var richOff struct {
	sync.Mutex
	until time.Time
}

func richDisabled() bool {
	richOff.Lock()
	defer richOff.Unlock()
	return time.Now().Before(richOff.until)
}

func richFailed(err error) {
	var te *tgError
	if errors.As(err, &te) && te.code == 404 {
		richOff.Lock()
		richOff.until = time.Now().Add(time.Hour)
		richOff.Unlock()
	}
}

func (c *client) sendCard(ctx context.Context, chat int64, m tmsg, kb Keyboard) (int64, error) {
	if m.Rich != "" && !richDisabled() {
		p := map[string]any{"chat_id": chat, "rich_message": map[string]any{"html": m.Rich}}
		if kb != nil {
			p["reply_markup"] = markup(kb)
		}
		var out tgMessage
		err := c.call(ctx, "sendRichMessage", p, &out)
		if err != nil && kb != nil && styleRejected(err) {
			p["reply_markup"] = markup(kb.withoutStyle())
			err = c.call(ctx, "sendRichMessage", p, &out)
		}
		if err == nil {
			return out.MessageID, nil
		}
		if ctx.Err() != nil {
			return 0, err
		}
		var te *tgError
		if !errors.As(err, &te) {
			return 0, err
		}
		richFailed(err)
	}
	parts := splitMessage(m.Plain)
	var id int64
	var err error
	for i, part := range parts {
		var k Keyboard
		if i == len(parts)-1 {
			k = kb
		}
		id, err = c.sendText(ctx, chat, part, k)
	}
	return id, err
}

func (c *client) deleteMsg(ctx context.Context, chat, id int64) {
	if id > 0 {
		_ = c.call(ctx, "deleteMessage", map[string]any{"chat_id": chat, "message_id": id}, nil)
	}
}

func (c *client) answer(ctx context.Context, id, text string) {
	p := map[string]any{"callback_query_id": id}
	if text != "" {
		p["text"] = text
	}
	_ = c.call(ctx, "answerCallbackQuery", p, nil)
}

func (c *client) typing(ctx context.Context, chat int64, action string) {
	_ = c.call(ctx, "sendChatAction", map[string]any{"chat_id": chat, "action": action}, nil)
}

func (c *client) sendDocument(ctx context.Context, chat int64, name string, data []byte, caption string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", strconv.FormatInt(chat, 10))
	if caption != "" {
		_ = w.WriteField("caption", caption)
		_ = w.WriteField("parse_mode", "HTML")
	}
	fw, err := w.CreateFormFile("document", name)
	if err != nil {
		return err
	}
	fw.Write(data)
	if err := w.Close(); err != nil {
		return err
	}
	body := buf.Bytes()
	ct := w.FormDataContentType()
	return c.do(func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", apiBase+"/bot"+c.token+"/sendDocument", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", ct)
		}
		return req, err
	}, nil)
}

func (c *client) download(ctx context.Context, fileID string, max int64) ([]byte, error) {
	var f struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, err
	}
	if f.Size > max {
		return nil, fmt.Errorf("file is too big")
	}
	var data []byte
	err := c.do(func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, "GET", apiBase+"/file/bot"+c.token+"/"+f.Path, nil)
	}, rawOut{&data, max})
	return data, err
}

type rawOut struct {
	b   *[]byte
	max int64
}

func splitMessage(s string) []string {
	const max = 4000
	var out []string
	for len(s) > max {
		i := strings.LastIndex(s[:max], "\n")
		if i <= 0 {
			i = max
		}
		out = append(out, s[:i])
		s = s[i:]
	}
	return append(out, s)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
