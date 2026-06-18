package render

import (
	"strings"
	"testing"

	"github.com/yrstm/agentdash/internal/board"
)

func TestBanner(t *testing.T) {
	b := &board.Board{Rows: []board.Row{{}, {}, {}}, NWork: 2, NIdle: 1, IdleCtx: 1000, BurnCtx: 2000}
	out := Banner(b, NewTheme(false), 100)
	for _, want := range []string{"operator on watch", "supervising 3 agents", "2 active · 1 idle", "held idle", "burning", "ready"} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q", want)
		}
	}
	// figlet at wide width; one-line fallback when narrow
	if !strings.Contains(out, "█") {
		t.Error("wide banner should include the figlet wordmark")
	}
	if narrow := Banner(b, NewTheme(false), 40); strings.Contains(narrow, "█") {
		t.Error("narrow banner should fall back to the one-line wordmark")
	}
}
