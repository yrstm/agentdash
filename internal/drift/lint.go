package drift

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yrstm/agentdash/internal/config"
)

// A7 context-rot checks. All deterministic and evidence-backed — no model
// calls, no network — and none of them ever edit a file; a suggested fix is
// printed and the optional --handoff pack is written for a human/agent to act on.

// ctxThreshold is the always-loaded token budget over which heavy_context
// fires. Default 4000; AGENTDASH_LINT_CTX_TOKENS overrides.
func ctxThreshold() int {
	if v := os.Getenv("AGENTDASH_LINT_CTX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 4000
}

func sevRank(s string) int {
	switch s {
	case "high":
		return 3
	case "warn":
		return 2
	default:
		return 1
	}
}

// ---- conflicting_rule: same topic, opposite values across files ------------

// topicRe maps a topic to the regexps that recognise each competing value on a
// line. Kept intentionally small (A7: "start with a small pattern set") and
// gated on a context word so incidental mentions don't trip it.
var topicValues = []struct {
	topic  string
	gate   *regexp.Regexp // the line must mention the subject
	values map[string]*regexp.Regexp
}{
	{
		topic: "indentation",
		gate:  regexp.MustCompile(`(?i)indent|tabs?|spaces?`),
		values: map[string]*regexp.Regexp{
			"tabs":   regexp.MustCompile(`(?i)\btabs?\b`),
			"spaces": regexp.MustCompile(`(?i)\bspaces?\b`),
		},
	},
	{
		topic: "package manager",
		gate:  regexp.MustCompile(`(?i)install|package manager|dependenc`),
		values: map[string]*regexp.Regexp{
			"npm":  regexp.MustCompile(`(?i)\bnpm\b`),
			"yarn": regexp.MustCompile(`(?i)\byarn\b`),
			"pnpm": regexp.MustCompile(`(?i)\bpnpm\b`),
			"bun":  regexp.MustCompile(`(?i)\bbun\b`),
		},
	},
	{
		topic: "test runner",
		gate:  regexp.MustCompile(`(?i)test`),
		values: map[string]*regexp.Regexp{
			"jest":     regexp.MustCompile(`(?i)\bjest\b`),
			"vitest":   regexp.MustCompile(`(?i)\bvitest\b`),
			"mocha":    regexp.MustCompile(`(?i)\bmocha\b`),
			"pytest":   regexp.MustCompile(`(?i)\bpytest\b`),
			"unittest": regexp.MustCompile(`(?i)\bunittest\b`),
		},
	},
}

// conflictingRules flags a topic asserted with different values in different
// instruction/rule files (e.g. CLAUDE.md says tabs, a cursor rule says spaces).
func conflictingRules(items []config.Item) []Finding {
	// topic -> value -> set of "file:line — excerpt"
	seen := map[string]map[string][]string{}
	for _, it := range items {
		if it.Kind != "instruction" && it.Kind != "rule" {
			continue
		}
		for i, line := range config.ContentLines(it.Path) {
			for _, tv := range topicValues {
				if !tv.gate.MatchString(line) {
					continue
				}
				for val, re := range tv.values {
					if !re.MatchString(line) {
						continue
					}
					if seen[tv.topic] == nil {
						seen[tv.topic] = map[string][]string{}
					}
					ev := shortPath(it.Path) + ":" + strconv.Itoa(i+1) + " — " + trunc(line, 80)
					seen[tv.topic][val] = append(seen[tv.topic][val], ev)
				}
			}
		}
	}
	var out []Finding
	for topic, byVal := range seen {
		if len(byVal) < 2 {
			continue // one value asserted (or none) — not a conflict
		}
		var vals []string
		var evidence []string
		for v, evs := range byVal {
			vals = append(vals, v)
			evidence = append(evidence, evs...)
		}
		sort.Strings(vals)
		sort.Strings(evidence)
		out = append(out, Finding{
			ID:         fingerprintID("conflicting_rule", topic),
			Kind:       "conflicting_rule",
			Phrase:     topic + ": " + strings.Join(vals, " vs "),
			Count:      len(evidence),
			Severity:   "high",
			Evidence:   evidence,
			Suggestion: "pick one " + topic + " value and make every instruction file agree; conflicting rules make the agent guess",
		})
	}
	return out
}

// ---- duplicate_rule: the same instruction line in ≥2 files -----------------

func duplicateRules(items []config.Item) []Finding {
	locs := map[string][]string{} // normalized line -> ["file:line", ...]
	for _, it := range items {
		if it.Kind != "instruction" && it.Kind != "rule" {
			continue
		}
		for i, line := range config.ContentLines(it.Path) {
			norm := normLine(line)
			if len(norm) < 24 || strings.HasPrefix(norm, "#") { // skip short lines and headings
				continue
			}
			locs[norm] = append(locs[norm], shortPath(it.Path)+":"+strconv.Itoa(i+1))
		}
	}
	var out []Finding
	for norm, where := range locs {
		files := distinctFiles(where)
		if len(files) < 2 {
			continue // duplicated within one file is not a cross-file drift
		}
		out = append(out, Finding{
			ID:         fingerprintID("duplicate_rule", norm),
			Kind:       "duplicate_rule",
			Phrase:     trunc(norm, 80),
			Count:      len(where),
			Severity:   "info",
			Evidence:   where,
			Suggestion: "keep this instruction in one file; duplicates drift out of sync and waste context",
		})
	}
	return out
}

// ---- dead_hook: a settings hook points at a missing/non-executable script --

// scriptRe pulls the first script-looking path out of a hook command line.
var scriptRe = regexp.MustCompile(`(?:^|\s)((?:\./|/|[\w.-]+/)[\w./-]+\.(?:sh|py|js|ts|rb|pl))\b`)

func deadHooks(items []config.Item, project string) []Finding {
	var out []Finding
	for _, it := range items {
		if it.Kind != "hook" {
			continue
		}
		m := scriptRe.FindStringSubmatch(it.Summary)
		if m == nil {
			continue // inline shell, not a script file — nothing to resolve
		}
		ref := m[1]
		abs := ref
		if !filepath.IsAbs(ref) {
			abs = filepath.Join(project, ref)
		}
		st, err := os.Stat(abs)
		switch {
		case os.IsNotExist(err):
			out = append(out, Finding{
				ID:         fingerprintID("dead_hook", it.Path+":"+ref),
				Kind:       "dead_hook",
				Phrase:     ref + " (missing)",
				Count:      1,
				Severity:   "high",
				RuleFile:   it.Path,
				Evidence:   []string{trunc(it.Summary, 100)},
				Suggestion: "the hook script " + ref + " does not exist; fix the path or remove the hook",
			})
		case err == nil && st.Mode()&0o111 == 0:
			out = append(out, Finding{
				ID:         fingerprintID("dead_hook", it.Path+":"+ref),
				Kind:       "dead_hook",
				Phrase:     ref + " (not executable)",
				Count:      1,
				Severity:   "warn",
				RuleFile:   it.Path,
				Evidence:   []string{trunc(it.Summary, 100)},
				Suggestion: "chmod +x " + ref + " — the hook script is not executable",
			})
		}
	}
	return out
}

// ---- heavy_context: always-loaded instruction chain over the budget --------

func heavyContext(inv config.Result, threshold int) []Finding {
	if inv.AlwaysLoadedTokens <= threshold {
		return nil
	}
	type f struct {
		path string
		tok  int
	}
	var heavy []f
	for _, it := range inv.Items {
		if it.Kind == "instruction" && it.TokenEst > 0 {
			heavy = append(heavy, f{shortPath(it.Path), it.TokenEst})
		}
	}
	sort.Slice(heavy, func(i, j int) bool { return heavy[i].tok > heavy[j].tok })
	var evidence []string
	for _, h := range heavy {
		if len(evidence) >= 5 {
			break
		}
		evidence = append(evidence, h.path+" ~"+strconv.Itoa(h.tok)+" tokens")
	}
	return []Finding{{
		ID:         fingerprintID("heavy_context", inv.Project),
		Kind:       "heavy_context",
		Phrase:     "always-loaded instructions ~" + strconv.Itoa(inv.AlwaysLoadedTokens) + " tokens (budget " + strconv.Itoa(threshold) + ")",
		Count:      inv.AlwaysLoadedTokens,
		Severity:   "warn",
		Evidence:   evidence,
		Suggestion: "trim the heaviest always-loaded files or move detail into on-demand docs/commands; every turn pays this cost",
	}}
}

// ---- helpers ---------------------------------------------------------------

func normLine(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func distinctFiles(locs []string) map[string]bool {
	out := map[string]bool{}
	for _, l := range locs {
		if i := strings.LastIndexByte(l, ':'); i > 0 {
			out[l[:i]] = true
		}
	}
	return out
}
