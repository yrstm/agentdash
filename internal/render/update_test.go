package render

import (
	"strings"
	"testing"
)

func TestUpdateHint(t *testing.T) {
	th := NewTheme(true) // plain: assert on text, not SGR

	// no provenance, or disabled -> empty
	if got := UpdateHint(th, "abc1234", false, -1, 14); got != "" {
		t.Errorf("no-provenance should be empty, got %q", got)
	}
	if got := UpdateHint(th, "abc1234", false, 99*86400, 0); got != "" {
		t.Errorf("staleDays<=0 should disable, got %q", got)
	}

	// fresh build: age line, no nudge
	fresh := UpdateHint(th, "abc1234", false, 2*86400, 14)
	if !strings.Contains(fresh, "build abc1234") || !strings.Contains(fresh, "2d old") {
		t.Errorf("fresh hint missing age stamp: %q", fresh)
	}
	if strings.Contains(fresh, "reinstall") {
		t.Errorf("fresh build should not nudge: %q", fresh)
	}

	// stale + dirty: nudge with day count, reinstall command, dirty marker
	stale := UpdateHint(th, "abc1234", true, 30*86400, 14)
	for _, want := range []string{"abc1234+", "30d since build", "reinstall:", "go install", "cmd/agentdash@main"} {
		if !strings.Contains(stale, want) {
			t.Errorf("stale hint missing %q: %q", want, stale)
		}
	}
}
