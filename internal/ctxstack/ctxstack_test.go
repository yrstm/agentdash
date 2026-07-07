package ctxstack

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func has(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func TestInventoryAndMCP(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "work", "svc")
	write(t, filepath.Join(cwd, "CLAUDE.md"), "# svc\nUse tabs.\n")
	write(t, filepath.Join(cwd, ".cursor", "rules", "s.mdc"), "---\ndescription: style\n---\nrule body\n")
	write(t, filepath.Join(cwd, ".claude", "settings.json"),
		`{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"./h.sh"}]}]}}`)
	write(t, filepath.Join(cwd, ".mcp.json"), `{"mcpServers":{"fs":{"command":"x"}}}`)
	write(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"global-srv":{}},"projects":{"`+cwd+`":{"mcpServers":{"proj-srv":{}}}}}`)

	chain, hooks, chainTokens, mcp := Inventory(home, cwd)
	if len(chain) < 2 {
		t.Fatalf("chain = %+v, want >=2 (CLAUDE.md + rule)", chain)
	}
	var sawInstr bool
	for _, l := range chain {
		if l.Kind == "instruction" && l.Tokens > 0 {
			sawInstr = true
		}
	}
	if !sawInstr {
		t.Error("no instruction layer with a token estimate")
	}
	if chainTokens <= 0 {
		t.Error("chainTokens should be > 0")
	}
	if len(hooks) != 1 {
		t.Errorf("hooks = %+v, want 1", hooks)
	}
	for _, want := range []string{"fs", "global-srv", "proj-srv"} {
		if !has(mcp, want) {
			t.Errorf("mcp servers %v missing %q", mcp, want)
		}
	}
}

func TestCompactions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	write(t, path,
		`{"type":"user","timestamp":"2026-07-01T10:00:00.000Z","message":{"content":"hi"}}`+"\n"+
			`{"type":"summary","timestamp":"2026-07-01T11:30:00.000Z","summary":"compacted"}`+"\n"+
			`{"type":"assistant","message":{"content":"ok"}}`+"\n"+
			`{"type":"summary","timestamp":"2026-07-01T14:31:00.000Z","summary":"compacted again"}`+"\n")

	got := Compactions(path)
	if len(got) != 2 {
		t.Fatalf("compactions = %v, want 2", got)
	}
	want := time.Date(2026, 7, 1, 14, 31, 0, 0, time.UTC).Unix()
	if got[1] != want {
		t.Errorf("second compaction = %d, want %d", got[1], want)
	}
}
