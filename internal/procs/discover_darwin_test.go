package procs

import (
	"fmt"
	"testing"
)

// TestDiscoverThroughSeam drives the real macOS Discover wrapper with canned
// `ps`/`lsof` output fed through the run seam — proving the shell-out wiring
// (ps for the table, batched lsof for cwd) matches what the parsers expect.
// Darwin-tagged: it exercises the darwin-only wrappers, so it runs on the macOS
// CI job; the parsing itself is covered on every platform in darwin_parse_test.go.
func TestDiscoverThroughSeam(t *testing.T) {
	origRun, origSelf := run, selfPID
	defer func() { run, selfPID = origRun, origSelf }()
	selfPID = 9999

	run = func(name string, args ...string) ([]byte, error) {
		switch {
		case name == "ps" && len(args) > 0 && args[0] == "-A":
			return []byte("" +
				" 1234   501 S+ s001 05:12 node /usr/bin/codex resume\n" +
				" 1250  1234 S+ s001 05:10 codex resume\n" +
				" 1300   501 S+ s002 00:30 claude --resume abc\n" +
				" 9999   501 R+ s003 00:05 ps -A -ww\n"), nil
		case name == "lsof":
			return []byte("p1250\nfcwd\nn/Users/user/proj\np1300\nfcwd\nn/Users/user/app\n"), nil
		}
		return nil, fmt.Errorf("unexpected exec: %s %v", name, args)
	}

	agents := Discover(1_000_000)
	if len(agents) != 2 {
		t.Fatalf("Discover = %d agents (%+v), want 2", len(agents), agents)
	}
	byPID := map[int]Proc{}
	for _, a := range agents {
		byPID[a.PID] = a
	}
	if _, ok := byPID[1234]; ok {
		t.Error("same-kind launcher 1234 should be dropped")
	}
	if c := byPID[1250]; c.Kind != "codex" || c.Cwd != "/Users/user/proj" {
		t.Errorf("codex = %+v, want kind codex cwd /Users/user/proj", c)
	}
	if c := byPID[1300]; c.Kind != "claude" || c.Cwd != "/Users/user/app" {
		t.Errorf("claude = %+v, want kind claude cwd /Users/user/app", c)
	}
}
