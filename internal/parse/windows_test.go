package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context-windows.conf")
	if err := os.WriteFile(path, []byte(`
# comment line
my-model 400_000   # separators stripped
other-model 1,000,000
broken-line
bad-number nope
`), 0o600); err != nil {
		t.Fatal(err)
	}
	o := LoadOverrides(path)
	if len(o) != 2 {
		t.Fatalf("got %d overrides, want 2", len(o))
	}
	eq(t, "sub", o[0].Sub, "my-model")
	eq(t, "win", o[0].Win, int64(400000))
	eq(t, "win comma", o[1].Win, int64(1000000))
}

func TestWindowFor(t *testing.T) {
	ovr := []Override{{"special", 999}, {"claude", 5}}
	w, src := WindowFor("my-special-model", ovr)
	eq(t, "override win", w, int64(999))
	if !strings.Contains(src, "conf override") {
		t.Errorf("src = %q", src)
	}
	w, _ = WindowFor("claude-opus-4-8", ovr) // first match wins, file order
	eq(t, "override beats builtin", w, int64(5))

	w, _ = WindowFor("claude-fable-5[1m]", nil)
	eq(t, "[1m] id", w, int64(1_000_000))
	w, _ = WindowFor("claude-opus-4-8", nil)
	eq(t, "claude default", w, int64(200_000))
	w, _ = WindowFor("fable-x", nil)
	eq(t, "fable default", w, int64(200_000))
	w, _ = WindowFor("gpt-5.5", nil)
	eq(t, "gpt default", w, int64(272_000))
	w, src = WindowFor("mystery-model", nil)
	eq(t, "unknown win", w, int64(0))
	eq(t, "unknown src", src, "")
	w, _ = WindowFor("", nil)
	eq(t, "empty model", w, int64(0))
}

func TestLearnWindowPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf", "context-windows.conf")
	var ovr []Override
	LearnWindow(path, "new-model-id", 1_000_000, &ovr)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "# agentdash context-window overrides") {
		t.Error("header missing on first write")
	}
	if !strings.Contains(string(b), "new-model-id 1000000") {
		t.Errorf("learned line missing:\n%s", b)
	}
	w, _ := WindowFor("new-model-id", ovr)
	eq(t, "in-memory adoption", w, int64(1_000_000))

	// a model already covered by an override never re-learns
	LearnWindow(path, "new-model-id", 2_000_000, &ovr)
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "2000000") {
		t.Error("re-learned a model that already had an override")
	}
}
