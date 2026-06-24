// Package eventlog records structural observations about live agent sessions to
// an append-only, size-capped NDJSON file. It is a substrate for the drift
// detector and for `agentdash mem`. Local only, no network, no daemon.
//
// Recording is enabled by default and disabled with AGENTDASH_MEM=0.
// Prompt excerpts are omitted when AGENTDASH_MEM_NO_PROMPTS=1.
package eventlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const schemaVersion = 1

// DefaultMaxMB is the default size cap for the event log.
const DefaultMaxMB = 16

// Event types.
const (
	TypeSessionSeen    = "session_seen"
	TypeStatusChange   = "status_change"
	TypePromptObserved = "prompt_observed"
	TypeCtxHigh        = "ctx_high"
	TypeRespawn        = "respawn"
)

// Event is one NDJSON record in the event log.
type Event struct {
	SchemaVersion int    `json:"schema_version"`
	TS            string `json:"ts"`             // UTC RFC3339
	Type          string `json:"type"`
	Host          string `json:"host,omitempty"`

	// session_seen
	SessionPath string `json:"session_path,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	PairingTier string `json:"pairing_tier,omitempty"`

	// status_change
	FromStatus string `json:"from_status,omitempty"`
	ToStatus   string `json:"to_status,omitempty"`

	// prompt_observed
	PromptDigest  string `json:"prompt_digest,omitempty"`  // sha256 hex prefix
	PromptExcerpt string `json:"prompt_excerpt,omitempty"` // first 120 chars

	// ctx_high
	CtxPct   int    `json:"ctx_pct,omitempty"`
	CtxModel string `json:"ctx_model,omitempty"`

	// respawn
	RespawnN int `json:"respawn_n,omitempty"`
}

// Summary is aggregate statistics for the event log.
type Summary struct {
	Total     int
	ByType    map[string]int
	OldestTS  string
	NewestTS  string
	Projects  int
	LogPath   string
	SizeBytes int64
}

// LogPath returns the event log path (XDG_STATE_HOME or ~/.local/state).
func LogPath() string {
	if p := os.Getenv("AGENTDASH_EVENTLOG"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "agentdash", "events.ndjson")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "agentdash", "events.ndjson")
}

// Enabled reports whether event recording is active (AGENTDASH_MEM=0 disables).
func Enabled() bool { return os.Getenv("AGENTDASH_MEM") != "0" }

// PromptRecording reports whether prompt excerpts are stored.
func PromptRecording() bool { return os.Getenv("AGENTDASH_MEM_NO_PROMPTS") != "1" }

// maxMBVal reads AGENTDASH_MEM_MAX_MB, defaulting to DefaultMaxMB.
func maxMBVal() float64 {
	if v := os.Getenv("AGENTDASH_MEM_MAX_MB"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxMB
}

// Emit appends events to the default log path, fail-soft.
func Emit(events []Event) {
	if len(events) == 0 || !Enabled() {
		return
	}
	Append(LogPath(), events, maxMBVal())
}

// Append appends events to logPath, rotating when the file exceeds maxMB.
func Append(logPath string, events []Event, maxMB float64) {
	if len(events) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}
	host, _ := os.Hostname()
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range events {
		if events[i].SchemaVersion == 0 {
			events[i].SchemaVersion = schemaVersion
		}
		if events[i].TS == "" {
			events[i].TS = now
		}
		if events[i].Host == "" {
			events[i].Host = host
		}
	}
	rotateCap(logPath, maxMB)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, e := range events {
		_ = enc.Encode(e)
	}
}

// Load reads every event from logPath, skipping malformed lines.
func Load(logPath string) []Event {
	f, err := os.Open(logPath)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Type != "" {
			out = append(out, e)
		}
	}
	return out
}

// Tail returns the last n events from logPath (n ≤ 0 returns all).
func Tail(logPath string, n int) []Event {
	all := Load(logPath)
	if n > 0 && len(all) > n {
		return all[len(all)-n:]
	}
	return all
}

// Summarize computes aggregate statistics for logPath.
func Summarize(logPath string) Summary {
	sum := Summary{LogPath: logPath, ByType: map[string]int{}}
	if st, err := os.Stat(logPath); err == nil {
		sum.SizeBytes = st.Size()
	}
	projects := map[string]bool{}
	for _, e := range Load(logPath) {
		sum.Total++
		sum.ByType[e.Type]++
		if sum.OldestTS == "" {
			sum.OldestTS = e.TS
		}
		sum.NewestTS = e.TS
		if e.Cwd != "" {
			projects[e.Cwd] = true
		}
	}
	sum.Projects = len(projects)
	return sum
}

// AgeSecs returns how many seconds ago a UTC RFC3339 timestamp was.
func AgeSecs(ts string, now time.Time) int64 {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		if d := now.Sub(t); d >= 0 {
			return int64(d.Seconds())
		}
	}
	return -1
}

// FormatTS formats a UTC RFC3339 timestamp in local time.
func FormatTS(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return ts
}

// CtxHighSummary returns a short human summary for a ctx_high event.
func CtxHighSummary(e Event) string {
	return fmt.Sprintf("%s @ %d%%", e.CtxModel, e.CtxPct)
}

// rotateCap drops the oldest quarter of lines when the file exceeds maxMB.
func rotateCap(logPath string, maxMB float64) {
	maxBytes := int64(maxMB * 1024 * 1024)
	st, err := os.Stat(logPath)
	if err != nil || st.Size() <= maxBytes {
		return
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	drop := len(lines) / 4
	if drop < 1 {
		drop = 1
	}
	_ = os.WriteFile(logPath, []byte(strings.Join(lines[drop:], "\n")), 0o600)
}

// MarshalJSON returns a schema_version 1 JSON document for a slice of events.
func MarshalJSON(events []Event) ([]byte, error) {
	doc := struct {
		SchemaVersion int     `json:"schema_version"`
		Events        []Event `json:"events"`
	}{SchemaVersion: 1, Events: events}
	if doc.Events == nil {
		doc.Events = []Event{}
	}
	return json.MarshalIndent(doc, "", "  ")
}

// SummarizeJSON returns a schema_version 1 JSON document for a Summary.
func SummarizeJSON(sum Summary) ([]byte, error) {
	doc := struct {
		SchemaVersion int     `json:"schema_version"`
		Summary       Summary `json:"summary"`
	}{SchemaVersion: 1, Summary: sum}
	return json.MarshalIndent(doc, "", "  ")
}

// per-process in-memory dedup: prevents repeated identical events within one
// agentdash invocation (watch mode refreshes every few seconds).
var (
	emittedMu   sync.Mutex
	emittedOnce = map[string]bool{}
)

// AlreadyEmitted reports whether key was already emitted in this process.
func AlreadyEmitted(key string) bool {
	emittedMu.Lock()
	defer emittedMu.Unlock()
	return emittedOnce[key]
}

// MarkEmitted records key as emitted for this process lifetime.
func MarkEmitted(key string) {
	emittedMu.Lock()
	defer emittedMu.Unlock()
	emittedOnce[key] = true
}
