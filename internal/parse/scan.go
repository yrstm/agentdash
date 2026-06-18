package parse

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"runtime"
	"sync"
)

const histSlots = 8

// ScanSession folds the bytes appended to path since the last look into
// its cache entry and returns it; nil when the file is unreadable. Only
// complete lines are consumed (the agent may be mid-write); a partial
// tail waits for the next call. The entry resets when the kind changed,
// the file shrank, or the parser version was bumped.
func ScanSession(path string, c *Cache, kind string, now float64) *Entry {
	ent := scanOne(path, c.Entries[path], kind, now)
	if ent == nil {
		return nil
	}
	c.Entries[path] = ent
	return ent
}

// ScanMany scans several session files concurrently (a cold cache means
// parsing every paired session in full, and the wall time of a serial
// pass is the sum instead of the max). Entries are written back to the
// cache serially after the join.
func ScanMany(jobs map[string]string, c *Cache, now float64) {
	if len(jobs) == 0 {
		return
	}
	type res struct {
		path string
		ent  *Entry
	}
	sem := make(chan struct{}, runtime.NumCPU())
	out := make(chan res, len(jobs))
	var wg sync.WaitGroup
	for path, kind := range jobs {
		wg.Add(1)
		go func(path, kind string, prev *Entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out <- res{path, scanOne(path, prev, kind, now)}
		}(path, kind, c.Entries[path])
	}
	wg.Wait()
	close(out)
	for r := range out {
		if r.ent != nil {
			c.Entries[r.path] = r.ent
		}
	}
}

// scanOne is the pure scan step: it mutates and returns the entry (or a
// fresh one on reset) without touching the cache map, so callers may run
// it concurrently for distinct paths. The appended region is streamed
// line by line through a fixed window (with an overflow path for entries
// larger than it), so a cold multi-MB scan does not hold whole files in
// memory.
func scanOne(path string, ent *Entry, kind string, now float64) *Entry {
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if ent == nil || ent.Kind != kind || ent.Offset > st.Size() || ent.V != ParserV {
		ent = &Entry{Kind: kind, V: ParserV}
	}
	var consumed int64
	if st.Size() > ent.Offset {
		consumed = scanRegion(path, ent, kind, st.Size())
		ent.Offset += consumed
	}
	if len(ent.Hist) > histSlots-1 {
		ent.Hist = ent.Hist[len(ent.Hist)-(histSlots-1):]
	}
	ent.Hist = append(ent.Hist, consumed)
	ent.Mtime = float64(st.ModTime().UnixNano()) / 1e9
	ent.Seen = now
	return ent
}

const scanWindow = 1 << 20

// scanRegion folds the complete lines in [ent.Offset, size) into ent and
// returns the bytes consumed; a partial tail line (the agent may be
// mid-write) is left for the next scan.
func scanRegion(path string, ent *Entry, kind string, size int64) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }() // read-only
	if _, err := f.Seek(ent.Offset, io.SeekStart); err != nil {
		return 0
	}
	r := bufio.NewReaderSize(io.LimitReader(f, size-ent.Offset), scanWindow)
	var consumed int64
	var overflow []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull { // an entry larger than the window
			overflow = append(overflow, chunk...)
			continue
		}
		if err != nil { // EOF: chunk is a partial tail, leave it unconsumed
			break
		}
		line := chunk
		if len(overflow) > 0 {
			overflow = append(overflow, chunk...)
			line = overflow
		}
		consumed += int64(len(line))
		if ln := bytes.TrimSpace(line); len(ln) > 0 {
			Apply(kind, ent, ln)
		}
		overflow = overflow[:0]
	}
	return consumed
}
