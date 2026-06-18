//go:build hermes

package render

import "github.com/yrstm/agentdash/internal/hermesdb"

func init() {
	RegisterExternalTurns(func(kind, key string, n int) ([][2]string, bool) {
		if kind != "hermes" {
			return nil, false
		}
		return hermesdb.RecentTurns(key, n), true
	})
}
