module github.com/yrstm/agentdash

go 1.25.0

// v1.0.0 was the pre-rewrite (bash) line, tagged by mistake; its proxy
// snapshot carries stale errors and must not be used. Retracted so tooling
// (go install @latest, go list) skips it. v1.0.1 is a retraction-only shim:
// it must outrank v1.0.0 for the go tool to read this block, so it retracts
// itself too, leaving v0.2.4 as the resolved @latest.
retract (
	v1.0.0
	v1.0.1
)

require (
	github.com/mattn/go-runewidth v0.0.24
	golang.org/x/term v0.44.0
	modernc.org/sqlite v1.52.0
)

require (
	github.com/clipperhouse/stringish v0.1.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.5.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.46.0 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
