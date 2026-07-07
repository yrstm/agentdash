package procs

import "os/exec"

// run is the macOS collector's exec seam. The default calls the real binary;
// the darwin integration test replaces it to feed canned `ps`/`lsof` output.
// It lives in the darwin build only — the shared parsing logic it feeds
// (psparse.go) exec's nothing and is unit-tested on every platform, and the
// Linux path reads /proc instead of shelling out. It exists only for the
// shell-outs this package owns (`ps`, `lsof`, `who`); tmux keeps its own
// boundary in tmux.go.
var run = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}
