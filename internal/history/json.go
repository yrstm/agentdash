package history

import "encoding/json"

// SchemaVersion is the independent, additive-only sessions JSON contract.
const SchemaVersion = 1

type document struct {
	SchemaVersion int       `json:"schema_version"`
	Sessions      []Session `json:"sessions"`
	Skipped       int       `json:"skipped"`
}

// JSON emits the browseable session history consumed by companion UIs.
func JSON(res Result) ([]byte, error) {
	if res.Sessions == nil {
		res.Sessions = []Session{}
	}
	return json.MarshalIndent(document{
		SchemaVersion: SchemaVersion,
		Sessions:      res.Sessions,
		Skipped:       len(res.Skipped),
	}, "", "  ")
}
