package bot

import (
	"fmt"
	"strings"
)

type tmsg struct {
	Rich  string
	Plain string
}

const cardLine = "────────────────────"

type card struct {
	rich, plain strings.Builder
	kvs         [][2]string
	kvHead      []string
}

func newCard(title string) *card {
	c := &card{}
	fmt.Fprintf(&c.rich, "<p><b>%s</b>%s</p>", htmlEscape(title), widePad(title))
	c.plain.WriteString("<b>" + htmlEscape(title) + "</b>\n" + cardLine + "\n")
	return c
}

const wideCols = 40

func widePad(title string) string {
	n := wideCols - len([]rune(title))
	if n < 0 {
		return ""
	}
	return strings.Repeat("⠀", n)
}

func (c *card) flush() {
	if len(c.kvs) == 0 {
		return
	}
	c.rich.WriteString("<table bordered compact>")
	if len(c.kvHead) == 2 {
		fmt.Fprintf(&c.rich, "<tr><th>%s</th><th>%s</th></tr>", htmlEscape(c.kvHead[0]), htmlEscape(c.kvHead[1]))
		c.kvHead = nil
	}
	for _, kv := range c.kvs {
		fmt.Fprintf(&c.rich, "<tr><td>%s</td><td>%s</td></tr>", htmlEscape(kv[0]), cellCode(kv[1]))
	}
	c.rich.WriteString("</table>")
	c.kvs = nil
}

func cellCode(v string) string {
	if v == "" {
		return "-"
	}
	return "<code>" + htmlEscape(v) + "</code>"
}

func (c *card) kv(k, v string) *card {
	if v != "" {
		c.kvs = append(c.kvs, [2]string{k, v})
		fmt.Fprintf(&c.plain, "%s : <code>%s</code>\n", htmlEscape(k), htmlEscape(v))
	}
	return c
}

func (c *card) head(k, v string) *card {
	c.flush()
	c.kvHead = []string{k, v}
	c.plain.WriteString("\n")
	return c
}

func (c *card) end() *card {
	c.flush()
	return c
}

func (c *card) note(s string) *card {
	c.flush()
	fmt.Fprintf(&c.rich, "<p><i>%s</i></p>", htmlEscape(s))
	c.plain.WriteString("<i>" + htmlEscape(s) + "</i>\n")
	return c
}

func (c *card) text(s string) *card {
	c.flush()
	fmt.Fprintf(&c.rich, "<p>%s</p>", htmlEscape(s))
	c.plain.WriteString(htmlEscape(s) + "\n")
	return c
}

type cell struct {
	Text string
	Code bool
}

func cv(s string) cell { return cell{s, true} }
func ct(s string) cell { return cell{s, false} }

func (c *card) table(head []string, rows [][]cell) *card {
	c.flush()
	if len(rows) == 0 {
		return c
	}
	c.rich.WriteString("<table bordered compact>")
	c.plain.WriteString("\n")
	if len(head) > 0 {
		c.rich.WriteString("<tr>")
		for _, h := range head {
			fmt.Fprintf(&c.rich, "<th>%s</th>", htmlEscape(h))
		}
		c.rich.WriteString("</tr>")
		c.plain.WriteString("<b>" + htmlEscape(strings.Join(head, " · ")) + "</b>\n")
	}
	for _, r := range rows {
		c.rich.WriteString("<tr>")
		var parts []string
		for _, x := range r {
			v := htmlEscape(x.Text)
			if x.Code && x.Text != "" {
				v = "<code>" + v + "</code>"
			} else if x.Text == "" {
				v = "-"
			}
			fmt.Fprintf(&c.rich, "<td>%s</td>", v)
			if x.Text != "" {
				parts = append(parts, v)
			}
		}
		c.rich.WriteString("</tr>")
		c.plain.WriteString(strings.Join(parts, " · ") + "\n")
	}
	c.rich.WriteString("</table>")
	return c
}

func (c *card) pre(label, v string) *card {
	c.flush()
	if v == "" {
		return c
	}
	blk := fmt.Sprintf(`<pre><code class="language-%s">%s</code></pre>`, label, htmlEscape(v))
	c.rich.WriteString(blk)
	c.plain.WriteString(blk + "\n")
	return c
}

func (c *card) msg() tmsg {
	c.flush()
	return tmsg{Rich: c.rich.String(), Plain: strings.TrimRight(c.plain.String(), "\n")}
}
