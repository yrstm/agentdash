package render

import (
	"fmt"
	"strings"

	"github.com/yrstm/agentdash/internal/drift"
)

// DriftFindings renders the list of drift findings as a human-readable report.
func DriftFindings(findings []drift.Finding, project string, t Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%sDRIFT%s: config vs observed agent behaviour for %s%s%s\n", t.B, t.N, t.D, shortProj(project), t.N)
	if len(findings) == 0 {
		fmt.Fprintf(&b, "  no drift detected — config appears consistent with observed prompts\n")
		return b.String()
	}
	for _, f := range findings {
		b.WriteString(DriftFindingDetail(f, t))
	}
	fmt.Fprintf(&b, "\n  %s%d finding(s) · uncertain findings (?) are heuristic matches · agentdash drift never edits files%s\n",
		t.D, len(findings), t.N)
	return b.String()
}

// DriftFindingDetail renders a single drift finding with evidence.
func DriftFindingDetail(f drift.Finding, t Theme) string {
	var b strings.Builder
	bullet := t.Y
	if f.Kind == "stale_rule" {
		bullet = t.R
	}
	uncertain := ""
	if f.Uncertain {
		uncertain = "?"
	}
	fmt.Fprintf(&b, "\n  %s[%s]%s %s%s%s%s\n",
		bullet, f.Kind, t.N,
		t.B, f.Phrase, t.N, uncertain)
	fmt.Fprintf(&b, "    id: %s", f.ID)
	if f.Count > 1 {
		fmt.Fprintf(&b, " · seen %d time(s)", f.Count)
	}
	if f.WindowS > 0 {
		fmt.Fprintf(&b, " · window %s", agoFromSecs(f.WindowS))
	}
	b.WriteString("\n")
	if f.RuleFile != "" {
		fmt.Fprintf(&b, "    rule: %s:%d\n", shortProj(f.RuleFile), f.RuleLine)
	}
	if len(f.Evidence) > 0 {
		fmt.Fprintf(&b, "    %sevidence:%s\n", t.D, t.N)
		for _, ev := range f.Evidence {
			fmt.Fprintf(&b, "      %s%s%s\n", t.D, truncStr(ev, 100), t.N)
		}
	}
	switch f.Kind {
	case "missing_rule":
		fmt.Fprintf(&b, "    %ssuggest: add this instruction to your CLAUDE.md or .cursor/rules/%s\n", t.D, t.N)
	case "stale_rule":
		fmt.Fprintf(&b, "    %ssuggest: remove or update the path reference in the rule file%s\n", t.D, t.N)
	}
	return b.String()
}
