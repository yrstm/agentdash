//go:build hermes

package board

import (
	"testing"

	"github.com/yrstm/agentdash/internal/hermesdb"
	"github.com/yrstm/agentdash/internal/parse"
)

func TestResumeCmdHermesDefaultAndProfile(t *testing.T) {
	key := hermesdb.Key("/home/dev/.hermes/state.db", "sess_dummy_123")
	got := ResumeCmd(parse.PidInfo{Kind: "hermes", Path: key, Cwd: "/work/dummy"})
	want := "cd /work/dummy && hermes --resume sess_dummy_123"
	if got != want {
		t.Fatalf("default resume = %q, want %q", got, want)
	}

	profileKey := hermesdb.Key("/home/dev/.hermes/profiles/work/state.db", "sess_dummy_456")
	got = ResumeCmd(parse.PidInfo{Kind: "hermes", Path: profileKey, Cwd: "/work/dummy", Profile: "work"})
	want = "cd /work/dummy && hermes -p work --resume sess_dummy_456"
	if got != want {
		t.Fatalf("profile resume = %q, want %q", got, want)
	}
}
