package du

import "path/filepath"

// osCategories returns the Linux-specific locations. The MCP servers the CLI
// launches write their logs under ~/.cache on Linux.
func osCategories(home string) []catSpec {
	return []catSpec{
		{
			name: "mcp log cache", path: filepath.Join(home, ".cache", "claude-cli-nodejs"),
			what:    "Cached logs from MCP servers the CLI launches. Safe to delete.",
			cleanup: "rm -rf ~/.cache/claude-cli-nodejs/*",
		},
	}
}
