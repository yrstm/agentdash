package procs

import (
	"reflect"
	"testing"
)

// These tests exercise the macOS collector's parsing and classification against
// canned `ps`/`lsof`/`who` output. They carry no build tag on purpose: the
// logic lives in psparse.go (which exec's nothing), so it is covered on Linux
// CI as well as on the macOS runner, per the A0 acceptance split.

func TestParseEtime(t *testing.T) {
	for in, want := range map[string]int64{
		"05":         5,                        // ss only (unlikely, but fail soft)
		"05:12":      5*60 + 12,                // mm:ss
		"01:02:03":   1*3600 + 2*60 + 3,        // hh:mm:ss
		"3-01:02:03": 3*86400 + 3600 + 120 + 3, // dd-hh:mm:ss
		"":           0,
		"garbage":    0,
	} {
		if got := parseEtime(in); got != want {
			t.Errorf("parseEtime(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNormTTY(t *testing.T) {
	for in, want := range map[string]string{
		"??":           "?",
		"?":            "?",
		"-":            "?",
		"":             "?",
		"s001":         "ttys001",
		"ttys001":      "ttys001",
		"/dev/ttys002": "ttys002",
		"console":      "console",
	} {
		if got := normTTY(in); got != want {
			t.Errorf("normTTY(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParsePSTableAndAgents(t *testing.T) {
	// pid ppid state tty etime command — padded like real ps -o ...=
	table := "" +
		"    1     0 Ss   ??           10-00:00:00 /sbin/launchd\n" +
		"  501     1 S    ??              1:02:03 /usr/libexec/secd\n" +
		" 1234   501 S+   s001              05:12 node /usr/bin/codex resume\n" + // same-kind launcher
		" 1250  1234 S+   s001              05:10 codex resume\n" + // real codex it spawned
		" 1300   501 S+   s002              00:30 claude --resume abc\n" +
		" 1400   501 S    ??              2-03:00:00 pgrep -fl claude\n" + // excluded helper
		" 9999   501 R+   s003              00:05 ps -A -ww -o pid=\n" // our own pid, excluded

	rows := parsePSTable([]byte(table))
	if len(rows) != 7 {
		t.Fatalf("parsePSTable rows = %d, want 7", len(rows))
	}
	// command internal spacing preserved; tty normalized; etime parsed
	if rows[2].command != "node /usr/bin/codex resume" || rows[2].tty != "ttys001" || rows[2].etime != 312 {
		t.Errorf("row 2 = %+v", rows[2])
	}
	if rows[0].tty != "?" {
		t.Errorf("launchd tty = %q, want ?", rows[0].tty)
	}

	const now = 1_000_000
	agents := agentsFromPS(rows, now, 9999)
	got := map[int]Proc{}
	for _, a := range agents {
		got[a.PID] = a
	}
	if len(agents) != 2 {
		t.Fatalf("agents = %d (%+v), want 2 (codex 1250, claude 1300)", len(agents), agents)
	}
	if _, dropped := got[1234]; !dropped {
		// present in map means NOT dropped
	} else {
		t.Error("same-kind launcher pid 1234 should be dropped")
	}
	c := got[1250]
	if c.Kind != "codex" || c.Uptime != 310 || c.Start != now-310 {
		t.Errorf("codex proc = %+v, want kind codex uptime 310 start %d", c, now-310)
	}
	if got[1300].Kind != "claude" || got[1300].TTY != "ttys002" {
		t.Errorf("claude proc = %+v", got[1300])
	}
	if _, ok := got[1400]; ok {
		t.Error("pgrep helper should be excluded")
	}
	if _, ok := got[9999]; ok {
		t.Error("self pid should be excluded")
	}
}

func TestZombiesAndOrphansFromPS(t *testing.T) {
	rows := parsePSTable([]byte("" +
		" 100     1 S    ??       00:10 bash -c claude-runner\n" + // orphan: headless wrapper, no child
		" 101     1 S    ??       00:10 nohup codex run\n" + // orphan
		" 200     1 S    ??       00:10 bash -c agent-loop\n" + // has a child → not orphan
		" 201   200 S    s000     00:09 claude --resume x\n" +
		" 300     1 S    s004     00:10 bash -c interactive\n" + // has a tty → not orphan
		" 400     1 S    ??       00:10 node server.js\n" + // not a wrapper → not orphan
		" 500     1 Z    ??       00:10 (oldworker)\n")) // zombie

	wantOrph := []string{"100 bash -c claude-runner", "101 nohup codex run"}
	if got := orphansFromPS(rows, 1); !reflect.DeepEqual(got, wantOrph) {
		t.Errorf("orphansFromPS = %v, want %v", got, wantOrph)
	}
	wantZomb := []string{"500 (oldworker) <defunct>"}
	if got := zombiesFromPS(rows); !reflect.DeepEqual(got, wantZomb) {
		t.Errorf("zombiesFromPS = %v, want %v", got, wantZomb)
	}
}

func TestParseLsofListeners(t *testing.T) {
	// -Fpn: p<pid> then one n<name> per listening socket
	out := parseLsofListeners([]byte("" +
		"p1234\n" +
		"n*:8080\n" +
		"n127.0.0.1:3000\n" +
		"p5678\n" +
		"n[::1]:8080\n" + // duplicate port, first pid wins
		"n192.168.1.2:5432\n"))
	want := map[int]int{8080: 1234, 3000: 1234, 5432: 5678}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("parseLsofListeners = %v, want %v", out, want)
	}
}

func TestParseFpnPairsAndOpenFDs(t *testing.T) {
	cwds := parseFpnPairs([]byte("p1234\nfcwd\nn/Users/user/proj\np5678\nn/Users/user/other\n"))
	if cwds[1234] != "/Users/user/proj" || cwds[5678] != "/Users/user/other" {
		t.Errorf("parseFpnPairs = %v", cwds)
	}
	fds := parseOpenFDs([]byte("p1234\nn/Users/user/.claude/projects/x/sess.jsonl\nnpipe:->0x1\nn/dev/null\n"))
	want := []string{"/Users/user/.claude/projects/x/sess.jsonl", "/dev/null"}
	if !reflect.DeepEqual(fds, want) {
		t.Errorf("parseOpenFDs = %v, want %v", fds, want)
	}
}

func TestParseWhoLineAndNewestOnTTY(t *testing.T) {
	user, tty, host, ok := parseWhoLine("user     ttys002  Jun 30 12:34 (10.0.0.5)")
	if !ok || user != "user" || tty != "ttys002" || host != "10.0.0.5" {
		t.Errorf("parseWhoLine remote = %q/%q/%q ok=%v", user, tty, host, ok)
	}
	_, _, host, _ = parseWhoLine("user  console  Jun 30 09:00 ")
	if host != "" {
		t.Errorf("local login host = %q, want empty", host)
	}
	if _, _, _, ok := parseWhoLine(""); ok {
		t.Error("blank who line accepted")
	}

	rows := parsePSTable([]byte("" +
		" 10   1 S  s002  02:00 -bash\n" +
		" 20  10 S+ s002  00:30 vim notes.md\n")) // newer (smaller etime) → the WHAT
	what, live := newestOnTTY("ttys002", rows)
	if !live || what != "vim notes.md" {
		t.Errorf("newestOnTTY = %q live=%v, want 'vim notes.md' true", what, live)
	}
	if _, live := newestOnTTY("ttys999", rows); live {
		t.Error("newestOnTTY on an unused tty reported live")
	}
}

func TestParseLoadAvgAndComm(t *testing.T) {
	if got := parseLoadAvg([]byte("{ 1.52 1.10 1.00 }")); got != "1.52" {
		t.Errorf("parseLoadAvg = %q, want 1.52", got)
	}
	if got := parseLoadAvg([]byte("garbage")); got != "?" {
		t.Errorf("parseLoadAvg(garbage) = %q, want ?", got)
	}
	if got := commOf("/usr/local/bin/claude --resume x"); got != "claude" {
		t.Errorf("commOf = %q, want claude", got)
	}
}
