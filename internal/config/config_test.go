package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestScanEnrichesColumns(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	// a project CLAUDE.md of a known size -> known token estimate
	body := make([]byte, 400) // 400 bytes -> ~100 tokens
	for i := range body {
		body[i] = 'x'
	}
	body[0], body[1] = '#', ' ' // a heading so firstMeaningfulLine has something
	claudePath := filepath.Join(project, "CLAUDE.md")
	if err := os.WriteFile(claudePath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	res := Scan(project, home, false)
	var instr *Item
	for i := range res.Items {
		if res.Items[i].Path == claudePath {
			instr = &res.Items[i]
		}
	}
	if instr == nil {
		t.Fatalf("project CLAUDE.md not inventoried: %+v", res.Items)
	}
	if instr.Bytes != 400 || instr.TokenEst != 100 {
		t.Errorf("size/token = %d/%d, want 400/100", instr.Bytes, instr.TokenEst)
	}
	if instr.Modified == 0 {
		t.Error("modified not set")
	}
	if instr.Tracked {
		t.Error("file should not be tracked (not a git repo)")
	}
	if res.AlwaysLoadedTokens != 100 {
		t.Errorf("always-loaded total = %d, want 100", res.AlwaysLoadedTokens)
	}

	// once committed to a repo, Tracked flips true
	if _, err := exec.LookPath("git"); err == nil {
		run := func(args ...string) {
			c := exec.Command("git", append([]string{"-C", project}, args...)...)
			c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v (%s)", args, err, out)
			}
		}
		run("init", "-q")
		run("add", "CLAUDE.md")
		run("commit", "-q", "-m", "add")
		res2 := Scan(project, home, false)
		for _, it := range res2.Items {
			if it.Path == claudePath && !it.Tracked {
				t.Error("committed CLAUDE.md should be tracked")
			}
		}
	}
}
