//go:build hermes

package board

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/yrstm/agentdash/internal/hermesdb"
	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/procs"

	_ "modernc.org/sqlite"
)

// mkHermesDB writes a state.db with the given active sessions (all in one cwd).
func mkHermesDB(t *testing.T, path string, ids []string, started []float64, cwd string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, s := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, model TEXT, started_at REAL,
			ended_at REAL, input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER,
			cache_write_tokens INTEGER, reasoning_tokens INTEGER, cwd TEXT, title TEXT)`,
		`CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, role TEXT,
			content TEXT, tool_name TEXT, timestamp REAL)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range ids {
		if _, err := db.Exec(`INSERT INTO sessions(id, source, started_at, cwd, title)
			VALUES (?, 'cli', ?, ?, ?)`, id, started[i], cwd, "title-"+id); err != nil {
			t.Fatal(err)
		}
	}
}

// TestHermesBatchClaimsEachSessionOnce reproduces the duplicate-rows bug: several
// Hermes processes in one cwd with no HERMES_SESSION_ID must not all collapse onto
// the same session. Each session is claimed at most once; the overflow process is
// left unpaired rather than duplicated.
func TestHermesBatchClaimsEachSessionOnce(t *testing.T) {
	home := t.TempDir()
	dbDir := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := "/work/x"
	mkHermesDB(t, filepath.Join(dbDir, "state.db"), []string{"sA", "sB"}, []float64{1000, 1050}, cwd)

	mk := func(pid int, start int64) procs.Proc {
		return procs.Proc{Kind: "hermes", PID: pid, Cwd: cwd, Start: start, Extra: map[string]string{}}
	}
	agents := []procs.Proc{mk(101, 1000), mk(102, 1050), mk(103, 1050)}

	cache := parse.NewCache()
	res := hermesBatch(agents, home, cache, map[string]parse.PidInfo{})

	if len(res) != 2 {
		t.Fatalf("want 2 distinct pairings, got %d: %v", len(res), res)
	}
	a, b := res[101].Path, res[102].Path
	if a == "" || b == "" || a == b {
		t.Fatalf("processes must claim distinct sessions: 101=%q 102=%q", a, b)
	}
	if _, paired := res[103]; paired {
		t.Fatalf("the overflow process must be unpaired, got %q", res[103].Path)
	}
	if cache.Entries[a] == nil || cache.Entries[b] == nil {
		t.Fatal("claimed session entries must be populated in the cache")
	}
}

func TestResumeCmdHermesDefaultAndProfile(t *testing.T) {
	key := hermesdb.Key("/home/dev/.hermes/state.db", "sess_dummy_123")
	got := ResumeCmd(parse.PidInfo{Kind: "hermes", Path: key, Cwd: "/work/dummy"})
	want := "cd /work/dummy && hermes --resume sess_dummy_123"
	if got != want {
		t.Fatalf("default resume = %q, want %q", got, want)
	}

	profileKey := hermesdb.Key("/home/dev/.hermes/profiles/work/state.db", "sess_dummy_456")
	got = ResumeCmd(parse.PidInfo{Kind: "hermes", Path: profileKey, Cwd: "/work/dummy", Profile: "work"})
	want = "cd /work/dummy && hermes -p work --resume sess_dummy_456"
	if got != want {
		t.Fatalf("profile resume = %q, want %q", got, want)
	}
}
