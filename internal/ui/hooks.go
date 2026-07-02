package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/yrstm/agentdash/internal/board"
	"github.com/yrstm/agentdash/internal/jsonout"
)

// Hooks are user commands run on agent state transitions in watch mode. An
// empty command disables that hook. The binary itself opens no socket; the
// command is what reaches the network, on its own.
type Hooks struct {
	OnNeedsYou string // fires when an agent enters a needs-you state
	OnStuck    string // fires when an agent's status becomes stuck?
}

// Any reports whether at least one hook is configured.
func (h Hooks) Any() bool { return h.OnNeedsYou != "" || h.OnStuck != "" }

// hookTimeout bounds a hook command so a hung notifier cannot leak a
// process or goroutine across the session.
const hookTimeout = 10 * time.Second

// hookDebounce is the minimum gap between fires for the same session and event.
// Edge detection already fires once per transition; this guards the case where
// a status flickers (e.g. waiting->working->waiting) and re-crosses the same
// edge within a minute.
const hookDebounce = 60 * time.Second

// hookKey identifies a debounce bucket: one session (pid) and event name, so a
// working->stuck? transition still raises both needs_you and stuck at once.
type hookKey struct {
	pid  int
	name string
}

// hookEvent is one fired transition: the event name, the command to run,
// and the row it fired for.
type hookEvent struct {
	name string
	cmd  string
	row  board.Row
}

// hookPayload is the JSON handed to a hook command on stdin. Agent is a
// single schema_version 1 agent object, byte-identical to an entry in the
// `agents` array of `agentdash --json`.
type hookPayload struct {
	Event    string          `json:"event"`
	TS       int64           `json:"ts"`
	Attached bool            `json:"attached"`
	Agent    json.RawMessage `json:"agent"`
}

// needsYou mirrors board's needs-you classification so the hook can tell
// whether a prior status already counted as needing you (and so a single
// transition raises a single event).
func needsYou(status string) bool {
	return status == "waiting" || status == "stuck?" || strings.HasPrefix(status, "respawn")
}

// statusMap snapshots the per-pid status of a board for the next tick's
// transition comparison.
func statusMap(b *board.Board) map[int]string {
	if b == nil {
		return nil
	}
	m := make(map[int]string, len(b.Rows))
	for _, r := range b.Rows {
		m[r.PID] = r.Status
	}
	return m
}

// detectHooks returns the events implied by the transition from prev (last
// tick's status per pid) to the new board. It fires on the *entry* into a
// state, so it never fires on the first tick (empty prev), never for a pid
// with no prior state, and never while a state persists. Pure: the firing
// side effects live in fireHooks, so this is unit-tested without spawning a
// process. A working->stuck? transition raises both events when both hooks
// are set; stuck? is itself a needs-you state.
func detectHooks(h Hooks, prev map[int]string, nb *board.Board) []hookEvent {
	if !h.Any() || len(prev) == 0 || nb == nil {
		return nil
	}
	var out []hookEvent
	for _, r := range nb.Rows {
		was, seen := prev[r.PID]
		if !seen {
			continue // a brand-new pid has no prior state to transition from
		}
		if h.OnNeedsYou != "" && r.Need && !needsYou(was) {
			out = append(out, hookEvent{"needs_you", h.OnNeedsYou, r})
		}
		if h.OnStuck != "" && r.Status == "stuck?" && was != "stuck?" {
			out = append(out, hookEvent{"stuck", h.OnStuck, r})
		}
	}
	return out
}

// buildPayload marshals the JSON a hook command receives on stdin.
func buildPayload(ev hookEvent, now int64) ([]byte, error) {
	agent, err := jsonout.AgentJSON(ev.row)
	if err != nil {
		return nil, err
	}
	return json.Marshal(hookPayload{
		Event:    ev.name,
		TS:       now,
		Attached: ev.row.Glyph == "●",
		Agent:    agent,
	})
}

// runHook executes one event's command via `sh -c`, non-blocking so a slow
// notifier never stalls the refresh tick. The payload arrives on stdin and
// the headline fields are also exported as env vars for shell one-liners.
// The child is reaped (no zombie) and bounded by hookTimeout.
func runHook(ev hookEvent) {
	payload, err := buildPayload(ev, time.Now().Unix())
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", ev.cmd)
		cmd.Stdin = bytes.NewReader(payload)
		cmd.Env = append(os.Environ(),
			"AGENTDASH_EVENT="+ev.name,
			"AGENTDASH_PID="+strconv.Itoa(ev.row.PID),
			"AGENTDASH_TASK="+ev.row.Task,
			"AGENTDASH_AGENT="+ev.row.Kind,
			"AGENTDASH_CWD="+ev.row.Cwd,
			"AGENTDASH_STATUS="+ev.row.Status,
		)
		_ = cmd.Run() // fire-and-forget: a hook's own output is its concern
	}()
}

// debounceHooks drops events whose (session, event) fired less than
// hookDebounce ago, recording the fire time of those it keeps. Pure and
// unit-tested; last is mutated in place so the caller carries state across
// ticks. now is epoch seconds.
func debounceHooks(events []hookEvent, last map[hookKey]int64, now int64) []hookEvent {
	const gap = int64(hookDebounce / time.Second)
	var kept []hookEvent
	for _, ev := range events {
		k := hookKey{ev.row.PID, ev.name}
		if t, ok := last[k]; ok && now-t < gap {
			continue
		}
		last[k] = now
		kept = append(kept, ev)
	}
	return kept
}

// fireHooks runs every command implied by the transition to nb, after the
// per-session debounce. last carries fire times across ticks; now is epoch
// seconds.
func fireHooks(h Hooks, prev map[int]string, nb *board.Board, last map[hookKey]int64, now int64) {
	for _, ev := range debounceHooks(detectHooks(h, prev, nb), last, now) {
		runHook(ev)
	}
}
