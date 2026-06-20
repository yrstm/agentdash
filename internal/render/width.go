package render

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// escEnd returns the index just past the escape sequence starting at s[i]
// (i points at ESC). Handles CSI (ESC [ … final) and the simple two-byte form.
func escEnd(s string, i int) int {
	if i+1 >= len(s) {
		return i + 1
	}
	if s[i+1] == '[' { // CSI: params until a final byte in @–~
		j := i + 2
		for j < len(s) {
			if c := s[j]; c >= 0x40 && c <= 0x7e {
				return j + 1
			}
			j++
		}
		return j
	}
	return i + 2
}

// VisibleWidth is the display width of s in terminal cells, ignoring ANSI escape
// sequences (SGR colors etc.). Replaces ansi.StringWidth — no charm dependency.
func VisibleWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = escEnd(s, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w += runewidth.RuneWidth(r)
		i += size
	}
	return w
}

// ClipANSI truncates s to w display cells, copying ANSI escape sequences through
// verbatim (they cost no width) so colors survive the cut. Replaces
// ansi.Truncate(s, w, ""): no ellipsis, never exceeds w visible cells.
func ClipANSI(s string, w int) string {
	if VisibleWidth(s) <= w {
		return s
	}
	var b strings.Builder
	width := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := escEnd(s, i)
			b.WriteString(s[i:j])
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runewidth.RuneWidth(r)
		if width+rw > w {
			break
		}
		b.WriteRune(r)
		width += rw
		i += size
	}
	return b.String()
}

// Display-width helpers: pad and truncate by terminal cells, not bytes,
// so ×, … and CJK glyphs keep the columns aligned. go-runewidth is the one
// piece of the old TUI stack kept on purpose — Unicode alignment is easy to
// get subtly wrong by hand.

// Pad right-pads s to w display cells.
func Pad(s string, w int) string {
	n := runewidth.StringWidth(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

// Trunc hard-truncates to w display cells with an ellipsis.
func Trunc(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w-1, "") + "…"
}

// CleanTask tidies a task for display only (the raw task stays in --json):
// collapses scraped whitespace into single spaces and shortens an unpaired
// row's marker to a tight "(no session)".
func CleanTask(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if strings.Contains(s, "(no session found)") {
		return "(no session)"
	}
	return s
}

// FishPath abbreviates leading path segments fish-style, keeping the
// tail: ~/code/checkout-api -> ~/c/checkout-api; over-width paths keep
// their end.
func FishPath(p, home string, w int) string {
	if home != "" && strings.HasPrefix(p, home) {
		p = "~" + strings.TrimPrefix(p, home)
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		head, last := p[:i], p[i+1:]
		var b strings.Builder
		for _, seg := range strings.Split(head, "/") {
			if seg != "" {
				b.WriteString(string([]rune(seg)[:1]))
			}
			b.WriteByte('/')
		}
		p = b.String() + last
	}
	r := []rune(p)
	if len(r) > w {
		return "…" + string(r[len(r)-(w-1):])
	}
	return p
}

// FmtUp compacts an uptime: 42m / 16h / 1d6h.
func FmtUp(s int64) string {
	var u string
	switch {
	case s < 60:
		u = itoa(s) + "s"
	case s < 3600:
		u = itoa(s/60) + "m"
	case s < 86400:
		u = itoa(s/3600) + "h"
	default:
		u = itoa(s/86400) + "d" + itoa(s%86400/3600) + "h"
		if len(u) > 5 {
			u = itoa(s/86400) + "d"
		}
	}
	return u
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
