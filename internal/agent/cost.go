package agent

import (
	"fmt"
	"os"
	"strconv"

	"minicode/internal/llm"
	"minicode/internal/ui"
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

// Line renders a one-line footer.
func (c *Cost) Line() string {
	return fmt.Sprintf("%s  %d in / %d out tokens · ~$%.4f (est.)%s",
		ui.Gray, c.PromptTokens, c.CompletionTokens, c.USD(), ui.Reset)
}
