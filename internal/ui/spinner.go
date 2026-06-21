package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Spinner renders a single, self-updating status line while the agent waits on the
// model — an animated glyph, a label, elapsed time, a live token estimate and an
// interrupt hint, e.g.
//
//	⠹ Cooking…  12s · ~1.2k tokens · esc to interrupt
//
// It repaints in place with a carriage return, so it must only run while nothing
// else writes to stdout (the agent buffers the model's answer until Stop).
type Spinner struct {
	label  string
	tokens func() int

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// brailleFrames are the classic spinner glyphs — unmistakable "working" motion.
var brailleFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NewSpinner builds a spinner with a fixed label and a live token-count provider.
func NewSpinner(label string, tokens func() int) *Spinner {
	return &Spinner{
		label:  label,
		tokens: tokens,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Start begins animating in a background goroutine. Call Stop exactly once when the
// work finishes; Stop is safe to call from any goroutine and is idempotent.
func (s *Spinner) Start() {
	start := time.Now()
	go func() {
		defer close(s.done)
		t := time.NewTicker(110 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-s.stop:
				return
			case <-t.C:
				glyph := brailleFrames[i%len(brailleFrames)]
				elapsed := elapsedShort(time.Since(start))
				toks := tokensShort(s.tokens())
				fmt.Printf("\r  %s%s %s…%s  %s%s · ~%s tokens · esc to interrupt%s   ",
					Magenta, glyph, s.label, Reset,
					Gray, elapsed, toks, Reset)
			}
		}
	}()
}

// Stop ends the animation and clears the status line.
func (s *Spinner) Stop() {
	s.once.Do(func() {
		close(s.stop)
		<-s.done
		fmt.Print("\r" + strings.Repeat(" ", 72) + "\r")
	})
}

// elapsedShort formats a running duration as "8s" or "1m 04s".
func elapsedShort(d time.Duration) string {
	secs := int(d / time.Second)
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm %02ds", secs/60, secs%60)
}

// tokensShort formats a token count compactly: 850, 1.2k, 15.0k.
func tokensShort(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}
