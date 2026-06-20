package ui

import (
	"testing"

	"github.com/yrstm/agentdash/internal/board"
)

// decode drains a single feed into a slice for assertions.
func decode(b []byte) []key {
	ch := make(chan key, 64)
	var d keyDecoder
	d.feed(b, ch)
	close(ch)
	var out []key
	for k := range ch {
		out = append(out, k)
	}
	return out
}

func TestDecodeKeys(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"\x1b[A", []string{"up"}},
		{"\x1b[B", []string{"down"}},
		{"\x1b[C", []string{"right"}},
		{"\x1b[D", []string{"left"}},
		{"jk", []string{"j", "k"}},
		{"\r", []string{"enter"}},
		{"\n", []string{"enter"}},
		{"\x7f", []string{"backspace"}},
		{"\x03", []string{"ctrl+c"}},
		{"\t", []string{"tab"}},
		{"/", []string{"/"}},
		{"j\x1b[Bq", []string{"j", "down", "q"}}, // burst with an arrow in the middle
	}
	for _, c := range cases {
		got := decode([]byte(c.in))
		if len(got) != len(c.want) {
			t.Errorf("%q -> %d keys, want %d (%v)", c.in, len(got), len(c.want), keyNames(got))
			continue
		}
		for i := range got {
			if got[i].name != c.want[i] {
				t.Errorf("%q -> key %d = %q, want %q", c.in, i, got[i].name, c.want[i])
			}
		}
	}
}

// TestDecodeKeysSplitReads is the raw-terminal hardening: an escape or UTF-8
// sequence delivered across separate reads must still decode to one key, and a
// partial sequence must be held (emit nothing) until completed.
func TestDecodeKeysSplitReads(t *testing.T) {
	ch := make(chan key, 16)
	var d keyDecoder
	drain := func() []string {
		var out []string
		for {
			select {
			case k := <-ch:
				out = append(out, k.name)
			default:
				return out
			}
		}
	}

	// ESC, then "[", then "A" — three reads, one Up key
	d.feed([]byte("\x1b"), ch)
	if got := drain(); len(got) != 0 {
		t.Fatalf("lone ESC must be held, got %v", got)
	}
	d.feed([]byte("["), ch)
	if got := drain(); len(got) != 0 {
		t.Fatalf("ESC[ must be held, got %v", got)
	}
	d.feed([]byte("A"), ch)
	if got := drain(); len(got) != 1 || got[0] != "up" {
		t.Fatalf("split ESC[A -> %v, want [up]", got)
	}

	// a normal char immediately before a split arrow
	d.feed([]byte("j\x1b"), ch)
	if got := drain(); len(got) != 1 || got[0] != "j" {
		t.Fatalf("j + held ESC -> %v, want [j]", got)
	}
	d.feed([]byte("[B"), ch)
	if got := drain(); len(got) != 1 || got[0] != "down" {
		t.Fatalf("completed split arrow -> %v, want [down]", got)
	}

	// a multibyte rune split across reads (é = C3 A9)
	d.feed([]byte{0xC3}, ch)
	if got := drain(); len(got) != 0 {
		t.Fatalf("partial UTF-8 must be held, got %v", got)
	}
	d.feed([]byte{0xA9}, ch)
	if got := drain(); len(got) != 1 || got[0] != "é" {
		t.Fatalf("split UTF-8 -> %v, want [é]", got)
	}
}

// TestDecodeKeysFlushEsc: a lone ESC that never gets a CSI continuation is
// emitted as the Esc key by the idle flush.
func TestDecodeKeysFlushEsc(t *testing.T) {
	ch := make(chan key, 4)
	var d keyDecoder
	d.feed([]byte("\x1b"), ch)
	select {
	case k := <-ch:
		t.Fatalf("ESC must be held before flush, got %q", k.name)
	default:
	}
	d.flush(ch)
	select {
	case k := <-ch:
		if k.name != "esc" {
			t.Fatalf("flush -> %q, want esc", k.name)
		}
	default:
		t.Fatal("flush must emit esc for a held lone ESC")
	}
	if len(d.pending) != 0 {
		t.Fatalf("flush must clear pending, got %v", d.pending)
	}
}

func keyNames(ks []key) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = k.name
	}
	return out
}

func TestLineInput(t *testing.T) {
	var li lineInput
	li.insert('h')
	li.insert('i')
	if li.Value() != "hi" {
		t.Fatalf("insert: %q", li.Value())
	}
	li.backspace()
	if li.Value() != "h" {
		t.Fatalf("backspace: %q", li.Value())
	}
	li.backspace()
	li.backspace() // must not underflow
	if li.Value() != "" {
		t.Fatalf("underflow: %q", li.Value())
	}
	li.SetValue("xyz")
	if li.Value() != "xyz" {
		t.Fatalf("setvalue: %q", li.Value())
	}
}

func TestHandleKeyNavigationAndQuit(t *testing.T) {
	m := &model{rows: []board.Row{{PID: 1}, {PID: 2}, {PID: 3}}, sel: 0, selPID: 1}
	m.handleKey(key{name: "j"})
	if m.sel != 1 || m.selPID != 2 {
		t.Fatalf("j: sel=%d pid=%d", m.sel, m.selPID)
	}
	m.handleKey(key{name: "down"})
	m.handleKey(key{name: "j"}) // clamp at the bottom
	if m.sel != 2 || m.selPID != 3 {
		t.Fatalf("clamp: sel=%d pid=%d", m.sel, m.selPID)
	}
	m.handleKey(key{name: "k"})
	if m.sel != 1 || m.selPID != 2 {
		t.Fatalf("k: sel=%d pid=%d", m.sel, m.selPID)
	}
	if m.handleKey(key{name: "q"}) != actQuit || m.handleKey(key{name: "ctrl+c"}) != actQuit {
		t.Fatal("q and ctrl+c must quit")
	}
	if m.handleKey(key{name: "t"}) != actCollect || m.handleKey(key{name: "a"}) != actCollect {
		t.Fatal("t/a toggles must request a re-collect")
	}
}

func TestFilterNarrowsRows(t *testing.T) {
	m := &model{
		b:      &board.Board{Rows: []board.Row{{PID: 1, Task: "alpha"}, {PID: 2, Task: "beta"}}},
		filter: lineInput{prompt: "/"},
	}
	m.applyView()
	if len(m.rows) != 2 {
		t.Fatalf("pre-filter rows=%d", len(m.rows))
	}
	m.handleKey(key{name: "/"}) // enter filter mode
	if !m.filtering {
		t.Fatal("/ should start filtering")
	}
	for _, r := range "alph" {
		m.handleKey(key{name: string(r), r: r, printable: true})
	}
	if len(m.rows) != 1 || m.rows[0].PID != 1 {
		t.Fatalf("filter 'alph' -> %d rows %v (want 1 / pid 1)", len(m.rows), m.rows)
	}
	m.handleKey(key{name: "esc"})
	if m.filtering || m.filter.Value() != "" || len(m.rows) != 2 {
		t.Fatalf("esc should clear: filtering=%v val=%q rows=%d", m.filtering, m.filter.Value(), len(m.rows))
	}
}
