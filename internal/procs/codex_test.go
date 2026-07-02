package procs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeRollout drops a codex rollout whose session_meta records cwd, with a
// filename timestamp at the given session-start epoch.
func writeRollout(t *testing.T, sessions, cwd string, start int64, id string) string {
	t.Helper()
	ts := time.Unix(start, 0).UTC().Format("2006-01-02T15-04-05")
	dir := filepath.Join(sessions, "2026", "06", "18")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-"+ts+"-"+id+".jsonl")
	meta := `{"type":"session_meta","payload":{"cwd":"` + cwd + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Reproduces the codex false-positive respawn bug: an old process sharing a
// busy cwd (~) must NOT be paired to the newest unrelated rollout just because
// the cwd matches. Only a process whose start lines up with a rollout pairs,
// and that pairing is reliable. Old, unpaired processes give respawn detection
// nothing to count.
func TestCodexPairsByStartTimeNotCwdAlone(t *testing.T) {
	home := t.TempDir()
	sessions := filepath.Join(home, ".codex", "sessions")
	cwd := home // a busy cwd, like ~
	now := time.Now().Unix()

	fresh := writeRollout(t, sessions, cwd, now-30, "019edb7f") // a new session in ~

	// an old process (~1d21h up) must not inherit the fresh rollout
	oldStart := now - 45*3600
	if path, sure := LocateCodex(home, cwd, oldStart); path != "" || sure {
		t.Fatalf("old proc wrongly paired: path=%q sure=%v, want unpaired", path, sure)
	}

	// a process whose start lines up with the rollout pairs, reliably
	if path, sure := LocateCodex(home, cwd, now-30); path != fresh || !sure {
		t.Fatalf("matching proc not paired: path=%q sure=%v, want %q true", path, sure, fresh)
	}

	// two unrelated old processes in ~ both fail to pair, so respawn detection
	// (which only counts reliable pairings) has nothing to miscount
	rolls := CodexRollouts(home, cwd)
	for _, st := range []int64{now - 45*3600, now - 30*3600} {
		if p, ok := MatchCodex(rolls, st); ok {
			t.Fatalf("old proc start=%d wrongly matched %q", st, p)
		}
	}

	// the rollout still pairs its own session even when an older one exists
	older := writeRollout(t, sessions, cwd, now-30*3600, "00000000")
	if path, sure := LocateCodex(home, cwd, now-30*3600); path != older || !sure {
		t.Fatalf("second session not paired by its own start: path=%q sure=%v", path, sure)
	}
}
