package drift

import (
	"fmt"
	"strings"
)

// Handoff renders an evidence pack: the findings plus a ready prompt asking the
// user's own agent to apply the fixes. This file is the LLM boundary — agentdash
// writes it and calls nothing; a human decides whether to feed it to an agent.
func Handoff(findings []Finding, project string) string {
	var b strings.Builder
	b.WriteString("# agentdash audit hand-off\n\n")
	fmt.Fprintf(&b, "Project: %s\n\n", shortPath(project))
	if len(findings) == 0 {
		b.WriteString("No findings — nothing to hand off.\n")
		return b.String()
	}
	b.WriteString("## Findings\n\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "### [%s] %s — %s\n\n", strings.ToUpper(f.Severity), f.Kind, f.Phrase)
		if f.RuleFile != "" {
			loc := shortPath(f.RuleFile)
			if f.RuleLine > 0 {
				loc = fmt.Sprintf("%s:%d", loc, f.RuleLine)
			}
			fmt.Fprintf(&b, "- File: `%s`\n", loc)
		}
		for _, e := range f.Evidence {
			fmt.Fprintf(&b, "- Evidence: %s\n", e)
		}
		if f.Suggestion != "" {
			fmt.Fprintf(&b, "- Suggested fix: %s\n", f.Suggestion)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Prompt for your agent\n\n")
	b.WriteString("Apply the suggested fixes above to this project's instruction and\n")
	b.WriteString("configuration files. For each finding: make the smallest edit that\n")
	b.WriteString("resolves it, keep one source of truth per rule, and do not introduce\n")
	b.WriteString("new conflicts. Show me a diff before writing anything. Do not touch\n")
	b.WriteString("files outside this project.\n")
	return b.String()
}
