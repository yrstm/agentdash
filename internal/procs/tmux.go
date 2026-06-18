package procs

import (
	"os/exec"
	"strconv"
	"strings"
)

// tmux stays an exec boundary: it has no stable public API, and an absent
// tmux degrades to empty results exactly like v1.

// Pane is one tmux pane keyed by its pty.
type Pane struct {
	TTY      string // /dev/pts/N
	Attached bool
	Session  string
	Window   string
	PaneID   string
}

// Session is one tmux session for the expanded view.
type Session struct {
	Name     string
	Attached bool
	Created  int64
}

func tmuxLines(args ...string) []string {
	out, err := exec.Command("tmux", args...).Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

// PanesByTTY maps /dev/pts/N to its pane, for attachment glyphs and `go`.
func PanesByTTY() map[string]Pane {
	out := map[string]Pane{}
	for _, ln := range tmuxLines("list-panes", "-a", "-F",
		"#{pane_tty}|#{session_attached}|#{session_name}|#{window_index}|#{pane_id}") {
		f := strings.Split(ln, "|")
		if len(f) < 5 || f[0] == "" {
			continue
		}
		n, _ := strconv.Atoi(f[1])
		out[f[0]] = Pane{TTY: f[0], Attached: n >= 1, Session: f[2], Window: f[3], PaneID: f[4]}
	}
	return out
}

// PanePaths lists every pane's current working directory.
func PanePaths() []string {
	return tmuxLines("list-panes", "-a", "-F", "#{pane_current_path}")
}

// ClientsByTTY maps a tmux client's tty (e.g. /dev/pts/7) to the session it
// is attached to. A login shell that ran `tmux` is a client on its own tty,
// so this ties an outer login row to the tmux work it is driving.
func ClientsByTTY() map[string]string {
	out := map[string]string{}
	for _, ln := range tmuxLines("list-clients", "-F", "#{client_tty}|#{session_name}") {
		f := strings.Split(ln, "|")
		if len(f) < 2 || f[0] == "" {
			continue
		}
		out[f[0]] = f[1]
	}
	return out
}

// Sessions lists tmux sessions for the expanded section.
func Sessions() []Session {
	var out []Session
	for _, ln := range tmuxLines("ls", "-F",
		"#{session_name}|#{?session_attached,attached,detached}|#{session_created}") {
		f := strings.Split(ln, "|")
		if len(f) < 3 {
			continue
		}
		created, _ := strconv.ParseInt(f[2], 10, 64)
		out = append(out, Session{Name: f[0], Attached: f[1] == "attached", Created: created})
	}
	return out
}
