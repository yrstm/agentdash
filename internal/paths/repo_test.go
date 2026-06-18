package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoRoot(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	child := filepath.Join(repo, "service")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := RepoRoot(child); got != repo {
		t.Fatalf("RepoRoot(%q) = %q, want %q", child, got, repo)
	}
	if got := RepoRoot(""); got != "" {
		t.Fatalf("RepoRoot empty = %q, want empty", got)
	}
}
