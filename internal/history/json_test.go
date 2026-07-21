package history

import (
	"encoding/json"
	"testing"
)

func TestJSONContract(t *testing.T) {
	b, err := JSON(Result{Sessions: []Session{{
		Agent: "codex", SessionID: "s1", Cwd: "/work/api", Title: "fix checkout",
		Last: 42, Live: true, Resume: "cd /work/api && codex resume s1",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SchemaVersion int       `json:"schema_version"`
		Sessions      []Session `json:"sessions"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || len(doc.Sessions) != 1 || !doc.Sessions[0].Live {
		t.Fatalf("bad document: %s", b)
	}
}
