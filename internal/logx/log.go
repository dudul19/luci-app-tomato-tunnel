package logx

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	Debug = iota
	Info
	Error
)

type Logger struct {
	mu    sync.Mutex
	lines []string
	max   int
	path  string
	level int
	echo  bool
}

func New(path string, max int, echo bool) *Logger {
	return &Logger{path: path, max: max, level: Info, echo: echo}
}

func (l *Logger) SetLevel(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch s {
	case "debug":
		l.level = Debug
	case "error":
		l.level = Error
	default:
		l.level = Info
	}
}

func (l *Logger) IsDebug() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.level == Debug
}

func (l *Logger) logf(level int, tag, format string, a ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	eff := level
	if level >= okLevel {
		eff = Info
	}
	if eff < l.level {
		return
	}
	msg := fmt.Sprintf(format, a...)
	msg = strings.ReplaceAll(msg, "\n", " ")
	mark := ""
	switch level {
	case Error:
		mark = "ERR "
	case Debug:
		mark = "DBG "
	case okLevel:
		mark, level = "OK ", Info
	case warnLevel:
		mark, level = "WRN ", Info
	case msgLevel:
		mark, level = "MSG ", Info
	}
	line := fmt.Sprintf("%s  %-8s %s%s", time.Now().Format("15:04:05"), tag, mark, msg)
	l.lines = append(l.lines, line)
	if len(l.lines) > l.max {
		l.lines = append([]string(nil), l.lines[len(l.lines)-l.max:]...)
	}
	if l.echo {
		fmt.Fprintln(os.Stderr, line)
	}
	if l.path != "" {
		tmp := l.path + ".tmp"
		if err := os.WriteFile(tmp, []byte(strings.Join(l.lines, "\n")+"\n"), 0600); err == nil {
			os.Rename(tmp, l.path)
		}
	}
}

const (
	okLevel   = 10
	warnLevel = 11
	msgLevel  = 12
)

func (l *Logger) Okf(tag, f string, a ...any)   { l.logf(okLevel, tag, f, a...) }
func (l *Logger) Warnf(tag, f string, a ...any) { l.logf(warnLevel, tag, f, a...) }
func (l *Logger) Msgf(tag, f string, a ...any)  { l.logf(msgLevel, tag, f, a...) }

func (l *Logger) Debugf(tag, f string, a ...any) { l.logf(Debug, tag, f, a...) }
func (l *Logger) Infof(tag, f string, a ...any)  { l.logf(Info, tag, f, a...) }
func (l *Logger) Errorf(tag, f string, a ...any) { l.logf(Error, tag, f, a...) }

func (l *Logger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = nil
	if l.path != "" {
		os.WriteFile(l.path, nil, 0600)
	}
}
