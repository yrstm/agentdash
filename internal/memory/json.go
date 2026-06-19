package memory

import (
	"encoding/json"
	"time"
)

// JSON output for `agentdash memory --json`, a stable schema_version 1 contract
// for tooling (drift alerts, write-guards, push gates). Mirrors the board's
// jsonout style: indented, empty collections as [] not null.

type boardDoc struct {
	SchemaVersion int           `json:"schema_version"`
	Generated     string        `json:"generated"`
	Projects      []projectJSON `json:"projects"`
}

type projectJSON struct {
	Project    string   `json:"project"`
	Files      []string `json:"files"`
	MemoryAgeS int64    `json:"memory_age_s"` // -1 when never logged
	WorkAgeS   int64    `json:"work_age_s"`   // -1 when unknown
	WorkSource string   `json:"work_source"`  // git | fs | ""
	Dirty      bool     `json:"dirty"`
	Stale      bool     `json:"stale"`
	Concurrent bool     `json:"concurrent"`
}

type logDoc struct {
	SchemaVersion int         `json:"schema_version"`
	Generated     string      `json:"generated"`
	Project       string      `json:"project"`
	Events        []eventJSON `json:"events"`
}

type eventJSON struct {
	TS       string `json:"ts"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Mtime    string `json:"mtime"`
	Sessions int    `json:"sessions"`
}

// BoardJSON renders the cross-project memory board as schema_version 1.
func BoardJSON(rows []BoardRow, now time.Time) ([]byte, error) {
	doc := boardDoc{SchemaVersion: 1, Generated: now.UTC().Format(time.RFC3339), Projects: []projectJSON{}}
	for _, r := range rows {
		files := r.Files
		if files == nil {
			files = []string{}
		}
		doc.Projects = append(doc.Projects, projectJSON{
			Project: r.Project, Files: files, MemoryAgeS: r.MemAgeS, WorkAgeS: r.WorkAgeS,
			WorkSource: r.WorkSrc, Dirty: r.Dirty, Stale: r.Stale, Concurrent: r.Concurrent,
		})
	}
	return json.MarshalIndent(doc, "", "  ")
}

// LogJSON renders one project's memory change log as schema_version 1.
func LogJSON(project string, entries []LogEntry, now time.Time) ([]byte, error) {
	doc := logDoc{SchemaVersion: 1, Generated: now.UTC().Format(time.RFC3339), Project: project, Events: []eventJSON{}}
	for _, e := range entries {
		doc.Events = append(doc.Events, eventJSON{
			TS: e.TS, Path: e.Path, Kind: e.Kind, Label: e.Label,
			Bytes: e.Bytes, SHA256: e.SHA256, Mtime: e.Mtime, Sessions: e.Sessions,
		})
	}
	return json.MarshalIndent(doc, "", "  ")
}
