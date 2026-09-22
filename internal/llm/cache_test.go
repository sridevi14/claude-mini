package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUsageOpenAIConventionSubtractsCachedFromPrompt(t *testing.T) {
	// OpenAI-compatible endpoints count the cached prefix inside prompt_tokens.
	var u Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":5000,"completion_tokens":100,
		"prompt_tokens_details":{"cached_tokens":4000}}`), &u); err != nil {
		t.Fatal(err)
	}
	if got := u.CachedTokens(); got != 4000 {
		t.Errorf("CachedTokens = %d, want 4000", got)
	}
	if got := u.Uncached(); got != 1000 {
		t.Errorf("Uncached = %d, want 1000 (5000 total minus 4000 cached)", got)
	}
}

func TestUsageAnthropicConventionLeavesPromptAlone(t *testing.T) {
	// Anthropic reports input_tokens with the cached prefix already excluded, so
	// subtracting again would under-report what was actually billed.
	var u Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":1000,"completion_tokens":100,
		"cache_read_input_tokens":4000,"cache_creation_input_tokens":200}`), &u); err != nil {
		t.Fatal(err)
	}
	if got := u.CachedTokens(); got != 4000 {
		t.Errorf("CachedTokens = %d, want 4000", got)
	}
	if got := u.Uncached(); got != 1000 {
		t.Errorf("Uncached = %d, want 1000 unchanged", got)
	}
}

func TestUsageWithNoCacheInfoIsUnchanged(t *testing.T) {
	var u Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":800,"completion_tokens":50}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.CachedTokens() != 0 || u.Uncached() != 800 {
		t.Errorf("cached=%d uncached=%d, want 0 and 800", u.CachedTokens(), u.Uncached())
	}
}

func TestCacheBreakpointsMarkSystemAndTail(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "fix the bug"},
		{Role: "assistant", Content: "looking", ToolCalls: []ToolCall{{ID: "1"}}},
		{Role: "tool", ToolCallID: "1", Name: "read_file", Content: "file body"},
		{Role: "user", Content: "carry on"},
	}
	out := withCacheBreakpoints(msgs)
	if len(out) != len(msgs) {
		t.Fatalf("length changed: %d vs %d", len(out), len(msgs))
	}
	if _, ok := out[0].(wireMessage); !ok {
		t.Error("system prompt should carry a breakpoint")
	}
	if _, ok := out[4].(wireMessage); !ok {
		t.Error("the last message should carry a breakpoint")
	}
	// Positions where structured content is riskiest must be left untouched.
	for _, i := range []int{1, 2, 3} {
		if _, marked := out[i].(wireMessage); marked {
			t.Errorf("message %d should have been left as a plain string", i)
		}
	}
}

func TestCacheBreakpointsSkipToolCallAndToolTail(t *testing.T) {
	// A turn ending in a tool result must not be reshaped: gateways differ on
	// whether they accept structured content there.
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "1"}}},
		{Role: "tool", ToolCallID: "1", Name: "search", Content: "hits"},
	}
	out := withCacheBreakpoints(msgs)
	if _, marked := out[3].(wireMessage); marked {
		t.Error("a trailing tool result must stay a plain string")
	}
}

func TestCacheBreakpointJSONShape(t *testing.T) {
	out := withCacheBreakpoints([]Message{{Role: "system", Content: "sys"}})
	b, err := json.Marshal(out[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"text"`, `"text":"sys"`, `"cache_control":{"type":"ephemeral"}`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("marshalled breakpoint missing %s:\n%s", want, b)
		}
	}
}

func TestRequestBytesUnchangedWhenCacheDisabled(t *testing.T) {
	// Caching is opt-in precisely so the default request shape never moves. If
	// this drifts, every existing deployment changes behaviour silently.
	t.Setenv(envCache, "")
	if cacheEnabled() {
		t.Fatal("caching should be off by default")
	}
	msgs := []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}}
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"role":"system","content":"sys"},{"role":"user","content":"hi"}]`
	if string(b) != want {
		t.Errorf("default message encoding changed:\ngot  %s\nwant %s", b, want)
	}
}

func TestCacheEnabledAcceptsCommonTruthyValues(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", " on "} {
		t.Setenv(envCache, v)
		if !cacheEnabled() {
			t.Errorf("%q should enable caching", v)
		}
	}
	for _, v := range []string{"", "0", "false", "off"} {
		t.Setenv(envCache, v)
		if cacheEnabled() {
			t.Errorf("%q should not enable caching", v)
		}
	}
}
