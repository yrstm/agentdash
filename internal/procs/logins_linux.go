package procs

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// utmpRecord mirrors struct utmp on linux/amd64 (384 bytes).
type utmpRecord struct {
	Type    int16
	_       [2]byte
	Pid     int32
	Line    [32]byte
	ID      [4]byte
	User    [32]byte
	Host    [256]byte
	Exit    [4]byte
	Session int32
	TvSec   int32
	TvUsec  int32
	Addr    [16]byte
	_       [20]byte
}

const userProcess = 7

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// rawLogins reads /var/run/utmp directly; when that fails it falls back to
// `w -h` (documented fallback for distros with odd utmp layouts). The
// excluded set drops ttys already shown as agent rows. The shared Logins
// wrapper adds the tmux tie-in.
func rawLogins(now int64, exclude map[string]bool) []Login {
	out := utmpLogins(now, exclude)
	if out == nil {
		out = wLogins(exclude)
	}
	return out
}

func utmpLogins(now int64, exclude map[string]bool) []Login {
	b, err := os.ReadFile("/var/run/utmp")
	if err != nil {
		return nil
	}
	recSize := binary.Size(utmpRecord{})
	if recSize <= 0 || len(b)%recSize != 0 {
		return nil
	}
	var out []Login
	for off := 0; off+recSize <= len(b); off += recSize {
		var r utmpRecord
		if binary.Read(bytes.NewReader(b[off:off+recSize]), binary.LittleEndian, &r) != nil {
			return nil
		}
		if r.Type != userProcess {
			continue
		}
		tty := cstr(r.Line[:])
		if tty == "" || exclude[tty] {
			continue
		}
		what, live := ttyWhat(tty)
		out = append(out, Login{
			User:  cstr(r.User[:]),
			TTY:   tty,
			From:  cstr(r.Host[:]),
			Idle:  ttyIdle(tty, now),
			What:  what,
			Stale: !live,
		})
	}
	if out == nil {
		out = []Login{} // parsed fine, genuinely empty: don't trigger the fallback
	}
	return out
}

// ttyIdle derives idle time the way w(1) does: atime of the tty device.
func ttyIdle(tty string, now int64) string {
	st, err := os.Stat("/dev/" + tty)
	if err != nil {
		return "?"
	}
	idle := now - st.ModTime().Unix()
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		idle = now - sys.Atim.Sec
	}
	if idle < 0 {
		idle = 0
	}
	return agoCompact(idle)
}

// ttyWhat picks the newest process holding the tty as its controlling
// terminal, standing in for the WHAT column of w(1). The bool is false when
// no live process owns the tty — a stale utmp record left by a dropped login.
func ttyWhat(tty string) (string, bool) {
	root := Root()
	newest, args := int64(-1), ""
	live := false
	btime := BootTime()
	for _, pid := range listPIDs(root) {
		p, ok := readPID(root, pid, btime, 0)
		if !ok || p.TTY != tty {
			continue
		}
		live = true
		if p.Start > newest {
			newest, args = p.Start, p.Args
		}
	}
	return args, live
}

func wLogins(exclude map[string]bool) []Login {
	out, err := exec.Command("w", "-h").Output()
	if err != nil {
		return nil
	}
	var res []Login
	for _, ln := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.Fields(ln)
		if len(f) < 5 || exclude[f[1]] {
			continue
		}
		if f[1] == "?" || f[4] == "?" {
			continue
		}
		what := ""
		if len(f) >= 8 {
			what = strings.Join(f[7:], " ")
		}
		if staleWhat(what) {
			continue
		}
		res = append(res, Login{User: f[0], TTY: f[1], From: f[2], Idle: f[4], What: what})
	}
	return res
}
