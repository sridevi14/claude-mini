package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// StreamHandler receives streamed tokens as they arrive.
type StreamHandler struct {
	OnReasoning func(string)                 // chain-of-thought tokens (delta.reasoning_content)
	OnContent   func(string)                 // visible answer tokens (delta.content)
	OnRetry     func(attempt int, err error) // called before a retry after a transient failure
}

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []Message      `json:"messages"`
	Tools         []Tool         `json:"tools,omitempty"`
	ToolChoice    string         `json:"tool_choice,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	Temperature   float64        `json:"temperature"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
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
	reqBody := chatRequest{
		Model:         c.Model,
		Messages:      messages,
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

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data: ") {
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

	for _, idx := range order {
		out.ToolCalls = append(out.ToolCalls, *toolByIndex[idx])
	}
	return out, usage, emitted, nil
}
