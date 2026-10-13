package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// status prints progress to stderr. On a terminal, Update redraws a single
// line in place; when stderr is redirected, only permanent lines are
// printed so logs stay readable.
type status struct {
	tty     bool
	pending bool // an Update line is on screen and must be cleared first
	start   time.Time
}

func newStatus() *status {
	fi, err := os.Stderr.Stat()
	return &status{
		tty:   err == nil && fi.Mode()&os.ModeCharDevice != 0,
		start: time.Now(),
	}
}

// Line prints a permanent line.
func (s *status) Line(format string, args ...any) {
	s.clear()
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// Update replaces the transient status line. It does nothing when stderr is
// not a terminal.
func (s *status) Update(format string, args ...any) {
	if !s.tty {
		return
	}
	line := fmt.Sprintf(format, args...)
	if r := []rune(line); len(r) > 100 {
		line = string(r[:99]) + "…" // keep it on one line in narrow terminals
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

// Elapsed returns the time since the status was created, rounded for display.
func (s *status) Elapsed() time.Duration {
	return time.Since(s.start).Round(100 * time.Millisecond)
}

// bar renders a fixed-width progress bar with a count and percentage.
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

// truncate shortens s to at most n runes.
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// humanSize formats a byte count as KB/MB.
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
