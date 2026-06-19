//go:build hermes

package render

import "github.com/yrstm/agentdash/internal/hermesdb"

// updateTags tailors the reinstall hint so a Hermes-built binary tells you to
// rebuild *with* the tag, keeping session monitoring.
const updateTags = " -tags=hermes"

func init() {
	RegisterExternalTurns(func(kind, key string, n int) ([][2]string, bool) {
		if kind != "hermes" {
			return nil, false
		}
		return hermesdb.RecentTurns(key, n), true
	})
}
