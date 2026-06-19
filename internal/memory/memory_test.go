package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocateScopeIsTight(t *testing.T) {
	proj := t.TempDir()
	write(t, filepath.Join(proj, "CLAUDE.md"), "a")
	write(t, filepath.Join(proj, "AGENTS.md"), "b")
	write(t, filepath.Join(proj, "README.md"), "c")           // not memory
	write(t, filepath.Join(proj, "sub", "CLAUDE.md"), "deep") // not repo-root

	got := Locate(proj)
	if len(got) != 2 {
		t.Fatalf("Locate found %d artifacts, want 2 (repo-root CLAUDE.md/AGENTS.md only): %+v", len(got), got)
	}
	kinds := map[string]bool{}
	for _, a := range got {
		kinds[a.Kind] = true
	}
	if !kinds["claude"] || !kinds["agents"] {
		t.Errorf("kinds = %v, want claude+agents", kinds)
	}
}

func TestSampleAppendsOnlyOnContentChange(t *testing.T) {
	proj := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "memory-log.jsonl")
	cm := filepath.Join(proj, "CLAUDE.md")
	write(t, cm, "v1")
	projects := map[string]int{proj: 1}

	// first sample: created
	Sample(logPath, projects, time.Now())
	if n := len(Load(logPath)); n != 1 {
		t.Fatalf("after create: %d events, want 1", n)
	}

	// re-sample with no change: no new row
	Sample(logPath, projects, time.Now())
	if n := len(Load(logPath)); n != 1 {
		t.Fatalf("no-op re-sample appended: %d events, want 1", n)
	}

	// content change (same byte count) must still append — size alone is not enough
	if err := os.WriteFile(cm, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	// bump mtime so the short-circuit doesn't skip the hash
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(cm, future, future)
	Sample(logPath, projects, time.Now())
	if n := len(Load(logPath)); n != 2 {
		t.Fatalf("same-size content change not recorded: %d events, want 2", n)
	}
}

func TestLabels(t *testing.T) {
	base := Event{Bytes: 100, SHA256: "a"}
	cases := []struct {
		prev *Event
		cur  Event
		want string
	}{
		{nil, base, "created"},
		{&Event{Bytes: 50}, Event{Bytes: 100}, "grew"},
		{&Event{Bytes: 100}, Event{Bytes: 50}, "shrunk"},
		{&Event{Bytes: 100, SHA256: "a"}, Event{Bytes: 100, SHA256: "b"}, "same-size-rewrite"},
	}
	for _, c := range cases {
		if got := LabelFor(c.prev, c.cur); got != c.want {
			t.Errorf("LabelFor(%+v, %+v) = %q, want %q", c.prev, c.cur, got, c.want)
		}
	}
}

func TestProjectLogDerivesLabelsPerPath(t *testing.T) {
	proj := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "memory-log.jsonl")
	cm := filepath.Join(proj, "CLAUDE.md")
	projects := map[string]int{proj: 1}

	for i, content := range []string{"a", "aa", "a"} { // created, grew, shrunk
		write(t, cm, content)
		future := time.Now().Add(time.Duration(i+1) * time.Second)
		_ = os.Chtimes(cm, future, future)
		Sample(logPath, projects, future)
	}
	log := ProjectLog(logPath, proj)
	if len(log) != 3 {
		t.Fatalf("got %d log entries, want 3", len(log))
	}
	want := []string{"created", "grew", "shrunk"}
	for i, e := range log {
		if e.Label != want[i] {
			t.Errorf("entry %d label = %q, want %q", i, e.Label, want[i])
		}
	}
}

func TestBuildBoardStaleOrderingAndConcurrency(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "memory-log.jsonl")
	now := time.Now()

	fresh := t.TempDir() // memory newer than work -> not stale
	stale := t.TempDir() // memory older than work -> stale
	write(t, filepath.Join(fresh, "CLAUDE.md"), "x")
	write(t, filepath.Join(stale, "AGENTS.md"), "y")

	// hand-place events: stale's memory change is long ago; fresh's is recent.
	// stale also had a concurrent (2-session) change.
	appendEvents(logPath, []Event{
		{TS: now.Add(-72 * time.Hour).Format(time.RFC3339), Project: stale,
			Path: filepath.Join(stale, "AGENTS.md"), Kind: "agents", Bytes: 1, SHA256: "1",
			Mtime: now.Add(-72 * time.Hour).Format(time.RFC3339), Sessions: 2},
		{TS: now.Add(-1 * time.Minute).Format(time.RFC3339), Project: fresh,
			Path: filepath.Join(fresh, "CLAUDE.md"), Kind: "claude", Bytes: 1, SHA256: "2",
			Mtime: now.Add(-1 * time.Minute).Format(time.RFC3339), Sessions: 1},
	})
	// make the fallback work-signal: touch stale's file recently (fs work newer
	// than its 72h-old memory change), fresh's file old (work older than memory)
	recent := now.Add(-1 * time.Hour)
	old := now.Add(-96 * time.Hour)
	_ = os.Chtimes(filepath.Join(stale, "AGENTS.md"), recent, recent)
	_ = os.Chtimes(filepath.Join(fresh, "CLAUDE.md"), old, old)

	rows := BuildBoard(logPath, map[string]int{}, now)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// most-stale first
	if rows[0].Project != stale {
		t.Errorf("ordering: row0 = %s, want stale project first", rows[0].Project)
	}
	if !rows[0].Stale {
		t.Errorf("stale project not flagged stale: %+v", rows[0])
	}
	if !rows[0].Concurrent {
		t.Errorf("stale project missed concurrent flag")
	}
	if rows[1].Stale {
		t.Errorf("fresh project wrongly flagged stale: %+v", rows[1])
	}
}

func TestLoadFailsSoftOnGarbage(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "memory-log.jsonl")
	good := `{"ts":"2026-06-19T00:00:00Z","project":"/p","path":"/p/CLAUDE.md","kind":"claude","bytes":3,"sha256":"x","mtime":"2026-06-19T00:00:00Z"}`
	write(t, logPath, good+"\nnot json at all\n\n"+good+"\n")
	if n := len(Load(logPath)); n != 2 {
		t.Fatalf("Load returned %d, want 2 (garbage + blank lines skipped)", n)
	}
	// missing file is empty, not an error
	if got := Load(filepath.Join(t.TempDir(), "nope.jsonl")); got != nil {
		t.Errorf("Load(missing) = %v, want nil", got)
	}
}
