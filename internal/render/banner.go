package render

import (
	"fmt"
	"strings"

	"github.com/yrstm/agentdash/internal/board"
	"github.com/yrstm/agentdash/internal/parse"
)

// ANSI Shadow "AGENTDASH" wordmark (figlet).
const wordmark = ` █████╗  ██████╗ ███████╗███╗   ██╗████████╗██████╗  █████╗ ███████╗██╗  ██╗
██╔══██╗██╔════╝ ██╔════╝████╗  ██║╚══██╔══╝██╔══██╗██╔══██╗██╔════╝██║  ██║
███████║██║  ███╗█████╗  ██╔██╗ ██║   ██║   ██║  ██║███████║███████╗███████║
██╔══██║██║   ██║██╔══╝  ██║╚██╗██║   ██║   ██║  ██║██╔══██║╚════██║██╔══██║
██║  ██║╚██████╔╝███████╗██║ ╚████║   ██║   ██████╔╝██║  ██║███████║██║  ██║
╚═╝  ╚═╝ ╚═════╝ ╚══════╝╚═╝  ╚═══╝   ╚═╝   ╚═════╝ ╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝`

const wordmarkWidth = 76 // show the figlet only when it won't wrap

// Banner renders the violet AGENTDASH splash plus a one-glance HUD off the
// board. Callers must show it on a TTY only — never in --json/--plain/pipes.
// Narrower than the figlet, it falls back to a bold one-line wordmark.
func Banner(b *board.Board, t Theme, width int) string {
	var w strings.Builder
	if width >= wordmarkWidth {
		for _, ln := range strings.Split(wordmark, "\n") {
			fmt.Fprintf(&w, "%s%s%s\n", t.V, ln, t.N)
		}
	} else {
		fmt.Fprintf(&w, "%s%sAGENTDASH%s\n", t.B, t.V, t.N)
	}

	active := b.NWork + b.NLoop + b.NBlocked
	health := "nominal"
	switch {
	case b.NLoop > 0:
		health = "crash-loop"
	case b.NNeed > 0:
		health = "needs you"
	}
	hud := []string{
		"operator on watch — agentdash online · " + health,
		fmt.Sprintf("supervising %d agents · %d active · %d idle", len(b.Rows), active, b.NIdle),
		fmt.Sprintf("context · %s held idle · %s burning", parse.Hum(b.IdleCtx), parse.Hum(b.BurnCtx)),
		"ready — -w to watch live · ? for help in watch",
	}
	for _, h := range hud {
		fmt.Fprintf(&w, "  %s▸%s %s%s%s\n", t.V, t.N, t.D, h, t.N)
	}
	return w.String()
}
