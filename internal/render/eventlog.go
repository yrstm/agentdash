package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/yrstm/agentdash/internal/eventlog"
)

// EventLogSummary renders aggregate stats for the event log.
func EventLogSummary(sum eventlog.Summary, t Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%sMEM%s: agentdash event log\n", t.B, t.N)
	fmt.Fprintf(&b, "  Path:     %s\n", sum.LogPath)
	if sum.SizeBytes > 0 {
		fmt.Fprintf(&b, "  Size:     %.1f KB\n", float64(sum.SizeBytes)/1024)
	} else {
		fmt.Fprintf(&b, "  Size:     empty\n")
	}
	fmt.Fprintf(&b, "  Events:   %d total\n", sum.Total)
	if sum.Projects > 0 {
		fmt.Fprintf(&b, "  Projects: %d\n", sum.Projects)
	}
	if sum.OldestTS != "" {
		fmt.Fprintf(&b, "  Oldest:   %s\n", eventlog.FormatTS(sum.OldestTS))
	}
	if sum.NewestTS != "" {
		fmt.Fprintf(&b, "  Newest:   %s\n", eventlog.FormatTS(sum.NewestTS))
	}
	if len(sum.ByType) > 0 {
		fmt.Fprintf(&b, "\n  %sby type%s\n", t.D, t.N)
		for _, typ := range []string{
			eventlog.TypeSessionSeen,
			eventlog.TypeStatusChange,
			eventlog.TypePromptObserved,
			eventlog.TypeCtxHigh,
			eventlog.TypeRespawn,
		} {
			if n, ok := sum.ByType[typ]; ok {
				fmt.Fprintf(&b, "    %-20s %d\n", typ, n)
			}
		}
		for typ, n := range sum.ByType {
			switch typ {
			case eventlog.TypeSessionSeen, eventlog.TypeStatusChange,
				eventlog.TypePromptObserved, eventlog.TypeCtxHigh, eventlog.TypeRespawn:
				// already printed above
			default:
				fmt.Fprintf(&b, "    %-20s %d\n", typ, n)
			}
		}
	}
	fmt.Fprintf(&b, "\n  %srecording enabled · AGENTDASH_MEM=0 to disable · AGENTDASH_MEM_NO_PROMPTS=1 to omit excerpts%s\n", t.D, t.N)
	return b.String()
}

// EventLogTail renders a list of recent events, one per line.
func EventLogTail(events []eventlog.Event, t Theme) string {
	if len(events) == 0 {
		return "  (no events recorded yet)\n"
	}
	now := time.Now()
	var b strings.Builder
	for _, e := range events {
		age := eventlog.AgeSecs(e.TS, now)
		ageStr := ""
		if age >= 0 {
			ageStr = agoFromSecs(age)
		} else {
			ageStr = e.TS
		}
		color := t.D
		switch e.Type {
		case eventlog.TypeSessionSeen:
			color = t.G
		case eventlog.TypeCtxHigh:
			color = t.Y
		case eventlog.TypeRespawn:
			color = t.R
		case eventlog.TypePromptObserved:
			color = ""
		}
		detail := fmtEventDetail(e)
		fmt.Fprintf(&b, "  %s%-8s%s %-20s %s%s%s\n",
			t.D, ageStr, t.N,
			color+e.Type+t.N,
			t.D, detail, t.N)
	}
	return b.String()
}

func fmtEventDetail(e eventlog.Event) string {
	switch e.Type {
	case eventlog.TypeSessionSeen:
		parts := []string{}
		if e.Agent != "" {
			parts = append(parts, e.Agent)
		}
		if e.Model != "" {
			parts = append(parts, e.Model)
		}
		if e.Cwd != "" {
			parts = append(parts, shortProj(e.Cwd))
		}
		return strings.Join(parts, " · ")
	case eventlog.TypeStatusChange:
		return e.FromStatus + " → " + e.ToStatus + " " + shortProj(e.Cwd)
	case eventlog.TypePromptObserved:
		if e.PromptExcerpt != "" {
			return truncStr(e.PromptExcerpt, 60)
		}
		return shortProj(e.Cwd)
	case eventlog.TypeCtxHigh:
		return eventlog.CtxHighSummary(e) + " " + shortProj(e.Cwd)
	case eventlog.TypeRespawn:
		return fmt.Sprintf("n=%d %s", e.RespawnN, shortProj(e.Cwd))
	default:
		return shortProj(e.Cwd)
	}
}

func agoFromSecs(secs int64) string {
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm", secs/60)
	case secs < 86400:
		return fmt.Sprintf("%dh", secs/3600)
	default:
		return fmt.Sprintf("%dd", secs/86400)
	}
}

func truncStr(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return string(r)
}
