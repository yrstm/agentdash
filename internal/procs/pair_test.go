package procs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrstm/agentdash/internal/parse"
)

// writeClaudeSession stages a claude session file in cwd's project dir whose
// first entry is at firstTS, with the file mtime staged to mtime — both are
// offsets from the test's injected now, never wall-clock reads mid-test.
func writeClaudeSession(t *testing.T, home, cwd string, firstTS, mtime int64, id string) string {
	t.Helper()
	dir := ProjDir(home, cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	iso := time.Unix(firstTS, 0).UTC().Format("2006-01-02T15:04:05") + ".000Z"
	body := `{"type":"user","timestamp":"` + iso + `","message":{"content":"synthetic"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	mt := time.Unix(mtime, 0)
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}
	return path
}

func pairOne(agents []Proc, home string, now int64) map[int]Pairing {
	return PairClaude(agents, home, now, map[string]parse.PidInfo{}, map[string]parse.PidInfo{})
}

// The /clear case: one long-lived process, two session files. The first
// file's timestamp matches the process start, but the process is writing the
// newer file now. start-ts alone pinned the process to the dead file forever
// — a live, working agent rendered as an idle corpse (observed on a devbox:
// an actively-working session shown "idle 8d" with the old file's title).
func TestPairClaudeFollowsAClearedSessionToItsCurrentFile(t *testing.T) {
	home := t.TempDir()
	cwd := "/code/proj"
	now := time.Now().Unix()

	old := writeClaudeSession(t, home, cwd, now-8*86400, now-7*86400, "aaaa0001")
	cur := writeClaudeSession(t, home, cwd, now-2*86400, now-30, "bbbb0002")
	pr := Proc{PID: 42, Kind: "claude", Cwd: cwd, Start: now - 8*86400, Uptime: 8 * 86400}

	got := pairOne([]Proc{pr}, home, now)
	p := got[42]
	if p.Path != cur || p.How != "follow" || !p.Sure {
		t.Fatalf("got %+v, want follow to %q (old file %q is dead)", p, cur, old)
	}
}

// A successor gone quiet is not followed: past followFreshS the process
// keeps its start-ts match — both files would display as idle anyway, so
// the pairing stops guessing.
func TestPairClaudeDoesNotFollowAStaleTrail(t *testing.T) {
	home := t.TempDir()
	cwd := "/code/proj"
	now := time.Now().Unix()

	old := writeClaudeSession(t, home, cwd, now-8*86400, now-7*86400, "aaaa0001")
	writeClaudeSession(t, home, cwd, now-2*86400, now-2*3600, "bbbb0002")
	pr := Proc{PID: 42, Kind: "claude", Cwd: cwd, Start: now - 8*86400, Uptime: 8 * 86400}

	got := pairOne([]Proc{pr}, home, now)
	if p := got[42]; p.Path != old || p.How != "start-ts" {
		t.Fatalf("got %+v, want start-ts to %q (stale successor must not be followed)", p, old)
	}
}

// A newer file owned by another live process is that process's session, not
// a continuation: each pairs to its own via start-ts and the follow rule
// stays out of it.
func TestPairClaudeFollowRespectsOtherProcessesSessions(t *testing.T) {
	home := t.TempDir()
	cwd := "/code/proj"
	now := time.Now().Unix()

	fileA := writeClaudeSession(t, home, cwd, now-8*86400, now-40, "aaaa0001")
	fileB := writeClaudeSession(t, home, cwd, now-3600, now-20, "bbbb0002")
	prA := Proc{PID: 41, Kind: "claude", Cwd: cwd, Start: now - 8*86400, Uptime: 8 * 86400}
	prB := Proc{PID: 43, Kind: "claude", Cwd: cwd, Start: now - 3600, Uptime: 3600}

	got := pairOne([]Proc{prA, prB}, home, now)
	if got[43].Path != fileB || got[41].Path != fileA {
		t.Fatalf("want each process on its own session, got 41=%+v 43=%+v", got[41], got[43])
	}
	if got[41].How == "follow" {
		t.Fatalf("process A followed into B's session: %+v", got[41])
	}
}
