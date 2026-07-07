package render

import (
	"fmt"
	"strings"

	"github.com/yrstm/agentdash/internal/drift"
)

// DriftFindings renders the list of drift findings as a human-readable report.
func DriftFindings(findings []drift.Finding, project string, t Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%sAUDIT%s: config vs observed agent behaviour for %s%s%s\n", t.B, t.N, t.D, shortProj(project), t.N)
	if len(findings) == 0 {
		fmt.Fprintf(&b, "  no drift detected — config appears consistent with observed prompts\n")
		return b.String()
	}
	for _, f := range findings {
		b.WriteString(DriftFindingDetail(f, t))
	}
	fmt.Fprintf(&b, "\n  %s%d finding(s) · uncertain findings (?) are heuristic matches · agentdash audit never edits files%s\n",
		t.D, len(findings), t.N)
	return b.String()
}

// DriftFindingDetail renders a single drift finding with evidence.
func DriftFindingDetail(f drift.Finding, t Theme) string {
	var b strings.Builder
	bullet := t.D
	switch f.Severity {
	case "high":
		bullet = t.R
	case "warn":
		bullet = t.Y
	}
	uncertain := ""
	if f.Uncertain {
		uncertain = "?"
	}
	sev := f.Severity
	if sev == "" {
		sev = "info"
	}
	fmt.Fprintf(&b, "\n  %s[%s %s]%s %s%s%s%s\n",
		bullet, sev, f.Kind, t.N,
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
		if f.RuleLine > 0 {
			fmt.Fprintf(&b, "    rule: %s:%d\n", shortProj(f.RuleFile), f.RuleLine)
		} else {
			fmt.Fprintf(&b, "    rule: %s\n", shortProj(f.RuleFile))
		}
	}
	if len(f.Evidence) > 0 {
		fmt.Fprintf(&b, "    %sevidence:%s\n", t.D, t.N)
		for _, ev := range f.Evidence {
			fmt.Fprintf(&b, "      %s%s%s\n", t.D, truncStr(ev, 100), t.N)
		}
	}
	if f.Suggestion != "" {
		fmt.Fprintf(&b, "    %ssuggest: %s%s\n", t.D, f.Suggestion, t.N)
	}
	return b.String()
}
