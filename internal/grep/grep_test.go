package grep

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// writeLines writes JSONL content to a path, creating parent dirs.
func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, ln := range lines {
		body += ln + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// synthHome builds a tiny but realistic synthetic HOME: one Claude session and
// one Codex rollout, both with known message text.
func synthHome(t *testing.T) string {
	home := t.TempDir()
	ts := "2026-07-01T10:00:00.000Z"

	claude := filepath.Join(home, ".claude", "projects", "-home-user-work-api", "sess-abc.jsonl")
	writeLines(t, claude,
		`{"type":"user","timestamp":"`+ts+`","sessionId":"sess-abc","cwd":"/home/user/work/api","message":{"role":"user","content":"fix the flaky checkout test please"}}`,
		`{"type":"assistant","timestamp":"`+ts+`","message":{"role":"assistant","content":[{"type":"text","text":"I rebased the branch and fixed it"},{"type":"tool_use","name":"Bash","input":{"command":"git rebase main"}}]}}`,
	)

	codex := filepath.Join(home, ".codex", "sessions", "2026", "07", "01", "rollout-2026-07-01T09-00-00-019xyz.jsonl")
	writeLines(t, codex,
		`{"type":"session_meta","timestamp":"`+ts+`","payload":{"id":"019xyz","cwd":"/home/user/work/db"}}`,
		`{"type":"event_msg","timestamp":"`+ts+`","payload":{"type":"user_message","message":"investigate the memory leak"}}`,
		`{"type":"event_msg","timestamp":"`+ts+`","payload":{"type":"agent_message","message":"found the leak in the pool"}}`,
	)
	return home
}

func search(t *testing.T, home, pat string, mut func(*Options)) Result {
	t.Helper()
	opt := Options{Home: home, Pattern: regexp.MustCompile(pat), Now: time.Now().Unix()}
	if mut != nil {
		mut(&opt)
	}
	return Search(opt)
}

func TestSearchBasic(t *testing.T) {
	home := synthHome(t)

	res := search(t, home, "flaky", nil)
	if len(res.Hits) != 1 {
		t.Fatalf("flaky: %d hits, want 1", len(res.Hits))
	}
	h := res.Hits[0]
	if h.Agent != "claude" || h.Cwd != "/home/user/work/api" || h.Matches != 1 {
		t.Errorf("hit = %+v", h)
	}
	if h.Title != "fix the flaky checkout test please" {
		t.Errorf("title = %q", h.Title)
	}
	if h.Snippet == "" || h.Resume == "" {
		t.Errorf("missing snippet/resume: %+v", h)
	}
	if h.Resume != "cd /home/user/work/api && claude --resume sess-abc" {
		t.Errorf("resume = %q", h.Resume)
	}

	// codex message text, two occurrences (user + agent)
	if res := search(t, home, "leak", nil); len(res.Hits) != 1 || res.Hits[0].Matches != 2 {
		t.Fatalf("leak: %+v", res.Hits)
	}
}

func TestSearchRole(t *testing.T) {
	home := synthHome(t)
	// "rebased" only appears in an assistant message
	if res := search(t, home, "rebased", func(o *Options) { o.Role = "assistant" }); len(res.Hits) != 1 {
		t.Fatalf("assistant role: %d hits, want 1", len(res.Hits))
	}
	if res := search(t, home, "rebased", func(o *Options) { o.Role = "user" }); len(res.Hits) != 0 {
		t.Fatalf("user role should not match assistant text: %+v", res.Hits)
	}
}

func TestSearchTools(t *testing.T) {
	home := synthHome(t)
	// the git command lives in a tool_use payload, off by default
	if res := search(t, home, "git rebase", nil); len(res.Hits) != 0 {
		t.Fatalf("tool payload matched without --tools: %+v", res.Hits)
	}
	if res := search(t, home, "git rebase", func(o *Options) { o.Tools = true }); len(res.Hits) != 1 {
		t.Fatalf("--tools did not reach the tool payload: %+v", res.Hits)
	}
}

func TestSearchProjectAndSince(t *testing.T) {
	home := synthHome(t)
	// both sessions mention "the"; project filter narrows to the api cwd
	if res := search(t, home, "the", func(o *Options) { o.Project = "api" }); len(res.Hits) != 1 || res.Hits[0].Cwd != "/home/user/work/api" {
		t.Fatalf("project filter: %+v", res.Hits)
	}
	// a future cutoff excludes everything (mtime is just now)
	future := time.Now().Unix() + 3600
	if res := search(t, home, "the", func(o *Options) { o.Since = future }); len(res.Hits) != 0 {
		t.Fatalf("since cutoff not applied: %+v", res.Hits)
	}
}

func TestSearchMaxTruncates(t *testing.T) {
	home := synthHome(t)
	// "the" matches both sessions; -n 1 stops after the first, flags truncated
	res := search(t, home, "the", func(o *Options) { o.Max = 1 })
	if len(res.Hits) != 1 || !res.Truncated {
		t.Fatalf("max/truncate: hits=%d truncated=%v", len(res.Hits), res.Truncated)
	}
}
