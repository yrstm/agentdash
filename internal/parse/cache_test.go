package parse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache", "usage.json")
	c := NewCache()
	c.Entries["/s/a.jsonl"] = &Entry{Kind: "claude", Offset: 42, V: ParserV,
		Model: "claude-opus-4-8", In: 1000, Out: 50, Seen: tNow,
		Hist: []int64{0, 10, 20}}
	c.Labels["/s/a.jsonl"] = "the big refactor"
	c.PidMap["123"] = PidInfo{Path: "/s/a.jsonl", Start: 1767220000, Sure: true,
		Cwd: "/work", How: "fd"}
	c.PidsByPath["/s/a.jsonl"] = map[string]float64{"123": tNow}
	c.RecapTS = tNow

	if err := c.Save(path, tNow); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "mode", st.Mode().Perm(), os.FileMode(0o600))

	r := LoadCache(path)
	ent := r.Entries["/s/a.jsonl"]
	if ent == nil {
		t.Fatal("entry lost in round trip")
	}
	eq(t, "Offset", ent.Offset, int64(42))
	eq(t, "Model", ent.Model, "claude-opus-4-8")
	eq(t, "hist", len(ent.Hist), 3)
	eq(t, "label", r.Labels["/s/a.jsonl"], "the big refactor")
	eq(t, "pidmap how", r.PidMap["123"].How, "fd")
	eq(t, "pidmap sure", r.PidMap["123"].Sure, true)
	eq(t, "recap ts", r.RecapTS, float64(tNow))
	eq(t, "pids_by_path", r.PidsByPath["/s/a.jsonl"]["123"], float64(tNow))
}

func TestCacheTTLEviction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	c := NewCache()
	c.Entries["/fresh"] = &Entry{Kind: "claude", V: ParserV, Seen: tNow - 100}
	c.Entries["/stale"] = &Entry{Kind: "claude", V: ParserV, Seen: tNow - 8*86400}
	if err := c.Save(path, tNow); err != nil {
		t.Fatal(err)
	}
	r := LoadCache(path)
	if r.Entries["/fresh"] == nil {
		t.Error("fresh entry evicted")
	}
	if r.Entries["/stale"] != nil {
		t.Error("stale entry survived the 7-day TTL")
	}
}

// A cache written by the v1 Python engine must load as-is: same path, same
// keys, no rescan. This literal mirrors python json.dump output shapes
// (ints for offsets, floats for times).
func TestCacheV1PythonCompat(t *testing.T) {
	v1 := `{"/home/u/.claude/projects/-w/s1.jsonl": {"kind": "claude", "offset": 1234,
	  "v": 3, "model": "claude-opus-4-8", "in": 179000000, "out": 1500000,
	  "ctx": 10500, "last_type": "assistant", "last_user_ts": 1767225600,
	  "title_user": "fix the failing checkout test", "last_mid": "msg_01",
	  "hist": [0, 0, 512], "mtime": 1767225600.5, "seen": 1767226000.0,
	  "cwd": "/w"},
	 "_labels": {"/home/u/.claude/projects/-w/s1.jsonl": "pinned"},
	 "_pidmap": {"9001": {"path": "/home/u/.claude/projects/-w/s1.jsonl",
	   "start": 1767225000, "sure": true, "cwd": "/w", "how": "cwd"}},
	 "_recap_ts": 1767226000.0,
	 "_pids_by_path": {"/home/u/.claude/projects/-w/s1.jsonl": {"9001": 1767226000.0}},
	 "_future_key": {"anything": 1}}`
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}

	c := LoadCache(path)
	ent := c.Entries["/home/u/.claude/projects/-w/s1.jsonl"]
	if ent == nil {
		t.Fatal("v1 entry not loaded")
	}
	eq(t, "offset survives (no rescan)", ent.Offset, int64(1234))
	eq(t, "v", ent.V, 3)
	eq(t, "in", ent.In, int64(179000000))
	eq(t, "mtime", ent.Mtime, 1767225600.5)
	eq(t, "label", c.Labels["/home/u/.claude/projects/-w/s1.jsonl"], "pinned")
	eq(t, "pidmap", c.PidMap["9001"].How, "cwd")

	// unknown underscore keys survive a save
	if err := c.Save(path, tNow); err != nil {
		t.Fatal(err)
	}
	r := LoadCache(path)
	if _, ok := r.extra["_future_key"]; !ok {
		t.Error("unknown special key dropped on save")
	}
	if r.Entries["/home/u/.claude/projects/-w/s1.jsonl"] == nil {
		t.Error("entry dropped on save")
	}
}

func TestCacheCorruptFileFailsSoft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := LoadCache(path)
	if c == nil || len(c.Entries) != 0 {
		t.Fatal("corrupt cache must load as empty, not crash")
	}
	c = LoadCache(filepath.Join(t.TempDir(), "missing.json"))
	if c == nil {
		t.Fatal("missing cache must load as empty")
	}
}
