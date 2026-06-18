//go:build !hermes

package history

// extraHistoryReads is empty in the default build: the History tab reads only
// the Claude and Codex JSONL transcripts.
var extraHistoryReads = ""
