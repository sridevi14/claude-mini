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
	PromptTokens     int
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
	c.PromptTokens += u.PromptTokens
	c.CompletionTokens += u.CompletionTokens
	c.Turns++
}

// USD returns the estimated total cost so far.
func (c *Cost) USD() float64 {
	return float64(c.PromptTokens)/1e6*c.priceIn + float64(c.CompletionTokens)/1e6*c.priceOut
}

// Line renders a one-line usage footer with a token breakdown and estimated cost.
func (c *Cost) Line() string {
	total := c.PromptTokens + c.CompletionTokens
	return fmt.Sprintf("%s  ↑ %s in · ↓ %s out · %s total · ~$%.4f (est.)%s",
		ui.Gray, kfmt(c.PromptTokens), kfmt(c.CompletionTokens), kfmt(total), c.USD(), ui.Reset)
}
