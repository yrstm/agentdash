package filehist

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryTrackedWithAttribution(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	home := t.TempDir()
	claudeMd := filepath.Join(repo, "CLAUDE.md")

	c1 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	c2 := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)

	// deterministic commits: fixed author + fixed author/committer dates
	commit := func(when time.Time) {
		c := exec.Command("git", "-C", repo, "commit", "-q", "-m", "edit")
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Fixture Bot", "GIT_AUTHOR_EMAIL=bot@example",
			"GIT_COMMITTER_NAME=Fixture Bot", "GIT_COMMITTER_EMAIL=bot@example",
			"GIT_AUTHOR_DATE="+when.Format(time.RFC3339),
			"GIT_COMMITTER_DATE="+when.Format(time.RFC3339))
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("commit: %v (%s)", err, out)
		}
	}
	git := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q")
	write(t, claudeMd, "# rules\nUse tabs.\n")
	git("add", "CLAUDE.md")
	commit(c1)
	write(t, claudeMd, "# rules\nUse tabs.\nPrefer rebase over merge.\n")
	git("add", "CLAUDE.md")
	commit(c2)

	// a transcript with an Edit of CLAUDE.md ~1 min after the second commit
	editTS := c2.Add(60 * time.Second).Format(time.RFC3339)
	proj := filepath.Join(home, ".claude", "projects", "-repo")
	write(t, filepath.Join(proj, "s1.jsonl"),
		`{"type":"user","timestamp":"`+c2.Format(time.RFC3339)+`","sessionId":"s1","message":{"content":"tighten the merge rule"}}`+"\n"+
			fmt.Sprintf(`{"type":"assistant","timestamp":"%s","sessionId":"s1","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"%s"}}]}}`, editTS, claudeMd)+"\n")

	lg := History(claudeMd, home, c2.Add(time.Hour).Unix())

	if !lg.Tracked {
		t.Fatal("CLAUDE.md should be git-tracked")
	}
	if len(lg.Changes) != 2 {
		t.Fatalf("changes = %d, want 2: %+v", len(lg.Changes), lg.Changes)
	}
	// newest last
	if lg.Changes[0].TS != c1.Unix() || lg.Changes[1].TS != c2.Unix() {
		t.Errorf("timeline order = %d,%d want %d,%d", lg.Changes[0].TS, lg.Changes[1].TS, c1.Unix(), c2.Unix())
	}
	if lg.Changes[1].Added < 1 {
		t.Errorf("second commit should add >=1 line: %+v", lg.Changes[1])
	}
	if lg.Changes[1].Author != "Fixture Bot" {
		t.Errorf("author = %q, want Fixture Bot", lg.Changes[1].Author)
	}
	// c2 has a nearby Edit -> attributed to the session; c1 does not
	if !strings.Contains(lg.Changes[1].Attribution, "by claude session") || !strings.Contains(lg.Changes[1].Attribution, "tighten the merge rule") {
		t.Errorf("c2 attribution = %q, want an agent-session attribution", lg.Changes[1].Attribution)
	}
	if !strings.Contains(lg.Changes[0].Attribution, "outside any recorded agent session") {
		t.Errorf("c1 attribution = %q, want 'outside any recorded agent session'", lg.Changes[0].Attribution)
	}
	// Path stays as-discovered.
	if lg.Path != claudeMd {
		t.Errorf("Path = %q, want as-discovered %q", lg.Path, claudeMd)
	}
}

func TestHistoryUntrackedSnapshots(t *testing.T) {
	home := t.TempDir()
	// a file that is not in any git repo
	file := filepath.Join(home, ".claude", "CLAUDE.md")
	write(t, file, "global rules\n")

	// a memory snapshot log with two events for the file
	logPath := filepath.Join(home, "mem.jsonl")
	t.Setenv("AGENTDASH_MEMORY_LOG", logPath)
	ev := func(ts string, bytes int, sha string) string {
		return fmt.Sprintf(`{"ts":"%s","project":"%s","path":"%s","kind":"claude","bytes":%d,"sha256":"%s","mtime":"%s"}`,
			ts, home, file, bytes, sha, ts)
	}
	t1 := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 5, 9, 0, 0, 0, time.UTC)
	write(t, logPath,
		ev(t1.Format(time.RFC3339), 100, "aaaa1111")+"\n"+
			ev(t2.Format(time.RFC3339), 140, "bbbb2222")+"\n")

	lg := History(file, home, t2.Add(time.Hour).Unix())
	if lg.Tracked {
		t.Fatal("file should be untracked")
	}
	if len(lg.Changes) != 2 || lg.Changes[0].Source != "snapshot" {
		t.Fatalf("snapshot changes = %+v", lg.Changes)
	}
	if lg.Changes[0].Excerpt != "first observed" || lg.Changes[1].Bytes != 140 {
		t.Errorf("snapshot detail = %+v", lg.Changes)
	}
	// no transcripts -> all outside any recorded session
	if !strings.Contains(lg.Changes[1].Attribution, "outside any recorded agent session") {
		t.Errorf("attribution = %q", lg.Changes[1].Attribution)
	}
}
