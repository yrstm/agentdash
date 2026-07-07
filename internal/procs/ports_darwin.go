package procs

import (
	"strconv"
	"strings"
)

// listeners returns listening TCP ports mapped to the owning pid, from
// `lsof -nP -iTCP -sTCP:LISTEN`. macOS has no /proc/net, so lsof is both the
// socket source and the owner in one pass. CollectPorts (shared) adds flags.
func listeners() map[int]int {
	b, err := run("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn")
	if err != nil {
		return map[int]int{}
	}
	return parseLsofListeners(b)
}

// ttyOf resolves whether a pid has a controlling terminal, for the no-agent
// listener heuristic. "?" when none, gone, or unreadable.
func ttyOf(pid int) string {
	out, err := run("ps", "-p", strconv.Itoa(pid), "-o", "tty=")
	if err != nil {
		return "?"
	}
	return normTTY(strings.TrimSpace(string(out)))
}
