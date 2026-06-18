package procs

import "testing"

func TestParseStat(t *testing.T) {
	// comm with spaces and parens must not break field positions
	st, ok := parseStat([]byte(`123 (tmux: client (x)) S 1 123 123 34817 0 0 0 0 0 0 0 0 0 0 0 0 0 0 5000 0 0 0`))
	if !ok {
		t.Fatal("parseStat failed")
	}
	if st.comm != "tmux: client (x)" || st.ppid != 1 || st.ttyNr != 34817 || st.startTick != 5000 {
		t.Errorf("got %+v", st)
	}
	if _, ok := parseStat([]byte("garbage")); ok {
		t.Error("garbage accepted")
	}
}

func TestTTYName(t *testing.T) {
	for nr, want := range map[int]string{
		0:           "?",
		34816:       "pts/0",
		34817 + 255: "pts/256", // minor overflowing into major 137? no: 34817+255 = major 136 minor 256 path
		1024 + 1:    "tty1",    // major 4 minor 1
		1024 + 65:   "ttyS1",   // major 4 minor 65
	} {
		if got := ttyName(nr); got != want {
			t.Errorf("ttyName(%d) = %q, want %q", nr, got, want)
		}
	}
}

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
