package parse

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// TestFixtureStagingReachesStatusStates is an A12 smoke test: it runs the
// synthetic fixture generator (tests/fixtures/generate.sh) with a pinned
// ref-epoch, asserts the staged mtime actually landed (stat against ref),
// and asserts the three live-status states the generator promises to
// stage are each reachable through the real ScanSession/StatusOf path.
func TestFixtureStagingReachesStatusStates(t *testing.T) {
	const ref = int64(1751500000) // arbitrary fixed epoch; only relative offsets matter
	dest := t.TempDir()
	script, err := filepath.Abs("../../tests/fixtures/generate.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, dest, "7", strconv.FormatInt(ref, 10))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate.sh: %v\n%s", err, out)
	}

	projDirs, err := filepath.Glob(filepath.Join(dest, ".claude", "projects", "*"))
	if err != nil || len(projDirs) != 1 {
		t.Fatalf("expected exactly one claude project dir, got %v (err %v)", projDirs, err)
	}
	sessDir := projDirs[0]

	// Prove the mtime staging actually took: session-normal.jsonl is staged
	// at exactly ref-540s (9 minutes before ref).
	normalPath := filepath.Join(sessDir, "session-normal.jsonl")
	st, err := os.Stat(normalPath)
	if err != nil {
		t.Fatal(err)
	}
	wantMtime := ref - 9*60
	if got := st.ModTime().Unix(); got != wantMtime {
		t.Fatalf("session-normal.jsonl mtime = %d, want %d (ref-9min)", got, wantMtime)
	}

	cases := []struct {
		file       string
		wantStatus string
	}{
		{"session-normal.jsonl", "waiting"},    // mtime ref-9min, ends on assistant text
		{"session-truncated.jsonl", "working"}, // mtime ref-10s
		{"session-compacted.jsonl", "idle"},    // mtime ref-3d
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			path := filepath.Join(sessDir, c.file)
			ent := ScanSession(path, NewCache(), "claude", float64(ref))
			if ent == nil {
				t.Fatalf("ScanSession(%s) = nil", c.file)
			}
			got := StatusOf(ent, 0, float64(ref), DefaultThresholds)
			if got != c.wantStatus {
				t.Errorf("StatusOf(%s) = %q, want %q", c.file, got, c.wantStatus)
			}
		})
	}
}
