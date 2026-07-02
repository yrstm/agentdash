package health

import "encoding/json"

// JSON renders a Report as the schema_version 1 health document.
func JSON(rep Report) ([]byte, error) {
	agents := rep.Agents
	if agents == nil {
		agents = []Agent{}
	}
	zombies := rep.ZombieMCP
	if zombies == nil {
		zombies = []string{}
	}
	doc := struct {
		SchemaVersion int      `json:"schema_version"`
		Flagged       bool     `json:"flagged"`
		Agents        []Agent  `json:"agents"`
		ZombieMCP     []string `json:"zombie_mcp"`
	}{SchemaVersion, rep.Flagged, agents, zombies}
	return json.MarshalIndent(doc, "", "  ")
}
