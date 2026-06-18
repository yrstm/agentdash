package board

import "testing"

func TestCollapseRuns(t *testing.T) {
	rows := []Row{
		// four identical codex respawns -> one row, Count 4, first PID kept
		{Kind: "codex", Model: "gpt-5.5", Tokens: "179k/1.0k", Status: "respawn ×4", Task: "x", Cwd: "/home/dev", PID: 10},
		{Kind: "codex", Model: "gpt-5.5", Tokens: "179k/1.0k", Status: "respawn ×4", Task: "x", Cwd: "/home/dev", PID: 11},
		{Kind: "codex", Model: "gpt-5.5", Tokens: "179k/1.0k", Status: "respawn ×4", Task: "x", Cwd: "/home/dev", PID: 12},
		{Kind: "codex", Model: "gpt-5.5", Tokens: "179k/1.0k", Status: "respawn ×4", Task: "x", Cwd: "/home/dev", PID: 13},
		// same agent, different status -> not merged
		{Kind: "claude", Model: "opus-4-8", Tokens: "1m/2k", Status: "working", Task: "y", Cwd: "/w", PID: 20},
		{Kind: "claude", Model: "opus-4-8", Tokens: "1m/2k", Status: "idle", Task: "y", Cwd: "/w", PID: 21},
	}
	got := CollapseRuns(rows)
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(got), got)
	}
	if got[0].PID != 10 || got[0].Count != 4 {
		t.Errorf("group 0: want PID 10 ×4, got PID %d ×%d", got[0].PID, got[0].Count)
	}
	if got[1].PID != 20 || got[1].Count > 1 {
		t.Errorf("row 1: want PID 20 single, got PID %d ×%d", got[1].PID, got[1].Count)
	}
	if got[2].PID != 21 || got[2].Count > 1 {
		t.Errorf("row 2: want PID 21 single, got PID %d ×%d", got[2].PID, got[2].Count)
	}
}
