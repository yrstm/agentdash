package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrstm/agentdash/internal/board"
)

// names extracts "<event>:<pid>" for every fired event, for terse asserts.
func names(evs []hookEvent) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.name+":"+itoa(e.row.PID))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func boardOf(rows ...board.Row) *board.Board { return &board.Board{Rows: rows} }

func row(pid int, status string, need bool) board.Row {
	return board.Row{PID: pid, Kind: "claude", Status: status, Need: need}
}

func TestDetectHooks(t *testing.T) {
	both := Hooks{OnNeedsYou: "n", OnStuck: "s"}

	cases := []struct {
		name string
		h    Hooks
		prev map[int]string
		nb   *board.Board
		want []string
	}{
		{
			name: "first tick never fires",
			h:    both,
			prev: nil,
			nb:   boardOf(row(1, "waiting", true)),
			want: nil,
		},
		{
			name: "working to waiting fires needs_you",
			h:    both,
			prev: map[int]string{1: "working"},
			nb:   boardOf(row(1, "waiting", true)),
			want: []string{"needs_you:1"},
		},
		{
			name: "persisting waiting does not re-fire",
			h:    both,
			prev: map[int]string{1: "waiting"},
			nb:   boardOf(row(1, "waiting", true)),
			want: nil,
		},
		{
			name: "working to stuck fires both (stuck is a needs-you state)",
			h:    both,
			prev: map[int]string{1: "working"},
			nb:   boardOf(row(1, "stuck?", true)),
			want: []string{"needs_you:1", "stuck:1"},
		},
		{
			name: "waiting to stuck fires only stuck (already needed you)",
			h:    both,
			prev: map[int]string{1: "waiting"},
			nb:   boardOf(row(1, "stuck?", true)),
			want: []string{"stuck:1"},
		},
		{
			name: "only on-stuck configured: a plain wait fires nothing",
			h:    Hooks{OnStuck: "s"},
			prev: map[int]string{1: "working"},
			nb:   boardOf(row(1, "waiting", true)),
			want: nil,
		},
		{
			name: "new pid (no prior state) never fires",
			h:    both,
			prev: map[int]string{1: "working"},
			nb:   boardOf(row(2, "waiting", true)),
			want: nil,
		},
		{
			name: "no hooks configured fires nothing",
			h:    Hooks{},
			prev: map[int]string{1: "working"},
			nb:   boardOf(row(1, "stuck?", true)),
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := names(detectHooks(tc.h, tc.prev, tc.nb))
			if !equal(got, tc.want) {
				t.Fatalf("detectHooks = %v, want %v", got, tc.want)
			}
		})
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBuildPayload checks the stdin contract: an event/ts/attached envelope
// around a schema_version 1 agent object.
func TestBuildPayload(t *testing.T) {
	r := board.Row{PID: 42, Kind: "codex", Glyph: "●", Status: "waiting",
		Need: true, Task: "flaky test", Cwd: "/home/u/api"}
	b, err := buildPayload(hookEvent{name: "needs_you", row: r}, 1700000000)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Event    string          `json:"event"`
		TS       int64           `json:"ts"`
		Attached bool            `json:"attached"`
		Agent    json.RawMessage `json:"agent"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("payload is not valid json: %v", err)
	}
	if got.Event != "needs_you" || got.TS != 1700000000 || !got.Attached {
		t.Fatalf("envelope wrong: %+v", got)
	}
	var agent struct {
		PID   int    `json:"pid"`
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}
	if err := json.Unmarshal(got.Agent, &agent); err != nil {
		t.Fatalf("agent is not valid json: %v", err)
	}
	if agent.PID != 42 || agent.Agent != "codex" || agent.Task != "flaky test" {
		t.Fatalf("agent fields wrong: %+v", agent)
	}
}

// TestRunHookExec exercises the real exec path: a shell command that reads
// the payload on stdin and the env vars, and writes them out for the test to
// read back. runHook is fire-and-forget (goroutine), so we poll briefly.
func TestRunHookExec(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "fired")
	r := board.Row{PID: 99, Kind: "claude", Status: "stuck?", Need: true, Task: "merge", Cwd: "/home/user/proj"}
	runHook(hookEvent{
		name: "stuck",
		cmd: "{ printf '%s|%s|%s|%s|%s|' \"$AGENTDASH_EVENT\" \"$AGENTDASH_PID\" " +
			"\"$AGENTDASH_AGENT\" \"$AGENTDASH_CWD\" \"$AGENTDASH_STATUS\"; cat; } > " + out,
		row: r,
	})

	var data []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			data = b
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if data == nil {
		t.Fatal("hook command never ran")
	}
	got := string(data)
	const wantPrefix = "stuck|99|claude|/home/user/proj|stuck?|"
	if len(got) < len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("env vars not passed: %q", got)
	}
	var env struct {
		Event string `json:"event"`
		Agent struct {
			PID int `json:"pid"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(got[len(wantPrefix):]), &env); err != nil {
		t.Fatalf("stdin payload not valid json: %v (%q)", err, got)
	}
	if env.Event != "stuck" || env.Agent.PID != 99 {
		t.Fatalf("stdin payload wrong: %+v", env)
	}
}

func TestStatusMap(t *testing.T) {
	if statusMap(nil) != nil {
		t.Fatal("nil board should map to nil")
	}
	m := statusMap(boardOf(row(1, "working", false), row(2, "idle", false)))
	if m[1] != "working" || m[2] != "idle" || len(m) != 2 {
		t.Fatalf("statusMap = %v", m)
	}
}

func TestDebounceHooks(t *testing.T) {
	ev := func(name string, pid int) hookEvent {
		return hookEvent{name: name, row: board.Row{PID: pid}}
	}
	last := map[hookKey]int64{}

	// first fire for a (pid,event) always passes and records the time
	if got := debounceHooks([]hookEvent{ev("needs_you", 1)}, last, 1000); len(got) != 1 {
		t.Fatalf("first fire dropped: %v", names(got))
	}
	// a re-cross of the same edge within the window is suppressed
	if got := debounceHooks([]hookEvent{ev("needs_you", 1)}, last, 1000+59); len(got) != 0 {
		t.Fatalf("re-fire within 60s not suppressed: %v", names(got))
	}
	// once the window elapses it fires again
	if got := debounceHooks([]hookEvent{ev("needs_you", 1)}, last, 1000+60); len(got) != 1 {
		t.Fatalf("re-fire after 60s suppressed: %v", names(got))
	}
	// simultaneous distinct events for one session both fire (working->stuck?
	// raises needs_you and stuck at the same instant)
	got := debounceHooks([]hookEvent{ev("needs_you", 2), ev("stuck", 2)}, last, 2000)
	if len(got) != 2 {
		t.Fatalf("simultaneous distinct events collapsed: %v", names(got))
	}
	// a different session is tracked independently
	if got := debounceHooks([]hookEvent{ev("needs_you", 3)}, last, 2000); len(got) != 1 {
		t.Fatalf("distinct session suppressed: %v", names(got))
	}
}
