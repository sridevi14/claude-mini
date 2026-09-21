package agent

import (
	"fmt"
	"os"
	"strconv"

	"github.com/sridevi14/claude-mini/internal/llm"
	"github.com/sridevi14/claude-mini/internal/ui"
)

// Cost accumulates token usage and estimates spend.
type Cost struct {
	PromptTokens     int // billed at the full input rate
	CachedTokens     int // served from a cached prefix, at roughly a tenth of it
	CompletionTokens int
	Turns            int

	priceIn  float64 // USD per 1M input tokens
	priceOut float64 // USD per 1M output tokens
}

// NewCost reads optional price overrides from the environment.
func NewCost() *Cost {
	c := &Cost{priceIn: 0.60, priceOut: 2.20} // rough GLM-4.5 defaults; override as needed
	if v := os.Getenv("MINI_PRICE_IN"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.priceIn = f
		}
	}
	if v := os.Getenv("MINI_PRICE_OUT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.priceOut = f
		}
	}
	return c
}

// Add folds in one completion's usage.
func (c *Cost) Add(u llm.Usage) {
	c.PromptTokens += u.Uncached()
	c.CachedTokens += u.CachedTokens()
	c.CompletionTokens += u.CompletionTokens
	c.Turns++
}

// cacheReadRate is what a cached prompt prefix costs relative to a fresh one.
const cacheReadRate = 0.1

// USD returns the estimated total cost so far.
func (c *Cost) USD() float64 {
	return float64(c.PromptTokens)/1e6*c.priceIn +
		float64(c.CachedTokens)/1e6*c.priceIn*cacheReadRate +
		float64(c.CompletionTokens)/1e6*c.priceOut
}

// Line renders a one-line usage footer with a token breakdown and estimated cost.
func (c *Cost) Line() string {
	in := c.PromptTokens + c.CachedTokens
	total := in + c.CompletionTokens
	// The cached figure is the only ground truth that caching is working. Its
	// absence is the diagnostic: no figure means the endpoint reported no cache
	// hit, whatever the request asked for.
	cached := ""
	if c.CachedTokens > 0 {
		cached = fmt.Sprintf(" (%s cached, %.0f%%)", kfmt(c.CachedTokens), 100*float64(c.CachedTokens)/float64(in))
	}
	return fmt.Sprintf("%s  ↑ %s in%s · ↓ %s out · %s total · ~$%.4f (est.)%s",
		ui.Gray, kfmt(in), cached, kfmt(c.CompletionTokens), kfmt(total), c.USD(), ui.Reset)
}
