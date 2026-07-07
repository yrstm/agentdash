package trail

import (
	"regexp"
	"sort"
	"unicode/utf8"
)

// secretPattern is a named high-confidence secret matcher. The patterns are
// deliberately specific (fixed prefixes, exact lengths) to keep false positives
// low — this is a "you definitely pasted a credential" scan, not entropy
// guessing. Order matters: most-specific first — a span claimed by an earlier
// pattern is skipped by later ones, so an `sk-ant-…` key reports once as
// anthropic-key instead of also matching the broader openai `sk-` prefix.
type secretPattern struct {
	name string
	re   *regexp.Regexp
}

var secretPatterns = []secretPattern{
	{"aws-access-key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\b(ghp|gho|ghs|ghu|ghr)_[0-9A-Za-z]{36}\b`)},
	{"github-pat", regexp.MustCompile(`\bgithub_pat_[0-9A-Za-z_]{60,}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}\b`)},
	{"anthropic-key", regexp.MustCompile(`\bsk-ant-[0-9A-Za-z_-]{20,}\b`)},
	{"openai-key", regexp.MustCompile(`\bsk-(proj-)?[0-9A-Za-z_-]{20,}\b`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"private-key", regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`)},
	{"jwt", regexp.MustCompile(`\beyJ[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}\b`)},
}

// Secrets scans every transcript line for high-confidence secret patterns and
// returns masked findings — the full value is never stored or returned. Results
// are deduplicated by (pattern, masked value, session).
func Secrets(opt Options) []Secret {
	var out []Secret
	seen := map[string]bool{}
	eachTranscript(opt.Home, func(agent, path string) {
		st := state{agent: agent, path: path}
		if agent == "claude" {
			st.cwd = cwdFromClaudePath(path)
		}
		scanLines(path, func(line []byte) {
			ts, cwd := lineMeta(line, agent, &st)
			if !keep(opt, ts, cwd) {
				return
			}
			s := string(line)
			var claimed [][2]int
			for _, p := range secretPatterns {
				for _, loc := range p.re.FindAllStringIndex(s, -1) {
					if overlapsAny(claimed, loc[0], loc[1]) {
						continue // a more specific earlier pattern owns this span
					}
					claimed = append(claimed, [2]int{loc[0], loc[1]})
					masked := mask(s[loc[0]:loc[1]])
					key := p.name + "\x00" + masked + "\x00" + st.path
					if seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, Secret{
						TS: ts, Agent: agent, Session: sessionName(st.path),
						Pattern: p.name, Masked: masked,
					})
				}
			}
		})
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

// lineMeta pulls the timestamp and cwd out of a line and keeps session/cwd
// state current, without the full command/file decode.
func lineMeta(line []byte, agent string, st *state) (ts int64, cwd string) {
	if agent == "claude" {
		cl, _ := decodeClaude(line, st)
		return cl.ts, st.cwd
	}
	_, ts = decodeCodex(line, st)
	return ts, st.cwd
}

// overlapsAny reports whether [start,end) intersects any claimed span.
func overlapsAny(claimed [][2]int, start, end int) bool {
	for _, c := range claimed {
		if start < c[1] && end > c[0] {
			return true
		}
	}
	return false
}

// mask keeps the first 4 characters and replaces the rest with an ellipsis, so
// a finding is identifiable without exposing the credential.
func mask(s string) string {
	if utf8.RuneCountInString(s) <= 4 {
		return "…"
	}
	r := []rune(s)
	return string(r[:4]) + "…"
}
