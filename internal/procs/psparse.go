package procs

// psparse.go holds the pure parsing and classification logic behind the macOS
// collector: turning `ps`, `lsof` and `who` output into the same Proc/Port/
// Login shapes the Linux /proc path produces. It carries no build tag on
// purpose — it exec's nothing, so it compiles and is unit-tested on every
// platform (see darwin_parse_test.go), which is how the macOS logic stays
// covered on Linux CI. The thin wrappers that actually run ps/lsof live in the
// *_darwin.go siblings.

import (
	"path/filepath"
	"strconv"
	"strings"
)

// psProc is one parsed ps row before agent classification.
type psProc struct {
	pid     int
	ppid    int
	state   string
	tty     string // normalized: ttys000 style, "?" when none
	etime   int64  // elapsed seconds
	command string
}

// parsePSTable parses `ps` output in the psArgs column order
// (pid ppid state tty etime command). Malformed rows are skipped (fail soft);
// the command field keeps its internal spacing.
func parsePSTable(b []byte) []psProc {
	var out []psProc
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		f := fieldsN(ln, 6)
		if len(f) < 6 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		out = append(out, psProc{
			pid:     pid,
			ppid:    ppid,
			state:   f[2],
			tty:     normTTY(f[3]),
			etime:   parseEtime(f[4]),
			command: strings.TrimRight(f[5], " "),
		})
	}
	return out
}

// fieldsN splits a line into at most n whitespace-delimited fields; the last
// field keeps the remainder verbatim (so a command's internal spaces survive).
func fieldsN(s string, n int) []string {
	var out []string
	for i := 0; i < n-1; i++ {
		s = strings.TrimLeft(s, " \t")
		j := strings.IndexAny(s, " \t")
		if j < 0 {
			break
		}
		out = append(out, s[:j])
		s = s[j:]
	}
	s = strings.TrimLeft(s, " \t")
	if s != "" {
		out = append(out, s)
	}
	return out
}

// parseEtime turns ps elapsed time ([[dd-]hh:]mm:ss) into seconds.
func parseEtime(s string) int64 {
	s = strings.TrimSpace(s)
	var days int64
	if i := strings.IndexByte(s, '-'); i >= 0 {
		days, _ = strconv.ParseInt(s[:i], 10, 64)
		s = s[i+1:]
	}
	var secs int64
	for _, p := range strings.Split(s, ":") {
		n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil {
			return days * 86400
		}
		secs = secs*60 + n
	}
	return days*86400 + secs
}

// normTTY maps a ps/who tty column to the shape the rest of the tool expects:
// a name under /dev (ttys000), or "?" for no controlling terminal. It accepts
// both the short (s000) and full (ttys000) forms the tools may print.
func normTTY(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/dev/")
	switch s {
	case "", "?", "??", "-":
		return "?"
	}
	if strings.HasPrefix(s, "tty") || s == "console" {
		return s
	}
	return "tty" + s
}

// commOf is the ps analogue of /proc/<pid>/comm: the executable base name.
func commOf(command string) string {
	f := strings.Fields(command)
	if len(f) == 0 {
		return ""
	}
	return filepath.Base(f[0])
}

// agentsFromPS classifies a ps table into agent Procs, applying the same
// exclusion and launcher-dedup rules as the Linux path. self is agentdash's own
// pid, dropped from the board. Cwd is left empty for the caller to fill (one
// batched lsof pass on macOS).
func agentsFromPS(rows []psProc, now int64, self int) []Proc {
	var cand []Proc
	for _, p := range rows {
		if p.pid == self || excluded(p.command) {
			continue
		}
		kind := KindOf(p.command)
		if kind == "" {
			continue
		}
		cand = append(cand, Proc{
			PID: p.pid, PPID: p.ppid, Kind: kind, TTY: p.tty, State: p.state,
			Start: now - p.etime, Uptime: p.etime, Args: p.command,
		})
	}
	return dropSameKindLaunchers(cand)
}

// zombiesFromPS returns "pid comm <defunct>" lines for processes in the Z state.
func zombiesFromPS(rows []psProc) []string {
	var out []string
	for _, p := range rows {
		if strings.HasPrefix(p.state, "Z") {
			out = append(out, strconv.Itoa(p.pid)+" "+commOf(p.command)+" <defunct>")
		}
	}
	return out
}

// orphansFromPS returns "pid args" lines for headless wrapper processes
// (bash -c / nohup, no controlling tty) whose children have all exited.
func orphansFromPS(rows []psProc, self int) []string {
	hasChild := map[int]bool{}
	for _, p := range rows {
		hasChild[p.ppid] = true
	}
	var out []string
	for _, p := range rows {
		if p.pid == self || hasChild[p.pid] || p.tty != "?" {
			continue
		}
		if !strings.Contains(p.command, "bash -c") && !strings.Contains(p.command, "nohup") {
			continue
		}
		out = append(out, strconv.Itoa(p.pid)+" "+p.command)
	}
	return out
}

// newestOnTTY picks the newest process (smallest elapsed time) holding a tty as
// its controlling terminal, standing in for the WHAT column of w(1). The bool
// is false when no live process owns the tty — a stale login record.
func newestOnTTY(tty string, rows []psProc) (string, bool) {
	best, bestEt := "", int64(-1)
	live := false
	for _, p := range rows {
		if p.tty != tty {
			continue
		}
		live = true
		if bestEt < 0 || p.etime < bestEt {
			best, bestEt = p.command, p.etime
		}
	}
	return best, live
}

// parseFpnPairs parses `lsof -Fp...n` output into pid -> first name seen. A
// p<pid> line opens a process; each following n<name> line belongs to it.
func parseFpnPairs(b []byte) map[int]string {
	out := map[int]string{}
	cur := 0
	for _, ln := range strings.Split(string(b), "\n") {
		if ln == "" {
			continue
		}
		switch ln[0] {
		case 'p':
			cur, _ = strconv.Atoi(ln[1:])
		case 'n':
			if cur != 0 {
				if _, seen := out[cur]; !seen {
					out[cur] = ln[1:]
				}
			}
		}
	}
	return out
}

// parseLsofListeners parses `lsof -nP -iTCP -sTCP:LISTEN -Fpn` into
// port -> owning pid. The port is the text after the final colon of each name
// (handles *:8080, 127.0.0.1:8080, [::1]:8080); first pid per port wins.
func parseLsofListeners(b []byte) map[int]int {
	out := map[int]int{}
	cur := 0
	for _, ln := range strings.Split(string(b), "\n") {
		if ln == "" {
			continue
		}
		switch ln[0] {
		case 'p':
			cur, _ = strconv.Atoi(ln[1:])
		case 'n':
			if cur == 0 {
				continue
			}
			name := ln[1:]
			i := strings.LastIndexByte(name, ':')
			if i < 0 {
				continue
			}
			port, err := strconv.Atoi(name[i+1:])
			if err != nil {
				continue
			}
			if _, seen := out[port]; !seen {
				out[port] = cur
			}
		}
	}
	return out
}

// parseOpenFDs pulls the absolute file paths out of `lsof -p <pid> -Fn` output.
func parseOpenFDs(b []byte) []string {
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		if len(ln) > 1 && ln[0] == 'n' && strings.HasPrefix(ln[1:], "/") {
			out = append(out, ln[1:])
		}
	}
	return out
}

// parseWhoLine parses one `who` line into user, normalized tty and remote host
// (the parenthesized last field, "" when local). ok is false for a blank or
// malformed line.
func parseWhoLine(line string) (user, tty, host string, ok bool) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return "", "", "", false
	}
	if last := f[len(f)-1]; strings.HasPrefix(last, "(") && strings.HasSuffix(last, ")") {
		host = strings.TrimSuffix(strings.TrimPrefix(last, "("), ")")
	}
	return f[0], normTTY(f[1]), host, true
}

// parseLoadAvg pulls the 1-minute figure out of `sysctl -n vm.loadavg`
// ("{ 1.23 1.10 1.00 }").
func parseLoadAvg(b []byte) string {
	for _, f := range strings.Fields(string(b)) {
		if _, err := strconv.ParseFloat(f, 64); err == nil {
			return f
		}
	}
	return "?"
}
