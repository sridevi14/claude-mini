package agent

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/sridevi14/claude-mini/internal/ui"
)

// A little personality: a playful "working" beat when a turn starts and a warm
// "all done" line (with how long it took) when a task finishes. Purely cosmetic —
// nothing here affects the agent's behavior or the model conversation.

// workingVerbs are present-tense labels shown while the model is thinking.
var workingVerbs = []string{
	"Thinking", "Pondering", "Percolating", "Noodling", "Cooking",
	"Brewing", "Conjuring", "Tinkering", "Simmering", "Wrangling bytes",
}

// doneVerbs are past-tense labels shown when a task wraps up.
var doneVerbs = []string{
	"cooked", "baked", "brewed", "whipped up", "simmered",
	"conjured", "crafted", "wrangled", "sorted",
}

// workingLine is the cute "thinking" header printed before a model turn (used in
// non-animated contexts).
func workingLine() string {
	return ui.Magenta + "  ✻ " + pickWorkingVerb() + "…" + ui.Reset
}

// pickWorkingVerb returns a random present-tense working verb (no ellipsis).
func pickWorkingVerb() string { return workingVerbs[rand.Intn(len(workingVerbs))] }

// kfmt formats a token count compactly: 850, 1.2k, 15.0k.
func kfmt(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// doneLine is the cheerful completion line, e.g. "✓ baked for 2m 13s".
func doneLine(d time.Duration) string {
	v := doneVerbs[rand.Intn(len(doneVerbs))]
	return ui.Green + "  ✓ " + v + " for " + humanizeDuration(d) + ui.Reset
}

// humanizeDuration formats a duration the friendly way: "8s", "2m 13s", "3m".
func humanizeDuration(d time.Duration) string {
	secs := int(d.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	m, s := secs/60, secs%60
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}
