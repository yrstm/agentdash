package render

import "os"

// Theme carries the six SGR fragments the board uses; one meaning per
// color (green working, yellow worth a look, red needs you, dim
// ignorable). Plain mode and NO_COLOR empty them all.
type Theme struct {
	B, D, G, Y, R, V, N string
}

// NewTheme honors --plain and NO_COLOR.
func NewTheme(plain bool) Theme {
	if plain || os.Getenv("NO_COLOR") != "" {
		return Theme{}
	}
	return Theme{
		B: "\x1b[1m",
		D: "\x1b[2m",
		G: "\x1b[32m",
		Y: "\x1b[33m",
		R: "\x1b[31m",
		V: "\x1b[38;5;141m", // violet, for the launch banner
		N: "\x1b[0m",
	}
}
