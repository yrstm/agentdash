package procs

import (
	"os"
	"path/filepath"
	"testing"
)

// A resumed codex session keeps the rollout open; the held fd pairs it exactly
// even though MatchCodex's start-time window would miss it. This is the gap that
// made resumed sessions show as "unmatched". Linux-only: it drives the /proc fd
// reader through AGENTDASH_PROC_ROOT (macOS resolves fds via lsof instead).
func TestCodexFDSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTDASH_PROC_ROOT", root)

	fd := filepath.Join(root, "4242", "fd")
	if err := os.MkdirAll(fd, 0o755); err != nil {
		t.Fatal(err)
	}
	target := "/home/user/.codex/sessions/2026/06/16/rollout-2026-06-16T14-17-41-019ed0cb.jsonl"
	if err := os.Symlink(target, filepath.Join(fd, "53")); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink("/dev/null", filepath.Join(fd, "1")) // unrelated fd, ignored
	if got := CodexFDSession(4242); got != target {
		t.Fatalf("CodexFDSession = %q, want %q", got, target)
	}

	// a non-rollout jsonl elsewhere must not match
	other := filepath.Join(root, "99", "fd")
	_ = os.MkdirAll(other, 0o755)
	_ = os.Symlink("/var/log/agent.jsonl", filepath.Join(other, "3"))
	if got := CodexFDSession(99); got != "" {
		t.Fatalf("CodexFDSession(no rollout) = %q, want empty", got)
	}

	// missing pid dir fails soft
	if got := CodexFDSession(123456); got != "" {
		t.Fatalf("CodexFDSession(missing) = %q, want empty", got)
	}
}
