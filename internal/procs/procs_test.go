package procs

import "testing"

func TestKindOfAndExclusions(t *testing.T) {
	for args, want := range map[string]string{
		"claude --resume abc":     "claude",
		"codex /work/x":           "codex",
		"hermes -p api":           "hermes",
		"node server.js":          "",
		"hermes-wrapper of claud": "hermes", // first match wins
	} {
		if got := KindOf(args); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", args, got, want)
		}
	}
	for _, args := range []string{
		"pgrep -af claude",
		"bash /usr/bin/hermes-snap",
		"claude shell-snapshot worker",
		"node --ping claude-helper",
		"/x/sandboxes/y claude",
		"/bin/bash -c claude things",
		"tmux attach -t claude",
		"/home/u/.codex/tmp/arg0/x/codex-linux-sandbox --command-cwd /code -- /bin/bash -lc black .",
		"bwrap --argv0 codex-linux-sandbox -- /usr/lib/.../codex --command-cwd /code -- pytest",
	} {
		if !excluded(args) {
			t.Errorf("excluded(%q) = false, want true", args)
		}
	}
	if excluded("claude --help") {
		t.Error("plain agent invocation excluded")
	}
}

func TestProjectDir(t *testing.T) {
	for cwd, want := range map[string]bool{
		"/code/api":     true,
		"/home/u/proj":  true,
		"/home/u":       false,
		"/var/lib":      false,
		"/codex-things": false,
	} {
		if got := projectDir(cwd); got != want {
			t.Errorf("projectDir(%q) = %v, want %v", cwd, got, want)
		}
	}
}

func TestDropSameKindLaunchers(t *testing.T) {
	got := dropSameKindLaunchers([]Proc{
		{PID: 100, PPID: 1, Kind: "codex"},   // node launcher
		{PID: 101, PPID: 100, Kind: "codex"}, // real binary it spawned
		{PID: 200, PPID: 1, Kind: "claude"},  // standalone agent
	})
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(got), got)
	}
	for _, p := range got {
		if p.PID == 100 {
			t.Error("the same-kind launcher (pid 100) should be dropped")
		}
	}
}
