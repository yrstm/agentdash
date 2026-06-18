package render

import "testing"

func TestCleanTask(t *testing.T) {
	cases := map[string]string{
		"normal task":                    "normal task",
		"scraped   \t  white\nspace":     "scraped white space",
		"/home/dev/x (no session found)": "(no session)",
		"(no session found)":             "(no session)",
		"":                               "",
	}
	for in, want := range cases {
		if got := CleanTask(in); got != want {
			t.Errorf("CleanTask(%q) = %q, want %q", in, got, want)
		}
	}
}
