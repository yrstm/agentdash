package procs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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

// listeners returns listening TCP ports mapped to the owning pid, pairing the
// /proc/net LISTEN inodes with the fd walk. Ports whose owner can't be
// resolved are dropped, matching the v1 behaviour. See CollectPorts (shared)
// for the flag logic built on top.
func listeners() map[int]int {
	inodes := listenInodes()
	want := make(map[string]bool, len(inodes))
	for _, ino := range inodes {
		want[ino] = true
	}
	owners := inodeOwners(want)
	out := make(map[int]int, len(inodes))
	for port, ino := range inodes {
		if pid, ok := owners[ino]; ok {
			out[port] = pid
		}
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
