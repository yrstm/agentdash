package config

import "encoding/json"

// JSON returns a schema_version 1 JSON document for a Result.
func JSON(r Result) ([]byte, error) {
	doc := struct {
		SchemaVersion int    `json:"schema_version"`
		Result        Result `json:"result"`
	}{SchemaVersion: 1, Result: r}
	return json.MarshalIndent(doc, "", "  ")
}
