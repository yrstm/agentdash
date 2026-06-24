//go:build hermes

package hermesdb

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func makeDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			model TEXT,
			started_at REAL NOT NULL,
			ended_at REAL,
			end_reason TEXT,
			message_count INTEGER DEFAULT 0,
			tool_call_count INTEGER DEFAULT 0,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cache_read_tokens INTEGER DEFAULT 0,
			cache_write_tokens INTEGER DEFAULT 0,
			reasoning_tokens INTEGER DEFAULT 0,
			cwd TEXT,
			title TEXT,
			api_call_count INTEGER DEFAULT 0,
			handoff_state TEXT,
			handoff_platform TEXT,
			handoff_error TEXT
		)`,
		`CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT,
			tool_name TEXT,
			timestamp REAL NOT NULL,
			token_count INTEGER DEFAULT 0
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec(`INSERT INTO sessions(id, source, model, started_at, message_count, tool_call_count,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, cwd, title, api_call_count)
		VALUES (?, 'cli', 'dummy-model', 1000, 3, 1, 100000, 7000, 50000, 18000, 2000, ?, 'dummy dashboard review', 4)`,
		"sess_dummy_123", "/work/dummy")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO messages(session_id, role, content, tool_name, timestamp, token_count) VALUES
		('sess_dummy_123', 'user', 'review this dummy plan', '', 1001, 10),
		('sess_dummy_123', 'assistant', 'dummy analysis', 'terminal', 1060, 20)`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveHomeUsesProfileAndHermesHome(t *testing.T) {
	if got := ResolveHome("/home/dev", "", ""); got != "/home/dev/.hermes" {
		t.Fatalf("default home = %q", got)
	}
	if got := ResolveHome("/home/dev", "", "work"); got != "/home/dev/.hermes/profiles/work" {
		t.Fatalf("profile home = %q", got)
	}
	if got := ResolveHome("/home/dev", "/tmp/hermes", "work"); got != "/tmp/hermes" {
		t.Fatalf("HERMES_HOME override = %q", got)
	}
}

func TestFindBySessionIDIsExactAndReadOnly(t *testing.T) {
	path := makeDB(t)
	s, ok := Find(path, Query{SessionID: "sess_dummy_123", Cwd: "/different", Start: 1})
	if !ok {
		t.Fatal("expected exact session id match")
	}
	if s.ID != "sess_dummy_123" || s.How != "session-id" || !s.Sure {
		t.Fatalf("bad pairing: %#v", s)
	}
	if s.Entry.Model != "dummy-model" || s.Entry.In != 168000 || s.Entry.Out != 9000 {
		t.Fatalf("bad entry usage: %#v", s.Entry)
	}
	if s.Entry.Summary != "dummy dashboard review" || s.Entry.LastType != "assistant" || s.Entry.LastTool != "terminal" {
		t.Fatalf("bad entry text/activity: %#v", s.Entry)
	}
}

func TestFindFallsBackToCwdAndStartHeuristic(t *testing.T) {
	path := makeDB(t)
	s, ok := Find(path, Query{Cwd: "/work/dummy", Start: 1008})
	if !ok {
		t.Fatal("expected heuristic match")
	}
	if s.ID != "sess_dummy_123" || s.How != "cwd+start" || s.Sure {
		t.Fatalf("bad heuristic pairing: %#v", s)
	}
}

func TestFindExcludeSkipsClaimedSession(t *testing.T) {
	path := makeDB(t) // one active session: sess_dummy_123, cwd /work/dummy
	if _, ok := Find(path, Query{Cwd: "/work/dummy", Start: 1008}); !ok {
		t.Fatal("expected heuristic match without exclude")
	}
	ex := map[string]bool{"sess_dummy_123": true}
	if s, ok := Find(path, Query{Cwd: "/work/dummy", Start: 1008, Exclude: ex}); ok {
		t.Fatalf("excluded session must not match heuristically, got %q", s.ID)
	}
	if _, ok := Find(path, Query{SessionID: "sess_dummy_123", Exclude: ex}); ok {
		t.Fatal("excluded exact id must not match")
	}
}

func TestKeyRoundTripKeepsDBPathAndSessionID(t *testing.T) {
	key := Key("/tmp/hermes/state.db", "sess_dummy_123")
	db, id, ok := SplitKey(key)
	if !ok || db != "/tmp/hermes/state.db" || id != "sess_dummy_123" || !strings.Contains(key, "#") {
		t.Fatalf("bad key split: key=%q db=%q id=%q ok=%v", key, db, id, ok)
	}
}
