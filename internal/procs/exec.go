package procs

import "os/exec"

// run is the package's exec seam. The default calls the real binary; tests
// replace it to feed canned `ps`/`lsof` output so the macOS collector's
// parsing and pairing logic can be exercised on any platform without a real
// macOS host. It exists only for the shell-outs this package owns (`ps`,
// `lsof`, `who`); tmux keeps its own boundary in tmux.go.
var run = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}
