package procs

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// writeFakeProc writes /proc/<pid>/{stat,cmdline} under root for one process.
// Only state, ppid, tty_nr and starttime are read out of stat.
func writeFakeProc(t *testing.T, root string, pid int, comm, state string, ppid, ttyNr int, args string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stat := strconv.Itoa(pid) + " (" + comm + ") " + state + " " +
		strconv.Itoa(ppid) + " 0 0 " + strconv.Itoa(ttyNr) +
		" 0 0 0 0 0 0 0 0 0 0 0 0 0 0 5000 0 0 0"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdline := strings.ReplaceAll(args, " ", "\x00")
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOrphans(t *testing.T) {
	root := t.TempDir()
	// orphans: headless (tty 0) wrapper commands with no living children
	writeFakeProc(t, root, 100, "bash", "S", 1, 0, "bash -c claude-runner")
	writeFakeProc(t, root, 101, "nohup", "S", 1, 0, "nohup codex run")
	// not an orphan: a bash -c wrapper that still has a child
	writeFakeProc(t, root, 200, "bash", "S", 1, 0, "bash -c agent-loop")
	writeFakeProc(t, root, 201, "claude", "S", 200, 0, "claude --resume x")
	// not an orphan: a wrapper attached to a tty
	writeFakeProc(t, root, 300, "bash", "S", 1, 34816, "bash -c interactive")
	// not an orphan: headless and childless but not a wrapper command
	writeFakeProc(t, root, 400, "node", "S", 1, 0, "node server.js")

	t.Setenv("AGENTDASH_PROC_ROOT", root)
	got := Orphans()
	sort.Strings(got)

	want := []string{"100 bash -c claude-runner", "101 nohup codex run"}
	if len(got) != len(want) {
		t.Fatalf("Orphans() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Orphans()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
