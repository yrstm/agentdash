// Package procs reads agent processes and system state straight from the
// kernel interfaces (/proc, /proc/net, utmp, the docker unix socket),
// replacing the procps/iproute2 forks of the v1 script. tmux remains an
// exec boundary: it has no stable public API.
package procs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Root is the proc filesystem root; tests and the parity harness point it
// at a fixture tree via AGENTDASH_PROC_ROOT.
func Root() string {
	if r := os.Getenv("AGENTDASH_PROC_ROOT"); r != "" {
		return r
	}
	return "/proc"
}

// userHz is the kernel's USER_HZ for /proc time fields, fixed at 100 on
// Linux regardless of the scheduler tick.
const userHz = 100

// Proc is one process of interest on the board.
type Proc struct {
	PID    int
	PPID   int
	Kind   string // claude / codex / hermes; "" for non-agents
	TTY    string // pts/1 style, "?" when none
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
// to enrich a Proc from /proc/<pid> data (e.g. session-store env vars). Empty
// in the default build, so process reading behaves exactly as the core always
// has.
var extraRuntime []func(args, dir string, p *Proc)

// RegisterRuntime adds a hook that may enrich a Proc from /proc/<pid> data.
func RegisterRuntime(f func(args, dir string, p *Proc)) { extraRuntime = append(extraRuntime, f) }

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

type statLine struct {
	comm      string
	state     string
	ppid      int
	ttyNr     int
	startTick int64
}

// parseStat splits /proc/<pid>/stat around the parenthesised comm, which
// may itself contain spaces or parens.
func parseStat(b []byte) (statLine, bool) {
	s := string(b)
	cl := strings.LastIndexByte(s, ')')
	op := strings.IndexByte(s, '(')
	if op < 0 || cl < op {
		return statLine{}, false
	}
	rest := strings.Fields(s[cl+1:])
	if len(rest) < 20 {
		return statLine{}, false
	}
	ppid, _ := strconv.Atoi(rest[1])
	ttyNr, _ := strconv.Atoi(rest[4])
	start, _ := strconv.ParseInt(rest[19], 10, 64)
	return statLine{
		comm:      s[op+1 : cl],
		state:     rest[0],
		ppid:      ppid,
		ttyNr:     ttyNr,
		startTick: start,
	}, true
}

// ttyName decodes a stat tty_nr the way procps does for the common cases.
func ttyName(nr int) string {
	if nr == 0 {
		return "?"
	}
	major := (nr >> 8) & 0xfff
	minor := (nr & 0xff) | ((nr >> 12) & 0xfff00)
	switch {
	case major >= 136 && major <= 143:
		return "pts/" + strconv.Itoa(minor+(major-136)*256)
	case major == 4 && minor < 64:
		return "tty" + strconv.Itoa(minor)
	case major == 4:
		return "ttyS" + strconv.Itoa(minor-64)
	}
	return "?"
}

// BootTime reads btime from /proc/stat.
func BootTime() int64 {
	b, err := os.ReadFile(filepath.Join(Root(), "stat"))
	if err != nil {
		return 0
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(ln, "btime "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}

func readPID(root string, pid int, btime, now int64) (Proc, bool) {
	dir := filepath.Join(root, strconv.Itoa(pid))
	cl, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil || len(cl) == 0 {
		return Proc{}, false
	}
	args := strings.TrimRight(strings.ReplaceAll(string(cl), "\x00", " "), " ")
	sb, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return Proc{}, false
	}
	st, ok := parseStat(sb)
	if !ok {
		return Proc{}, false
	}
	start := btime + st.startTick/userHz
	cwd, _ := os.Readlink(filepath.Join(dir, "cwd"))
	p := Proc{
		PID: pid, PPID: st.ppid, TTY: ttyName(st.ttyNr), State: st.state,
		Start: start, Uptime: now - start, Cwd: cwd, Args: args,
	}
	for _, f := range extraRuntime {
		f(args, dir, &p)
	}
	return p, true
}

func listPIDs(root string) []int {
	des, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]int, 0, len(des))
	for _, de := range des {
		if pid, err := strconv.Atoi(de.Name()); err == nil {
			out = append(out, pid)
		}
	}
	return out
}

// Discover walks /proc and returns the agent processes, applying the v1
// detection and exclusion rules. Processes that vanish mid-walk are
// skipped; the scanner itself never lists.
func Discover(now int64) []Proc {
	root := Root()
	btime := BootTime()
	self := os.Getpid()
	var out []Proc
	for _, pid := range listPIDs(root) {
		if pid == self {
			continue
		}
		p, ok := readPID(root, pid, btime, now)
		if !ok || excluded(p.Args) {
			continue
		}
		if p.Kind = KindOf(p.Args); p.Kind == "" {
			continue
		}
		out = append(out, p)
	}
	return dropSameKindLaunchers(out)
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

// ParentMap returns pid -> ppid for every live process (tree view, port
// descendant expansion).
func ParentMap() map[int]int {
	root := Root()
	out := map[int]int{}
	for _, pid := range listPIDs(root) {
		sb, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat"))
		if err != nil {
			continue
		}
		if st, ok := parseStat(sb); ok {
			out[pid] = st.ppid
		}
	}
	return out
}

// Zombies returns "pid comm" style lines for defunct processes.
func Zombies() []string {
	root := Root()
	var out []string
	for _, pid := range listPIDs(root) {
		sb, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat"))
		if err != nil {
			continue
		}
		if st, ok := parseStat(sb); ok && strings.HasPrefix(st.state, "Z") {
			out = append(out, strconv.Itoa(pid)+" "+st.comm+" <defunct>")
		}
	}
	return out
}

// Orphans returns "pid args" lines for headless wrapper processes
// (bash -c / nohup, no controlling tty) whose children have all exited:
// leftover launchers whose agent died. Distinct from Zombies (defunct
// processes); render-only, never enriched as agents or emitted in --json.
func Orphans() []string {
	root := Root()
	self := os.Getpid()
	pids := listPIDs(root)
	stats := make(map[int]statLine, len(pids))
	hasChild := map[int]bool{}
	for _, pid := range pids {
		sb, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat"))
		if err != nil {
			continue
		}
		if st, ok := parseStat(sb); ok {
			stats[pid] = st
			hasChild[st.ppid] = true
		}
	}
	var out []string
	for _, pid := range pids {
		st, ok := stats[pid]
		if !ok || pid == self || hasChild[pid] || ttyName(st.ttyNr) != "?" {
			continue
		}
		cl, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "cmdline"))
		if err != nil || len(cl) == 0 {
			continue
		}
		args := strings.TrimRight(strings.ReplaceAll(string(cl), "\x00", " "), " ")
		if !strings.Contains(args, "bash -c") && !strings.Contains(args, "nohup") {
			continue
		}
		out = append(out, strconv.Itoa(pid)+" "+args)
	}
	return out
}

// Cwd resolves a process working directory; "" when gone or unreadable.
func Cwd(pid int) string {
	c, _ := os.Readlink(filepath.Join(Root(), strconv.Itoa(pid), "cwd"))
	return c
}

// Comm returns the process short name.
func Comm(pid int) string {
	b, err := os.ReadFile(filepath.Join(Root(), strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// LoadAvg returns the 1-minute load as written in /proc/loadavg.
func LoadAvg() string {
	b, err := os.ReadFile(filepath.Join(Root(), "loadavg"))
	if err != nil {
		return "?"
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return "?"
	}
	return f[0]
}
