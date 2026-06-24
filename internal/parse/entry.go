// Package parse is the Go port of the embedded Python engine from the v1
// bash script (legacy/agentdash.sh): it locates and folds agent session
// JSONL into per-session entries, incrementally by byte offset.
//
// Parser contract (see CONTRIBUTING.md): an update function folds one
// parsed line into the entry; every field is optional; never fail on a
// weird line, just skip it.
package parse

// ParserV is bumped when parsers extract new fields: forces a one-time
// rescan. It tracks the v1 PARSER_V so a v1 cache survives the upgrade.
const ParserV = 6

// Entry is one session file's accumulated state. The JSON tags match the
// v1 Python cache exactly so ~/.cache/agentdash/usage.json round-trips
// between versions without a rescan.
type Entry struct {
	Kind       string  `json:"kind"`
	Offset     int64   `json:"offset"`
	V          int     `json:"v"`
	Hist       []int64 `json:"hist,omitempty"`
	Mtime      float64 `json:"mtime,omitempty"`
	Seen       float64 `json:"seen,omitempty"`
	Cwd        string  `json:"cwd,omitempty"`
	Model      string  `json:"model,omitempty"`
	In         int64   `json:"in,omitempty"`
	Out        int64   `json:"out,omitempty"`
	Ctx        int64   `json:"ctx,omitempty"`
	Win        int64   `json:"win,omitempty"`
	LastType   string  `json:"last_type,omitempty"`
	LastUserTS int64   `json:"last_user_ts,omitempty"`
	TitleUser  string  `json:"title_user,omitempty"`
	Summary    string  `json:"summary,omitempty"`
	LastText   string  `json:"last_text,omitempty"`
	LastTool   string  `json:"last_tool,omitempty"`
	Activity    string `json:"activity,omitempty"`
	LastMid     string `json:"last_mid,omitempty"`
	CompactionN int    `json:"compaction_n,omitempty"` // number of context-compaction summary entries
}

// updaters maps an agent kind to its line updater. Adding an agent is one
// update function, one locate function, and an entry here.
var updaters = map[string]func(*Entry, []byte){
	"claude": updateClaude,
	"codex":  updateCodex,
}

// Known reports whether kind has a registered parser.
func Known(kind string) bool { _, ok := updaters[kind]; return ok }

// Apply folds one raw JSONL line into ent, failing soft: malformed lines,
// surprising shapes and panics never propagate.
func Apply(kind string, ent *Entry, line []byte) {
	defer func() { _ = recover() }()
	upd, ok := updaters[kind]
	if !ok {
		return
	}
	upd(ent, line)
}
