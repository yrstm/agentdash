package parse

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestPythonEngineParity runs the v1 embedded Python engine over the same
// fixtures and asserts both parsers produce identical cache entries. This
// is the line-level ground truth for the port; tools/parity.sh covers the
// full-board --json surface.
func TestPythonEngineParity(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	engine := extractEngine(t)

	t.Run("claude", func(t *testing.T) {
		home := t.TempDir()
		proj := filepath.Join(home, ".claude", "projects", "-work-x")
		if err := os.MkdirAll(proj, 0o755); err != nil {
			t.Fatal(err)
		}
		sess := filepath.Join(proj, "s.jsonl")
		copyFile(t, "testdata/claude-golden.jsonl", sess)
		pyEnt := runEngine(t, engine, home, "901\tclaude\t/work/x\t10", sess)
		goEnt := ScanSession(sess, NewCache(), "claude", tNow)
		compareEntries(t, pyEnt, goEnt)
	})

	t.Run("codex", func(t *testing.T) {
		home := t.TempDir()
		day := filepath.Join(home, ".codex", "sessions", "2026", "01", "01")
		if err := os.MkdirAll(day, 0o755); err != nil {
			t.Fatal(err)
		}
		sess := filepath.Join(day, "rollout-2026-01-01T00-00-00-x.jsonl")
		copyFile(t, "testdata/codex-golden.jsonl", sess)
		pyEnt := runEngine(t, engine, home, "902\tcodex\t/work/svc\t10", sess)
		goEnt := ScanSession(sess, NewCache(), "codex", tNow)
		compareEntries(t, pyEnt, goEnt)
	})
}

func extractEngine(t *testing.T) string {
	t.Helper()
	var script string
	for _, p := range []string{"../../agentdash", "../../legacy/agentdash.sh"} {
		if _, err := os.Stat(p); err == nil {
			script = p
			break
		}
	}
	if script == "" {
		t.Skip("v1 script not found")
	}
	f, err := os.Open(script)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // read-only
	var b strings.Builder
	in := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		ln := sc.Text()
		switch {
		case strings.HasSuffix(ln, "3<<'PYEOF'"):
			in = true
		case ln == "PYEOF":
			in = false
		case in:
			b.WriteString(ln + "\n")
		}
	}
	if b.Len() == 0 {
		t.Fatal("failed to extract the python engine")
	}
	out := filepath.Join(t.TempDir(), "engine.py")
	if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

func runEngine(t *testing.T, engine, home, row, sess string) *Entry {
	t.Helper()
	cmd := exec.Command("python3", engine)
	cmd.Env = append(os.Environ(), "HOME="+home, "AGENTDASH_MODE=table")
	cmd.Stdin = strings.NewReader(row + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python engine: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(home, ".cache", "agentdash", "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	var ent Entry
	if err := json.Unmarshal(raw[sess], &ent); err != nil {
		t.Fatalf("no cache entry for %s: %v", sess, err)
	}
	return &ent
}

func compareEntries(t *testing.T, py, gO *Entry) {
	t.Helper()
	// volatile or caller-owned fields: NOW differs per process, cwd is set
	// by the table loop, mtime float precision differs across languages
	for _, e := range []*Entry{py, gO} {
		e.Mtime, e.Seen, e.Cwd = 0, 0, ""
		e.V = 0
		e.LastTool, e.Activity = "", ""
	}
	if !reflect.DeepEqual(py, gO) {
		pj, _ := json.MarshalIndent(py, "", " ")
		gj, _ := json.MarshalIndent(gO, "", " ")
		t.Errorf("python and go entries diverge\npython: %s\ngo:     %s", pj, gj)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
