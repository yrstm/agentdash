package render

import "testing"

func TestPadCountsDisplayCells(t *testing.T) {
	// multibyte glyphs must not break column alignment
	if got := Pad("×2", 5); got != "×2   " {
		t.Errorf("Pad(×2) = %q", got)
	}
	if got := Pad("abc", 2); got != "abc" {
		t.Errorf("over-width Pad = %q", got)
	}
}

func TestTrunc(t *testing.T) {
	if got := Trunc("hello world", 6); got != "hello…" {
		t.Errorf("Trunc = %q", got)
	}
	if got := Trunc("hi", 6); got != "hi" {
		t.Errorf("short Trunc = %q", got)
	}
}

func TestFishPath(t *testing.T) {
	home := "/home/u"
	for in, want := range map[string]string{
		"/home/u/code/checkout-api": "~/c/checkout-api",
		"/work/api":                 "/w/api",
		"/home/u":                   "~",
		"plain":                     "plain",
	} {
		if got := FishPath(in, home, 16); got != want {
			t.Errorf("FishPath(%q) = %q, want %q", in, got, want)
		}
	}
	// the tail survives truncation
	got := FishPath("/very/deep/path/to/the-final-component-name", "", 16)
	if got != "…l-component-name" && len([]rune(got)) != 16 {
		t.Errorf("truncated FishPath = %q (%d runes)", got, len([]rune(got)))
	}
}

func TestFmtUp(t *testing.T) {
	for in, want := range map[int64]string{
		42:      "42s",
		2520:    "42m",
		57600:   "16h",
		108000:  "1d6h",
		950400:  "11d0h", // length 5 still fits, matching v1
		1083600: "12d",   // "12d13h" overflows 5 chars and drops the hours
	} {
		if got := FmtUp(in); got != want {
			t.Errorf("FmtUp(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestCtxCellPlain(t *testing.T) {
	plain := Theme{}
	if got := CtxCell("40%", plain); got != "40%       " {
		t.Errorf("plain CtxCell = %q", got)
	}
	if got := CtxCell("-", plain); got != "-         " {
		t.Errorf("dash CtxCell = %q", got)
	}
}

func TestCtxCellBar(t *testing.T) {
	th := Theme{R: "\x1b[31m", N: "\x1b[0m"}
	got := CtxCell("40%", th)
	if want := "▓▓░░░  40%" + th.N; got != want {
		t.Errorf("CtxCell(40%%) = %q, want %q", got, want)
	}
	if got := CtxCell("90%", th); got != th.R+"▓▓▓▓▓  90%"+th.N {
		t.Errorf("red CtxCell = %q", got)
	}
}

func TestFriendlyWhat(t *testing.T) {
	for in, want := range map[string]string{
		"-bash":                          "shell",
		"tmux attach":                    "tmux",
		"/x/node_modules/vite/bin/vite":  "vite (npx)",
		"/srv/app/venv/bin/gunicorn -w4": "gunicorn",
		"/usr/bin/htop":                  "htop",
	} {
		if got := FriendlyWhat(in); got != want {
			t.Errorf("FriendlyWhat(%q) = %q, want %q", in, got, want)
		}
	}
}
