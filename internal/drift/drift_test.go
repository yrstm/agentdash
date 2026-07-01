package drift

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSyntheticFixtureFindsDeadPath is the A12 smoke test: it runs the
// synthetic fixture generator (tests/fixtures/generate.sh) into a temp
// $HOME and asserts Detect finds the planted dead-path reference in
// work/widget/CLAUDE.md, proving the generator and this existing harness
// work together end to end.
func TestSyntheticFixtureFindsDeadPath(t *testing.T) {
	dir := t.TempDir()
	script, err := filepath.Abs("../../tests/fixtures/generate.sh")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", script, dir, "1").CombinedOutput(); err != nil {
		t.Fatalf("generate.sh: %v\n%s", err, out)
	}
	t.Setenv("HOME", dir) // never read the real $HOME

	project := filepath.Join(dir, "work", "widget")
	findings := Detect(DefaultOptions(project, dir))

	var found *Finding
	for i := range findings {
		if findings[i].Kind == "stale_rule" && findings[i].Phrase == "docs/setup.md" {
			found = &findings[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a stale_rule finding for docs/setup.md, got %+v", findings)
	}
	if found.RuleFile != filepath.Join(project, "CLAUDE.md") {
		t.Errorf("RuleFile = %q, want %s", found.RuleFile, filepath.Join(project, "CLAUDE.md"))
	}
}
