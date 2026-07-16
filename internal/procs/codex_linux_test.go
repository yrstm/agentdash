package procs

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
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

// fdProc stages a fixture process whose fd table holds the given target open.
func fdProc(t *testing.T, root string, pid int, target string) {
	t.Helper()
	fd := filepath.Join(root, strconv.Itoa(pid), "fd")
	if err := os.MkdirAll(fd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(fd, "53")); err != nil {
		t.Fatal(err)
	}
}

// Two `codex resume`s of one session hold the same rollout open. Per-process
// resolution gave both processes the session — two identical clone rows,
// double-counted in the header tallies. The batch pass claims the rollout once
// (newest process wins) and leaves the second attach unpaired: exact fd
// evidence names its session, so it must not fall through to a timestamp
// match either. Linux-only: drives /proc fds via AGENTDASH_PROC_ROOT.
func TestPairCodexClaimsARolloutOnce(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTDASH_PROC_ROOT", root)
	home := t.TempDir()
	sessions := filepath.Join(home, ".codex", "sessions")
	now := time.Now().Unix()
	cwd := "/code/synth"

	shared := writeRollout(t, sessions, cwd, now-7200, "019eshare")
	// decoy: a same-cwd rollout whose timestamp sits within TSSlack of the
	// older attach's start — fd evidence must keep it from being matched
	decoy := writeRollout(t, sessions, cwd, now-3600, "019edecoy")

	fdProc(t, root, 101, shared)
	fdProc(t, root, 102, shared)
	newer := Proc{PID: 101, Kind: "codex", Cwd: cwd, Start: now - 600, Uptime: 600}
	older := Proc{PID: 102, Kind: "codex", Cwd: cwd, Start: now - 3600, Uptime: 3600}

	got := PairCodex([]Proc{older, newer}, home)
	if p, ok := got[101]; !ok || p.Path != shared || !p.Sure || p.How != "fd" {
		t.Fatalf("newest attach: got %+v, want exact fd pairing to %q", p, shared)
	}
	if p, ok := got[102]; ok {
		t.Fatalf("second attach paired to %q, want unpaired (its session is claimed, decoy %q must not be matched)", p.Path, decoy)
	}
}

// The meta tier claims once too: two processes whose starts both sit within
// TSSlack of a single rollout must not both inherit it. The closer process
// wins via MatchCodex; the other stays unpaired rather than cloning the row.
func TestPairCodexMetaTierClaimsOnce(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTDASH_PROC_ROOT", root) // empty: no fd evidence for anyone
	home := t.TempDir()
	sessions := filepath.Join(home, ".codex", "sessions")
	now := time.Now().Unix()
	cwd := "/code/synth"

	roll := writeRollout(t, sessions, cwd, now-1000, "019eonly")
	near := Proc{PID: 201, Kind: "codex", Cwd: cwd, Start: now - 990, Uptime: 990}
	far := Proc{PID: 202, Kind: "codex", Cwd: cwd, Start: now - 1200, Uptime: 1200}

	got := PairCodex([]Proc{far, near}, home)
	paired := 0
	for _, p := range got {
		if p.Path == roll {
			paired++
		}
	}
	if paired != 1 {
		t.Fatalf("rollout claimed %d times, want exactly 1 (got %+v)", paired, got)
	}
	if p, ok := got[201]; !ok || p.Path != roll || p.How != "meta" {
		t.Fatalf("newest proc: got %+v, want meta pairing to %q", got[201], roll)
	}

	// two rollouts, two processes: each pairs to its own
	roll2 := writeRollout(t, sessions, cwd, now-1200, "019eother")
	got = PairCodex([]Proc{far, near}, home)
	if got[201].Path != roll || got[202].Path != roll2 {
		t.Fatalf("want each proc on its own rollout, got 201=%+v 202=%+v", got[201], got[202])
	}
}
