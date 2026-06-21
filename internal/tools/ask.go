package tools

import (
	"context"
	"fmt"

	"minicode/internal/llm"
	"minicode/internal/ui"
)

// askUser lets the model pause and ask the user a clarifying question instead of
// guessing. It is read-only (no file/system mutation), so it needs no approval —
// the interaction itself is the user's input.
type askUser struct{ r *Registry }

func (t *askUser) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name: "ask_user",
		Description: "Ask the user a clarifying question when the task is ambiguous, underspecified, " +
			"or you must choose between several reasonable approaches. Provide 2-4 concrete options " +
			"when possible so the user can pick a number. Returns the user's answer. " +
			"Prefer this over guessing on decisions that meaningfully affect the result.",
		Parameters: obj(map[string]any{
			"question": str("the question to ask the user"),
			"options":  arr("2-4 concrete answer options the user can choose from (optional)"),
		}, "question"),
	}}
}

func (t *askUser) Mutating() bool                         { return false }
func (t *askUser) Preview(map[string]any) (string, error) { return "", nil }

func (t *askUser) Run(_ context.Context, args map[string]any) (string, error) {
	question := getStr(args, "question")
	if question == "" {
		return "", fmt.Errorf("question is required")
	}
	options := getStrSlice(args, "options")
	answer := ui.AskUser(question, options)
	return fmt.Sprintf("The user answered: %s", answer), nil
}
