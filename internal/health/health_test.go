package health

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrstm/agentdash/internal/procs"
)

func TestMCPZombiesFrom(t *testing.T) {
	all := []procs.LiteProc{
		{PID: 10, PPID: 1, Args: "npx -y @modelcontextprotocol/server-filesystem /work"}, // reparented MCP -> zombie
		{PID: 11, PPID: 1, Args: "node mcp-server-github"},                               // reparented MCP -> zombie
		{PID: 12, PPID: 4321, Args: "node mcp-server-github"},                            // MCP but still owned -> not
		{PID: 13, PPID: 1, Args: "/usr/lib/systemd/systemd --user"},                      // reparented but not MCP -> not
	}
	got := mcpZombiesFrom(all)
	if len(got) != 2 {
		t.Fatalf("got %d zombies, want 2: %v", len(got), got)
	}
	if got[0] != "10 npx -y @modelcontextprotocol/server-filesystem /work" {
		t.Errorf("first zombie = %q", got[0])
	}
}

func TestScanTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	var lines []string
	// 5 clean assistant turns, then errors/interrupts near the end
	for i := 0; i < 5; i++ {
		lines = append(lines, `{"type":"assistant","message":{"content":"ok"}}`)
	}
	lines = append(lines,
		`{"type":"assistant","isApiErrorMessage":true,"message":{"content":"API Error: overloaded"}}`,
		`{"type":"user","message":{"content":"[Request interrupted by user]"}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`,
		`{"type":"assistant","message":{"content":"resuming"}}`,
	)
	if err := os.WriteFile(path, []byte(join(lines)), 0o644); err != nil {
		t.Fatal(err)
	}

	// whole file: 9 turns, 1 api error, 2 interrupts
	st := scanTail(path, 0)
	if st.turns != 9 || st.apiErrors != 1 || st.interrupts != 2 {
		t.Fatalf("full scan = %+v, want 9/1/2", st)
	}
	// last 3 turns only: the interrupt+interrupt+resuming tail -> 0 api, 2 interrupts... last 3 are interrupt,interrupt(array),resuming
	st = scanTail(path, 3)
	if st.turns != 3 || st.apiErrors != 0 || st.interrupts != 2 {
		t.Fatalf("windowed scan = %+v, want 3/0/2", st)
	}
}

func TestWaitingTodayByPath(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).Unix()
	at := func(ago int64) string { return time.Unix(now-ago, 0).UTC().Format(time.RFC3339) }

	logPath := filepath.Join(t.TempDir(), "events.ndjson")
	t.Setenv("AGENTDASH_EVENTLOG", logPath)
	ev := func(path, to string, ago int64) string {
		return fmt.Sprintf(`{"type":"status_change","session_path":"%s","to_status":"%s","ts":"%s"}`, path, to, at(ago))
	}
	lines := []string{
		ev("/p", "waiting", 7200), // /p waits from -2h
		ev("/p", "working", 1800), // ...until -30m  => 90m waiting
		ev("/q", "working", 3600), //
		ev("/q", "waiting", 600),  // /q still waiting from -10m => 10m to now
		ev("/r", "working", 900),  // /r never waits
	}
	if err := os.WriteFile(logPath, []byte(join(lines)), 0o644); err != nil {
		t.Fatal(err)
	}

	w := waitingTodayByPath(now)
	if w["/p"] != 5400 {
		t.Errorf("/p waiting = %ds, want 5400", w["/p"])
	}
	if w["/q"] != 600 {
		t.Errorf("/q waiting = %ds, want 600", w["/q"])
	}
	if _, ok := w["/r"]; ok {
		t.Errorf("/r should have no waiting time")
	}
}

func join(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
