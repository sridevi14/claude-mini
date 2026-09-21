package llm

// Message is a single chat message in the OpenAI-compatible format.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall is a function-calling request emitted by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the tool name and its raw JSON arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool describes a callable function exposed to the model.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the schema half of a Tool.
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// Usage reports token counts for a completion.
//
// Endpoints disagree on how they report a cached prompt prefix. OpenAI-compatible
// servers nest it under prompt_tokens_details and count it inside prompt_tokens;
// endpoints that pass an Anthropic response through report cache_read_input_tokens
// and leave it out of the prompt total. Both are parsed, and the two accessors
// below paper over the difference so callers never have to care which arrived.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// CachedTokens reports how much of this request's prompt was served from cache.
func (u Usage) CachedTokens() int {
	if u.CacheReadInputTokens > 0 {
		return u.CacheReadInputTokens
	}
	return u.PromptTokensDetails.CachedTokens
}

// Uncached returns the prompt tokens billed at the full input rate. Under the
// OpenAI convention the cached prefix is included in PromptTokens and has to be
// subtracted; under the Anthropic one it was never counted there to begin with.
func (u Usage) Uncached() int {
	if u.CacheReadInputTokens > 0 || u.CacheCreationInputTokens > 0 {
		return u.PromptTokens
	}
	if n := u.PromptTokensDetails.CachedTokens; n > 0 && n <= u.PromptTokens {
		return u.PromptTokens - n
	}
	return u.PromptTokens
}
