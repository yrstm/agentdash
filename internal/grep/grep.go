// Package grep is a structured full-text search across the transcripts both
// agents already write (Claude Code's ~/.claude/projects/**/*.jsonl and Codex's
// ~/.codex/sessions/**/*.jsonl). It is read-only, opens each file once, and
// scans newest-first so a bounded -n search stops early instead of reading the
// whole history. One hit is one session, mirroring the History tab's model.
package grep

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/paths"
)

// SchemaVersion is the --json contract version for `agentdash grep`. Additive
// only, independent of the other commands' versions.
const SchemaVersion = 1

// Options configure a search. Pattern is required; the rest narrow it.
type Options struct {
	Home    string
	Pattern *regexp.Regexp
	Role    string // "", "user", or "assistant": which message roles to search
	Project string // substring filter on a session's cwd or repo root
	Since   int64  // epoch cutoff; sessions last active before this are skipped (0 = no cutoff)
	Max     int    // stop after this many matching sessions (0 = no limit)
	Tools   bool   // also search tool-call payloads, not just message text
	Now     int64
}

// Hit is one matching session, richest-signal fields first.
type Hit struct {
	Agent     string `json:"agent"`
	Path      string `json:"path"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Repo      string `json:"repo,omitempty"`
	Title     string `json:"title"`
	AgeSecs   int64  `json:"age_secs"`
	Last      int64  `json:"last"`
	Matches   int    `json:"matches"` // total occurrences across searched messages
	Snippet   string `json:"snippet"`
	Resume    string `json:"resume"`
}

// Result is a completed search.
type Result struct {
	Hits      []Hit
	Roots     []string
	Truncated bool // true when Max stopped the scan before the roots were exhausted
}

type candidate struct {
	path  string
	agent string
	mtime int64
}

// Search runs the query. Files are gathered and sorted newest-first (by mtime,
// cheap), then scanned until Max matching sessions are found.
func Search(opt Options) Result {
	roots := []struct{ kind, dir string }{
		{"claude", filepath.Join(opt.Home, ".claude", "projects")},
		{"codex", filepath.Join(opt.Home, ".codex", "sessions")},
	}
	var res Result
	var cands []candidate
	for _, r := range roots {
		res.Roots = append(res.Roots, r.dir)
		_ = filepath.WalkDir(r.dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			if r.kind == "claude" && isClaudeSubagent(path) {
				return nil // subagent transcripts fold under their parent, not their own row
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			mt := info.ModTime().Unix()
			if opt.Since != 0 && mt < opt.Since {
				return nil // mtime is a cheap lower bound on last activity
			}
			cands = append(cands, candidate{path, r.kind, mt})
			return nil
		})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime > cands[j].mtime })

	for _, c := range cands {
		if opt.Max > 0 && len(res.Hits) >= opt.Max {
			res.Truncated = true
			break
		}
		if h, ok := scan(c, opt); ok {
			res.Hits = append(res.Hits, h)
		}
	}
	return res
}

// scan reads one transcript, searching every in-scope message and collecting
// the session metadata needed to render a hit. Returns ok=false when nothing
// matched (or the file yielded no timestamped records).
func scan(c candidate, opt Options) (Hit, bool) {
	h := Hit{Agent: c.agent, Path: c.path}
	var firstMatch string
	scanLines(c.path, func(line []byte) {
		var msgs []message
		var meta lineMeta
		if c.agent == "claude" {
			msgs, meta = decodeClaude(line, opt.Tools)
		} else {
			msgs, meta = decodeCodex(line, opt.Tools)
		}
		if meta.sessionID != "" {
			h.SessionID = meta.sessionID
		}
		if meta.cwd != "" && h.Cwd == "" {
			h.Cwd = meta.cwd
		}
		if meta.ts != 0 && meta.ts > h.Last {
			h.Last = meta.ts
		}
		for _, m := range msgs {
			if opt.Role != "" && m.role != opt.Role {
				continue
			}
			if h.Title == "" && m.role == "user" {
				if t := parse.Clean(m.text, 100); t != "" {
					h.Title = t
				}
			}
			if n := len(opt.Pattern.FindAllStringIndex(m.text, -1)); n > 0 {
				h.Matches += n
				if firstMatch == "" {
					firstMatch = m.text
				}
			}
		}
	})
	if h.Matches == 0 {
		return Hit{}, false
	}
	if h.SessionID == "" {
		h.SessionID = idFromPath(c.agent, c.path)
	}
	if h.Cwd == "" && c.agent == "claude" {
		h.Cwd = cwdFromClaudePath(c.path)
	}
	if h.Last == 0 {
		if st, err := os.Stat(c.path); err == nil {
			h.Last = st.ModTime().Unix()
		}
	}
	h.Repo = paths.RepoRoot(h.Cwd)
	if opt.Project != "" && !strings.Contains(h.Cwd, opt.Project) && !strings.Contains(h.Repo, opt.Project) {
		return Hit{}, false
	}
	if h.Title == "" {
		h.Title = "(untitled)"
	}
	h.AgeSecs = opt.Now - h.Last
	if h.AgeSecs < 0 {
		h.AgeSecs = 0
	}
	h.Snippet = snippet(firstMatch, opt.Pattern)
	h.Resume = resumeCmd(c.agent, h.Cwd, h.SessionID)
	return h, true
}

// message is one searchable message: its role and its flattened text.
type message struct {
	role string
	text string
}

// lineMeta is the per-line session metadata a scan accumulates.
type lineMeta struct {
	sessionID string
	cwd       string
	ts        int64
}

func snippet(text string, re *regexp.Regexp) string {
	line := strings.Join(strings.Fields(text), " ")
	loc := re.FindStringIndex(line)
	if loc == nil { // matched on original spacing but not collapsed (e.g. \s across a newline)
		return parse.Clean(line, 160)
	}
	rs := []rune(line)
	ri := utf8.RuneCountInString(line[:loc[0]])
	const pad = 60
	start, end := ri-pad, ri+pad
	if start < 0 {
		start = 0
	}
	if end > len(rs) {
		end = len(rs)
	}
	out := string(rs[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(rs) {
		out += "…"
	}
	return out
}

func resumeCmd(agent, cwd, id string) string {
	cd := ""
	if cwd != "" {
		cd = "cd " + cwd + " || exit; "
	}
	if agent == "codex" {
		return cd + "codex resume " + id
	}
	return cd + "claude --resume " + id
}

func isClaudeSubagent(path string) bool {
	return filepath.Base(filepath.Dir(path)) == "subagents" ||
		strings.HasPrefix(filepath.Base(path), "agent-")
}

func cwdFromClaudePath(path string) string {
	dir := filepath.Base(filepath.Dir(path))
	if dir == "" {
		return ""
	}
	return "/" + strings.TrimLeft(strings.ReplaceAll(dir, "-", "/"), "/")
}

var codexIDRe = regexp.MustCompile(`^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-(.+)$`)

func idFromPath(agent, path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if agent == "codex" {
		if m := codexIDRe.FindStringSubmatch(base); m != nil {
			return m[1]
		}
	}
	return base
}

// scanLines streams a JSONL file, handing each non-blank line to fn. Lines
// longer than the buffer are reassembled so a huge pasted entry never splits.
func scanLines(path string, fn func([]byte)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1<<20)
	var overflow []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			overflow = append(overflow, chunk...)
			continue
		}
		line := chunk
		if len(overflow) > 0 {
			overflow = append(overflow, chunk...)
			line = overflow
		}
		if ln := bytes.TrimSpace(line); len(ln) > 0 {
			fn(ln)
		}
		overflow = overflow[:0]
		if err != nil {
			return
		}
	}
}

func iso(ts string) int64 {
	if len(ts) < 19 {
		return 0
	}
	t, err := time.Parse("2006-01-02T15:04:05", ts[:19])
	if err != nil {
		return 0
	}
	return t.Unix()
}
