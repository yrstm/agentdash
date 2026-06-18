// Package hermesdb is an optional, read-only adapter for the Hermes agent's
// SQLite session store. It is compiled only into builds tagged "hermes"
// (go build -tags hermes); the default agentdash binary excludes it entirely,
// so the core stays dependency-free and SQLite-free. The agent CLIs agentdash
// supports out of the box (Claude, Codex) write JSONL transcripts; Hermes is
// the one store that is SQLite-backed, hence the opt-in.
//
// Expected schema (read-only; every query fails soft, so a schema change just
// yields no Hermes rows, never a crash):
//
//	sessions(id TEXT, source TEXT, model TEXT, started_at REAL, ended_at REAL,
//	         input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
//	         reasoning_tokens INTEGER, cwd TEXT, title TEXT)
//	messages(id INTEGER, session_id TEXT, role TEXT, content TEXT,
//	         tool_name TEXT, timestamp REAL)
//
// Only those columns are read. The adapter opens each state.db with
// mode=ro&_pragma=query_only(1) and never writes.
package hermesdb
