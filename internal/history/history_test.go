package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectClaudeAndCodex(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude", "projects", "-tmp-work")
	codexDir := filepath.Join(home, ".codex", "sessions", "2026", "06", "16")
	work := filepath.Join(home, "code", "work")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(claudeDir, "s1.jsonl")
	claude := strings.Join([]string{
		`{"type":"mode","sessionId":"s1"}`,
		`{"type":"user","timestamp":"2026-06-16T09:59:00.000Z","message":{"role":"user","content":"<local-command-stdout>Set model to Fable 5</local-command-stdout>"},"cwd":"` + work + `","sessionId":"s1"}`,
		`{"type":"summary","summary":"checkout repair"}`,
		`{"type":"user","timestamp":"2026-06-16T10:00:00.000Z","message":{"role":"user","content":"fix checkout"},"cwd":"` + work + `","sessionId":"s1","gitBranch":"main"}`,
		`{"type":"assistant","timestamp":"2026-06-16T10:02:00.000Z","message":{"id":"m1","model":"claude-opus-4-8","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":7}},"cwd":"` + work + `","sessionId":"s1"}`,
		``,
	}, "\n")
	if err := os.WriteFile(claudePath, []byte(claude), 0o600); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(codexDir, "rollout-2026-06-16T11-00-00-svc.jsonl")
	codex := strings.Join([]string{
		`{"timestamp":"2026-06-16T11:00:00.000Z","type":"session_meta","payload":{"id":"svc","timestamp":"2026-06-16T11:00:00.000Z","cwd":"` + work + `","cli_version":"0.139.0"}}`,
		`{"timestamp":"2026-06-16T11:00:05.000Z","type":"turn_context","payload":{"model":"gpt-5.5"}}`,
		`{"timestamp":"2026-06-16T11:00:06.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>noise</environment_context>"}]}}`,
		`{"timestamp":"2026-06-16T11:00:10.000Z","type":"event_msg","payload":{"type":"user_message","message":"port cron"}}`,
		`{"timestamp":"2026-06-16T11:01:00.000Z","type":"event_msg","payload":{"type":"agent_message","message":"done"}}`,
		`{"timestamp":"2026-06-16T11:01:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":500,"output_tokens":80},"last_token_usage":{"input_tokens":700,"cached_input_tokens":100}}}}`,
		``,
	}, "\n")
	if err := os.WriteFile(codexPath, []byte(codex), 0o600); err != nil {
		t.Fatal(err)
	}

	res := Collect(home, map[string]bool{codexPath: true})
	if len(res.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2: %#v skipped=%#v", len(res.Sessions), res.Sessions, res.Skipped)
	}
	var claudeRow, codexRow Session
	for _, s := range res.Sessions {
		if s.Agent == "claude" {
			claudeRow = s
		}
		if s.Agent == "codex" {
			codexRow = s
		}
	}
	if claudeRow.Cwd != work || claudeRow.Repo != work || claudeRow.Title != "checkout repair" || claudeRow.GitBranch != "main" ||
		claudeRow.Tokens != "0.1k/7" || claudeRow.Ctx != "0.1k" {
		t.Fatalf("bad claude row: %#v", claudeRow)
	}
	if codexRow.Cwd != work || codexRow.Repo != work || codexRow.Title != "port cron" || !codexRow.Live ||
		codexRow.Model != "gpt-5.5" || codexRow.Tokens != "1.5k/0.1k" || codexRow.Ctx != "0.8k" {
		t.Fatalf("bad codex row: %#v", codexRow)
	}
	if !strings.Contains(codexRow.Resume, "codex resume svc") {
		t.Fatalf("bad codex resume: %q", codexRow.Resume)
	}
}

func TestMalformedSessionIsSkipped(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.jsonl"), []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Collect(home, nil)
	if len(res.Sessions) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("got sessions=%d skipped=%d", len(res.Sessions), len(res.Skipped))
	}
}
