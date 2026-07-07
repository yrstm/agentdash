package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isoAt renders an epoch as the timestamp string the transcripts carry.
func isoAt(now, agoSecs int64) string {
	return time.Unix(now-agoSecs, 0).UTC().Format(time.RFC3339)
}

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func claudeUsage(ts, id, model string, in, read, create, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":"%s","message":{"id":"%s","model":"%s","usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":%d}}}`,
		ts, id, model, in, read, create, out)
}

func TestCollectWindows(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).Unix()

	proj := filepath.Join(home, ".claude", "projects", "-home-user-api")
	// session A: opus, cwd /home/user/api
	write(t, filepath.Join(proj, "a.jsonl"),
		`{"type":"user","timestamp":"`+isoAt(now, 600)+`","cwd":"/home/user/api","message":{"role":"user","content":"add pagination"}}`,
		claudeUsage(isoAt(now, 600), "m1", "claude-opus-4-8", 100, 900, 0, 50),    // 5h+burn: in 1000 out 50
		claudeUsage(isoAt(now, 600), "m1", "claude-opus-4-8", 100, 900, 0, 50),    // duplicate id, ignored
		claudeUsage(isoAt(now, 7200), "m2", "claude-opus-4-8", 500, 0, 500, 20),   // 5h, not burn: in 1000 out 20
		claudeUsage(isoAt(now, 8*86400), "m9", "claude-opus-4-8", 999, 0, 0, 999), // older than 7d, excluded
	)
	// session B: a subagent in the same project
	write(t, filepath.Join(proj, "agent-x.jsonl"),
		claudeUsage(isoAt(now, 600), "s1", "claude-opus-4-8", 200, 0, 0, 10), // 5h+burn
	)
	// codex session, 3d ago (7d only)
	write(t, filepath.Join(home, ".codex", "sessions", "2026", "c.jsonl"),
		`{"type":"session_meta","timestamp":"`+isoAt(now, 3*86400)+`","payload":{"cwd":"/home/user/db"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-5-codex"}}`,
		`{"type":"event_msg","timestamp":"`+isoAt(now, 3*86400)+`","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":300,"cached_input_tokens":0,"output_tokens":30}}}}`,
	)

	rep := Collect(Options{Home: home, Now: now})

	// m1 (1000/50) + m2 (1000/20) + subagent (200/10) = 2000+2000+210... in: 1000+1000+200=2200, out 50+20+10=80
	if rep.Total5h != 2200+80 {
		t.Errorf("Total5h = %d, want %d", rep.Total5h, 2200+80)
	}
	// 7d adds the codex 330; the 8d-old claude line is excluded
	if rep.Total7d != 2280+330 {
		t.Errorf("Total7d = %d, want %d", rep.Total7d, 2280+330)
	}
	// burn: only the two now-600s events (m1 1050 + subagent 210) inside 30m
	if got := rep.BurnPerMin; got != float64(1050+210)/30 {
		t.Errorf("BurnPerMin = %v, want %v", got, float64(1260)/30)
	}

	// model split: opus in5h 2200 (1000+1000+200) out5h 80; codex only in 7d
	var opus, codex *ModelUse
	for i := range rep.Models {
		switch rep.Models[i].Model {
		case "opus-4-8":
			opus = &rep.Models[i]
		case "gpt-5-codex":
			codex = &rep.Models[i]
		}
	}
	if opus == nil || opus.In5h != 2200 || opus.Out5h != 80 {
		t.Errorf("opus = %+v", opus)
	}
	if codex == nil || codex.In7d != 300 || codex.Out5h != 0 {
		t.Errorf("codex = %+v", codex)
	}

	// attribution: the subagent row is flagged; shares sum to ~100
	var sub *SessionUse
	var shareSum float64
	for i := range rep.Sessions {
		shareSum += rep.Sessions[i].SharePct
		if rep.Sessions[i].IsSubagent {
			sub = &rep.Sessions[i]
		}
	}
	if sub == nil || sub.In != 200 {
		t.Errorf("subagent session = %+v", sub)
	}
	if shareSum < 99.9 || shareSum > 100.1 {
		t.Errorf("shares sum to %v, want ~100", shareSum)
	}

	// cache hit ratio for the api project: read 900 / (900 + 500 creation)
	var api *ProjectCache
	for i := range rep.Projects {
		if rep.Projects[i].Project == "/home/user/api" {
			api = &rep.Projects[i]
		}
	}
	if api == nil {
		t.Fatalf("no cache stats for /home/user/api: %+v", rep.Projects)
	}
	if api.CacheRead != 900 || api.CacheCreation != 500 {
		t.Errorf("cache = %+v", api)
	}
	if r := api.HitRatio; r < 0.64 || r > 0.65 {
		t.Errorf("hit ratio = %v, want ~0.643", r)
	}
}

func TestMessageDedupeSpansFiles(t *testing.T) {
	// Resuming/forking a session copies prior assistant messages (same id,
	// same usage) into a new session file; the copy must not double-count
	// into the window totals.
	home := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).Unix()
	proj := filepath.Join(home, ".claude", "projects", "-home-user-api")
	write(t, filepath.Join(proj, "a.jsonl"),
		claudeUsage(isoAt(now, 600), "m1", "claude-opus-4-8", 100, 0, 0, 50),
	)
	write(t, filepath.Join(proj, "b.jsonl"), // the resumed session's copy
		claudeUsage(isoAt(now, 600), "m1", "claude-opus-4-8", 100, 0, 0, 50),
		claudeUsage(isoAt(now, 300), "m2", "claude-opus-4-8", 40, 0, 0, 10),
	)
	rep := Collect(Options{Home: home, Now: now})
	if rep.Total5h != 150+50 { // m1 once (150) + m2 (50)
		t.Errorf("Total5h = %d, want %d (m1 counted once across files)", rep.Total5h, 200)
	}
}

func TestProjectionAndCacheDrop(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).Unix()
	proj := filepath.Join(home, ".claude", "projects", "-p")

	// last day: poor cache (read 2k of 40k). prior days: good cache (read 38k of 40k).
	write(t, filepath.Join(proj, "s.jsonl"),
		claudeUsage(isoAt(now, 600), "r1", "claude-opus-4-8", 0, 2000, 38000, 100),     // recent (& burn), ratio 0.05
		claudeUsage(isoAt(now, 3*86400), "r2", "claude-opus-4-8", 0, 38000, 2000, 100), // prior, ratio 0.95
	)

	rep := Collect(Options{Home: home, Now: now, Limit: 1_000_000})

	// projection: limit 1M, 5h total 40100 (r1 in=40000 out=100), burning -> fills in some hours
	if rep.ProjFillSecs <= 0 {
		t.Errorf("expected a positive projection with a limit and burn, got %d", rep.ProjFillSecs)
	}

	if len(rep.Projects) != 1 || !rep.Projects[0].Dropped {
		t.Fatalf("expected a flagged cache drop: %+v", rep.Projects)
	}
	p := rep.Projects[0]
	if p.RecentRatio > 0.1 || p.PriorRatio < 0.9 {
		t.Errorf("ratios recent=%v prior=%v, want ~0.05 / ~0.95", p.RecentRatio, p.PriorRatio)
	}
}
