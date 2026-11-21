package tunnel

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/dudul19/tomato-tunnel/internal/logx"
)

type skipConn struct {
	net.Conn
	br       *bufio.Reader
	enhanced bool
	done     bool
	replies  []string
	err      error
	log      *logx.Logger
}

func newSkipConn(c net.Conn, enhanced bool) *skipConn {
	return &skipConn{Conn: c, br: bufio.NewReaderSize(c, 16*1024), enhanced: enhanced}
}

const maxSkip = 64 * 1024

var errNoBanner = errors.New("no SSH banner from server (payload rejected?)")

func (s *skipConn) prepare() error {
	if s.done {
		return nil
	}
	s.done = true
	skipped := 0
	for {
		head, err := s.br.Peek(5)
		if err != nil {
			if len(s.replies) > 0 {
				return fmt.Errorf("%w · server replied %s", errNoBanner, strings.Join(s.replies, " → "))
			}
			return err
		}
		if bytes.HasPrefix(head, []byte("SSH-")) {
			return nil
		}
		if bytes.Equal(head, []byte("HTTP/")) {
			n, status, err := s.dropHeaders()
			if err != nil {
				return err
			}
			s.replies = append(s.replies, status)
			if s.log != nil {
				if strings.Contains(status, " 101 ") || strings.Contains(status, " 200 ") {
					s.log.Okf("http", "Response: %s", status)
				} else {
					s.log.Warnf("http", "Response: %s", status)
				}
			}
			skipped += n
		} else {
			line, err := s.br.ReadSlice('\n')
			if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
				return err
			}
			skipped += len(line)
		}
		if skipped > maxSkip {
			return fmt.Errorf("%w · server replied %s", errNoBanner, strings.Join(s.replies, " → "))
		}
	}
}

func (s *skipConn) dropHeaders() (int, string, error) {
	total := 0
	status := ""
	for {
		line, err := s.br.ReadString('\n')
		total += len(line)
		if err != nil {
			return total, status, err
		}
		if status == "" {
			status = strings.TrimSpace(line)
		}
		if strings.TrimRight(line, "\r\n") == "" {
			return total, status, nil
		}
		if total > maxSkip {
			return total, status, errNoBanner
		}
	}
}

func (s *skipConn) Read(p []byte) (int, error) {
	if !s.done {
		if err := s.prepare(); err != nil {
			s.err = err
			return 0, err
		}
	}
	return s.br.Read(p)
}
