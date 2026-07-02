// Package filehist renders the change history of a single instruction file
// (a CLAUDE.md / AGENTS.md / rule file): a git-log timeline when the file is
// tracked, or agentdash's own hash/size snapshots when it isn't, with each
// change attributed to the agent session that made it (or to no recorded
// session) by correlating the change time against Edit/Write tool calls in the
// transcripts. Read-only, no network; git is a local exec boundary only.
package filehist

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/yrstm/agentdash/internal/memory"
)

// SchemaVersion is the docs command's bump for the new <file> history document
// (its board/log outputs are unchanged at 1). Additive.
const SchemaVersion = 2

// Change is one recorded change to the file, newest last.
type Change struct {
	TS          int64  `json:"ts"`                // change time (git commit time, or snapshot time), epoch
	Source      string `json:"source"`            // "git" | "snapshot"
	Rev         string `json:"rev,omitempty"`     // short commit hash (git)
	Author      string `json:"author,omitempty"`  // commit author (git)
	Added       int    `json:"added,omitempty"`   // +lines (git)
	Removed     int    `json:"removed,omitempty"` // -lines (git)
	Bytes       int64  `json:"bytes,omitempty"`   // file size (snapshot)
	SHA         string `json:"sha,omitempty"`     // short content hash (snapshot)
	Excerpt     string `json:"excerpt,omitempty"` // first diff hunk (git) or note (snapshot)
	Attribution string `json:"attribution"`       // who made it, correlated from transcripts
}

// Log is a file's complete change history.
type Log struct {
	Path    string   `json:"path"`
	Tracked bool     `json:"tracked"`
	Changes []Change `json:"changes"`
}

// attrWindow is how close (seconds) an Edit/Write must be to a change to
// attribute it to that session.
const attrWindow = 300

// History builds the change log for one file. When git-tracked, it renders the
// git-log timeline; otherwise it renders agentdash's snapshot events for the
// file. Either way each change is attributed from the transcripts.
func History(file, home string, now int64) Log {
	abs, _ := filepath.Abs(file)
	lg := Log{Path: abs}
	edits := scanEdits(home, abs)

	if root, rel, ok := gitTracked(abs); ok {
		lg.Tracked = true
		lg.Changes = gitChanges(root, rel)
	} else {
		lg.Changes = snapshotChanges(abs)
	}
	for i := range lg.Changes {
		lg.Changes[i].Attribution = attribute(lg.Changes[i].TS, edits)
	}
	return lg
}

// gitTracked reports whether file is tracked, returning its repo root and the
// path relative to that root.
func gitTracked(file string) (root, rel string, ok bool) {
	dir := filepath.Dir(file)
	top, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", "", false
	}
	root = strings.TrimSpace(string(top))
	if err := exec.Command("git", "-C", dir, "ls-files", "--error-unmatch", filepath.Base(file)).Run(); err != nil {
		return "", "", false
	}
	if r, err := filepath.Rel(root, resolve(file)); err == nil {
		rel = r
	} else {
		rel = filepath.Base(file)
	}
	return root, rel, true
}

// gitChanges renders `git log --follow` for one tracked file, newest last.
func gitChanges(root, rel string) []Change {
	// unit-separated fields per commit: hash, author, author-time
	out, err := exec.Command("git", "-C", root, "log", "--follow",
		"--format=%H\x1f%an\x1f%at", "--", rel).Output()
	if err != nil {
		return nil
	}
	var changes []Change
	for _, ln := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.Split(ln, "\x1f")
		if len(f) != 3 {
			continue
		}
		ts, _ := strconv.ParseInt(f[2], 10, 64)
		c := Change{TS: ts, Source: "git", Rev: shortRev(f[0]), Author: f[1]}
		c.Added, c.Removed, c.Excerpt = commitDetail(root, f[0], rel)
		changes = append(changes, c)
	}
	// git log is newest-first; present newest last for a timeline read
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].TS < changes[j].TS })
	return changes
}

// commitDetail returns the +/- line counts and the first diff hunk for one
// commit's change to the file.
func commitDetail(root, rev, rel string) (added, removed int, hunk string) {
	out, err := exec.Command("git", "-C", root, "show", rev, "--format=",
		"--numstat", "--unified=1", "--", rel).Output()
	if err != nil {
		return 0, 0, ""
	}
	var hunkLines []string
	inHunk := false
	for _, ln := range strings.Split(string(out), "\n") {
		switch {
		case !inHunk && strings.Count(ln, "\t") >= 2 && (isNum(ln) || strings.HasPrefix(ln, "-\t")):
			f := strings.SplitN(ln, "\t", 3)
			added = atoiOrZero(f[0])
			removed = atoiOrZero(f[1])
		case strings.HasPrefix(ln, "@@"):
			inHunk = true
			hunkLines = append(hunkLines, ln)
		case inHunk:
			if strings.HasPrefix(ln, "diff --git") || len(hunkLines) >= 4 {
				return added, removed, strings.Join(hunkLines, "\n")
			}
			hunkLines = append(hunkLines, ln)
		}
	}
	return added, removed, strings.Join(hunkLines, "\n")
}

// snapshotChanges renders agentdash's own hash/size snapshots for an untracked
// file, newest last. Content is never stored — only size, hash, and the fact
// of a change.
func snapshotChanges(file string) []Change {
	target := resolve(file)
	var out []Change
	var prev *memory.Event
	for _, e := range memory.Load(memory.LogPath()) {
		if resolve(e.Path) != target {
			continue
		}
		note := "content changed"
		if prev == nil {
			note = "first observed"
		}
		out = append(out, Change{
			TS: iso(e.TS), Source: "snapshot", Bytes: e.Bytes, SHA: short(e.SHA256), Excerpt: note,
		})
		ev := e
		prev = &ev
	}
	return out
}

// attribute correlates a change time against Edit/Write tool calls: an edit of
// this file within attrWindow attributes the change to that agent session,
// otherwise it was made outside any recorded agent session (a human or another
// tool).
func attribute(ts int64, edits []edit) string {
	best := -1
	bestDiff := int64(attrWindow) + 1
	for i, e := range edits {
		d := ts - e.ts
		if d < 0 {
			d = -d
		}
		if d <= attrWindow && d < bestDiff {
			best, bestDiff = i, d
		}
	}
	if best < 0 {
		return "outside any recorded agent session (human or other tool)"
	}
	e := edits[best]
	task := e.task
	if task == "" {
		task = e.session
	}
	return "by " + e.agent + " session \"" + task + "\" at " + isoStr(e.ts)
}

func shortRev(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
func short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
func atoiOrZero(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }
func isNum(s string) bool {
	f := strings.SplitN(s, "\t", 2)
	_, err := strconv.Atoi(strings.TrimSpace(f[0]))
	return err == nil
}

// resolve canonicalizes a path for comparison only (§1b.6), with a
// nonexistent-path fallback; it is never used for display.
func resolve(p string) string {
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
