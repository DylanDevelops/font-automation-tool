package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// status prints progress to stderr. On a terminal, Update redraws one line in
// place; when stderr is redirected, only Line output is printed.
type status struct {
	tty     bool
	pending bool // an Update line is on screen
	start   time.Time
}

func newStatus() *status {
	fi, err := os.Stderr.Stat()
	return &status{
		tty:   err == nil && fi.Mode()&os.ModeCharDevice != 0,
		start: time.Now(),
	}
}

func (s *status) Line(format string, args ...any) {
	s.clear()
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func (s *status) Update(format string, args ...any) {
	if !s.tty {
		return
	}
	line := fmt.Sprintf(format, args...)
	if r := []rune(line); len(r) > 100 {
		line = string(r[:99]) + "…"
	}
	fmt.Fprint(os.Stderr, "\r\033[K"+line)
	s.pending = true
}

func (s *status) clear() {
	if s.pending {
		fmt.Fprint(os.Stderr, "\r\033[K")
		s.pending = false
	}
}

func (s *status) Elapsed() time.Duration {
	return time.Since(s.start).Round(100 * time.Millisecond)
}

func bar(done, total int) string {
	const width = 24
	if total <= 0 {
		total = 1
	}
	filled := width * done / total
	return fmt.Sprintf("[%s%s] %d/%d %3d%%",
		strings.Repeat("█", filled), strings.Repeat("░", width-filled),
		done, total, 100*done/total)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
