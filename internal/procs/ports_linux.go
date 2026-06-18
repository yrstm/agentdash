package procs

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

const tcpListen = "0A"

// listenInodes parses /proc/net/tcp and tcp6 for LISTEN sockets,
// returning port -> socket inode (first seen wins, ports ascending later).
func listenInodes() map[int]string {
	out := map[int]string{}
	for _, f := range []string{"net/tcp", "net/tcp6"} {
		b, err := os.ReadFile(filepath.Join(Root(), f))
		if err != nil {
			continue
		}
		lines := strings.Split(string(b), "\n")
		for _, ln := range lines[1:] {
			fl := strings.Fields(ln)
			// 1 local_address 2 rem_address 3 st ... 9 inode
			if len(fl) < 10 || fl[3] != tcpListen {
				continue
			}
			_, portHex, ok := strings.Cut(fl[1], ":")
			if !ok {
				continue
			}
			port64, err := strconv.ParseInt(portHex, 16, 32)
			if err != nil {
				continue
			}
			if _, dup := out[int(port64)]; !dup {
				out[int(port64)] = fl[9]
			}
		}
	}
	return out
}

// inodeOwners maps socket inodes to owning pids by walking every
// process's fd symlinks once.
func inodeOwners(want map[string]bool) map[string]int {
	root := Root()
	out := map[string]int{}
	for _, pid := range listPIDs(root) {
		fdDir := filepath.Join(root, strconv.Itoa(pid), "fd")
		des, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, de := range des {
			t, err := os.Readlink(filepath.Join(fdDir, de.Name()))
			if err != nil || !strings.HasPrefix(t, "socket:[") {
				continue
			}
			ino := strings.TrimSuffix(strings.TrimPrefix(t, "socket:["), "]")
			if want[ino] {
				if _, seen := out[ino]; !seen {
					out[ino] = pid
				}
			}
		}
	}
	return out
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
	return false
}

// CollectPorts lists listening TCP ports with their suspicion flags.
// activeDirs are the live agent/tmux working dirs (descendants included);
// prevPorts is the previous run's port set for NEW detection; ttyOf
// resolves whether a pid has a controlling terminal.
func CollectPorts(activeDirs []string, prevPorts map[int]bool, home string) []Port {
	inodes := listenInodes()
	want := map[string]bool{}
	for _, ino := range inodes {
		want[ino] = true
	}
	owners := inodeOwners(want)

	ports := make([]int, 0, len(inodes))
	for p := range inodes {
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
		pid, ok := owners[inodes[port]]
		if !ok {
			continue
		}
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

func ttyOf(pid int) string {
	sb, err := os.ReadFile(filepath.Join(Root(), strconv.Itoa(pid), "stat"))
	if err != nil {
		return "?"
	}
	st, ok := parseStat(sb)
	if !ok {
		return "?"
	}
	return ttyName(st.ttyNr)
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
