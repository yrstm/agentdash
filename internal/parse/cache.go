package parse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const cacheTTL = 7 * 86400 // entries unseen this long are evicted on save

// PidInfo records a pid-to-session pairing and the evidence tier that
// made it ("how"), for the why/show/resume/label subcommands.
type PidInfo struct {
	Path    string  `json:"path"`
	Start   float64 `json:"start"`
	Sure    bool    `json:"sure"`
	Cwd     string  `json:"cwd"`
	How     string  `json:"how"`
	Kind    string  `json:"kind,omitempty"`
	Profile string  `json:"profile,omitempty"`
}

// Cache mirrors ~/.cache/agentdash/usage.json: session entries keyed by
// path plus the v1 special keys (_labels, _pidmap, _recap_ts,
// _pids_by_path). Unknown underscore keys are preserved verbatim so a
// newer writer never destroys an older reader's state.
type Cache struct {
	Entries    map[string]*Entry
	Labels     map[string]string
	PidMap     map[string]PidInfo
	PidsByPath map[string]map[string]float64
	RecapTS    float64
	extra      map[string]json.RawMessage
}

func NewCache() *Cache {
	return &Cache{
		Entries:    map[string]*Entry{},
		Labels:     map[string]string{},
		PidMap:     map[string]PidInfo{},
		PidsByPath: map[string]map[string]float64{},
		extra:      map[string]json.RawMessage{},
	}
}

// LoadCache reads the cache, failing soft to an empty one: a missing or
// corrupt file never kills the board. Individual undecodable entries are
// skipped, matching v1's all-or-nothing only at the file level.
func LoadCache(path string) *Cache {
	c := NewCache()
	b, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return c
	}
	for k, v := range raw {
		switch k {
		case "_labels":
			_ = json.Unmarshal(v, &c.Labels)
		case "_pidmap":
			_ = json.Unmarshal(v, &c.PidMap)
		case "_pids_by_path":
			_ = json.Unmarshal(v, &c.PidsByPath)
		case "_recap_ts":
			_ = json.Unmarshal(v, &c.RecapTS)
		default:
			if strings.HasPrefix(k, "_") {
				c.extra[k] = v
				continue
			}
			var e Entry
			if json.Unmarshal(v, &e) == nil {
				c.Entries[k] = &e
			}
		}
	}
	return c
}

// Save writes the cache atomically (temp file in the same dir, chmod 600,
// rename: the cache holds prompt text), evicting entries unseen for the
// TTL window.
func (c *Cache) Save(path string, now float64) error {
	out := map[string]any{}
	for p, e := range c.Entries {
		if now-e.Seen < cacheTTL {
			out[p] = e
		}
	}
	if len(c.Labels) > 0 {
		out["_labels"] = c.Labels
	}
	if len(c.PidMap) > 0 {
		out["_pidmap"] = c.PidMap
	}
	if len(c.PidsByPath) > 0 {
		out["_pids_by_path"] = c.PidsByPath
	}
	if c.RecapTS != 0 {
		out["_recap_ts"] = c.RecapTS
	}
	for k, v := range c.extra {
		out[k] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil { // WriteFile mode is umask-filtered
		_ = os.Remove(tmp) // best-effort cleanup of the temp file
		return err
	}
	return os.Rename(tmp, path)
}
