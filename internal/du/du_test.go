package du

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(cats []Category, name string) (Category, bool) {
	for _, c := range cats {
		if c.Name == name {
			return c, true
		}
	}
	return Category{}, false
}

func TestCollect(t *testing.T) {
	home := t.TempDir()
	// two project sessions of different sizes, plus a small config file
	writeFile(t, filepath.Join(home, ".claude", "projects", "p1", "big.jsonl"), 3000)
	writeFile(t, filepath.Join(home, ".claude", "projects", "p1", "small.jsonl"), 100)
	writeFile(t, filepath.Join(home, ".claude", "projects", "p2", "mid.jsonl"), 1000)
	writeFile(t, filepath.Join(home, ".claude.json"), 50)
	writeFile(t, filepath.Join(home, ".codex", "sessions", "2026", "r.jsonl"), 500)

	res := Collect(home, 0)

	proj, ok := find(res.Categories, "claude projects")
	if !ok {
		t.Fatal("no claude projects category")
	}
	if proj.Bytes != 4100 || proj.Files != 3 || !proj.Present {
		t.Errorf("projects = %+v, want 4100 bytes / 3 files", proj)
	}
	// top files largest-first
	if len(proj.Top) < 2 || proj.Top[0].Bytes != 3000 || proj.Top[1].Bytes != 1000 {
		t.Errorf("top files not largest-first: %+v", proj.Top)
	}

	cfg, _ := find(res.Categories, "claude.json")
	if !cfg.Present || cfg.Bytes != 50 || cfg.Cleanup != "" {
		t.Errorf("claude.json = %+v (should be present, no cleanup command)", cfg)
	}

	// an absent category is reported, not dropped
	fh, ok := find(res.Categories, "claude file-history")
	if !ok || fh.Present {
		t.Errorf("file-history should be present-in-list but absent-on-disk: %+v", fh)
	}

	// categories are sorted largest-first and total adds up
	for i := 1; i < len(res.Categories); i++ {
		if res.Categories[i-1].Bytes < res.Categories[i].Bytes {
			t.Fatalf("categories not sorted largest-first at %d", i)
		}
	}
	if res.Total != 4100+50+500 {
		t.Errorf("total = %d, want %d", res.Total, 4100+50+500)
	}
}

func TestHumanBytes(t *testing.T) {
	for in, want := range map[int64]string{
		0:               "0B",
		512:             "512B",
		1024:            "1.0K",
		1536:            "1.5K",
		1024 * 1024:     "1.0M",
		150 * 1024:      "150K",
		3 * 1024 * 1024: "3.0M",
	} {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
