package du

import "encoding/json"

// JSON renders a Result as the schema_version 1 du document.
func JSON(res Result) ([]byte, error) {
	cats := res.Categories
	if cats == nil {
		cats = []Category{}
	}
	doc := struct {
		SchemaVersion int        `json:"schema_version"`
		TotalBytes    int64      `json:"total_bytes"`
		Categories    []Category `json:"categories"`
	}{SchemaVersion, res.Total, cats}
	return json.MarshalIndent(doc, "", "  ")
}
