package parse

import (
	"bufio"
	"encoding/json"
	"os"
)

// FirstTS returns the epoch of the session's first timestamped entry
// (approximately the session start), looking at most 25 lines in; 0 when
// none is found. Callers should memoize per draw.
func FirstTS(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }() // read-only
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for i := 0; i < 25 && sc.Scan(); i++ {
		var obj struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(sc.Bytes(), &obj) == nil && obj.Timestamp != "" {
			if ts := isoEpoch(obj.Timestamp); ts != 0 {
				return ts
			}
		}
	}
	return 0
}
