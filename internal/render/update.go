package render

import (
	"fmt"
	"strings"

	"github.com/yrstm/agentdash/internal/parse"
)

const updateModule = "github.com/yrstm/agentdash/cmd/agentdash@main"

// UpdateArgs is the `go` argv (after the leading "go") that reinstalls agentdash
// with the same build tags as the running binary — so a Hermes build self-updates
// with -tags=hermes and keeps session monitoring. Shared by the staleness hint
// and the `update` subcommand so the two can never drift.
func UpdateArgs() []string {
	args := []string{"install"}
	if t := strings.TrimSpace(updateTags); t != "" {
		args = append(args, t)
	}
	return append(args, updateModule)
}

// UpdateCmd is the human-readable form of UpdateArgs.
func UpdateCmd() string { return "go " + strings.Join(UpdateArgs(), " ") }

// UpdateHint is the no-network staleness nudge shown under the banner: how old
// the running binary is and, once it crosses staleDays, the reinstall command.
// agentdash makes no network calls, so it cannot know whether a newer release
// exists — build age is an honest local proxy, read from the binary's embedded
// VCS stamp. Returns "" when the binary carries no build provenance (ageSecs<0)
// or staleDays<=0 (the nudge is disabled).
func UpdateHint(t Theme, rev string, dirty bool, ageSecs int64, staleDays int) string {
	if ageSecs < 0 || staleDays <= 0 {
		return ""
	}
	stamp := rev
	if stamp == "" {
		stamp = "unknown"
	}
	if dirty {
		stamp += "+"
	}
	out := fmt.Sprintf("  %s▸%s %sbuild %s · %s old%s\n", t.V, t.N, t.D, stamp, parse.Ago(ageSecs), t.N)
	if days := ageSecs / 86400; days >= int64(staleDays) {
		out += fmt.Sprintf("  %s▸%s %s↑ %dd since build — run %sagentdash update%s%s (or %s)%s\n",
			t.V, t.N, t.Y, days, t.B, t.N, t.Y, UpdateCmd(), t.N)
	}
	return out
}
