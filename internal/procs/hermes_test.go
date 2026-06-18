//go:build hermes

package procs

import "testing"

func TestHermesRuntimePrefersEnvAndParsesProfileArgs(t *testing.T) {
	home, profile, sid := HermesRuntime("hermes -p ignored", map[string]string{
		"HERMES_HOME":       "/tmp/hermes-home",
		"HERMES_PROFILE":    "work",
		"HERMES_SESSION_ID": "sess_dummy_123",
		"EXTRA_ENV":         "must-not-be-read",
	})
	if home != "/tmp/hermes-home" || profile != "work" || sid != "sess_dummy_123" {
		t.Fatalf("bad runtime from env: home=%q profile=%q sid=%q", home, profile, sid)
	}
	_, profile, _ = HermesRuntime("hermes --profile demo chat", nil)
	if profile != "demo" {
		t.Fatalf("--profile parse = %q", profile)
	}
	_, profile, _ = HermesRuntime("hermes --profile=demo2 chat", nil)
	if profile != "demo2" {
		t.Fatalf("--profile= parse = %q", profile)
	}
}
