package procs

import (
	"strconv"
	"strings"
)

// Login is one interactive session for the logins section.
type Login struct {
	User  string
	TTY   string
	From  string // remote host from utmp; "" for a local login
	Idle  string
	What  string
	Tmux  string // tmux session this login is attached to, if any
	Stale bool   // logged in per the OS, but no live process owns the tty
}

// Logins lists interactive sessions. rawLogins is the per-OS source (utmp with
// a `w -h` fallback on Linux, `who` on macOS); the tmux tie-in below is shared:
// an attached tmux client is live by definition, so it overrides a stale guess.
func Logins(now int64, exclude map[string]bool) []Login {
	out := rawLogins(now, exclude)
	if len(out) > 0 {
		clients := ClientsByTTY()
		out = liveLogins(out, clients)
	}
	return out
}

func liveLogins(in []Login, clients map[string]string) []Login {
	out := in[:0]
	for i := range in {
		if in[i].TTY == "" || in[i].TTY == "?" || in[i].Idle == "?" {
			continue
		}
		if s, ok := clients["/dev/"+in[i].TTY]; ok {
			in[i].Tmux, in[i].Stale = s, false
		}
		if in[i].Stale || staleWhat(in[i].What) {
			continue
		}
		out = append(out, in[i])
	}
	return out
}

func staleWhat(what string) bool {
	what = strings.TrimSpace(what)
	return what == "" || what == "."
}

func agoCompact(sec int64) string {
	switch {
	case sec < 60:
		return strconv.FormatInt(sec, 10) + "s"
	case sec < 3600:
		return strconv.FormatInt(sec/60, 10) + "m"
	case sec < 86400:
		return strconv.FormatInt(sec/3600, 10) + "h"
	default:
		return strconv.FormatInt(sec/86400, 10) + "d"
	}
}
