package parse

import (
	"os"
	"testing"
)

// FuzzApply: no input, however hostile, may panic an updater. Mirrors the
// CONTRIBUTING ground rule: never raise on a weird line.
func FuzzApply(f *testing.F) {
	for _, p := range []string{"testdata/claude-golden.jsonl", "testdata/codex-golden.jsonl"} {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte(`{"type":"assistant","message":{"usage":{"input_tokens":"NaN"}}}`))
	f.Add([]byte(`{"type":"user","message":{"content":[{"type":"text"}]}}`))
	f.Add([]byte(`{"type":"event_msg","payload":{"type":"token_count","info":{}}}`))
	f.Add([]byte(`{"type":"event_msg","payload":{"type":"user_message","message":{"nested":1}}}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))
	f.Add([]byte(`�`))
	f.Fuzz(func(t *testing.T, line []byte) {
		for _, kind := range []string{"claude", "codex"} {
			ent := &Entry{Kind: kind, V: ParserV}
			Apply(kind, ent, line) // must not panic
		}
	})
}
