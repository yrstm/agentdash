package paths

import (
	"os"
	"path/filepath"
)

// RepoRoot returns the nearest parent directory that looks like a git worktree.
// It only stats parent directories; it does not shell out to git.
func RepoRoot(cwd string) string {
	if cwd == "" {
		return ""
	}
	dir := filepath.Clean(cwd)
	for {
		if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (st.IsDir() || st.Mode().IsRegular()) {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			return ""
		}
		dir = next
	}
}
