package board

import (
	"github.com/yrstm/agentdash/internal/parse"
	"github.com/yrstm/agentdash/internal/procs"
)

// External agent adapters (e.g. a DB-backed agent enabled by a build tag)
// register here. All of this is inert in the default build: externalKinds is
// empty and the hooks are nil, so the board pairs only claude and codex exactly
// as it always has.
var (
	externalKinds  = map[string]bool{}
	externalBatch  func(agents []procs.Proc, h string, cache *parse.Cache, newPidMap map[string]parse.PidInfo) map[int]procs.Pairing
	externalResume func(parse.PidInfo) (string, bool)
)

func isExternalKind(kind string) bool { return externalKinds[kind] }

// RegisterExternalKind marks a kind as paired by an external adapter rather
// than the built-in claude/codex locators.
func RegisterExternalKind(kind string) { externalKinds[kind] = true }

// RegisterExternalBatch installs the pairing hook for external kinds. It resolves
// every external process at once (a batch, like PairClaude) so the adapter can
// claim each session at most once and not collapse several processes onto one.
// It returns pid->pairing and populates cache.Entries and newPidMap.
func RegisterExternalBatch(f func(agents []procs.Proc, h string, cache *parse.Cache, newPidMap map[string]parse.PidInfo) map[int]procs.Pairing) {
	externalBatch = f
}

// RegisterExternalResume installs the resume-command hook for external kinds.
func RegisterExternalResume(f func(parse.PidInfo) (string, bool)) { externalResume = f }
