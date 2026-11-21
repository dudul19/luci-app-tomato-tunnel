package uci

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

type Section struct {
	Type    string
	Name    string
	Options map[string]string
	Lists   map[string][]string
}

func (s *Section) Get(key, def string) string {
	if v, ok := s.Options[key]; ok {
		return v
	}
	return def
}

func LoadFile(path string) ([]*Section, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

func Parse(r io.Reader) ([]*Section, error) {
	var out []*Section
	var cur *Section
	counts := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	ln := 0
	for sc.Scan() {
		ln++
		words, err := split(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", ln, err)
		}
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "package":
		case "config":
			if len(words) < 2 {
				return nil, fmt.Errorf("line %d: config without type", ln)
			}
			cur = &Section{Type: words[1], Options: map[string]string{}, Lists: map[string][]string{}}
			if len(words) >= 3 && words[2] != "" {
				cur.Name = words[2]
			} else {
				cur.Name = fmt.Sprintf("@%s[%d]", words[1], counts[words[1]])
			}
			counts[words[1]]++
			out = append(out, cur)
		case "option":
			if cur == nil || len(words) < 2 {
				return nil, fmt.Errorf("line %d: option outside section", ln)
			}
			v := ""
			if len(words) >= 3 {
				v = words[2]
			}
			cur.Options[words[1]] = v
		case "list":
			if cur == nil || len(words) < 3 {
				return nil, fmt.Errorf("line %d: bad list", ln)
			}
			cur.Lists[words[1]] = append(cur.Lists[words[1]], words[2])
		default:
			return nil, fmt.Errorf("line %d: unknown keyword %q", ln, words[0])
		}
	}
	return out, sc.Err()
}

func split(line string) ([]string, error) {
	var words []string
	var b strings.Builder
	in := false
	i := 0
	for i < len(line) {
		c := line[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			if in {
				words = append(words, b.String())
				b.Reset()
				in = false
			}
			i++
		case c == '#' && !in:
			return words, nil
		case c == '\'' || c == '"':
			in = true
			q := c
			i++
			closed := false
			for i < len(line) {
				ch := line[i]
				if ch == q {
					closed = true
					i++
					break
				}
				if ch == '\\' && q == '"' && i+1 < len(line) {
					i++
					ch = line[i]
				}
				b.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quote")
			}
		case c == '\\' && i+1 < len(line):
			in = true
			b.WriteByte(line[i+1])
			i += 2
		default:
			in = true
			b.WriteByte(c)
			i++
		}
	}
	if in {
		words = append(words, b.String())
	}
	return words, nil
}
