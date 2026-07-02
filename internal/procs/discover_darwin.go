package procs

// discover_darwin.go is the macOS counterpart of discover_linux.go. macOS has
// no /proc, so process and system state come from `ps` and `lsof`. Every
// shell-out goes through the package-local run seam; the parsing/classification
// it feeds lives in psparse.go (no build tag), so the logic is unit-tested on
// every platform. The OS-independent types and heuristics live in procs.go.

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// selfPID is agentdash's own pid, excluded from the board. A var so tests can
// pin it against canned ps output.
var selfPID = os.Getpid()

// psArgs is the column set read from ps in one pass. command is last because it
// is the only field that can contain spaces. state is BSD's process-state
// keyword (Linux calls it stat); etime is elapsed wall time [[dd-]hh:]mm:ss.
// -ww disables the terminal-width truncation of the command column.
var psArgs = []string{"-A", "-ww", "-o", "pid=,ppid=,state=,tty=,etime=,command="}

// psTable runs ps once and returns the parsed rows; an unavailable ps yields
// no rows (empty board), never a crash.
func psTable() []psProc {
	out, err := run("ps", psArgs...)
	if err != nil {
		return nil
	}
	return parsePSTable(out)
}

// Discover returns the agent processes, applying the same detection, exclusion
// and launcher-dedup rules as the Linux path. cwds are filled in one batched
// lsof pass for the survivors only.
func Discover(now int64) []Proc {
	out := agentsFromPS(psTable(), now, selfPID)

	pids := make([]int, 0, len(out))
	for _, p := range out {
		pids = append(pids, p.PID)
	}
	cwds := cwdsFor(pids)
	for i := range out {
		out[i].Cwd = cwds[out[i].PID]
		for _, f := range extraRuntime {
			f(out[i].Args, "", &out[i]) // no per-process environ on macOS; adapters fall back to argv
		}
	}
	return out
}

// ParentMap returns pid -> ppid for every live process.
func ParentMap() map[int]int {
	out := map[int]int{}
	for _, p := range psTable() {
		out[p.pid] = p.ppid
	}
	return out
}

// Zombies returns "pid comm <defunct>" lines for processes in the Z state.
func Zombies() []string { return zombiesFromPS(psTable()) }

// Orphans returns "pid args" lines for headless wrapper processes whose
// children have all exited. Mirrors the Linux heuristic, sourced from ps.
func Orphans() []string { return orphansFromPS(psTable(), selfPID) }

// Comm returns a process short name.
func Comm(pid int) string {
	out, err := run("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	if err != nil {
		return ""
	}
	return commOf(strings.TrimSpace(string(out)))
}

// Cwd resolves one process working directory via lsof; "" when gone.
func Cwd(pid int) string { return cwdsFor([]int{pid})[pid] }

// cwdsFor batches the cwd lookup for several pids into one lsof call.
// `lsof -a -d cwd -p a,b,c -Fpn` emits a p<pid> line then an n<path> line per
// process; unresolved pids are simply absent from the result.
func cwdsFor(pids []int) map[int]string {
	if len(pids) == 0 {
		return map[int]string{}
	}
	list := make([]string, len(pids))
	for i, p := range pids {
		list[i] = strconv.Itoa(p)
	}
	b, err := run("lsof", "-a", "-d", "cwd", "-p", strings.Join(list, ","), "-Fpn")
	if err != nil {
		return map[int]string{}
	}
	return parseFpnPairs(b)
}

// openFDs returns the target paths of a process's open file descriptors via
// `lsof -p <pid> -Fn`; a gone or unreadable pid yields nil (fail soft).
func openFDs(pid int) []string {
	b, err := run("lsof", "-p", strconv.Itoa(pid), "-Fn")
	if err != nil {
		return nil
	}
	return parseOpenFDs(b)
}

// LoadAvg returns the 1-minute load from sysctl vm.loadavg.
func LoadAvg() string {
	b, err := run("sysctl", "-n", "vm.loadavg")
	if err != nil {
		return "?"
	}
	return parseLoadAvg(b)
}

// Alive reports whether a pid is still live, using signal 0. EPERM means the
// process exists but is owned by another user — still alive.
func Alive(pid string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil {
		return false
	}
	err = syscall.Kill(n, 0)
	return err == nil || err == syscall.EPERM
}
