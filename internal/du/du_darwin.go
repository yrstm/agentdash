package du

import "path/filepath"

// osCategories returns the macOS-specific locations: the MCP log cache lives
// under ~/Library/Caches, and the Claude desktop app keeps its own data and
// logs under ~/Library.
func osCategories(home string) []catSpec {
	lib := filepath.Join(home, "Library")
	return []catSpec{
		{
			name: "mcp log cache", path: filepath.Join(lib, "Caches", "claude-cli-nodejs"),
			what:    "Cached logs from MCP servers the CLI launches. Safe to delete.",
			cleanup: "rm -rf ~/Library/Caches/claude-cli-nodejs/*",
		},
		{
			name: "claude app support", path: filepath.Join(lib, "Application Support", "Claude"),
			what: "Claude desktop app data. Managed by the app; delete only with the app closed, and expect to re-sign-in.",
		},
		{
			name: "claude app logs", path: filepath.Join(lib, "Logs", "Claude"),
			what:    "Claude desktop app logs. Safe to delete.",
			cleanup: "rm -rf ~/Library/Logs/Claude/*",
		},
	}
}
