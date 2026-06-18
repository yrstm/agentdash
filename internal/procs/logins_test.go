package procs

import "testing"

func TestLiveLoginsDropsStalePlaceholders(t *testing.T) {
	in := []Login{
		{User: "dev", TTY: "?", Idle: "?", What: "bash"},
		{User: "dev", TTY: "pts/1", Idle: "?", What: "bash"},
		{User: "dev", TTY: "pts/2", Idle: "1m", Stale: true, What: "bash"},
		{User: "dev", TTY: "pts/3", Idle: "2m"},
		{User: "dev", TTY: "pts/6", Idle: "2m", What: "."},
		{User: "dev", TTY: "pts/4", Idle: "3m", What: "-bash"},
		{User: "dev", TTY: "pts/5", Idle: "4m", Stale: true, What: "tmux"},
	}
	got := liveLogins(in, map[string]string{"/dev/pts/5": "work"})
	if len(got) != 2 {
		t.Fatalf("liveLogins kept %d rows, want 2: %#v", len(got), got)
	}
	if got[0].TTY != "pts/4" {
		t.Fatalf("first live row = %#v, want pts/4", got[0])
	}
	if got[1].TTY != "pts/5" || got[1].Tmux != "work" || got[1].Stale {
		t.Fatalf("tmux client did not revive stale row: %#v", got[1])
	}
}
