// Package config inventories the agent-config files that shape behaviour for a
// project: instruction files, rules, hooks, and commands — across all supported
// harnesses — in one place. It is strictly read-only: it reports, never edits.
package config

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Item is one config file or hook entry that shapes agent behaviour.
type Item struct {
	Harness  string `json:"harness"` // claude-code | cursor | codex
	Kind     string `json:"kind"`    // instruction | rule | hook | command | settings
	Scope    string `json:"scope"`   // global | project | parent
	Path     string `json:"path"`    // absolute path to the file
	Summary  string `json:"summary"` // one-liner extracted from the file
	Exists   bool   `json:"exists"`
	Depth    int    `json:"depth,omitempty"` // parent items: dirs above project root
	Bytes    int64  `json:"bytes"`           // file size
	TokenEst int    `json:"token_est"`       // estimated tokens (~ bytes/4); 0 for hooks
	Modified int64  `json:"modified"`        // mtime, epoch seconds (0 if unstattable)
	Tracked  bool   `json:"tracked"`         // tracked in the project's git repo
}

// Result is the full inventory for one project. AlwaysLoadedTokens is the
// estimated token cost of the always-loaded instruction chain (the CLAUDE.md /
// AGENTS.md files that load on every turn), the footer figure for A6.
type Result struct {
	Project            string `json:"project"`
	Items              []Item `json:"items"`
	AlwaysLoadedTokens int    `json:"always_loaded_tokens"`
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

	res := Result{Project: project, Items: items}
	enrich(&res, project)
	return res
}

// enrich fills the per-item size / token-estimate / mtime / git-tracked columns
// and totals the always-loaded instruction tokens. Token cost is estimated as
// bytes/4 (documented as an estimate); hooks carry no token cost of their own.
func enrich(res *Result, project string) {
	tracked := gitTrackedSet(project)
	sizeCache := map[string][2]int{} // path -> {bytes, tokenEst}
	for i := range res.Items {
		it := &res.Items[i]
		if st, err := os.Stat(it.Path); err == nil {
			it.Modified = st.ModTime().Unix()
		}
		it.Tracked = tracked[resolvePath(it.Path)]
		if it.Kind == "hook" {
			continue // a hook is a trigger, not loaded context
		}
		sz, ok := sizeCache[it.Path]
		if !ok {
			b, _ := os.ReadFile(it.Path)
			sz = [2]int{len(b), len(b) / 4}
			sizeCache[it.Path] = sz
		}
		it.Bytes = int64(sz[0])
		it.TokenEst = sz[1]
		if it.Kind == "instruction" {
			res.AlwaysLoadedTokens += it.TokenEst // the chain that loads every turn
		}
	}
}

// gitTrackedSet returns the absolute paths git tracks in the project's repo, in
// one `git ls-files` pass. Empty when the project isn't a git repo (fail-soft);
// files outside the repo (a parent CLAUDE.md, the global one) are simply absent.
func gitTrackedSet(project string) map[string]bool {
	out := map[string]bool{}
	top, err := exec.Command("git", "-C", project, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return out
	}
	root := strings.TrimSpace(string(top))
	files, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return out
	}
	for _, rel := range strings.Split(string(files), "\x00") {
		if rel != "" {
			out[resolvePath(filepath.Join(root, rel))] = true
		}
	}
	return out
}

// resolvePath canonicalizes a path for comparison only — it is used to build
// the tracked-set keys and the lookup key, never to change Item.Path or any
// emitted/rendered path (those stay as-discovered). On macOS `git rev-parse
// --show-toplevel` resolves /var -> /private/var (temp dirs, symlinked homes)
// while a config item's Path keeps the caller's spelling, so both sides must be
// canonicalized at the boundary. For a path that does not exist (a dead-path
// item), EvalSymlinks fails, so canonicalize the nearest existing ancestor and
// rejoin, falling back to a lexical clean — the path is never dropped.
func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	dir, base := filepath.Split(p)
	if dir != "" {
		if r, err := filepath.EvalSymlinks(filepath.Clean(dir)); err == nil {
			return filepath.Join(r, base)
		}
	}
	return filepath.Clean(p)
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
