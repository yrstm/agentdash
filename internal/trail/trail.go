// Package trail reconstructs, from transcripts only, what agents actually did
// on this box: the commands they ran, the files they wrote, and any secrets
// that ended up in the conversation. It is strictly read-only — it never runs a
// command, never writes a transcript, and never prints or persists a full
// secret value (matches are masked). No network.
package trail

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SchemaVersion is the --json contract version for `agentdash trail`.
const SchemaVersion = 1

// Options narrow a scan.
type Options struct {
	Home    string
	Since   int64  // epoch cutoff; records older are skipped (0 = no cutoff)
	Project string // substring filter on a record's cwd
	Now     int64
}

// Command is one shell command an agent ran, per the transcript.
type Command struct {
	TS           int64  `json:"ts"`
	Agent        string `json:"agent"`
	Session      string `json:"session"`
	Cwd          string `json:"cwd"`
	Command      string `json:"command"`
	ApprovalsOff bool   `json:"approvals_off"` // codex: ran with approval_policy=never
	SandboxOff   bool   `json:"sandbox_off"`   // codex: ran with sandbox=danger-full-access
}

// FileEdit is one Edit/Write an agent performed.
type FileEdit struct {
	TS      int64  `json:"ts"`
	Agent   string `json:"agent"`
	Session string `json:"session"`
	Cwd     string `json:"cwd"`
	Op      string `json:"op"` // Edit | Write | MultiEdit | apply_patch
	Path    string `json:"path"`
}

// Secret is one masked high-confidence secret found in a transcript.
type Secret struct {
	TS      int64  `json:"ts"`
	Agent   string `json:"agent"`
	Session string `json:"session"`
	Pattern string `json:"pattern"`
	Masked  string `json:"masked"` // first 4 chars + … — the full value is never stored
}

// perLine is the parsed shape of one transcript line, normalised across agents.
type state struct {
	agent    string
	path     string
	cwd      string
	session  string
	approval string // codex current turn approval_policy
	sandbox  string // codex current turn sandbox_policy
}

// eachTranscript walks both agents' stores. Everything is included, subagents
// too — this is a forensics view, not the board.
func eachTranscript(home string, fn func(agent, path string)) {
	for _, r := range []struct{ kind, dir string }{
		{"claude", filepath.Join(home, ".claude", "projects")},
		{"codex", filepath.Join(home, ".codex", "sessions")},
	} {
		_ = filepath.WalkDir(r.dir, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
				fn(r.kind, path)
			}
			return nil
		})
	}
}

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

func keep(opt Options, ts int64, cwd string) bool {
	if opt.Since != 0 && ts != 0 && ts < opt.Since {
		return false
	}
	if opt.Project != "" && !strings.Contains(cwd, opt.Project) {
		return false
	}
	return true
}

func sessionName(path string) string { return filepath.Base(path) }
