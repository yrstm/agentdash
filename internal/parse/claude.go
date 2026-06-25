package parse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const lastTextW = 160

// content models claude's message.content, which is either a plain string
// or a list of typed parts; elements that are not objects are skipped.
type content struct {
	isStr bool
	str   string
	parts []contentPart
}

type contentPart struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (c *content) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if json.Unmarshal(b, &s) == nil {
			c.isStr, c.str = true, s
		}
		return nil
	}
	// fast path: a homogeneous list of typed parts (the usual shape)
	if json.Unmarshal(b, &c.parts) == nil {
		return nil
	}
	// mixed array: decode element-wise, skipping non-objects like v1
	c.parts = nil
	var raws []json.RawMessage
	if json.Unmarshal(b, &raws) != nil {
		return nil
	}
	for _, r := range raws {
		var p contentPart
		if json.Unmarshal(r, &p) == nil {
			c.parts = append(c.parts, p)
		}
	}
	return nil
}

// jsonPresent records that a key existed without copying its value
// (tool results can be huge).
type jsonPresent bool

func (p *jsonPresent) UnmarshalJSON([]byte) error { *p = true; return nil }

// usage mirrors the v1 truthiness check on the usage dict: present only
// when the object has at least one key. Non-numeric values fail soft to
// zero without discarding the rest of the line.
type usage struct {
	present                  bool
	in, cacheCreate, cacheRd int64
	out                      int64
}

func (u *usage) UnmarshalJSON(b []byte) error {
	var v struct {
		InputTokens              jsonInt `json:"input_tokens"`
		CacheCreationInputTokens jsonInt `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     jsonInt `json:"cache_read_input_tokens"`
		OutputTokens             jsonInt `json:"output_tokens"`
	}
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	u.present = bytes.ContainsRune(b, ':') // {} and null stay absent
	u.in, u.cacheCreate, u.cacheRd = int64(v.InputTokens), int64(v.CacheCreationInputTokens), int64(v.CacheReadInputTokens)
	u.out = int64(v.OutputTokens)
	return nil
}

// jsonInt decodes a number, failing soft to 0 on any other shape.
type jsonInt int64

func (n *jsonInt) UnmarshalJSON(b []byte) error {
	var f float64
	if json.Unmarshal(b, &f) == nil {
		*n = jsonInt(f)
	}
	return nil
}

type claudeMsg struct {
	ID      string  `json:"id"`
	Model   string  `json:"model"`
	Content content `json:"content"`
	Usage   usage   `json:"usage"`
}

type claudeLine struct {
	Type          string      `json:"type"`
	Timestamp     string      `json:"timestamp"`
	Summary       string      `json:"summary"`
	Message       *claudeMsg  `json:"message"`
	ToolUseResult jsonPresent `json:"toolUseResult"`
}

func firstUserText(m *claudeMsg) string {
	if m == nil {
		return ""
	}
	if m.Content.isStr {
		return m.Content.str
	}
	for _, p := range m.Content.parts {
		if p.Type == "text" {
			return p.Text
		}
	}
	return ""
}

func claudeToolActivity(m *claudeMsg) string {
	if m == nil || m.Content.isStr {
		return ""
	}
	for _, p := range m.Content.parts {
		if p.Type != "tool_use" {
			continue
		}
		name := p.Name
		if name == "" {
			name = "tool"
		}
		detail := toolInputDetail(p.Input)
		if detail == "" {
			return name
		}
		return fmt.Sprintf("%s: %s", name, detail)
	}
	return ""
}

func toolInputDetail(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var v struct {
		Description string `json:"description"`
		Command     string `json:"command"`
		Query       string `json:"query"`
		Pattern     string `json:"pattern"`
		Path        string `json:"path"`
		FilePath    string `json:"file_path"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	for _, s := range []string{v.Description, v.Command, v.Query, v.Pattern, v.Path, v.FilePath} {
		if s != "" {
			return truncRunes(collapseWS(s), lastTextW)
		}
	}
	return ""
}

// isoEpoch converts an ISO-8601 timestamp's first 19 chars to a UTC epoch;
// 0 means unparseable, mirroring the v1 None.
func isoEpoch(ts string) int64 {
	if len(ts) < 19 {
		return 0
	}
	t, err := time.Parse("2006-01-02T15:04:05", ts[:19])
	if err != nil {
		return 0
	}
	return t.Unix()
}

func collapseWS(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncRunes(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		return string(r[:w])
	}
	return s
}

func updateClaude(ent *Entry, line []byte) {
	var obj claudeLine
	if json.Unmarshal(line, &obj) != nil {
		return
	}
	t := obj.Type
	if t == "user" || t == "assistant" {
		ent.LastType = t
	}
	switch t {
	case "summary":
		ent.CompactionN++ // every summary entry is one context-compaction cycle
		if obj.Summary != "" && ent.Summary == "" {
			ent.Summary = obj.Summary // keep the first summary as the task title
		}
	case "user":
		txt := firstUserText(obj.Message)
		if ent.TitleUser == "" && usableTitle(txt) {
			ent.TitleUser = txt
		}
		// a user line carrying toolUseResult is a tool result, not a human turn
		if txt != "" && !obj.ToolUseResult {
			if ts := isoEpoch(obj.Timestamp); ts != 0 {
				ent.LastUserTS = ts
			}
		}
	case "assistant":
		if obj.Message == nil {
			return
		}
		if obj.Message.Model != "" {
			ent.Model = obj.Message.Model
		}
		if txt := firstUserText(obj.Message); txt != "" {
			ent.LastText = truncRunes(collapseWS(txt), lastTextW)
			ent.Activity = ent.LastText
		}
		if tool := claudeToolActivity(obj.Message); tool != "" {
			ent.LastType = "tool"
			ent.LastTool = truncRunes(collapseWS(tool), lastTextW)
			ent.Activity = ent.LastTool
		}
		// one turn writes a line per content block, all carrying the same
		// usage: dedupe by message id or totals get inflated
		u, mid := obj.Message.Usage, obj.Message.ID
		if u.present && (mid == "" || mid != ent.LastMid) {
			ent.LastMid = mid
			in := u.in + u.cacheCreate + u.cacheRd
			ent.In += in
			ent.Out += u.out
			if in != 0 {
				ent.Ctx = in
			}
		}
	}
}
