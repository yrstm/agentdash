// Package du is disk triage for the files the agent CLIs accumulate: a
// size breakdown by category, largest first, with a one-line explanation of
// what each is and whether deleting it is safe. It is read-only and never
// deletes anything — it only prints suggested cleanup commands and points at
// the relevant retention knob. Platform-specific locations (the MCP log cache,
// and the macOS desktop-app dirs) live in the du_linux.go / du_darwin.go
// siblings; everything else is shared.
package du

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// SchemaVersion is the --json contract version for `agentdash du`. Additive
// only, independent of the other commands' versions.
const SchemaVersion = 1

// Item is one file inside a category (used for the projects top-N list).
type Item struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// Category is one accounted-for location.
type Category struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Files   int    `json:"files"`
	Present bool   `json:"present"`
	What    string `json:"what"`              // what it is + whether deleting is safe (+ retention knob)
	Cleanup string `json:"cleanup,omitempty"` // a suggested command; agentdash never runs it
	Top     []Item `json:"top,omitempty"`     // largest files, for the projects category
}

// Result is the completed breakdown, categories sorted largest-first.
type Result struct {
	Categories []Category
	Total      int64
}

// catSpec describes a category to measure. topN>0 collects that many largest
// files (only the projects category uses it). isFile marks a single-file
// category (~/.claude.json) so it is stat'd, not walked.
type catSpec struct {
	name    string
	path    string
	what    string
	cleanup string
	topN    int
	isFile  bool
}

// Collect measures every category and returns them largest-first.
func Collect(home string, now int64) Result {
	specs := []catSpec{
		{
			name: "claude projects", path: filepath.Join(home, ".claude", "projects"),
			what:    "Conversation transcripts, one JSONL per session. Safe to delete sessions you will not resume; Claude Code auto-prunes them after cleanupPeriodDays (default 30). Deleting loses resume and history for those sessions.",
			cleanup: "# lower retention in ~/.claude/settings.json: \"cleanupPeriodDays\": 14",
			topN:    10,
		},
		{
			name: "claude file-history", path: filepath.Join(home, ".claude", "file-history"),
			what:    "Snapshots Claude Code keeps to power in-session file undo. Safe to delete when no session is live; you lose undo history.",
			cleanup: "rm -rf ~/.claude/file-history/*",
		},
		{
			name: "claude shell-snapshots", path: filepath.Join(home, ".claude", "shell-snapshots"),
			what:    "Captured shell-environment snapshots for the Bash tool. Safe to delete when no session is live.",
			cleanup: "rm -rf ~/.claude/shell-snapshots/*",
		},
		{
			name: "claude todos", path: filepath.Join(home, ".claude", "todos"),
			what:    "Per-session todo lists. Safe to delete; only affects recall of old sessions' todos.",
			cleanup: "rm -rf ~/.claude/todos/*",
		},
		{
			name: "claude.json", path: filepath.Join(home, ".claude.json"),
			what:   "Claude Code's main config and state file. Do NOT delete — it holds your settings and project state; trim history from inside the app instead.",
			isFile: true,
		},
		{
			name: "codex sessions", path: filepath.Join(home, ".codex", "sessions"),
			what:    "Codex rollout transcripts, one per session. Safe to delete rollouts you will not resume.",
			cleanup: "find ~/.codex/sessions -name '*.jsonl' -mtime +30",
		},
		{
			name: "codex log", path: filepath.Join(home, ".codex", "log"),
			what:    "Codex debug logs. Safe to delete.",
			cleanup: "rm -rf ~/.codex/log/*",
		},
	}
	specs = append(specs, osCategories(home)...)

	res := Result{}
	for _, s := range specs {
		c := measure(s)
		res.Categories = append(res.Categories, c)
		res.Total += c.Bytes
	}
	sort.SliceStable(res.Categories, func(i, j int) bool {
		return res.Categories[i].Bytes > res.Categories[j].Bytes
	})
	return res
}

// measure sizes one category, walking a directory (or stat'ing a single file).
func measure(s catSpec) Category {
	c := Category{Name: s.name, Path: s.path, What: s.what, Cleanup: s.cleanup}
	info, err := os.Stat(s.path)
	if err != nil {
		return c // absent: Present stays false, Bytes 0
	}
	c.Present = true
	if s.isFile || !info.IsDir() {
		c.Bytes = info.Size()
		c.Files = 1
		return c
	}
	var top []Item
	_ = filepath.WalkDir(s.path, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		c.Bytes += fi.Size()
		c.Files++
		if s.topN > 0 {
			top = append(top, Item{Path: path, Bytes: fi.Size()})
		}
		return nil
	})
	if s.topN > 0 && len(top) > 0 {
		sort.Slice(top, func(i, j int) bool { return top[i].Bytes > top[j].Bytes })
		if len(top) > s.topN {
			top = top[:s.topN]
		}
		c.Top = top
	}
	return c
}

// HumanBytes renders a byte count as a compact IEC size (B, K, M, G).
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + "B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	suffix := []string{"K", "M", "G", "T"}[exp]
	if v >= 100 {
		return strconv.FormatInt(int64(v+0.5), 10) + suffix
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + suffix
}
