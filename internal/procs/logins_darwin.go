package procs

import (
	"os"
	"strings"
	"syscall"
)

// rawLogins lists interactive sessions from `who` on macOS (there is no utmp to
// read directly). Idle time comes from the tty device's atime, and the WHAT
// column from the newest process holding that tty — the same signals the Linux
// path derives from /proc, sourced from ps here. The shared Logins wrapper adds
// the tmux tie-in.
func rawLogins(now int64, exclude map[string]bool) []Login {
	b, err := run("who")
	if err != nil {
		return nil
	}
	rows := psTable()
	var out []Login
	for _, ln := range strings.Split(string(b), "\n") {
		user, tty, host, ok := parseWhoLine(ln)
		if !ok || tty == "?" || exclude[tty] {
			continue
		}
		what, live := newestOnTTY(tty, rows)
		out = append(out, Login{
			User:  user,
			TTY:   tty,
			From:  host,
			Idle:  ttyIdle(tty, now),
			What:  what,
			Stale: !live,
		})
	}
	return out
}

// ttyIdle derives idle time the way w(1) does: atime of the tty device. Kept in
// the darwin file because Stat_t.Atimespec is a macOS-only field.
func ttyIdle(tty string, now int64) string {
	st, err := os.Stat("/dev/" + tty)
	if err != nil {
		return "?"
	}
	idle := now - st.ModTime().Unix()
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		idle = now - sys.Atimespec.Sec
	}
	if idle < 0 {
		idle = 0
	}
	return agoCompact(idle)
}
