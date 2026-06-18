package render

import (
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Display-width helpers: pad and truncate by terminal cells, not bytes,
// so ×, … and CJK glyphs keep the columns aligned. go-runewidth ships
// with lipgloss already; no new module.

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
