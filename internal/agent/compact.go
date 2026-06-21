package agent

import (
	"context"
	"strings"

	"github.com/sridevi14/claude-mini/internal/llm"
	"github.com/sridevi14/claude-mini/internal/ui"
)

const (
	// compactTriggerTokens: once the measured prompt size for a turn crosses
	// this, fold older history into a summary before the next turn.
	compactTriggerTokens = 60000
	// compactKeepUserTurns: how many of the most recent user turns to keep
	// verbatim (cutting on a user boundary never splits a tool-call/result pair).
	compactKeepUserTurns = 2
	// maxToolResultInHistory bounds a single tool result stored in history so
	// large outputs (big files, long search/bash output) don't get re-sent every
	// turn. The full result is still logged to the transcript.
	maxToolResultInHistory = 24000
)

// clampForHistory bounds one tool result kept in the conversation context.
func clampForHistory(s string) string {
	if len(s) <= maxToolResultInHistory {
		return s
	}
	return s[:maxToolResultInHistory] +
		"\n… (truncated in history to save tokens; re-read a specific section if you need more)"
}

// maybeCompact summarizes older history into a single note once the measured
// context size crosses the trigger, preserving the system prompt and the most
// recent turns. It is best-effort: any failure leaves history unchanged.
func (a *Agent) maybeCompact(ctx context.Context, lastPromptTokens int) {
	if lastPromptTokens < compactTriggerTokens || len(a.history) < 6 {
		return
	}
	cut := a.compactionCut()
	if cut <= 1 {
		return
	}
	old := a.history[1:cut]
	ui.Info("  · context is large (%d tokens) — compacting earlier turns ·", lastPromptTokens)
	summary, err := a.summarize(ctx, old)
	if err != nil || strings.TrimSpace(summary) == "" {
		return // keep going uncompacted rather than dropping context
	}
	note := llm.Message{
		Role:    "assistant",
		Content: "[earlier conversation compacted to save context]\n\n" + summary,
	}
	rebuilt := make([]llm.Message, 0, 2+len(a.history)-cut)
	rebuilt = append(rebuilt, a.history[0], note)
	rebuilt = append(rebuilt, a.history[cut:]...)
	a.history = rebuilt
	a.sess.Log("compaction", map[string]any{"summarized_messages": len(old)})
	ui.Success("compacted %d earlier messages into a summary", len(old))
}

// compactionCut returns an index such that history[cut:] begins at a user
// message, keeping the last compactKeepUserTurns user turns verbatim. Returns 0
// when there is nothing safe to compact.
func (a *Agent) compactionCut() int {
	var userIdx []int
	for i := 1; i < len(a.history); i++ {
		if a.history[i].Role == "user" {
			userIdx = append(userIdx, i)
		}
	}
	if len(userIdx) <= compactKeepUserTurns {
		return 0
	}
	return userIdx[len(userIdx)-compactKeepUserTurns]
}

// summarize asks the model for a terse progress note covering the given turns.
func (a *Agent) summarize(ctx context.Context, msgs []llm.Message) (string, error) {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			b.WriteString("USER: " + m.Content + "\n")
		case "assistant":
			if m.Content != "" {
				b.WriteString("ASSISTANT: " + m.Content + "\n")
			}
			for _, tc := range m.ToolCalls {
				b.WriteString("ASSISTANT called " + tc.Function.Name + "(" + truncate(tc.Function.Arguments, 200) + ")\n")
			}
		case "tool":
			b.WriteString("TOOL " + m.Name + " -> " + truncate(m.Content, 400) + "\n")
		}
	}
	prompt := []llm.Message{
		{Role: "system", Content: "You compress a coding agent's conversation history into a concise progress note. " +
			"Capture: the user's goals, files read or changed, key decisions and findings, commands run and their outcomes, " +
			"and what still remains to be done. Be specific (file and function names) but terse. Output only the note."},
		{Role: "user", Content: "Summarize this conversation so far:\n\n" + b.String()},
	}
	var out strings.Builder
	msg, usage, err := a.client.Stream(ctx, prompt, nil, llm.StreamHandler{
		OnContent: func(s string) { out.WriteString(s) },
	})
	if err != nil {
		return "", err
	}
	a.cost.Add(usage)
	if out.Len() == 0 {
		return msg.Content, nil
	}
	return out.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
