package procs

import "testing"

func TestParseStat(t *testing.T) {
	// comm with spaces and parens must not break field positions
	st, ok := parseStat([]byte(`123 (tmux: client (x)) S 1 123 123 34817 0 0 0 0 0 0 0 0 0 0 0 0 0 0 5000 0 0 0`))
	if !ok {
		t.Fatal("parseStat failed")
	}
	if st.comm != "tmux: client (x)" || st.ppid != 1 || st.ttyNr != 34817 || st.startTick != 5000 {
		t.Errorf("got %+v", st)
	}
	if _, ok := parseStat([]byte("garbage")); ok {
		t.Error("garbage accepted")
	}
}

func TestTTYName(t *testing.T) {
	for nr, want := range map[int]string{
		0:           "?",
		34816:       "pts/0",
		34817 + 255: "pts/256", // minor overflowing into major 137? no: 34817+255 = major 136 minor 256 path
		1024 + 1:    "tty1",    // major 4 minor 1
		1024 + 65:   "ttyS1",   // major 4 minor 65
	} {
		if got := ttyName(nr); got != want {
			t.Errorf("ttyName(%d) = %q, want %q", nr, got, want)
		}
	}
}
