package parse

// The presentation helpers the v1 Python engine owned (hum, short_model,
// ago, clean, spark_of, title_of): they format engine data and are shared
// by the table renderer and --json. Ported byte for byte; the bats and
// parity suites depend on that.

import (
	"fmt"
	"regexp"
	"strings"
)

const TaskW = 60

// Hum compacts a token count: 68k / 1.2m / 12m.
func Hum(n int64) string {
	switch {
	case n >= 10_000_000:
		return fmt.Sprintf("%.0fm", float64(n)/1e6)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 50:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

var (
	claudePrefix = regexp.MustCompile(`^claude-`)
	oneMTag      = regexp.MustCompile(`\[1m\]`)
	dateSuffix   = regexp.MustCompile(`-20\d{6}$`)
	angleTags    = regexp.MustCompile(`<[^>]{1,40}>`)
)

// ShortModel strips the vendor prefix, [1m] tag and date suffix.
func ShortModel(m string) string {
	if m == "" {
		return "-"
	}
	m = claudePrefix.ReplaceAllString(m, "")
	m = oneMTag.ReplaceAllString(m, "")
	return dateSuffix.ReplaceAllString(m, "")
}

// Ago renders an age in seconds compactly: 42s / 7m / 3h / 2d.
func Ago(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	switch {
	case sec < 60:
		return fmt.Sprintf("%ds", sec)
	case sec < 3600:
		return fmt.Sprintf("%dm", sec/60)
	case sec < 86400:
		return fmt.Sprintf("%dh", sec/3600)
	default:
		return fmt.Sprintf("%dd", sec/86400)
	}
}

// Clean collapses whitespace, strips slash-command style <tags>, and
// truncates to width with an ellipsis; "" means nothing usable.
func Clean(s string, width int) string {
	if s == "" {
		return ""
	}
	if strings.ContainsRune(s, '<') {
		s = angleTags.ReplaceAllString(s, " ")
	}
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > width {
		return string(r[:width-1]) + "…"
	}
	return s
}

var sparkCh = []rune(" ▁▂▃▄▅▆█")

// SparkOf renders the 8-slot bytes-consumed history on a log-ish scale:
// ~256B lights the first level, ~1MB the last.
func SparkOf(hist []int64) string {
	if len(hist) > 8 {
		hist = hist[len(hist)-8:]
	}
	out := make([]rune, 0, 8)
	for i := len(hist); i < 8; i++ {
		out = append(out, sparkCh[0])
	}
	for _, b := range hist {
		lvl := 0
		if b > 0 {
			lvl = bitLen(b)/2 - 3
			if lvl < 1 {
				lvl = 1
			}
			if lvl > 7 {
				lvl = 7
			}
		}
		out = append(out, sparkCh[lvl])
	}
	return string(out)
}

func bitLen(n int64) int {
	l := 0
	for ; n > 0; n >>= 1 {
		l++
	}
	return l
}

// TitleOf picks the row title: pinned label, else session summary, else
// the first prompt.
func TitleOf(ent *Entry, path string, labels map[string]string) string {
	if t := labels[path]; t != "" {
		return Clean(t, TaskW)
	}
	if ent.Summary != "" {
		return Clean(ent.Summary, TaskW)
	}
	return Clean(ent.TitleUser, TaskW)
}

// TaskOf picks the board row text. A pinned label is still explicit user
// intent; otherwise prefer the stable work name over the newest tool call so
// the board says what the session is about.
func TaskOf(ent *Entry, path string, labels map[string]string) string {
	if t := labels[path]; t != "" {
		return Clean(t, TaskW)
	}
	if ent.Summary != "" {
		return Clean(ent.Summary, TaskW)
	}
	if ent.TitleUser != "" {
		return Clean(ent.TitleUser, TaskW)
	}
	return Clean(ent.Activity, TaskW)
}

func usableTitle(s string) bool {
	raw := strings.ToLower(strings.Join(strings.Fields(s), " "))
	for _, p := range []string{
		"<environment_context>",
		"<permissions instructions>",
		"<collaboration_mode>",
		"<skills_instructions>",
		"<local-command-caveat>",
		"<local-command-stdout>",
		"<command-name>",
		"<command-message>",
	} {
		if strings.HasPrefix(raw, p) {
			return false
		}
	}
	s = Clean(s, TaskW)
	if s == "" {
		return false
	}
	l := strings.ToLower(s)
	for _, p := range []string{
		"/clear",
		"/resume",
		"/run",
		"clear",
		"resume",
		"resume previous coding session",
		"claude --resume",
		"you were working on a task before",
	} {
		if strings.HasPrefix(l, p) {
			return false
		}
	}
	return true
}
