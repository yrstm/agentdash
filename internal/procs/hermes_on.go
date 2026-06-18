//go:build hermes

package procs

import (
	"os"
	"path/filepath"
	"strings"
)

func init() {
	RegisterRuntime(func(args, dir string, p *Proc) {
		if KindOf(args) != "hermes" {
			return
		}
		home, profile, sid := HermesRuntime(args, readEnviron(dir))
		if home == "" && profile == "" && sid == "" {
			return
		}
		if p.Extra == nil {
			p.Extra = map[string]string{}
		}
		if home != "" {
			p.Extra["HERMES_HOME"] = home
		}
		if profile != "" {
			p.Extra["HERMES_PROFILE"] = profile
		}
		if sid != "" {
			p.Extra["HERMES_SESSION_ID"] = sid
		}
	})
}

func readEnviron(dir string) map[string]string {
	b, err := os.ReadFile(filepath.Join(dir, "environ"))
	if err != nil || len(b) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, part := range strings.Split(string(b), "\x00") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch k {
		case "HERMES_HOME", "HERMES_PROFILE", "HERMES_SESSION_ID":
			out[k] = v
		}
	}
	return out
}

// HermesRuntime extracts only Hermes routing metadata from argv and environ.
// It intentionally ignores all other environment variables.
func HermesRuntime(args string, env map[string]string) (home, profile, sessionID string) {
	if env != nil {
		home = env["HERMES_HOME"]
		profile = env["HERMES_PROFILE"]
		sessionID = env["HERMES_SESSION_ID"]
	}
	fields := strings.Fields(args)
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "-p", "--profile":
			if i+1 < len(fields) && profile == "" {
				profile = fields[i+1]
			}
		}
		if strings.HasPrefix(fields[i], "--profile=") && profile == "" {
			profile = strings.TrimPrefix(fields[i], "--profile=")
		}
	}
	return home, profile, sessionID
}
