package parse

import "fmt"

// Thresholds carries the working/idle cutoffs (AGENTDASH_WORKING_SECS,
// AGENTDASH_IDLE_SECS).
type Thresholds struct {
	WorkingSecs float64
	StuckSecs   float64
	IdleSecs    float64
}

var DefaultThresholds = Thresholds{WorkingSecs: 60, StuckSecs: 90, IdleSecs: 600}

// StatusOf derives the row status from write age and the last entry type:
// fresh writes are working, long-quiet is idle, and in between the agent
// is waiting on you (last turn assistant) or possibly stuck (user/tool).
// Three or more fresh pids on one file within 10 minutes is a respawn loop.
func StatusOf(ent *Entry, respawnN int, now float64, th Thresholds) string {
	if respawnN >= 3 {
		return fmt.Sprintf("respawn ×%d", respawnN)
	}
	age := now - ent.Mtime
	if age < th.WorkingSecs {
		return "working"
	}
	if age > th.IdleSecs {
		return "idle"
	}
	if ent.LastType == "assistant" {
		return "waiting"
	}
	if th.StuckSecs > 0 && age < th.StuckSecs {
		return "busy?" // quiet on a tool/user turn, not long enough to alarm
	}
	return "stuck?"
}
