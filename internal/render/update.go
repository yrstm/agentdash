package render

import (
	"fmt"
	"strings"

	"github.com/yrstm/agentdash/internal/parse"
)

const updateModule = "github.com/yrstm/agentdash/cmd/agentdash@main"

// UpdateCmd is the reinstall command the staleness nudge prints. It carries the
// running binary's build tags (updateTags), so a Hermes build advertises
// -tags=hermes and a reinstall keeps session monitoring. agentdash never runs
// this itself — it only ever prints it, keeping the binary fully no-network.
func UpdateCmd() string {
	tags := ""
	if t := strings.TrimSpace(updateTags); t != "" {
		tags = " " + t
	}
	return "go install" + tags + " " + updateModule
}

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
		out += fmt.Sprintf("  %s▸%s %s↑ %dd since build — reinstall: %s%s%s\n",
			t.V, t.N, t.Y, days, t.B, UpdateCmd(), t.N)
	}
	return out
}
