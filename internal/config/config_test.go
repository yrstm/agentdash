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

// TestScanTrackedThroughSymlink reproduces the macOS /var -> /private/var class
// on Linux: git resolves the repo toplevel through a symlink while the scanned
// project path keeps the symlink spelling. The git-tracked lookup must
// canonicalize both sides (§1b.6), and Item.Path must stay as-discovered.
func TestScanTrackedThroughSymlink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "CLAUDE.md"), []byte("# rules\nUse tabs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", real}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "CLAUDE.md")
	run("commit", "-q", "-m", "add")

	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// Scan via the symlink path: git resolves the toplevel to `real`, but the
	// item path keeps the `link` spelling.
	res := Scan(link, base, false)
	linkPath := filepath.Join(link, "CLAUDE.md")
	var found *Item
	for i := range res.Items {
		if res.Items[i].Path == linkPath {
			found = &res.Items[i]
		}
	}
	if found == nil {
		t.Fatalf("CLAUDE.md not inventoried via the symlink path: %+v", res.Items)
	}
	if !found.Tracked {
		t.Error("committed CLAUDE.md should be tracked even when scanned through a symlink")
	}
	// Item.Path stays as-discovered (the symlink spelling), never resolved.
	if found.Path != linkPath {
		t.Errorf("Item.Path = %q, want the as-discovered %q (must not be canonicalized)", found.Path, linkPath)
	}
}
