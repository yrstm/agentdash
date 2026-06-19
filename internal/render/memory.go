package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yrstm/agentdash/internal/memory"
	"github.com/yrstm/agentdash/internal/parse"
)

// MemoryBoard renders the cross-project memory-health board, most-stale first.
func MemoryBoard(rows []memory.BoardRow, t Theme) string {
	if len(rows) == 0 {
		return "  no agent memory tracked yet — needs a CLAUDE.md / AGENTS.md in a project with a live session\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%sMEMORY%s: agent memory vs recent work\n", t.B, t.N)
	fmt.Fprintf(&b, "  %-26s %-18s %-6s %-6s %-4s %s\n", "PROJECT", "FILES", "MEM", "WORK", "SRC", "FLAGS")
	for _, r := range rows {
		var flags []string
		if r.Stale {
			flags = append(flags, t.Y+"stale"+t.N)
		}
		if r.Dirty {
			flags = append(flags, t.D+"dirty"+t.N)
		}
		if r.Concurrent {
			flags = append(flags, t.R+"⚠concurrent"+t.N)
		}
		fmt.Fprintf(&b, "  %-26s %-18s %-6s %-6s %-4s %s\n",
			shortProj(r.Project), strings.Join(r.Files, ","),
			ageOr(r.MemAgeS), ageOr(r.WorkAgeS), orDash(r.WorkSrc), strings.Join(flags, " "))
	}
	fmt.Fprintf(&b, "\n  %sMEM = since memory last changed · WORK = since last commit/activity · "+
		"`agentdash memory <repo>` for the change log%s\n", t.D, t.N)
	return b.String()
}

// MemoryLog renders one project's memory change history, newest last.
func MemoryLog(project string, entries []memory.LogEntry, t Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%sMEMORY LOG%s %s%s%s\n", t.B, t.N, t.D, shortProj(project), t.N)
	if len(entries) == 0 {
		fmt.Fprintf(&b, "  no recorded memory changes yet for this project\n")
		return b.String()
	}
	for _, e := range entries {
		when := e.TS
		if ts, err := time.Parse(time.RFC3339, e.TS); err == nil {
			when = ts.Local().Format("2006-01-02 15:04")
		}
		flag := ""
		if e.Sessions >= 2 {
			flag = fmt.Sprintf("  %s⚠ %d sessions%s", t.R, e.Sessions, t.N)
		}
		fmt.Fprintf(&b, "  %s%s%s  %-7s %-12s %s%-17s%s %6s%s\n",
			t.D, when, t.N, e.Kind, filepath.Base(e.Path),
			labelColor(t, e.Label), e.Label, t.N, parse.Hum(e.Bytes), flag)
	}
	fmt.Fprintf(&b, "\n  %snewest last · labels: created/grew/shrunk/same-size-rewrite%s\n", t.D, t.N)
	return b.String()
}

func labelColor(t Theme, label string) string {
	switch label {
	case "created":
		return t.G
	case "grew":
		return t.B
	case "shrunk":
		return t.Y
	default: // same-size-rewrite
		return t.D
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func ageOr(sec int64) string {
	if sec < 0 {
		return "-"
	}
	return parse.Ago(sec)
}

// shortProj abbreviates $HOME to ~ for display.
func shortProj(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}
