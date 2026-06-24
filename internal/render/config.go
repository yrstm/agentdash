package render

import (
	"fmt"
	"strings"

	"github.com/yrstm/agentdash/internal/config"
)

// ConfigInventory renders the config inventory as a human-readable table.
func ConfigInventory(r config.Result, t Theme, treeView bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%sCONFIG%s: agent config files for %s%s%s\n", t.B, t.N, t.D, shortProj(r.Project), t.N)
	if len(r.Items) == 0 {
		fmt.Fprintf(&b, "  no config files found\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  %-12s %-12s %-8s %s\n", "HARNESS", "KIND", "SCOPE", "PATH / SUMMARY")
	for _, it := range r.Items {
		scope := it.Scope
		if it.Depth > 0 {
			scope = fmt.Sprintf("parent+%d", it.Depth)
		}
		path := shortProj(it.Path)
		line := fmt.Sprintf("  %-12s %-12s %-8s %s", it.Harness, it.Kind, scope, path)
		if it.Summary != "" {
			line += fmt.Sprintf("\n%s    %s%s", t.D, it.Summary, t.N)
		}
		b.WriteString(line + "\n")
	}
	total := len(r.Items)
	fmt.Fprintf(&b, "\n  %s%d item(s) total · agentdash config why <file> for provenance%s\n", t.D, total, t.N)
	return b.String()
}

// ConfigWhy explains why a particular file affects the project (scope chain).
func ConfigWhy(r config.Result, filePath string, t Theme) string {
	var b strings.Builder
	var matched []config.Item
	for _, it := range r.Items {
		if it.Path == filePath || shortProj(it.Path) == filePath {
			matched = append(matched, it)
		}
	}
	if len(matched) == 0 {
		fmt.Fprintf(&b, "  %s not found in the config inventory for %s\n", filePath, shortProj(r.Project))
		return b.String()
	}
	for _, it := range matched {
		fmt.Fprintf(&b, "%s%s%s\n\n", t.B, shortProj(it.Path), t.N)
		fmt.Fprintf(&b, "  Harness:  %s\n", it.Harness)
		fmt.Fprintf(&b, "  Kind:     %s\n", it.Kind)
		scope := it.Scope
		if it.Depth > 0 {
			scope = fmt.Sprintf("parent (depth %d above project root)", it.Depth)
		}
		fmt.Fprintf(&b, "  Scope:    %s\n", scope)
		if it.Summary != "" {
			fmt.Fprintf(&b, "  Summary:  %s\n", it.Summary)
		}
		switch it.Scope {
		case "project":
			fmt.Fprintf(&b, "\n  %sApplies to this project only — loaded because the file sits at the project root.%s\n", t.D, t.N)
		case "parent":
			fmt.Fprintf(&b, "\n  %sApplies because the file is in a parent directory (%d level(s) up). Claude Code walks up toward home.%s\n", t.D, it.Depth, t.N)
		case "global":
			fmt.Fprintf(&b, "\n  %sApplies globally — loaded from ~/.claude/CLAUDE.md for every project on this machine.%s\n", t.D, t.N)
		}
	}
	return b.String()
}
