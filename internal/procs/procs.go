// Package procs reads agent processes and system state from the OS.
//
// On Linux it goes straight to the kernel interfaces (/proc, /proc/net,
// utmp, the docker unix socket), replacing the procps/iproute2 forks of the
// v1 script. On macOS, which has no /proc, the same information comes from
// `ps` and `lsof` (see the *_darwin.go siblings); those shell-outs go through
// the package-local run seam so their parsing is unit-testable on any
// platform. tmux stays an exec boundary everywhere: it has no stable public
// API.
//
// The OS-independent types and heuristics (Proc/Port/Login, KindOf,
// exclusion and launcher-dedup rules) live here so both platform builds share
// one definition; only the collection mechanism is per-OS.
package procs

import "strings"

// Proc is one process of interest on the board.
type Proc struct {
	PID    int
	PPID   int
	Kind   string // claude / codex / hermes; "" for non-agents
	TTY    string // pts/1 (linux) or ttys000 (macOS) style, "?" when none
	State  string
	Start  int64 // epoch seconds
	Uptime int64 // seconds, relative to now passed to Discover
	Cwd    string
	Args   string // full command line, NULs as spaces

	// Extra holds optional per-kind routing metadata populated by build-tagged
	// extensions (e.g. session-store env vars). Nil in the default build.
	Extra map[string]string
}

// extraRuntime is an extension point for optional, build-tagged agent adapters
// to enrich a Proc from per-process data (e.g. session-store env vars). Empty
// in the default build, so process reading behaves exactly as the core always
// has. On Linux the dir argument is /proc/<pid>; on macOS it is empty (the
// darwin collector reads no per-process environ), so adapters degrade to argv.
var extraRuntime []func(args, dir string, p *Proc)

// RegisterRuntime adds a hook that may enrich a Proc from per-process data.
func RegisterRuntime(f func(args, dir string, p *Proc)) { extraRuntime = append(extraRuntime, f) }

// LiteProc is a minimal process record for whole-system scans that don't need
// the full Proc enrichment (e.g. the health check's zombie-MCP sweep).
type LiteProc struct {
	PID  int
	PPID int
	Args string
}

// WrapperKinds are agents with no session files of their own: listed,
// not enriched.
var WrapperKinds = map[string]bool{"hermes": true}

// KindOf maps a command line to an agent kind; first match wins, specific
// names before generic ones.
func KindOf(args string) string {
	switch {
	case strings.Contains(args, "hermes"):
		return "hermes"
	case strings.Contains(args, "claude"):
		return "claude"
	case strings.Contains(args, "codex"):
		return "codex"
	}
	return ""
}

// excluded mirrors the v1 pgrep|grep -v pipeline: helper processes that
// mention an agent's name without being one.
func excluded(args string) bool {
	for _, pat := range []string{"pgrep", "hermes-snap", "shell-snapshot",
		"node --ping", "sandboxes/", "/bin/bash -c", "codex-linux-sandbox"} {
		if strings.Contains(args, pat) {
			return true
		}
	}
	return strings.HasPrefix(args, "tmux ") || args == "tmux"
}

// dropSameKindLaunchers removes a process whose child is another agent of the
// same kind — the wrapper that re-execs the real one (e.g. `node /usr/bin/codex`
// spawning the vendored codex binary), so one chat is one row, not two.
func dropSameKindLaunchers(ps []Proc) []Proc {
	childKind := make(map[int]string, len(ps))
	for _, p := range ps {
		childKind[p.PPID] = p.Kind
	}
	out := make([]Proc, 0, len(ps))
	for _, p := range ps {
		if childKind[p.PID] == p.Kind {
			continue // p is the launcher of a same-kind child; keep the child
		}
		out = append(out, p)
	}
	return out
}
