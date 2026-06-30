// Package drift detects mismatches between what agent sessions repeatedly ask
// for and what committed config files say. It is strictly read-only; output
// is evidence-backed findings for humans to act on. Detection uses explicit,
// auditable pattern matching — no model calls, no network.
package drift

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yrstm/agentdash/internal/config"
	"github.com/yrstm/agentdash/internal/eventlog"
)

// Finding is one detected drift item.
type Finding struct {
	ID        string   `json:"id"`         // stable 4-byte hash of (kind, phrase)
	Kind      string   `json:"kind"`       // missing_rule | stale_rule
	Phrase    string   `json:"phrase"`     // the recurring instruction or stale ref
	Count     int      `json:"count"`      // occurrences across prompts
	WindowS   int64    `json:"window_s"`   // observation window in seconds
	Uncertain bool     `json:"uncertain"`  // heuristic match: ? convention
	RuleFile  string   `json:"rule_file,omitempty"` // stale_rule: the file
	RuleLine  int      `json:"rule_line,omitempty"` // stale_rule: line number
	Evidence  []string `json:"evidence"`   // supporting prompt excerpts
}

// Options controls detection behaviour.
type Options struct {
	Project       string
	Home          string
	MinOccurrence int  // minimum repeat count before surfacing (default 3)
	WindowDays    int  // how far back to look (default 7)
	MaxFindings   int  // cap on returned findings (default 20)
	IncludeGlobal bool // include user-global config files
}

// DefaultOptions returns conservative defaults (quiet unless a pattern is clear).
func DefaultOptions(project, home string) Options {
	return Options{
		Project: project, Home: home,
		MinOccurrence: 3, WindowDays: 7, MaxFindings: 20,
	}
}

// Detect runs drift detection. No network, no writes.
func Detect(opt Options) []Finding {
	if opt.MinOccurrence < 1 {
		opt.MinOccurrence = 3
	}
	if opt.WindowDays < 1 {
		opt.WindowDays = 7
	}
	if opt.MaxFindings < 1 {
		opt.MaxFindings = 20
	}

	inv := config.Scan(opt.Project, opt.Home, opt.IncludeGlobal)
	ruleBody := strings.Join(collectRuleLines(inv.Items), " ")

	cutoff := time.Now().Add(-time.Duration(opt.WindowDays) * 24 * time.Hour)
	phrases := gatherPhrases(opt.Project, cutoff)

	// ── missing rule: phrase repeated ≥ threshold, absent from all config ────
	buckets := map[string][]string{}
	for _, p := range phrases {
		buckets[p.norm] = append(buckets[p.norm], p.excerpt)
	}
	var findings []Finding
	for norm, excerpts := range buckets {
		if len(excerpts) < opt.MinOccurrence {
			continue
		}
		if containsFuzzy(ruleBody, norm) {
			continue
		}
		uniq := dedup(excerpts)
		if len(uniq) > 3 {
			uniq = uniq[:3]
		}
		findings = append(findings, Finding{
			ID:        fingerprintID("missing_rule", norm),
			Kind:      "missing_rule",
			Phrase:    norm,
			Count:     len(excerpts),
			WindowS:   int64(time.Since(cutoff).Seconds()),
			Uncertain: true,
			Evidence:  uniq,
		})
	}

	// ── stale rule: config references a path that no longer exists ───────────
	findings = append(findings, staleRules(inv.Items, opt.Project)...)

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind == "stale_rule"
		}
		return findings[i].Count > findings[j].Count
	})
	if len(findings) > opt.MaxFindings {
		findings = findings[:opt.MaxFindings]
	}
	return findings
}

// JSON returns a schema_version 1 JSON document for a slice of findings.
func JSON(findings []Finding) ([]byte, error) {
	doc := struct {
		SchemaVersion int       `json:"schema_version"`
		Findings      []Finding `json:"findings"`
	}{SchemaVersion: 1, Findings: findings}
	if doc.Findings == nil {
		doc.Findings = []Finding{}
	}
	return json.MarshalIndent(doc, "", "  ")
}

// observedPhrase is a normalised instruction phrase extracted from a prompt.
type observedPhrase struct {
	norm    string
	excerpt string
}

// instructionRe matches common imperative patterns in natural-language prompts.
var instructionRe = regexp.MustCompile(
	`(?i)\b(always|never|don'?t|do not|avoid|prefer|make sure|ensure|keep)\b[^.\n!?]{5,80}`)

// gatherPhrases collects instruction phrases from the event log and, when
// the log is sparse, directly from recent session transcripts.
func gatherPhrases(project string, cutoff time.Time) []observedPhrase {
	seen := map[string]bool{}
	var out []observedPhrase

	for _, e := range eventlog.Load(eventlog.LogPath()) {
		if e.Type != eventlog.TypePromptObserved || e.Cwd != project {
			continue
		}
		if ts, err := time.Parse(time.RFC3339, e.TS); err == nil && ts.Before(cutoff) {
			continue
		}
		for _, p := range extractPhrases(e.PromptExcerpt) {
			if !seen[p] {
				seen[p] = true
				out = append(out, observedPhrase{norm: p, excerpt: e.PromptExcerpt})
			}
		}
	}

	// fallback scan of session files for the project
	out = append(out, phrasesFromSessions(project, cutoff, seen)...)
	return out
}

// phrasesFromSessions scans Claude Code session files for the project and
// extracts instruction phrases from user turns.
func phrasesFromSessions(project string, cutoff time.Time, seen map[string]bool) []observedPhrase {
	home, _ := os.UserHomeDir()
	sessRoot := filepath.Join(home, ".claude", "projects")
	dirs, err := os.ReadDir(sessRoot)
	if err != nil {
		return nil
	}
	var out []observedPhrase
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		// decode dir name back to a cwd path: "-home-user-foo" → "/home/user/foo"
		cwd := "/" + strings.TrimLeft(
			strings.ReplaceAll(d.Name(), "-", "/"), "/")
		if cwd != project {
			continue
		}
		dirPath := filepath.Join(sessRoot, d.Name())
		entries, _ := os.ReadDir(dirPath)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			fi, err := e.Info()
			if err != nil || fi.ModTime().Before(cutoff) {
				continue
			}
			path := filepath.Join(dirPath, e.Name())
			for _, prompt := range promptsFromFile(path) {
				for _, p := range extractPhrases(prompt) {
					if seen[p] {
						continue
					}
					seen[p] = true
					out = append(out, observedPhrase{
						norm:    p,
						excerpt: trunc(prompt, 120),
					})
				}
			}
		}
	}
	return out
}

// promptsFromFile reads user turn text from the last 2 MB of a JSONL file.
func promptsFromFile(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	off := st.Size() - 2_000_000
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, 0); err != nil {
		return nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var out []string
	for sc.Scan() {
		var obj struct {
			Type          string          `json:"type"`
			ToolUseResult json.RawMessage `json:"toolUseResult"`
			Message       struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &obj) != nil {
			continue
		}
		if obj.Type != "user" || len(obj.ToolUseResult) > 0 {
			continue
		}
		if txt := contentText(obj.Message.Content); txt != "" {
			out = append(out, txt)
		}
	}
	return out
}

func contentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	for _, r := range parts {
		var p struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(r, &p) == nil && p.Type == "text" {
			return p.Text
		}
	}
	return ""
}

// extractPhrases returns normalised instruction phrases from a text snippet.
func extractPhrases(text string) []string {
	text = strings.Join(strings.Fields(text), " ")
	var out []string
	for _, m := range instructionRe.FindAllString(text, -1) {
		norm := strings.ToLower(strings.Join(strings.Fields(m), " "))
		if len(norm) >= 8 {
			out = append(out, norm)
		}
	}
	return out
}

// collectRuleLines gathers all text from instruction and rule config files.
func collectRuleLines(items []config.Item) []string {
	var lines []string
	for _, it := range items {
		if it.Kind == "instruction" || it.Kind == "rule" {
			lines = append(lines, config.ContentLines(it.Path)...)
		}
	}
	return lines
}

// containsFuzzy checks if a phrase has a fuzzy presence in the rule body:
// at least 2 significant (non-stop, length>4) words must appear.
func containsFuzzy(ruleBody, phrase string) bool {
	lower := strings.ToLower(ruleBody)
	significant := 0
	for _, w := range strings.Fields(phrase) {
		if len(w) > 4 && !stopWords[strings.ToLower(w)] {
			if strings.Contains(lower, strings.ToLower(w)) {
				significant++
			}
		}
	}
	return significant >= 2
}

var stopWords = map[string]bool{
	"always": true, "never": true, "avoid": true, "prefer": true,
	"ensure": true, "should": true, "would": true, "could": true,
	"will": true, "don't": true, "dont": true, "make": true,
	"sure": true, "that": true, "this": true, "with": true,
}

// staleRules finds rules that reference filesystem paths no longer present.
var pathRefRe = regexp.MustCompile(`(?:^|[\s(` + "`" + `"'])(\./[^\s)"'` + "`" + `]+|[a-zA-Z0-9_][a-zA-Z0-9_./-]+/[^\s)"'` + "`" + `]+)`)

func staleRules(items []config.Item, project string) []Finding {
	var out []Finding
	for _, it := range items {
		if it.Kind != "instruction" && it.Kind != "rule" {
			continue
		}
		for i, line := range config.ContentLines(it.Path) {
			for _, m := range pathRefRe.FindAllStringSubmatch(line, -1) {
				if len(m) < 2 {
					continue
				}
				ref := strings.TrimSpace(m[1])
				if ref == "" || strings.Contains(ref, "://") {
					continue
				}
				abs := ref
				if !filepath.IsAbs(ref) {
					abs = filepath.Join(project, ref)
				}
				if _, err := os.Stat(abs); os.IsNotExist(err) {
					out = append(out, Finding{
						ID:       fingerprintID("stale_rule", it.Path+":"+ref),
						Kind:     "stale_rule",
						Phrase:   ref,
						Count:    1,
						RuleFile: it.Path,
						RuleLine: i + 1,
						Evidence: []string{trunc(line, 120)},
					})
				}
			}
		}
	}
	return out
}

func fingerprintID(kind, phrase string) string {
	h := sha256.Sum256([]byte(kind + ":" + phrase))
	return hex.EncodeToString(h[:4])
}

func dedup(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func trunc(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return string(r)
}
