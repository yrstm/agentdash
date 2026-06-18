package parse

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Override is one line of ~/.config/agentdash/context-windows.conf:
// "<model-id-substring> <window-tokens>", first match wins.
type Override struct {
	Sub string
	Win int64
}

func LoadOverrides(path string) []Override {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []Override
	for _, ln := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(ln, '#'); i >= 0 {
			ln = ln[:i]
		}
		parts := strings.Fields(ln)
		if len(parts) < 2 {
			continue
		}
		n := strings.NewReplacer("_", "", ",", "").Replace(parts[1])
		w, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, Override{parts[0], w})
	}
	return out
}

// WindowFor resolves a model id to its context window and the evidence
// source string shown by `why`; (0, "") when unknown.
func WindowFor(model string, overrides []Override) (int64, string) {
	if model == "" {
		return 0, ""
	}
	for _, o := range overrides { // first match in file order wins
		if strings.Contains(model, o.Sub) {
			return o.Win, fmt.Sprintf("conf override %q", o.Sub)
		}
	}
	if strings.Contains(model, "[1m]") {
		return 1_000_000, "built-in ([1m] id)"
	}
	for _, k := range []string{"claude", "opus", "sonnet", "haiku", "fable"} {
		if strings.Contains(model, k) {
			return 200_000, "built-in default (200k)"
		}
	}
	if strings.Contains(model, "gpt") || strings.Contains(model, "codex") {
		return 272_000, "built-in default (272k)"
	}
	return 0, ""
}

// LearnWindow persists a self-correction (observed context exceeded the
// assumed window) so CTX% is right from the first refresh next time.
// The appended override is also added to the in-memory list.
func LearnWindow(confPath, model string, win int64, overrides *[]Override) {
	if model == "" {
		return
	}
	for _, o := range *overrides {
		if strings.Contains(model, o.Sub) {
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(confPath), 0o755); err != nil {
		return
	}
	_, statErr := os.Stat(confPath)
	f, err := os.OpenFile(confPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }() // best-effort overrides cache write
	if os.IsNotExist(statErr) {
		_, _ = fmt.Fprint(f, "# agentdash context-window overrides: <model-id-substring> <tokens>\n"+
			"# first match wins. example:\n#   my-model-id 400000\n")
	}
	_, _ = fmt.Fprintf(f, "%s %d  # learned by agentdash (observed context exceeded prior assumption)\n",
		model, win)
	*overrides = append(*overrides, Override{model, win})
}
