package parse

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// fallback lines if the golden testdata isn't present, shaped like the real
// claude transcript fields the parser reads (usage, content, model).
var benchFallback = [][]byte{
	[]byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"refactor the parser and add edge-case tests"}]}}`),
	[]byte(`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-8","usage":{"input_tokens":1200,"output_tokens":340,"cache_read_input_tokens":8000,"cache_creation_input_tokens":150},"content":[{"type":"text","text":"Here is the refactor."}]}}`),
	[]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/x"}}]}}`),
	[]byte(`{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}`),
}

func benchLines(tb testing.TB) [][]byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "claude-golden.jsonl"))
	if err != nil {
		return benchFallback
	}
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			out = append(out, append([]byte(nil), sc.Bytes()...))
		}
	}
	if len(out) == 0 {
		return benchFallback
	}
	return out
}

// BenchmarkApply measures the per-line parser hot path (the FuzzApply target),
// reporting throughput in bytes/op over a real transcript's lines.
func BenchmarkApply(b *testing.B) {
	lines := benchLines(b)
	var n int64
	for _, l := range lines {
		n += int64(len(l))
	}
	b.SetBytes(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ent := &Entry{Kind: "claude", V: ParserV}
		for _, l := range lines {
			Apply("claude", ent, l)
		}
	}
}

// BenchmarkScanSession measures a full cold scan of a multi-MB transcript — the
// cost the board pays for a session on a cold cache. A fresh cache each
// iteration forces a complete re-parse from offset 0.
func BenchmarkScanSession(b *testing.B) {
	lines := benchLines(b)
	path := filepath.Join(b.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	w := bufio.NewWriter(f)
	var total int64
	for i := 0; i < 2000; i++ { // ~a few MB of transcript
		for _, l := range lines {
			_, _ = w.Write(l)
			_ = w.WriteByte('\n')
			total += int64(len(l)) + 1
		}
	}
	if err := w.Flush(); err != nil {
		b.Fatal(err)
	}
	_ = f.Close()

	b.SetBytes(total)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if ScanSession(path, NewCache(), "claude", 1e9) == nil {
			b.Fatal("nil entry")
		}
	}
}
