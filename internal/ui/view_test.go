package ui

import (
	"fmt"
	"strings"
	"testing"

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
			if w := render.VisibleWidth(ln); w > width {
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

// A filter that matches nothing must say so — the summary line always counts
// the whole board, so a bare "No agents running" under "2 working" reads as
// the board contradicting itself (the file-drop-into-filter incident).
func TestViewNamesTheFilterWhenItEmptiesTheTable(t *testing.T) {
	m := &model{
		b: &board.Board{Host: "devbox", Load: "0.1", NWork: 2,
			Rows: []board.Row{{PID: 1, Task: "alpha", TreeCh: " "}, {PID: 2, Task: "beta", TreeCh: " "}}},
		width: 120, height: 20,
		cfg:    Config{Theme: render.NewTheme(true)},
		filter: lineInput{prompt: "/"},
	}
	m.applyView()
	m.handleKey(key{name: "/"})
	for _, r := range "zzz" {
		m.handleKey(key{name: string(r), r: r, printable: true})
	}
	v := m.View()
	if strings.Contains(v, "No agents running") {
		t.Fatalf("filtered-empty view still claims no agents:\n%s", v)
	}
	if !strings.Contains(v, `0 of 2 agents match "zzz"`) {
		t.Fatalf("filtered-empty view does not name the filter:\n%s", v)
	}
}

// After scrolling deep (long board, cursor far down), shrinking the selection
// back up must return the frame to the top: the summary and table header can
// never stay scrolled away when they fit.
func TestViewSnapsBackToTop(t *testing.T) {
	rows := make([]board.Row, 30)
	for i := range rows {
		// distinct tasks: identical rows would collapse into one "×30" line
		rows[i] = board.Row{PID: i + 1, Task: fmt.Sprintf("task %d", i), TreeCh: " "}
	}
	m := &model{
		b:     &board.Board{Host: "devbox-host", Load: "0.1", Rows: rows},
		width: 120, height: 12,
		cfg: Config{Theme: render.NewTheme(true)},
	}
	m.applyView()
	m.sel, m.selPID = 25, 26
	_ = m.View() // scrolls down to keep the cursor visible
	if m.scroll == 0 {
		t.Fatal("precondition: deep selection should have scrolled")
	}
	m.sel, m.selPID = 0, 1
	v := m.View()
	if m.scroll != 0 {
		t.Fatalf("scroll=%d after selection returned to the top, want 0", m.scroll)
	}
	if !strings.Contains(strings.SplitN(v, "\n", 2)[0], "devbox-host") {
		t.Fatalf("summary line not at the top of the frame:\n%s", v)
	}
}
