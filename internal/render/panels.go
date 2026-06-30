package render

// The drill-down (show) and provenance (why) panels: text builders shared
// by the subcommands and the watch-mode overlays.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/yrstm/agentdash/internal/board"
	"github.com/yrstm/agentdash/internal/parse"
)

// Why prints the provenance panel: which evidence produced every value.
func Why(cache *parse.Cache, pid int, t Theme, now float64) (string, error) {
	m, ent, err := board.PidEntry(cache, pid)
	if err != nil {
		return "", err
	}
	howTxt := map[string]string{
		"fd":       "process holds the jsonl open under its project dir (exact)",
		"cwd":      "only one live session file in the project dir for its cwd (exact)",
		"start-ts": "session first-entry timestamp matches process start ±5min (confident)",
		"sticky":   "kept last draw's guess (sticky heuristic: could be wrong)",
		"recency":  "newest unclaimed file written since process start (heuristic)",
		"meta": "session file records this cwd in its metadata " +
			"(exact when the filename timestamp also matches process start)",
		"session-id": "process exposed its session id directly (exact)",
		"cwd+start":  "active store session matched process cwd and start time (heuristic)",
		"start":      "nearest active store session by start time (heuristic)",
	}
	how := m.How
	if v, ok := howTxt[how]; ok {
		how = v
	}
	overrides := parse.LoadOverrides(board.ConfPath())
	win, src := parse.WindowFor(ent.Model, overrides)
	if ent.Win != 0 {
		win, src = ent.Win, "recorded in the rollout file (exact)"
	}
	if win != 0 && ent.Ctx > win {
		win, src = 1_000_000, src+", self-corrected to 1M (measured context exceeded it)"
	}
	model := ent.Model
	if model == "" {
		model = "?"
	}
	winS, ctxS := "?", "?"
	if win != 0 {
		winS = strconv.FormatInt(win, 10)
	}
	if ent.Ctx != 0 {
		ctxS = strconv.FormatInt(ent.Ctx, 10)
	}
	if src == "" {
		src = "unknown model"
	}
	var w strings.Builder
	fmt.Fprintf(&w, "%spid %d%s %s→ %s%s\n\n", t.B, pid, t.N, t.D, m.Path, t.N)
	fmt.Fprintf(&w, "  Pairing:  %s\n", how)
	fmt.Fprintf(&w, "  Model:    %q: last assistant message in the session file\n", model)
	fmt.Fprintf(&w, "  Window:   %s: %s\n", winS, src)
	fmt.Fprintf(&w, "  Context:  %s tokens in the most recent request (incl. cache)\n", ctxS)
	fmt.Fprintf(&w, "  Tokens:   in=%d out=%d: summed usage blocks, deduped by message id\n", ent.In, ent.Out)
	fmt.Fprintf(&w, "  Status:   file written %s ago, last entry type %q\n",
		parse.Ago(int64(now-ent.Mtime)), ent.LastType)
	fmt.Fprintf(&w, "\n  %svalues marked exact come from the file or /proc; heuristics say so%s\n", t.D, t.N)
	return w.String(), nil
}

// Show prints the drill-down panel: task, usage, recent turns, resume.
func Show(cache *parse.Cache, pid int, t Theme, now float64) (string, error) {
	m, ent, err := board.PidEntry(cache, pid)
	if err != nil {
		return "", err
	}
	title := parse.TitleOf(ent, m.Path, cache.Labels)
	if title == "" {
		title = "(untitled)"
	}
	var w strings.Builder
	fmt.Fprintf(&w, "%s%s%s\n\n", t.B, title, t.N)
	fmt.Fprintf(&w, "  Model:    %s\n", parse.ShortModel(ent.Model))
	fmt.Fprintf(&w, "  Tokens:   %s in / %s out  %s(input includes cache)%s\n",
		parse.Hum(ent.In), parse.Hum(ent.Out), t.D, t.N)
	overrides := parse.LoadOverrides(board.ConfPath())
	win, _ := parse.WindowFor(ent.Model, overrides)
	if ent.Win != 0 {
		win = ent.Win
	}
	if win != 0 && ent.Ctx > win {
		win = 1_000_000
	}
	if win != 0 && ent.Ctx != 0 {
		pct := int(math.RoundToEven(float64(ent.Ctx) * 100 / float64(win)))
		if pct > 100 {
			pct = 100
		}
		filled := int(math.RoundToEven(float64(pct) * 30 / 100))
		bc := ""
		if pct >= 85 {
			bc = t.R
		} else if pct >= 70 {
			bc = t.Y
		}
		fmt.Fprintf(&w, "  Context:  %s%s%s%s%s%s %d%% used\n",
			bc, strings.Repeat("█", filled), t.N, t.D, strings.Repeat("░", 30-filled), t.N, pct)
	} else {
		fmt.Fprintf(&w, "  Context:  %sunknown%s\n", t.D, t.N)
	}
	if ent.CompactionN > 0 {
		cc := t.Y
		if ent.CompactionN >= 3 {
			cc = t.R
		}
		fmt.Fprintf(&w, "  Compact:  %s%d context compaction(s) in this session%s\n", cc, ent.CompactionN, t.N)
	}
	fmt.Fprintf(&w, "  Last:     written %s ago\n", parse.Ago(int64(now-ent.Mtime)))
	fmt.Fprintf(&w, "  Session:  %s%s%s\n", t.D, m.Path, t.N)
	fmt.Fprintf(&w, "  Resume:   %s\n", board.ResumeCmd(m))
	fmt.Fprintf(&w, "\n  %sRecent turns%s\n", t.B, t.N)
	turns := recentTurns(m.Path, 6)
	if externalTurns != nil {
		if t2, ok := externalTurns(m.Kind, m.Path, 6); ok {
			turns = t2
		}
	}
	for _, turn := range turns {
		fmt.Fprintf(&w, "    %9s%s:%s %s\n", turn[0], t.D, t.N, turn[1])
	}
	if fts := parse.FirstTS(m.Path); fts != 0 && ent.Mtime != 0 {
		span := ent.Mtime - float64(fts)
		if ted := span / (18 * 60); ted >= 2 {
			fmt.Fprintf(&w, "\n  %sThis session spans ~%.0fx a TED talk%s\n", t.D, ted, t.N)
		}
	}
	return w.String(), nil
}

// recentTurns reads a wide tail (a single entry can be huge) and extracts
// the last n human-readable turns as (role, text) pairs.
func recentTurns(path string, n int) [][2]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }() // read-only
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	off := st.Size() - 4_000_000
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil
	}
	data, _ := io.ReadAll(f)
	var turns [][2]string
	for _, ln := range bytes.Split(data, []byte{'\n'}) {
		var obj struct {
			Type          string          `json:"type"`
			ToolUseResult json.RawMessage `json:"toolUseResult"`
			Message       json.RawMessage `json:"message"`
			Payload       struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"payload"`
		}
		if json.Unmarshal(ln, &obj) != nil {
			continue
		}
		switch obj.Type {
		case "user", "assistant":
			var msg struct {
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(obj.Message, &msg) != nil {
				continue
			}
			txt := contentText(msg.Content)
			if txt != "" && (obj.Type == "assistant" || obj.ToolUseResult == nil) {
				turns = append(turns, [2]string{obj.Type, collapse150(txt)})
			}
		case "event_msg": // codex rollout shape
			if obj.Payload.Message == "" {
				continue
			}
			switch obj.Payload.Type {
			case "user_message":
				turns = append(turns, [2]string{"user", collapse150(obj.Payload.Message)})
			case "agent_message":
				turns = append(turns, [2]string{"assistant", collapse150(obj.Payload.Message)})
			}
		}
	}
	if len(turns) > n {
		turns = turns[len(turns)-n:]
	}
	return turns
}

func contentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return s
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

func collapse150(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 150 {
		return string(r[:150])
	}
	return s
}
