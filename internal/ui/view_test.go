package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/yrstm/agentdash/internal/board"
	"github.com/yrstm/agentdash/internal/render"
)

// A line wider than the pane wraps and desyncs the alt-screen renderer (the
// repeated-footer-on-resize bug). View must clip every line to the width.
func TestViewClipsToWidth(t *testing.T) {
	b := &board.Board{
		Host: "devbox", Load: "0.1",
		Rows: []board.Row{
			{Kind: "codex", Glyph: "●", Last: "5m", Model: "gpt-5.5", Tokens: "179k/1.0k",
				Ctx: "37%", Status: "respawn ×4", Cwd: "/home/dev",
				Task: strings.Repeat("a very long task description ", 10), TreeCh: " "},
			{Kind: "claude", Glyph: " ", Last: "2m", Model: "opus-4-8", Tokens: "47m/420k",
				Ctx: "53%", Status: "working", Cwd: "/w/api", Task: "short", TreeCh: " "},
		},
	}
	for _, width := range []int{40, 60, 80, 100} {
		m := &model{
			b: b, rows: b.Rows, width: width, height: 12,
			cfg: Config{Theme: render.NewTheme(false)}, // color on -> ANSI in the lines
		}
		for i, ln := range strings.Split(m.View(), "\n") {
			if w := ansi.StringWidth(ln); w > width {
				t.Errorf("width=%d: line %d is %d cols (wraps): %q", width, i, w, ln)
			}
		}
	}
}

func TestViewShowsBannerWithoutColorUnlessPlain(t *testing.T) {
	b := &board.Board{
		Host:  "devbox",
		Load:  "0.1",
		Rows:  []board.Row{{Kind: "codex", Status: "working", Cwd: "/home/dev", Task: "work", TreeCh: " "}},
		NWork: 1,
	}
	m := &model{
		b: b, rows: b.Rows, width: 40, height: 30,
		cfg: Config{Theme: render.Theme{}},
	}
	if out := m.View(); !strings.Contains(out, "AGENTDASH") {
		t.Fatalf("watch view should show the ASCII banner without color: %q", out)
	}

	m.cfg.Plain = true
	if out := m.View(); strings.Contains(out, "AGENTDASH") {
		t.Fatalf("plain watch view should suppress the banner: %q", out)
	}
}
