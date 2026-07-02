// Package ctxstack reports the effective instruction stack for a session: what
// is (or would be) loaded for a given cwd — the memory-file chain, hooks, and
// configured MCP servers — each with an estimated token cost, plus the live
// window/context figures and the session's compaction events. Read-only, no
// network. Token costs are estimates (~chars/4) and marked as such; the model
// window and current context are exact when the transcript recorded usage.
package ctxstack

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yrstm/agentdash/internal/config"
)

// SchemaVersion is the --json contract version for `agentdash context`.
const SchemaVersion = 1

// Layer is one thing loaded into the context, with its estimated token cost.
type Layer struct {
	Kind    string `json:"kind"`  // instruction | rule | hook
	Scope   string `json:"scope"` // global | project | parent
	Path    string `json:"path"`
	Summary string `json:"summary,omitempty"`
	Tokens  int    `json:"tokens"` // estimated (~chars/4); 0 for hooks
}

// Stack is the effective instruction stack for a session.
type Stack struct {
	PID          int      `json:"pid"`
	Agent        string   `json:"agent"`
	Cwd          string   `json:"cwd"`
	Model        string   `json:"model"`
	WindowTokens int64    `json:"window_tokens"` // model context window (0 if unknown)
	CtxTokens    int64    `json:"ctx_tokens"`    // current context used (exact, from usage)
	CtxPct       int      `json:"ctx_pct"`
	ChainTokens  int      `json:"chain_tokens"` // estimated always-loaded instruction+rule total
	Chain        []Layer  `json:"chain"`
	Hooks        []Layer  `json:"hooks"`
	MCPServers   []string `json:"mcp_servers"`
	MCPTaxNote   string   `json:"mcp_tax_note"` // MCP tool-schema cost note (usually not measurable)
	Compactions  []int64  `json:"compactions"`  // epoch timestamps of compaction/summary events
}

// Inventory builds the static part of the stack (what config says would load
// for this cwd): the instruction/rule chain and hooks with estimated token
// costs, plus the configured MCP server names. Reuses config.Scan so the token
// estimates match `agentdash inspect`.
func Inventory(home, cwd string) (chain, hooks []Layer, chainTokens int, mcp []string) {
	inv := config.Scan(cwd, home, true)
	for _, it := range inv.Items {
		switch it.Kind {
		case "instruction", "rule":
			chain = append(chain, Layer{Kind: it.Kind, Scope: it.Scope, Path: it.Path, Summary: it.Summary, Tokens: it.TokenEst})
		case "hook":
			hooks = append(hooks, Layer{Kind: it.Kind, Scope: it.Scope, Path: it.Path, Summary: it.Summary})
		}
	}
	chainTokens = inv.AlwaysLoadedTokens
	mcp = mcpServers(home, cwd)
	return chain, hooks, chainTokens, mcp
}

// mcpServers collects configured MCP server names from the project .mcp.json
// and the user ~/.claude.json (top-level and per-project). Names only — the
// token cost of their tool schemas is not knowable from config, only from a
// transcript that recorded it.
func mcpServers(home, cwd string) []string {
	set := map[string]bool{}
	addFrom := func(m map[string]json.RawMessage) {
		for name := range m {
			set[name] = true
		}
	}
	// project .mcp.json
	var proj struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if readJSON(filepath.Join(cwd, ".mcp.json"), &proj) {
		addFrom(proj.MCPServers)
	}
	// user ~/.claude.json: top-level + this project's entry
	var user struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
		Projects   map[string]struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		} `json:"projects"`
	}
	if readJSON(filepath.Join(home, ".claude.json"), &user) {
		addFrom(user.MCPServers)
		if p, ok := user.Projects[cwd]; ok {
			addFrom(p.MCPServers)
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

// Compactions returns the epoch timestamps of context-compaction events in a
// Claude transcript (type "summary" entries — "the agent's memory was
// compacted at T"). Entries without a parseable timestamp are counted as a
// zero so the caller still sees the event happened.
func Compactions(path string) []int64 {
	var out []int64
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var obj struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(line, &obj) != nil || obj.Type != "summary" {
			continue
		}
		out = append(out, iso(obj.Timestamp))
	}
	return out
}

func iso(ts string) int64 {
	if len(ts) < 19 {
		return 0
	}
	t, err := time.Parse("2006-01-02T15:04:05", ts[:19])
	if err != nil {
		return 0
	}
	return t.Unix()
}

// JSON renders a Stack as the schema_version 1 context document.
func JSON(s Stack) ([]byte, error) {
	if s.Chain == nil {
		s.Chain = []Layer{}
	}
	if s.Hooks == nil {
		s.Hooks = []Layer{}
	}
	if s.MCPServers == nil {
		s.MCPServers = []string{}
	}
	if s.Compactions == nil {
		s.Compactions = []int64{}
	}
	doc := struct {
		SchemaVersion int `json:"schema_version"`
		Stack
	}{SchemaVersion, s}
	return json.MarshalIndent(doc, "", "  ")
}
