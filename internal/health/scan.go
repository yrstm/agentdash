package health

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"

	"github.com/yrstm/agentdash/internal/procs"
)

// tailStats counts API errors and user interrupts within the last N turns of a
// Claude transcript.
type tailStats struct {
	turns      int
	apiErrors  int
	interrupts int
}

// scanTail scans a Claude transcript and reports error/interrupt counts over
// the last n user/assistant turns. Detection keys on markers Claude Code
// writes: isApiErrorMessage on an assistant turn, and a user message beginning
// "[Request interrupted". Codex transcripts have no equivalent marker, so they
// contribute no turns here (the signal is Claude-only, and simply absent for
// codex rows).
func scanTail(path string, n int) tailStats {
	type turn struct{ apiErr, interrupt bool }
	var turns []turn
	scanLines(path, func(line []byte) {
		var obj struct {
			Type              string `json:"type"`
			IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
			Message           struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &obj) != nil {
			return
		}
		switch obj.Type {
		case "assistant":
			turns = append(turns, turn{apiErr: obj.IsAPIErrorMessage})
		case "user":
			it := strings.HasPrefix(strings.TrimSpace(flattenText(obj.Message.Content)), "[Request interrupted")
			turns = append(turns, turn{interrupt: it})
		}
	})
	if n > 0 && len(turns) > n {
		turns = turns[len(turns)-n:]
	}
	st := tailStats{turns: len(turns)}
	for _, t := range turns {
		if t.apiErr {
			st.apiErrors++
		}
		if t.interrupt {
			st.interrupts++
		}
	}
	return st
}

// flattenText pulls the human text out of a Claude message content (string or
// typed-part array), enough to spot an interrupt marker.
func flattenText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
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
		if json.Unmarshal(r, &p) == nil && (p.Type == "text" || p.Type == "input_text") {
			return p.Text
		}
	}
	return ""
}

// zombieMCP finds MCP server processes whose launching agent is gone —
// reparented to init (ppid 1). See mcpZombiesFrom for the (pure, tested) rule.
func zombieMCP() []string { return mcpZombiesFrom(procs.AllProcs()) }

// mcpZombiesFrom flags MCP-looking processes reparented to init: a strong sign
// the agent that launched them exited but the server kept running. Conservative
// on purpose — it keys on unmistakable MCP markers in the command line, so a
// server still owned by a live agent (ppid != 1) is never flagged.
func mcpZombiesFrom(all []procs.LiteProc) []string {
	var out []string
	for _, p := range all {
		if p.PPID != 1 || !looksMCP(p.Args) {
			continue
		}
		out = append(out, itoa(p.PID)+" "+p.Args)
	}
	return out
}

func looksMCP(args string) bool {
	return strings.Contains(args, "mcp-server") ||
		strings.Contains(args, "modelcontextprotocol") ||
		strings.Contains(args, "mcp_server")
}

// scanLines streams a JSONL file, reassembling oversized lines; fail-soft.
func scanLines(path string, fn func([]byte)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1<<20)
	var overflow []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			overflow = append(overflow, chunk...)
			continue
		}
		line := chunk
		if len(overflow) > 0 {
			overflow = append(overflow, chunk...)
			line = overflow
		}
		if ln := bytes.TrimSpace(line); len(ln) > 0 {
			fn(ln)
		}
		overflow = overflow[:0]
		if err != nil {
			return
		}
	}
}
