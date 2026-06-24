//go:build hermes

package hermesdb

import (
	"database/sql"
	"math"
	"path/filepath"
	"strings"

	"github.com/yrstm/agentdash/internal/parse"
	_ "modernc.org/sqlite"
)

const keySep = "#"

// Query describes the process-side evidence agentdash can use to pair a
// Hermes PID to a row in state.db. SessionID is exact when Hermes exposes
// HERMES_SESSION_ID; cwd/start are heuristic fallbacks. Exclude holds session
// ids already claimed by other processes this draw, so two processes that can't
// be told apart don't both collapse onto the same session.
type Query struct {
	SessionID string
	Cwd       string
	Start     int64
	Exclude   map[string]bool
}

// Session is the row data extracted from a Hermes state.db session.
type Session struct {
	ID     string
	Source string
	How    string
	Sure   bool
	Entry  parse.Entry
}

// ResolveHome maps Hermes profile metadata to the state directory. HERMES_HOME
// wins because Hermes itself treats it as the whole active home, not a base for
// profiles.
func ResolveHome(userHome, hermesHome, profile string) string {
	if hermesHome != "" {
		return hermesHome
	}
	base := filepath.Join(userHome, ".hermes")
	if profile != "" && profile != "default" {
		return filepath.Join(base, "profiles", profile)
	}
	return base
}

func StateDB(home string) string { return filepath.Join(home, "state.db") }

func Key(dbPath, sessionID string) string { return dbPath + keySep + sessionID }

func SplitKey(k string) (dbPath, sessionID string, ok bool) {
	db, id, ok := strings.Cut(k, keySep)
	return db, id, ok && db != "" && id != ""
}

// RecentTurns returns compact recent messages for a Hermes session key. It is
// read-only and fails soft for old/non-Hermes keys.
func RecentTurns(key string, n int) [][2]string {
	dbPath, sessionID, ok := SplitKey(key)
	if !ok || n <= 0 {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT role, COALESCE(NULLIF(tool_name, ''), content, '')
		FROM messages WHERE session_id = ? ORDER BY timestamp DESC, id DESC LIMIT ?`, sessionID, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var rev [][2]string
	for rows.Next() {
		var role, text string
		if rows.Scan(&role, &text) == nil {
			rev = append(rev, [2]string{role, parse.Clean(text, 120)})
		}
	}
	out := make([][2]string, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return out
}

// Find opens state.db read-only and returns the best session match. It never
// writes to Hermes-owned files.
func Find(dbPath string, q Query) (Session, bool) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return Session{}, false
	}
	defer db.Close()
	if q.SessionID != "" && !q.Exclude[q.SessionID] {
		if s, ok := loadByID(db, q.SessionID); ok {
			s.How, s.Sure = "session-id", true
			return s, true
		}
	}
	if q.Cwd != "" {
		if s, ok := loadHeuristic(db, q.Cwd, q.Start, true, q.Exclude); ok {
			s.How, s.Sure = "cwd+start", false
			return s, true
		}
	}
	if s, ok := loadHeuristic(db, "", q.Start, false, q.Exclude); ok {
		s.How, s.Sure = "start", false
		return s, true
	}
	return Session{}, false
}

// List returns every readable Hermes session in a state.db, newest first.
func List(dbPath string) []Session {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(sessionSQL + " ORDER BY COALESCE((SELECT MAX(timestamp) FROM messages WHERE session_id = s.id), s.started_at) DESC")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		if s, ok := scanRows(rows); ok {
			out = append(out, s)
		}
	}
	return out
}

func loadByID(db *sql.DB, id string) (Session, bool) {
	return scanSession(db.QueryRow(sessionSQL+" WHERE s.id = ?", id))
}

func loadHeuristic(db *sql.DB, cwd string, start int64, requireCwd bool, exclude map[string]bool) (Session, bool) {
	where := " WHERE s.ended_at IS NULL"
	args := []any{}
	if requireCwd {
		where += " AND s.cwd = ?"
		args = append(args, cwd)
	}
	rows, err := db.Query(sessionSQL+where, args...)
	if err != nil {
		return Session{}, false
	}
	defer rows.Close()
	var best Session
	bestScore := math.MaxFloat64
	for rows.Next() {
		s, ok := scanRows(rows)
		if !ok || exclude[s.ID] {
			continue
		}
		score := math.Abs(float64(start) - s.Entry.Seen)
		if start == 0 {
			score = -s.Entry.Mtime // newest active session
		}
		if score < bestScore {
			bestScore, best = score, s
		}
	}
	return best, best.ID != ""
}

const sessionSQL = `SELECT
	s.id, s.source, COALESCE(s.model, ''), s.started_at,
	COALESCE(s.input_tokens, 0), COALESCE(s.output_tokens, 0),
	COALESCE(s.cache_read_tokens, 0), COALESCE(s.cache_write_tokens, 0),
	COALESCE(s.reasoning_tokens, 0), COALESCE(s.cwd, ''), COALESCE(s.title, ''),
	COALESCE((SELECT MAX(timestamp) FROM messages WHERE session_id = s.id), s.started_at),
	COALESCE((SELECT role FROM messages WHERE session_id = s.id ORDER BY timestamp DESC, id DESC LIMIT 1), ''),
	COALESCE((SELECT content FROM messages WHERE session_id = s.id AND role = 'user' ORDER BY timestamp ASC, id ASC LIMIT 1), ''),
	COALESCE((SELECT content FROM messages WHERE session_id = s.id ORDER BY timestamp DESC, id DESC LIMIT 1), ''),
	COALESCE((SELECT tool_name FROM messages WHERE session_id = s.id AND tool_name IS NOT NULL AND tool_name != '' ORDER BY timestamp DESC, id DESC LIMIT 1), '')
FROM sessions s`

type scanner interface{ Scan(dest ...any) error }

func scanSession(row scanner) (Session, bool) { return scan(row) }
func scanRows(row scanner) (Session, bool)    { return scan(row) }

func scan(row scanner) (Session, bool) {
	var s Session
	var model, cwd, title, role, firstUser, lastText, lastTool string
	var started, last float64
	var inTok, outTok, cacheRead, cacheWrite, reasoning int64
	if err := row.Scan(&s.ID, &s.Source, &model, &started, &inTok, &outTok, &cacheRead,
		&cacheWrite, &reasoning, &cwd, &title, &last, &role, &firstUser, &lastText, &lastTool); err != nil {
		return Session{}, false
	}
	s.Entry = parse.Entry{
		Kind:      "hermes",
		V:         parse.ParserV,
		Seen:      started,
		Mtime:     last,
		Cwd:       cwd,
		Model:     model,
		In:        inTok + cacheRead + cacheWrite,
		Out:       outTok + reasoning,
		Ctx:       inTok + cacheRead + cacheWrite + outTok + reasoning,
		LastType:  role,
		TitleUser: firstUser,
		Summary:   title,
		LastText:  lastText,
		LastTool:  lastTool,
		Activity:  lastTool,
	}
	return s, true
}
