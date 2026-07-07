package usage

import "encoding/json"

// JSON renders a Report as the schema_version 1 usage document. The header
// note is part of the contract: everything here is a local estimate.
func JSON(rep Report) ([]byte, error) {
	if rep.Models == nil {
		rep.Models = []ModelUse{}
	}
	if rep.Sessions == nil {
		rep.Sessions = []SessionUse{}
	}
	if rep.Projects == nil {
		rep.Projects = []ProjectCache{}
	}
	doc := struct {
		SchemaVersion int            `json:"schema_version"`
		Note          string         `json:"note"`
		Limit         int64          `json:"limit"`
		Total5h       int64          `json:"total_5h"`
		Total7d       int64          `json:"total_7d"`
		BurnPerMin    float64        `json:"burn_per_min"`
		ProjFillSecs  int64          `json:"proj_fill_secs"`
		Models        []ModelUse     `json:"models"`
		Sessions      []SessionUse   `json:"sessions"`
		Projects      []ProjectCache `json:"projects"`
	}{
		SchemaVersion: SchemaVersion,
		Note:          "estimate from local transcripts only; not provider-reported. Cannot see provider-side limits or usage on other machines.",
		Limit:         rep.Limit,
		Total5h:       rep.Total5h,
		Total7d:       rep.Total7d,
		BurnPerMin:    rep.BurnPerMin,
		ProjFillSecs:  rep.ProjFillSecs,
		Models:        rep.Models,
		Sessions:      rep.Sessions,
		Projects:      rep.Projects,
	}
	return json.MarshalIndent(doc, "", "  ")
}
