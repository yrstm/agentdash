package trail

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// CommandsJSON / FilesJSON / SecretsJSON render schema_version 1 documents.
func CommandsJSON(cmds []Command, approvalsOff int) ([]byte, error) {
	if cmds == nil {
		cmds = []Command{}
	}
	return json.MarshalIndent(struct {
		SchemaVersion    int       `json:"schema_version"`
		Kind             string    `json:"kind"`
		UnsafeExecutions int       `json:"unsafe_executions"` // ran with approvals or sandbox off
		Commands         []Command `json:"commands"`
	}{SchemaVersion, "commands", approvalsOff, cmds}, "", "  ")
}

func FilesJSON(files []FileEdit) ([]byte, error) {
	if files == nil {
		files = []FileEdit{}
	}
	return json.MarshalIndent(struct {
		SchemaVersion int        `json:"schema_version"`
		Kind          string     `json:"kind"`
		Files         []FileEdit `json:"files"`
	}{SchemaVersion, "files", files}, "", "  ")
}

func SecretsJSON(secrets []Secret) ([]byte, error) {
	if secrets == nil {
		secrets = []Secret{}
	}
	return json.MarshalIndent(struct {
		SchemaVersion int      `json:"schema_version"`
		Kind          string   `json:"kind"`
		Secrets       []Secret `json:"secrets"`
	}{SchemaVersion, "secrets", secrets}, "", "  ")
}

// CommandsCSV / FilesCSV / SecretsCSV render CSV for spreadsheets/pipelines.
func CommandsCSV(cmds []Command) []byte {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"ts", "agent", "session", "cwd", "approvals_off", "sandbox_off", "command"})
	for _, c := range cmds {
		_ = w.Write([]string{
			strconv.FormatInt(c.TS, 10), c.Agent, c.Session, c.Cwd,
			strconv.FormatBool(c.ApprovalsOff), strconv.FormatBool(c.SandboxOff), c.Command,
		})
	}
	w.Flush()
	return b.Bytes()
}

func FilesCSV(files []FileEdit) []byte {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"ts", "agent", "session", "cwd", "op", "path"})
	for _, f := range files {
		_ = w.Write([]string{strconv.FormatInt(f.TS, 10), f.Agent, f.Session, f.Cwd, f.Op, f.Path})
	}
	w.Flush()
	return b.Bytes()
}

func SecretsCSV(secrets []Secret) []byte {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"ts", "agent", "session", "pattern", "masked"})
	for _, s := range secrets {
		_ = w.Write([]string{strconv.FormatInt(s.TS, 10), s.Agent, s.Session, s.Pattern, s.Masked})
	}
	w.Flush()
	return b.Bytes()
}

// UnsafeCount is the headline for `trail commands`: how many commands ran with
// approvals or the sandbox turned off.
func UnsafeCount(cmds []Command) int {
	n := 0
	for _, c := range cmds {
		if c.ApprovalsOff || c.SandboxOff {
			n++
		}
	}
	return n
}

// Blast is one file a session touched, with whether it currently differs in git.
type Blast struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	Op       string `json:"op"`
	GitDirty bool   `json:"git_dirty"`
}

// BlastFor filters file edits to one session, dedups the file set, and marks
// which of those files currently show as modified in `git status`.
func BlastFor(files []FileEdit, session string) []Blast {
	seen := map[string]Blast{}
	var order []string
	for _, f := range files {
		if f.Session != session && !strings.HasPrefix(f.Session, session) {
			continue
		}
		if _, ok := seen[f.Path]; !ok {
			order = append(order, f.Path)
		}
		seen[f.Path] = Blast{Path: f.Path, Cwd: f.Cwd, Op: f.Op}
	}
	dirty := gitDirtySet(seen)
	out := make([]Blast, 0, len(order))
	for _, p := range order {
		bl := seen[p]
		bl.GitDirty = dirty[p]
		out = append(out, bl)
	}
	return out
}

// gitDirtySet groups the touched files by their cwd's repo and runs one
// `git status --porcelain` per repo, marking which absolute paths are dirty.
func gitDirtySet(files map[string]Blast) map[string]bool {
	byCwd := map[string][]string{}
	for p, bl := range files {
		byCwd[bl.Cwd] = append(byCwd[bl.Cwd], p)
	}
	out := map[string]bool{}
	for cwd := range byCwd {
		if cwd == "" {
			continue
		}
		top, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
		if err != nil {
			continue
		}
		root := strings.TrimSpace(string(top))
		st, err := exec.Command("git", "-C", root, "status", "--porcelain", "-z").Output()
		if err != nil {
			continue
		}
		for _, rec := range strings.Split(string(st), "\x00") {
			if len(rec) < 4 {
				continue
			}
			// porcelain: "XY <path>"
			rel := rec[3:]
			out[filepath.Join(root, rel)] = true
		}
	}
	return out
}
