package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client talks to an OpenAI-compatible chat completions endpoint.
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// New builds a Client. baseURL should NOT include the trailing /chat/completions.
func New(baseURL, apiKey, model string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
	}
}

// modelList is the response shape of GET /models on an OpenAI-compatible endpoint.
type modelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ListModels asks the provider which models it serves, via the OpenAI-compatible
// GET /models discovery endpoint. Ids are returned verbatim — some endpoints
// namespace them (e.g. "models/gemini-2.0-flash") and that prefix is part of the
// id the chat endpoint expects.
//
// Not every endpoint implements discovery, and it needs a valid key on most, so
// callers must treat any error as "no catalog available" and fall back to their
// own defaults rather than surfacing it.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var list modelList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("unexpected /models response: %w", err)
	}

	out := make([]string, 0, len(list.Data))
	seen := map[string]bool{}
	for _, m := range list.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("provider listed no models")
	}
	return out, nil
}

// StreamHandler receives streamed tokens as they arrive.
type StreamHandler struct {
	OnReasoning func(string)                 // chain-of-thought tokens (delta.reasoning_content)
	OnContent   func(string)                 // visible answer tokens (delta.content)
	OnRetry     func(attempt int, err error) // called before a retry after a transient failure
}

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      any            `json:"messages"`
	Tools         []Tool         `json:"tools,omitempty"`
	ToolChoice    string         `json:"tool_choice,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	Temperature   float64        `json:"temperature"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// envCache opts into marking cache breakpoints on outgoing requests.
//
// Off by default, deliberately. Attaching a breakpoint means sending a message's
// content as a structured block rather than a plain string, and an endpoint that
// does not understand that shape rejects the whole request. Whether a given
// OpenAI-compatible gateway passes cache_control through to Anthropic is not
// discoverable from the outside, so it stays an explicit opt-in rather than
// something that can fail on first contact. Set CLAUDE_MINI_CACHE=1 to enable,
// then watch the cached figure in the usage footer to confirm it took effect.
const envCache = "CLAUDE_MINI_CACHE"

func cacheEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envCache))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

type cacheControl struct {
	Type string `json:"type"`
}

type textPart struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// wireMessage mirrors Message but carries structured content, which is how a
// cache breakpoint is expressed on an Anthropic-backed endpoint.
type wireMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// withCacheBreakpoints marks the two positions worth caching in an agent loop.
//
// Caching is a prefix match over tools -> system -> messages, so a breakpoint on
// the system prompt also covers the tool schemas that render ahead of it: together
// they are the fixed ~1.5k tokens this agent re-sends on every single call. The
// second breakpoint rides the end of the conversation, so each turn reads back
// everything accumulated so far and writes only what the last turn added.
//
// Only plain-text messages are marked. An assistant turn carrying tool_calls, and
// any tool result, keeps its original string form: gateways vary in whether they
// accept structured content in those positions, and a rejected request costs far
// more than a missed breakpoint.
func withCacheBreakpoints(messages []Message) []any {
	out := make([]any, len(messages))
	for i, m := range messages {
		out[i] = m
	}
	mark := func(i int) {
		if i < 0 || i >= len(messages) {
			return
		}
		m := messages[i]
		if m.Content == "" || len(m.ToolCalls) > 0 || m.Role == "tool" {
			return
		}
		out[i] = wireMessage{
			Role:       m.Role,
			Content:    []textPart{{Type: "text", Text: m.Content, CacheControl: &cacheControl{Type: "ephemeral"}}},
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
	}
	mark(0)                 // system prompt, and the tool schemas ahead of it
	mark(len(messages) - 1) // the conversation so far
	return out
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// Stream sends the conversation and streams the reply, retrying transient
// network failures with backoff as long as nothing has been emitted yet (so a
// retry can never duplicate streamed text or tool calls).
func (c *Client) Stream(ctx context.Context, messages []Message, tools []Tool, h StreamHandler) (Message, Usage, error) {
	const maxAttempts = 4
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		msg, usage, emitted, err := c.streamOnce(ctx, messages, tools, h)
		if err == nil {
			return msg, usage, nil
		}
		lastErr = err
		// Cannot safely retry once partial output reached the user, or if the
		// caller cancelled.
		if emitted || ctx.Err() != nil {
			return msg, usage, err
		}
		if attempt < maxAttempts {
			if h.OnRetry != nil {
				h.OnRetry(attempt, err)
			}
			backoff := time.Duration(attempt) * 2 * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return msg, usage, ctx.Err()
			}
		}
	}
	return Message{}, Usage{}, lastErr
}

// streamOnce performs a single request attempt. emitted reports whether any
// token/tool-call was observed (used to decide whether a retry is safe).
func (c *Client) streamOnce(ctx context.Context, messages []Message, tools []Tool, h StreamHandler) (_ Message, _ Usage, emitted bool, _ error) {
	// Default to the exact bytes this client has always sent; only reshape the
	// messages when caching is explicitly switched on.
	var wire any = messages
	if cacheEnabled() {
		wire = withCacheBreakpoints(messages)
	}
	reqBody := chatRequest{
		Model:         c.Model,
		Messages:      wire,
		Tools:         tools,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		Temperature:   0.3,
	}
	if len(tools) > 0 {
		reqBody.ToolChoice = "auto"
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return Message{}, Usage{}, false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return Message{}, Usage{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Message{}, Usage{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		err := fmt.Errorf("api returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		// 4xx (bad key, bad request) won't fix itself — mark as emitted so the
		// caller does not waste retries on it.
		fatal := resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests
		return Message{}, Usage{}, fatal, err
	}

	out := Message{Role: "assistant"}
	var usage Usage
	// tool calls arrive in fragments keyed by index; accumulate them.
	toolByIndex := map[int]*ToolCall{}
	var order []int

	// A 200 is not proof we reached a chat endpoint: some gateways answer an
	// unrecognized path with 200 and a plain JSON error. Track whether the body
	// actually looked like SSE, and keep a snippet to report if it didn't.
	sawSSE := false
	var preview strings.Builder

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if !sawSSE && preview.Len() < 300 && strings.TrimSpace(line) != "" {
				preview.WriteString(strings.TrimSpace(line))
			}
			if strings.HasPrefix(line, "data: ") {
				sawSSE = true
				payload := strings.TrimPrefix(line, "data: ")
				if payload == "[DONE]" {
					break
				}
				var chunk streamChunk
				if jErr := json.Unmarshal([]byte(payload), &chunk); jErr != nil {
					// skip malformed keep-alive / partial lines
					continue
				}
				if chunk.Usage != nil {
					usage = *chunk.Usage
				}
				for _, ch := range chunk.Choices {
					d := ch.Delta
					if d.ReasoningContent != "" {
						emitted = true
						if h.OnReasoning != nil {
							h.OnReasoning(d.ReasoningContent)
						}
					}
					if d.Content != "" {
						emitted = true
						out.Content += d.Content
						if h.OnContent != nil {
							h.OnContent(d.Content)
						}
					}
					for _, tcd := range d.ToolCalls {
						emitted = true
						tc, ok := toolByIndex[tcd.Index]
						if !ok {
							tc = &ToolCall{Type: "function"}
							toolByIndex[tcd.Index] = tc
							order = append(order, tcd.Index)
						}
						if tcd.ID != "" {
							tc.ID = tcd.ID
						}
						if tcd.Type != "" {
							tc.Type = tcd.Type
						}
						if tcd.Function.Name != "" {
							tc.Function.Name = tcd.Function.Name
						}
						tc.Function.Arguments += tcd.Function.Arguments
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, usage, emitted, err
		}
	}

	// No SSE frames at all means the endpoint answered with something that isn't a
	// chat stream. Returning success here would hand the agent an empty reply and
	// print "done" for a task that never ran, so surface it as the error it is.
	// It is a configuration problem, not a transient one, so it is non-retryable.
	if !sawSSE {
		hint := ""
		if strings.HasSuffix(strings.ToLower(c.BaseURL), "/chat/completions") {
			hint = " — the base URL ends in /chat/completions, which this tool appends itself; " +
				"set it to the part before that (e.g. …/v1)"
		}
		return Message{}, Usage{}, true, fmt.Errorf(
			"endpoint did not return a chat stream%s: %s", hint, truncateStr(preview.String(), 200))
	}

	for _, idx := range order {
		out.ToolCalls = append(out.ToolCalls, *toolByIndex[idx])
	}
	// Repair any malformed argument payload now, so an unserializable tool call
	// can never enter history and break every later request.
	SanitizeToolCalls(&out)
	return out, usage, emitted, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
