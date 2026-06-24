// Package config inventories the agent-config files that shape behaviour for a
// project: instruction files, rules, hooks, and commands — across all supported
// harnesses — in one place. It is strictly read-only: it reports, never edits.
package config

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Item is one config file or hook entry that shapes agent behaviour.
type Item struct {
	Harness string `json:"harness"` // claude-code | cursor | codex
	Kind    string `json:"kind"`    // instruction | rule | hook | command | settings
	Scope   string `json:"scope"`   // global | project | parent
	Path    string `json:"path"`    // absolute path to the file
	Summary string `json:"summary"` // one-liner extracted from the file
	Exists  bool   `json:"exists"`
	Depth   int    `json:"depth,omitempty"` // parent items: dirs above project root
}

// Result is the full inventory for one project.
type Result struct {
	Project string `json:"project"`
	Items   []Item `json:"items"`
}

// Scan collects all config items for a project. project must be an absolute
// path. When includeGlobal is true, user-level (~/.claude, etc.) files are
// included.
func Scan(project, home string, includeGlobal bool) Result {
	var items []Item
	items = append(items, claudeInstructions(project, home, includeGlobal)...)
	if f := filepath.Join(project, "AGENTS.md"); fileExists(f) {
		items = append(items, Item{
			Harness: "codex", Kind: "instruction", Scope: "project",
			Path: f, Summary: firstMeaningfulLine(f), Exists: true,
		})
	}
	items = append(items, cursorRules(project)...)
	items = append(items, claudeHooks(project, home, includeGlobal)...)
	items = append(items, claudeCommands(project, home, includeGlobal)...)
	return Result{Project: project, Items: items}
}

// claudeInstructions walks from project root toward home collecting CLAUDE.md
// files, then optionally adds the global ~/.claude/CLAUDE.md.
func claudeInstructions(project, home string, includeGlobal bool) []Item {
	var items []Item
	depth := 0
	dir := project
	for {
		f := filepath.Join(dir, "CLAUDE.md")
		scope := "project"
		if depth > 0 {
			scope = "parent"
		}
		if fileExists(f) {
			items = append(items, Item{
				Harness: "claude-code", Kind: "instruction", Scope: scope,
				Path: f, Summary: firstMeaningfulLine(f), Exists: true, Depth: depth,
			})
		}
		if dir == home || dir == "/" {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
		depth++
	}
	if includeGlobal {
		f := filepath.Join(home, ".claude", "CLAUDE.md")
		if fileExists(f) {
			items = append(items, Item{
				Harness: "claude-code", Kind: "instruction", Scope: "global",
				Path: f, Summary: firstMeaningfulLine(f), Exists: true,
			})
		}
	}
	return items
}

// cursorRules inventories Cursor rules (.cursor/rules/*.mdc and legacy
// .cursorrules) at the project root.
func cursorRules(project string) []Item {
	var items []Item
	rulesDir := filepath.Join(project, ".cursor", "rules")
	if entries, err := os.ReadDir(rulesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".mdc") {
				continue
			}
			f := filepath.Join(rulesDir, e.Name())
			items = append(items, Item{
				Harness: "cursor", Kind: "rule", Scope: "project",
				Path: f, Summary: mdcDescription(f), Exists: true,
			})
		}
	}
	if f := filepath.Join(project, ".cursorrules"); fileExists(f) {
		items = append(items, Item{
			Harness: "cursor", Kind: "rule", Scope: "project",
			Path: f, Summary: firstMeaningfulLine(f), Exists: true,
		})
	}
	return items
}

// claudeHooks extracts hook entries from Claude Code settings.json files.
func claudeHooks(project, home string, includeGlobal bool) []Item {
	var items []Item
	if includeGlobal {
		items = append(items, hooksFrom(filepath.Join(home, ".claude", "settings.json"), "global")...)
	}
	items = append(items, hooksFrom(filepath.Join(project, ".claude", "settings.json"), "project")...)
	return items
}

// hooksFrom extracts hook items from a settings.json file, fail-soft.
func hooksFrom(path, scope string) []Item {
	if !fileExists(path) {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var cfg struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if json.NewDecoder(f).Decode(&cfg) != nil {
		return nil
	}
	var items []Item
	for event, entries := range cfg.Hooks {
		for _, raw := range entries {
			var entry struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			}
			if json.Unmarshal(raw, &entry) != nil {
				continue
			}
			for _, h := range entry.Hooks {
				cmd := h.Command
				if len([]rune(cmd)) > 80 {
					cmd = string([]rune(cmd)[:79]) + "…"
				}
				summary := event
				if entry.Matcher != "" {
					summary += " [" + entry.Matcher + "]"
				}
				if cmd != "" {
					summary += ": " + cmd
				}
				items = append(items, Item{
					Harness: "claude-code", Kind: "hook", Scope: scope,
					Path: path, Summary: summary, Exists: true,
				})
			}
		}
	}
	return items
}

// claudeCommands lists slash-command .md files from .claude/commands/.
func claudeCommands(project, home string, includeGlobal bool) []Item {
	var items []Item
	if includeGlobal {
		items = append(items, commandsFrom(filepath.Join(home, ".claude", "commands"), "global")...)
	}
	items = append(items, commandsFrom(filepath.Join(project, ".claude", "commands"), "project")...)
	return items
}

func commandsFrom(dir, scope string) []Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var items []Item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		f := filepath.Join(dir, e.Name())
		name := strings.TrimSuffix(e.Name(), ".md")
		summary := "/" + name
		if desc := frontmatterField(f, "description"); desc != "" {
			summary += " — " + desc
		}
		items = append(items, Item{
			Harness: "claude-code", Kind: "command", Scope: scope,
			Path: f, Summary: summary, Exists: true,
		})
	}
	return items
}

// ContentLines returns non-blank lines from a file for drift matching.
func ContentLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// fileExists reports whether path is a regular file.
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// firstMeaningfulLine returns the first non-blank, non-comment, non-heading
// line of a file, capped at 100 chars.
func firstMeaningfulLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "<!--") {
			continue
		}
		line = strings.TrimLeft(line, "# ")
		if r := []rune(line); len(r) > 100 {
			return string(r[:99]) + "…"
		}
		return line
	}
	return ""
}

// mdcDescription reads the description field from a YAML frontmatter block.
func mdcDescription(path string) string {
	if d := frontmatterField(path, "description"); d != "" {
		return d
	}
	return firstMeaningfulLine(path)
}

// frontmatterField extracts a named scalar field from YAML frontmatter.
func frontmatterField(path, field string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	inFM := false
	prefix := field + ":"
	for sc.Scan() {
		line := sc.Text()
		if line == "---" {
			if !inFM {
				inFM = true
				continue
			}
			break
		}
		if inFM && strings.HasPrefix(strings.TrimSpace(line), prefix) {
			val := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), prefix))
			val = strings.Trim(val, `"'`)
			if r := []rune(val); len(r) > 100 {
				return string(r[:99]) + "…"
			}
			return val
		}
	}
	return ""
}
