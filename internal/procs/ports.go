package procs

import (
	"sort"
	"strings"
)

// Port is one listening TCP socket, deduplicated by port number.
type Port struct {
	Port  int
	Proc  string
	PID   int
	Cwd   string
	Flags []string // NEW, SUSPECT:dup-cwd, SUSPECT:no-agent
}

// projectDir reports whether a cwd looks like project work (the v1
// /code/* or /home/*/* patterns).
func projectDir(cwd string) bool {
	if strings.HasPrefix(cwd, "/code/") {
		return true
	}
	if rest, ok := strings.CutPrefix(cwd, "/home/"); ok {
		return strings.ContainsRune(rest, '/')
	}
	// macOS user work lives under /Users/<name>/...
	if rest, ok := strings.CutPrefix(cwd, "/Users/"); ok {
		return strings.ContainsRune(rest, '/')
	}
	return false
}

// CollectPorts lists listening TCP ports with their suspicion flags.
// activeDirs are the live agent/tmux working dirs (descendants included);
// prevPorts is the previous run's port set for NEW detection. The per-OS
// listeners() supplies port->owning-pid (/proc/net + fd walk on Linux, lsof
// on macOS); the flag logic below is shared.
func CollectPorts(activeDirs []string, prevPorts map[int]bool, home string) []Port {
	owner := listeners()

	ports := make([]int, 0, len(owner))
	for p := range owner {
		ports = append(ports, p)
	}
	sort.Ints(ports)

	isActive := func(cwd string) bool {
		// an agent idling in ~ or / does not own every project beneath it
		for _, d := range activeDirs {
			if d == "" || d == "/" || d == home {
				continue
			}
			if cwd == d || strings.HasPrefix(cwd, d+"/") || strings.HasPrefix(d, cwd+"/") {
				return true
			}
		}
		return false
	}

	cwdCount := map[string]int{}
	type pre struct {
		port int
		pid  int
		cwd  string
	}
	var pres []pre
	for _, port := range ports {
		pid := owner[port]
		cwd := Cwd(pid)
		pres = append(pres, pre{port, pid, cwd})
		if projectDir(cwd) {
			cwdCount[cwd]++
		}
	}

	var out []Port
	for _, p := range pres {
		var flags []string
		if prevPorts != nil && !prevPorts[p.port] {
			flags = append(flags, "NEW")
		}
		if p.cwd != "" && cwdCount[p.cwd] >= 2 {
			flags = append(flags, "SUSPECT:dup-cwd")
		}
		// no-agent only fires for daemonized listeners (tty ?): a server
		// with a live controlling tty is someone's interactive foreground
		// work, not an orphan
		if projectDir(p.cwd) && ttyOf(p.pid) == "?" && !isActive(p.cwd) {
			flags = append(flags, "SUSPECT:no-agent")
		}
		out = append(out, Port{p.port, Comm(p.pid), p.pid, p.cwd, flags})
	}
	return out
}

// Descendants expands a pid set to all transitive children.
func Descendants(roots []int) []int {
	pp := ParentMap()
	kids := map[int][]int{}
	for pid, ppid := range pp {
		kids[ppid] = append(kids[ppid], pid)
	}
	seen := map[int]bool{}
	stack := append([]int(nil), roots...)
	var out []int
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, k := range kids[p] {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
				stack = append(stack, k)
			}
		}
	}
	return out
}
