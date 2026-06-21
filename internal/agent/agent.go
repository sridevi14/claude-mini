package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sridevi14/claude-mini/internal/llm"
	"github.com/sridevi14/claude-mini/internal/session"
	"github.com/sridevi14/claude-mini/internal/tools"
	"github.com/sridevi14/claude-mini/internal/ui"
)

// Agent drives the multi-turn conversation + tool loop.
type Agent struct {
	client  *llm.Client
	tools   *tools.Registry
	sess    *session.Session
	cost    *Cost
	history []llm.Message
	allowed map[string]bool // scope keys approved for the whole session

	// watch, if set, is invoked around each streaming call so the host can listen
	// for an interrupt key (Esc) and cancel the turn. It returns a release func
	// called when streaming ends. It is only active during streaming — never while
	// a permission/ask prompt is reading stdin — so the two never fight over input.
	watch func(cancel context.CancelFunc) (release func())
}

// New builds an agent with its system prompt seeded.
func New(client *llm.Client, reg *tools.Registry, sess *session.Session, cost *Cost, root string) *Agent {
	return &Agent{
		client:  client,
		tools:   reg,
		sess:    sess,
		cost:    cost,
		allowed: map[string]bool{},
		history: []llm.Message{
			{Role: "system", Content: systemPrompt(root, reg.Names())},
		},
	}
}

// Cost exposes the running cost tracker.
func (a *Agent) Cost() *Cost { return a.cost }

// SetInterruptWatcher installs a watcher invoked around each streaming call. The
// watcher begins listening for the interrupt key and calls cancel when the user
// asks to pause; it returns a release func invoked when streaming ends.
func (a *Agent) SetInterruptWatcher(w func(cancel context.CancelFunc) (release func())) {
	a.watch = w
}

// Resume reconstructs prior conversation from a transcript's records and appends
// it after the system prompt. Tool results are paired with the preceding
// assistant message's tool calls in order. A trailing assistant turn whose tool
// calls weren't all answered (e.g. a previous session ended mid-tool) is dropped
// so the resumed history stays valid for the API. Returns the number of messages
// restored.
func (a *Agent) Resume(records []session.Record) int {
	base := len(a.history)
	var pending []llm.ToolCall // tool calls awaiting results from the last assistant
	pi := 0
	lastAssistant := -1

	for _, rec := range records {
		switch rec.Kind {
		case "user":
			var s string
			if json.Unmarshal(rec.Payload, &s) != nil {
				continue
			}
			a.history = append(a.history, llm.Message{Role: "user", Content: s})
			pending, pi, lastAssistant = nil, 0, -1
		case "assistant":
			var m llm.Message
			if json.Unmarshal(rec.Payload, &m) != nil {
				continue
			}
			if m.Role == "" {
				m.Role = "assistant"
			}
			lastAssistant = len(a.history)
			a.history = append(a.history, m)
			pending, pi = m.ToolCalls, 0
		case "tool_result":
			var p struct {
				Name   string `json:"name"`
				Result string `json:"result"`
			}
			if json.Unmarshal(rec.Payload, &p) != nil {
				continue
			}
			if pi >= len(pending) {
				continue // no tool call to pair with; skip to avoid an orphan message
			}
			tc := pending[pi]
			pi++
			name := p.Name
			if name == "" {
				name = tc.Function.Name
			}
			a.history = append(a.history, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       name,
				Content:    clampForHistory(p.Result),
			})
		}
	}

	// Drop a dangling final assistant turn with unanswered tool calls.
	if lastAssistant >= 0 && pi < len(pending) {
		a.history = a.history[:lastAssistant]
	}
	return len(a.history) - base
}

// Run handles one user task to completion (looping through tool calls). It runs
// under a cancelable child context: pressing the interrupt key (via the installed
// watcher) during streaming pauses the turn, leaving history in a valid state so
// the next message can add instructions and continue the same task, or pivot.
func (a *Agent) Run(ctx context.Context, userInput string) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	a.history = append(a.history, llm.Message{Role: "user", Content: userInput})
	a.sess.Log("user", userInput)

	for {
		stream := &ui.Streamer{}
		// Listen for an interrupt only while streaming — stdin is otherwise needed
		// by permission/ask prompts in the tool phase.
		var release func()
		if a.watch != nil {
			release = a.watch(cancel)
		}
		msg, usage, err := a.client.Stream(runCtx, a.history, a.tools.Defs(), llm.StreamHandler{
			OnReasoning: stream.Reasoning,
			OnContent:   stream.Content,
			OnRetry: func(attempt int, err error) {
				ui.Errorf("connection issue (%v) — retrying (attempt %d)…", err, attempt)
			},
		})
		if release != nil {
			release()
		}
		stream.End()
		if err != nil {
			if runCtx.Err() != nil {
				ui.Info("\n  ⏸ paused — type more instructions to continue this task, or a new request.")
				return
			}
			ui.Errorf("model request failed: %v", err)
			ui.Info("  your progress is kept — type \"continue\" to resume from here.")
			return
		}
		a.cost.Add(usage)
		a.history = append(a.history, msg)
		a.sess.Log("assistant", msg)

		if len(msg.ToolCalls) == 0 {
			fmt.Println(a.cost.Line())
			a.maybeCompact(runCtx, usage.PromptTokens)
			return
		}

		// Run every tool call, but guarantee each one gets a tool-result message
		// even if the user interrupts mid-loop — otherwise the next request would
		// carry tool_calls with no matching results and the API would reject it.
		interrupted := false
		for _, tc := range msg.ToolCalls {
			var result string
			switch {
			case interrupted || runCtx.Err() != nil:
				interrupted = true
				result = "[interrupted by user before this tool ran]"
			default:
				result = a.execTool(runCtx, tc)
				if runCtx.Err() != nil {
					interrupted = true
				}
			}
			a.history = append(a.history, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    clampForHistory(result),
			})
			a.sess.Log("tool_result", map[string]any{"name": tc.Function.Name, "result": result})
		}
		if interrupted {
			ui.Info("\n  ⏸ paused — type more instructions to continue this task, or a new request.")
			return
		}
		a.maybeCompact(runCtx, usage.PromptTokens)
	}
}

// execTool runs a single tool call, handling permission and errors gracefully.
// It always returns a string to feed back to the model (errors included), so a
// failing tool makes the model self-correct rather than crashing the program.
func (a *Agent) execTool(ctx context.Context, tc llm.ToolCall) string {
	name := tc.Function.Name
	tool, ok := a.tools.Get(name)
	if !ok {
		ui.Errorf("unknown tool %q", name)
		return fmt.Sprintf("Error: no such tool %q", name)
	}

	var args map[string]any
	raw := strings.TrimSpace(tc.Function.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		ui.Errorf("could not parse arguments for %s: %v", name, err)
		return fmt.Sprintf("Error: invalid JSON arguments: %v", err)
	}

	ui.ToolHeader(name, argSummary(args))

	if tool.Mutating() {
		preview, err := tool.Preview(args)
		if err != nil {
			ui.Errorf("%s: %v", name, err)
			return fmt.Sprintf("Error preparing %s: %v", name, err)
		}
		scope, label := permScope(name, args)
		if !a.allowed[scope] {
			switch ui.AskPermission(fmt.Sprintf("%s wants to proceed:", name), preview, label) {
			case ui.PermNo:
				ui.Info("  declined.")
				return "User declined this action. Do not retry it; consider an alternative or ask what to do."
			case ui.PermAlways:
				a.allowed[scope] = true
			}
		} else {
			// already approved for the session; still show what will happen
			ui.ToolResult(preview)
		}
	}

	result, err := tool.Run(ctx, args)
	if err != nil {
		ui.Errorf("%s failed: %v", name, err)
		return fmt.Sprintf("Error: %v", err)
	}
	ui.ToolResult(result)
	return result
}

// permScope derives a session-approval key (and a human label) for a mutating
// tool call. "Allow session" then covers only that scope — a given command verb,
// file, or server — instead of every future use of the tool.
func permScope(name string, args map[string]any) (key, label string) {
	switch name {
	case "run_bash":
		head := firstWord(getArgStr(args, "command"))
		if head == "" {
			head = "command"
		}
		return "bash:" + head, head + " commands"
	case "run_server":
		n := getArgStr(args, "name")
		if n == "" {
			n = firstWord(getArgStr(args, "command"))
		}
		return "server:" + n, "server " + n
	case "write_file", "edit_file":
		p := getArgStr(args, "path")
		return "file:" + p, p
	default:
		return name, name
	}
}

func getArgStr(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

func argSummary(args map[string]any) string {
	var parts []string
	for _, k := range []string{"path", "command", "pattern", "name", "glob"} {
		if v, ok := args[k]; ok {
			s := fmt.Sprintf("%v", v)
			if len(s) > 80 {
				s = s[:80] + "…"
			}
			parts = append(parts, k+"="+s)
		}
	}
	return strings.Join(parts, " ")
}
