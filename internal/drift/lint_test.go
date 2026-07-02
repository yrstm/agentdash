package drift

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yrstm/agentdash/internal/config"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findKind(fs []Finding, kind string) *Finding {
	for i := range fs {
		if fs[i].Kind == kind {
			return &fs[i]
		}
	}
	return nil
}

func TestConflictingAndDuplicateRules(t *testing.T) {
	proj := t.TempDir()
	claude := filepath.Join(proj, "CLAUDE.md")
	rule := filepath.Join(proj, ".cursor", "rules", "style.mdc")
	writeFile(t, claude, "# widget\nUse tabs for indentation.\nAlways rebase local branches before merging.\n")
	writeFile(t, rule, "Always use 2 spaces for indentation.\nAlways rebase local branches before merging.\n")

	items := []config.Item{
		{Kind: "instruction", Path: claude},
		{Kind: "rule", Path: rule},
	}

	conf := findKind(conflictingRules(items), "conflicting_rule")
	if conf == nil {
		t.Fatal("indentation conflict not detected")
	}
	if conf.Severity != "high" || len(conf.Evidence) < 2 {
		t.Errorf("conflict finding = %+v", conf)
	}

	dup := findKind(duplicateRules(items), "duplicate_rule")
	if dup == nil {
		t.Fatal("duplicate rule not detected")
	}
	if len(distinctFiles(dup.Evidence)) < 2 {
		t.Errorf("duplicate should span two files: %+v", dup.Evidence)
	}
}

func TestDeadHooks(t *testing.T) {
	proj := t.TempDir()
	settings := filepath.Join(proj, ".claude", "settings.json")
	writeFile(t, settings, "{}")
	// present + executable script -> no finding
	ok := filepath.Join(proj, "ok.sh")
	writeFile(t, ok, "#!/bin/sh\n")
	if err := os.Chmod(ok, 0o755); err != nil {
		t.Fatal(err)
	}
	// present but not executable
	noexec := filepath.Join(proj, "noexec.sh")
	writeFile(t, noexec, "#!/bin/sh\n")
	_ = os.Chmod(noexec, 0o644)

	items := []config.Item{
		{Kind: "hook", Path: settings, Summary: "PostToolUse: ./missing.sh --flag"},
		{Kind: "hook", Path: settings, Summary: "PreToolUse: ./ok.sh"},
		{Kind: "hook", Path: settings, Summary: "Stop: ./noexec.sh"},
		{Kind: "hook", Path: settings, Summary: "Notification: echo inline shell, no script"},
	}
	got := deadHooks(items, proj)
	if len(got) != 2 {
		t.Fatalf("dead hooks = %d, want 2 (missing + non-exec): %+v", len(got), got)
	}
	// the missing one is high severity
	var missing bool
	for _, f := range got {
		if f.Severity == "high" {
			missing = true
		}
	}
	if !missing {
		t.Error("missing hook script should be high severity")
	}
}

func TestHeavyContext(t *testing.T) {
	inv := config.Result{
		Project:            "/proj",
		AlwaysLoadedTokens: 6000,
		Items: []config.Item{
			{Kind: "instruction", Path: "/proj/CLAUDE.md", TokenEst: 5000},
			{Kind: "instruction", Path: "/proj/sub/CLAUDE.md", TokenEst: 1000},
		},
	}
	if got := heavyContext(inv, 4000); len(got) != 1 || got[0].Kind != "heavy_context" {
		t.Fatalf("expected a heavy_context finding: %+v", got)
	}
	// under budget -> nothing
	inv.AlwaysLoadedTokens = 3000
	if got := heavyContext(inv, 4000); got != nil {
		t.Fatalf("under-budget should not flag: %+v", got)
	}
}

func TestHandoffPack(t *testing.T) {
	fs := []Finding{{
		Kind: "conflicting_rule", Phrase: "indentation: spaces vs tabs", Severity: "high",
		Evidence: []string{"a:1 — use tabs", "b:2 — use spaces"}, Suggestion: "pick one",
	}}
	out := Handoff(fs, "/proj")
	for _, want := range []string{"conflicting_rule", "Suggested fix: pick one", "Prompt for your agent", "diff before writing"} {
		if !contains(out, want) {
			t.Errorf("handoff missing %q", want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
