package config

import "encoding/json"

// JSON returns the inspect JSON document. schema_version 2 adds the per-item
// bytes/token_est/modified/tracked columns and the result's
// always_loaded_tokens footer (A6) — additive over v1, no field removed.
func JSON(r Result) ([]byte, error) {
	doc := struct {
		SchemaVersion int    `json:"schema_version"`
		Result        Result `json:"result"`
	}{SchemaVersion: 2, Result: r}
	return json.MarshalIndent(doc, "", "  ")
}
