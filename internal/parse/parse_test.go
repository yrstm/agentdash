package parse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tNow = 1767226000 // a little after the fixture timestamps

func scanFile(t *testing.T, src, kind string) (*Entry, *Cache, string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	ent := ScanSession(path, c, kind, tNow)
	if ent == nil {
		t.Fatalf("scan of %s returned nil", src)
	}
	return ent, c, path
}

func eq[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %#v, want %#v", field, got, want)
	}
}

func TestClaudeGolden(t *testing.T) {
	ent, _, path := scanFile(t, "testdata/claude-golden.jsonl", "claude")
	eq(t, "Model", ent.Model, "claude-fable-5")
	eq(t, "In", ent.In, int64(3000))   // msg_01 counted once (dedup), then msg_02
	eq(t, "Out", ent.Out, int64(125))  // 50 + 75
	eq(t, "Ctx", ent.Ctx, int64(2000)) // last request
	eq(t, "LastMid", ent.LastMid, "msg_02")
	eq(t, "LastType", ent.LastType, "assistant")
	eq(t, "Summary", ent.Summary, "checkout fix session")
	eq(t, "TitleUser", ent.TitleUser, `fix the "quoted" test: ünïcode ✓ 中文`)
	// the toolUseResult line at 00:01:00 must not move the human-turn clock
	eq(t, "LastUserTS", ent.LastUserTS, int64(1767225720)) // 00:02:00
	eq(t, "LastText", ent.LastText, "plain string reply")
	eq(t, "LastTool", ent.LastTool, "Bash")
	eq(t, "Activity", ent.Activity, "plain string reply")
	st, _ := os.Stat(path)
	eq(t, "Offset", ent.Offset, st.Size())
	eq(t, "Kind", ent.Kind, "claude")
	eq(t, "V", ent.V, ParserV)
}

func TestCodexGolden(t *testing.T) {
	ent, _, path := scanFile(t, "testdata/codex-golden.jsonl", "codex")
	eq(t, "Model", ent.Model, "gpt-5.5")
	eq(t, "In", ent.In, int64(80000))
	eq(t, "Out", ent.Out, int64(4000))
	eq(t, "Ctx", ent.Ctx, int64(68000))
	eq(t, "Win", ent.Win, int64(272000))
	eq(t, "LastType", ent.LastType, "assistant")
	eq(t, "TitleUser", ent.TitleUser, "port the cron job")
	eq(t, "LastUserTS", ent.LastUserTS, int64(1767225603))
	eq(t, "LastText", ent.LastText, "Queue port done; tests green.")
	eq(t, "Activity", ent.Activity, "Queue port done; tests green.")
	st, _ := os.Stat(path)
	eq(t, "Offset", ent.Offset, st.Size())
}

func TestToolActivity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	line := `{"type":"assistant","message":{"id":"m1","model":"claude-opus-4-8","content":[{"type":"tool_use","name":"Bash","input":{"description":"Run the failing checkout tests","command":"pytest tests"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	ent := ScanSession(path, NewCache(), "claude", tNow)
	eq(t, "Claude LastType", ent.LastType, "tool")
	eq(t, "Claude LastTool", ent.LastTool, "Bash: Run the failing checkout tests")
	eq(t, "Claude Activity", ent.Activity, "Bash: Run the failing checkout tests")

	path = filepath.Join(t.TempDir(), "c.jsonl")
	line = `{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"go test ./...\",\"description\":\"Run Go tests\"}"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	ent = ScanSession(path, NewCache(), "codex", tNow)
	eq(t, "Codex LastType", ent.LastType, "tool")
	eq(t, "Codex LastTool", ent.LastTool, "exec_command: Run Go tests")
	eq(t, "Codex Activity", ent.Activity, "exec_command: Run Go tests")
}

func TestTaskOfPrefersStableWorkNameUnlessLabelled(t *testing.T) {
	ent := &Entry{TitleUser: "first prompt", Summary: "old summary", Activity: "Bash: running tests"}
	eq(t, "stable work name", TaskOf(ent, "/s", nil), "old summary")
	eq(t, "label override", TaskOf(ent, "/s", map[string]string{"/s": "pinned task"}), "pinned task")
	eq(t, "title still summary", TitleOf(ent, "/s", nil), "old summary")
	ent.Summary = ""
	eq(t, "first prompt fallback", TaskOf(ent, "/s", nil), "first prompt")
	ent.TitleUser = ""
	eq(t, "activity fallback", TaskOf(ent, "/s", nil), "Bash: running tests")
}

func TestTitleUserSkipsCommandScaffolding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"user","message":{"content":"<command-name>/clear</command-name> <command-message>clear</command-message>"}}` + "\n" +
		`{"type":"user","message":{"content":"<local-command-stdout>Set model to Fable 5</local-command-stdout>"}}` + "\n" +
		`{"type":"user","message":{"content":"you were working on a task before, keep doing it"}}` + "\n" +
		`{"type":"user","message":{"content":"fix the deploy dashboard"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ent := ScanSession(path, NewCache(), "claude", tNow)
	eq(t, "Claude TitleUser", ent.TitleUser, "fix the deploy dashboard")

	path = filepath.Join(t.TempDir(), "c.jsonl")
	body = `{"type":"event_msg","payload":{"type":"user_message","message":"<environment_context>noise</environment_context>"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"user_message","message":"repair the data sync"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ent = ScanSession(path, NewCache(), "codex", tNow)
	eq(t, "Codex TitleUser", ent.TitleUser, "repair the data sync")
}

func TestTitleFrom(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		// markdown-document openers are instruction content, not prompts —
		// codex embeds AGENTS.md as the rollout's first user message
		{"# AGENTS.md instructions for /code/synth\n\n# AGENTS.md — synth", "", false},
		{"```go\nfunc main() {}\n```", "", false},
		// leading file-drop paths strip down to the prompt that follows
		{"/tmp/drop-0000-example.md - review the notes", "review the notes", true},
		{"/tmp/a-example.md /tmp/b-example.md compare these two", "compare these two", true},
		{"~/notes/plan.md: implement step one", "implement step one", true},
		// a message that is only paths titles nothing
		{"/home/user/dev/scraper", "", false},
		{"/tmp/one.md /tmp/two.md", "", false},
		// paths mid-sentence are untouched; ordinary prompts pass through
		{"fix /code/foo/bar.go please", "fix /code/foo/bar.go please", true},
		{"tell me the total due this week", "tell me the total due this week", true},
		{"1. Find by recency and parse (quickest)", "1. Find by recency and parse (quickest)", true},
		// usableTitle rejections still hold
		{"/clear", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := TitleFrom(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("TitleFrom(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// A codex rollout opens with embedded instructions; the real prompt arrives
// later and must win the title. Same for a claude session opened by a file
// drop: the path strips, the request remains.
func TestTitleUserSkipsInstructionFilesAndDrops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.jsonl")
	body := `{"type":"event_msg","payload":{"type":"user_message","message":"# AGENTS.md instructions for /code/synth"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"user_message","message":"wire the exporter to the new schema"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ent := ScanSession(path, NewCache(), "codex", tNow)
	eq(t, "Codex TitleUser", ent.TitleUser, "wire the exporter to the new schema")

	path = filepath.Join(t.TempDir(), "s.jsonl")
	body = `{"type":"user","message":{"content":"/tmp/drop-1111-example.md - rework the intro section"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ent = ScanSession(path, NewCache(), "claude", tNow)
	eq(t, "Claude TitleUser", ent.TitleUser, "rework the intro section")
}

// The repo-level fixtures are the contract the v1 bats suite asserts
// against; the Go parser must read them identically.
func TestV1RepoFixtures(t *testing.T) {
	ent, _, _ := scanFile(t, "../../tests/fixtures/claude-basic.jsonl", "claude")
	eq(t, "Model", ent.Model, "claude-opus-4-8")
	eq(t, "In", ent.In, int64(1000))
	eq(t, "Out", ent.Out, int64(50))
	eq(t, "Ctx", ent.Ctx, int64(1000))
	eq(t, "TitleUser", ent.TitleUser, "fix the failing checkout test")

	ent, _, _ = scanFile(t, "../../tests/fixtures/codex-rollout.jsonl", "codex")
	eq(t, "Model", ent.Model, "gpt-5.5")
	eq(t, "In", ent.In, int64(80000))
	eq(t, "Win", ent.Win, int64(272000))
}

func TestTruncatedTailWaitsForNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	full := `{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"content":"hello"}}` + "\n"
	partial := `{"type":"assistant","message":{"id":"m1","model":"claude-opus-4-8","usa`
	if err := os.WriteFile(path, []byte(full+partial), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	ent := ScanSession(path, c, "claude", tNow)
	eq(t, "Offset (partial tail unconsumed)", ent.Offset, int64(len(full)))
	eq(t, "TitleUser", ent.TitleUser, "hello")
	eq(t, "Model (partial line not parsed)", ent.Model, "")

	// complete the line: the next scan consumes exactly the rest
	rest := `ge":{"input_tokens":5}}}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(rest); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	ent = ScanSession(path, c, "claude", tNow)
	eq(t, "Model (after completion)", ent.Model, "claude-opus-4-8")
	eq(t, "In", ent.In, int64(5))
	eq(t, "Offset", ent.Offset, int64(len(full)+len(partial)+len(rest)))
	eq(t, "consumed bytes in hist", ent.Hist[len(ent.Hist)-1], int64(len(partial)+len(rest)))
}

func TestOffsetResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	line := func(id string, in, out int) string {
		return fmt.Sprintf(`{"type":"assistant","message":{"id":%q,"model":"claude-opus-4-8","usage":{"input_tokens":%d,"output_tokens":%d}}}`+"\n", id, in, out)
	}
	if err := os.WriteFile(path, []byte(line("m1", 100, 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	ent := ScanSession(path, c, "claude", tNow)
	eq(t, "In after first scan", ent.In, int64(100))

	app := line("m2", 200, 20) + line("m3", 300, 30)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(app); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	ent = ScanSession(path, c, "claude", tNow)
	eq(t, "In accumulates", ent.In, int64(600))
	eq(t, "Out accumulates", ent.Out, int64(60))
	eq(t, "bytes read equals bytes appended", ent.Hist[len(ent.Hist)-1], int64(len(app)))
	st, _ := os.Stat(path)
	eq(t, "Offset", ent.Offset, st.Size())

	// nothing new: a third scan consumes zero
	ent = ScanSession(path, c, "claude", tNow)
	eq(t, "no growth consumes 0", ent.Hist[len(ent.Hist)-1], int64(0))
	eq(t, "In unchanged", ent.In, int64(600))
}

func TestHugeSingleEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	big := strings.Repeat("x", 1_200_000) // >1MB of text in one entry
	line := `{"type":"assistant","message":{"id":"m1","model":"claude-opus-4-8","content":[{"type":"text","text":"` + big + `"}],"usage":{"input_tokens":7}}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	ent := ScanSession(path, NewCache(), "claude", tNow)
	eq(t, "In", ent.In, int64(7))
	eq(t, "LastText truncated to 160 runes", len([]rune(ent.LastText)), 160)
}

func TestResets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","message":{"content":"a"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	ScanSession(path, c, "claude", tNow)

	// kind mismatch resets
	ent := ScanSession(path, c, "codex", tNow)
	eq(t, "kind after mismatch", ent.Kind, "codex")
	eq(t, "TitleUser cleared", ent.TitleUser, "")

	// shrink resets (offset beyond size)
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ent = ScanSession(path, c, "codex", tNow)
	eq(t, "Offset after shrink", ent.Offset, int64(1))

	// parser version bump resets
	c.Entries[path].V = ParserV - 1
	c.Entries[path].TitleUser = "stale"
	ent = ScanSession(path, c, "codex", tNow)
	eq(t, "V", ent.V, ParserV)
	eq(t, "stale state dropped", ent.TitleUser, "")
}

func TestHistKeepsEightSlots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	var ent *Entry
	for i := 0; i < 12; i++ {
		ent = ScanSession(path, c, "claude", tNow)
	}
	eq(t, "hist length", len(ent.Hist), 8)
}

func TestStatusOf(t *testing.T) {
	th := DefaultThresholds
	now := float64(tNow)
	eq(t, "working", StatusOf(&Entry{Mtime: now - 10}, 0, now, th), "working")
	eq(t, "idle", StatusOf(&Entry{Mtime: now - 700}, 0, now, th), "idle")
	eq(t, "waiting", StatusOf(&Entry{Mtime: now - 120, LastType: "assistant"}, 0, now, th), "waiting")
	eq(t, "stuck", StatusOf(&Entry{Mtime: now - 120, LastType: "user"}, 0, now, th), "stuck?")
	eq(t, "busy", StatusOf(&Entry{Mtime: now - 70, LastType: "user"}, 0, now, th), "busy?")
	eq(t, "respawn", StatusOf(&Entry{Mtime: now - 10}, 4, now, th), "respawn ×4")
}

// A mixed-version cache: entries scanned by an older binary must be fully
// rescanned by this one, or a derivation change (titles, model filtering)
// never reaches files that stopped growing. This is the long-running-old-
// watcher case: a stale `agentdash -w` kept rewriting v6 entries that the
// upgraded binary then trusted forever.
func TestOldParserVersionEntryIsRescanned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","message":{"content":"fix the exporter"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	// simulate an old binary's entry: fully scanned, junk title, stale V
	st, _ := os.Stat(path)
	c.Entries[path] = &Entry{Kind: "claude", V: ParserV - 1, Offset: st.Size(),
		TitleUser: "# AGENTS.md instructions for /x"}
	ent := ScanSession(path, c, "claude", tNow)
	eq(t, "V", ent.V, ParserV)
	eq(t, "TitleUser after rescan", ent.TitleUser, "fix the exporter")
}

// system-generated messages carry model "<synthetic>": it must not displace
// the session's real model (a board row read "<syntheti…" in the MODEL column).
func TestSyntheticModelDoesNotDisplaceReal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"assistant","message":{"id":"m1","model":"claude-fable-5","usage":{"input_tokens":10}}}` + "\n" +
		`{"type":"assistant","message":{"id":"m2","model":"<synthetic>","usage":{"input_tokens":5}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ent := ScanSession(path, NewCache(), "claude", tNow)
	eq(t, "Model", ent.Model, "claude-fable-5")

	// a session with only synthetic messages reports no model at all
	path = filepath.Join(t.TempDir(), "s2.jsonl")
	body = `{"type":"assistant","message":{"id":"m1","model":"<synthetic>","usage":{"input_tokens":5}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ent = ScanSession(path, NewCache(), "claude", tNow)
	eq(t, "Model (synthetic only)", ent.Model, "")
}
