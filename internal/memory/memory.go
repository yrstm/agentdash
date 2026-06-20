// Package memory is agentdash's local agent-memory sampler. It records when a
// project's known memory files (repo-root CLAUDE.md / AGENTS.md) actually change
// and keeps a long-term, unpruned event log separate from the session cache, so
// you can see memory drift across projects and a per-project change history.
//
// It follows agentdash's rules: local only, no network, no daemon, read-only
// toward project files, and fail-soft on anything malformed or missing. The
// locator scope is deliberately tight — it samples known files opportunistically,
// it never crawls the filesystem for markdown.
package memory

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// knownArtifacts is the v1 locator scope: repo-root files only.
var knownArtifacts = []struct{ name, kind string }{
	{"CLAUDE.md", "claude"},
	{"AGENTS.md", "agents"},
}

// Artifact is a located memory file and its kind.
type Artifact struct {
	Path string
	Kind string
}

// Event is one appended row of memory-log.jsonl: a content change of a memory
// file at a sampled moment. Append-only; never pruned.
type Event struct {
	TS       string `json:"ts"`                 // sample time, RFC3339
	Project  string `json:"project"`            // absolute project root
	Path     string `json:"path"`               // absolute file path
	Kind     string `json:"kind"`               // claude|agents|unknown
	Bytes    int64  `json:"bytes"`              // file size at sample
	SHA256   string `json:"sha256"`             // content hash
	Mtime    string `json:"mtime"`              // file mtime, RFC3339
	Sessions int    `json:"sessions,omitempty"` // live agent sessions on the project then
}

// LogEntry is an Event tagged with its change label (derived, not stored).
type LogEntry struct {
	Event
	Label string
}

// BoardRow is one project's memory health for the cross-project board.
type BoardRow struct {
	Project    string
	Files      []string
	MemAgeS    int64 // since memory last changed; -1 if never logged
	WorkAgeS   int64 // since latest work signal; -1 if unknown
	WorkSrc    string
	Dirty      bool
	Stale      bool
	Concurrent bool
}

// LogPath is ~/.cache/agentdash/memory-log.jsonl (beside usage.json), overridable
// via AGENTDASH_MEMORY_LOG for tests.
func LogPath() string {
	if p := os.Getenv("AGENTDASH_MEMORY_LOG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "agentdash", "memory-log.jsonl")
}

// Locate returns the known memory files at a project root that currently exist.
// v1 is deliberately tight: repo-root CLAUDE.md / AGENTS.md, never a crawl.
func Locate(project string) []Artifact {
	if project == "" {
		return nil
	}
	var out []Artifact
	for _, a := range knownArtifacts {
		p := filepath.Join(project, a.name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			out = append(out, Artifact{Path: p, Kind: a.kind})
		}
	}
	return out
}

// Sample records content changes for the given projects (key = project root,
// value = live agent-session count). It stats each known artifact and only
// hashes when mtime or size moved since the last logged event, appending a row
// only when the content hash actually changes. Read-only, fail-soft.
func Sample(logPath string, projects map[string]int, now time.Time) {
	if len(projects) == 0 {
		return
	}
	last := lastByPath(Load(logPath))
	var add []Event
	for project, sessions := range projects {
		for _, a := range Locate(project) {
			st, err := os.Stat(a.Path)
			if err != nil {
				continue
			}
			mt := st.ModTime().UTC().Format(time.RFC3339)
			prev, seen := last[a.Path]
			if seen && prev.Mtime == mt && prev.Bytes == st.Size() {
				continue // unchanged mtime+size: assume identical, skip the hash
			}
			sha, n, err := hashFile(a.Path)
			if err != nil {
				continue
			}
			if seen && prev.SHA256 == sha {
				continue // touched but identical content: no new event
			}
			add = append(add, Event{
				TS: now.UTC().Format(time.RFC3339), Project: project, Path: a.Path,
				Kind: a.Kind, Bytes: n, SHA256: sha, Mtime: mt, Sessions: sessions,
			})
		}
	}
	appendEvents(logPath, add)
}

// Load reads every event in order, skipping malformed lines.
func Load(logPath string) []Event {
	f, err := os.Open(logPath)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Path != "" {
			out = append(out, e)
		}
	}
	return out
}

// ProjectLog returns one project's events oldest-first, each tagged with its
// change label relative to the prior event for the same path.
func ProjectLog(logPath, project string) []LogEntry {
	prevByPath := map[string]Event{}
	var out []LogEntry
	for _, e := range Load(logPath) {
		if e.Project != project {
			continue
		}
		var prev *Event
		if p, ok := prevByPath[e.Path]; ok {
			prev = &p
		}
		out = append(out, LogEntry{Event: e, Label: LabelFor(prev, e)})
		prevByPath[e.Path] = e
	}
	return out
}

// LabelFor classifies an event against the previous one for the same path. With
// only bytes+hash we keep four honest, mechanical labels. The first event is
// "baseline" (not "created"): agentdash records when it first *observed* a file,
// which is usually not when the file was created.
func LabelFor(prev *Event, cur Event) string {
	switch {
	case prev == nil:
		return "baseline"
	case cur.Bytes > prev.Bytes:
		return "grew"
	case cur.Bytes < prev.Bytes:
		return "shrunk"
	default:
		return "same-size-rewrite"
	}
}

// BuildBoard aggregates per-project memory health. Scope is (live repos with a
// memory file) ∪ (every project already in the log). Rows are ordered by how far
// memory trails recent work (most stale first).
func BuildBoard(logPath string, live map[string]int, now time.Time) []BoardRow {
	events := Load(logPath)
	scope := map[string]bool{}
	for proj := range live {
		if len(Locate(proj)) > 0 {
			scope[proj] = true
		}
	}
	latestMem := map[string]int64{}
	concurrent := map[string]bool{}
	for _, e := range events {
		scope[e.Project] = true
		// "since memory last changed" is the file's own mtime, not when we first
		// sampled it — otherwise a months-old file looks freshly changed on the
		// first run, masking real staleness.
		if t := parseTS(e.Mtime); t > latestMem[e.Project] {
			latestMem[e.Project] = t
		}
		if e.Sessions >= 2 {
			concurrent[e.Project] = true
		}
	}
	var rows []BoardRow
	for proj := range scope {
		var files []string
		for _, a := range Locate(proj) {
			files = append(files, filepath.Base(a.Path))
		}
		sort.Strings(files)
		memAge := int64(-1)
		if t, ok := latestMem[proj]; ok && t > 0 {
			memAge = now.Unix() - t
		}
		workTS, src, dirty := workSignal(proj)
		workAge := int64(-1)
		if workTS > 0 {
			workAge = now.Unix() - workTS
		}
		rows = append(rows, BoardRow{
			Project: proj, Files: files, MemAgeS: memAge, WorkAgeS: workAge,
			WorkSrc: src, Dirty: dirty,
			Stale:      memAge >= 0 && workAge >= 0 && memAge > workAge,
			Concurrent: concurrent[proj],
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return lag(rows[i]) > lag(rows[j]) })
	return rows
}

// lag is how far memory trails recent work; unknowns sink to the bottom.
func lag(r BoardRow) int64 {
	if r.MemAgeS < 0 || r.WorkAgeS < 0 {
		return -1 << 62
	}
	return r.MemAgeS - r.WorkAgeS
}

// workSignal reports the latest "work" timestamp for a project: the last git
// commit plus a dirty-tree flag when it is a git repo, else the newest
// memory-file mtime as a filesystem fallback. Read-only; fails soft to 0.
func workSignal(project string) (ts int64, src string, dirty bool) {
	if t, d, ok := gitSignal(project); ok {
		return t, "git", d
	}
	var newest int64
	for _, a := range Locate(project) {
		if st, err := os.Stat(a.Path); err == nil {
			if m := st.ModTime().Unix(); m > newest {
				newest = m
			}
		}
	}
	if newest > 0 {
		return newest, "fs", false
	}
	return 0, "", false
}

func gitSignal(project string) (ts int64, dirty, ok bool) {
	if _, err := os.Stat(filepath.Join(project, ".git")); err != nil {
		return 0, false, false
	}
	// project is an untrusted directory (a live agent's cwd, or a CLI arg), so
	// harden the git calls: `-c core.fsmonitor=false` stops a malicious repo's
	// config from executing a filesystem-monitor command during `status`, and
	// GIT_OPTIONAL_LOCKS=0 keeps this read-only sampling from writing or locking
	// the repo. CLI `-c` overrides the repo-local config, which is the vector.
	gitCmd := func(args ...string) *exec.Cmd {
		c := exec.Command("git", append([]string{"-c", "core.fsmonitor=false", "-C", project}, args...)...)
		c.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		return c
	}
	out, err := gitCmd("log", "-1", "--format=%ct").Output()
	if err != nil {
		return 0, false, false
	}
	t, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, false, false
	}
	stat, _ := gitCmd("status", "--porcelain").Output()
	return t, strings.TrimSpace(string(stat)) != "", true
}

func lastByPath(events []Event) map[string]Event {
	m := make(map[string]Event, len(events))
	for _, e := range events {
		m[e.Path] = e // append order is chronological; latest wins
	}
	return m
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func appendEvents(logPath string, evs []Event) {
	if len(evs) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, e := range evs {
		_ = enc.Encode(e)
	}
}

func parseTS(s string) int64 {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix()
	}
	return 0
}
