package llm

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// MalformedArgsKey marks an arguments payload that could not be repaired. The
// caller can detect it and report a precise error to the model instead of
// running a tool with silently missing fields.
const MalformedArgsKey = "_malformed_arguments"

// SanitizeToolCalls makes every tool call's Arguments payload valid JSON before
// the message is stored in history.
//
// Models frequently emit raw control characters (most often a literal tab or
// newline copied out of indented source) inside the Arguments string. JSON
// forbids them, so the local parse fails - and, worse, the upstream gateway
// re-parses Arguments on every later request, so a single bad tool call makes
// every subsequent turn fail with a 400 until the process is restarted.
// Repairing the payload the moment it arrives keeps the conversation usable.
func SanitizeToolCalls(m *Message) {
	for i := range m.ToolCalls {
		m.ToolCalls[i].Function.Arguments = RepairArguments(m.ToolCalls[i].Function.Arguments)
	}
}

// RepairArguments returns s unchanged when it is already valid JSON. Otherwise
// it escapes raw control characters that appear inside string literals and
// returns the repaired text if that makes it parse. If it still will not parse,
// it returns a marker object so the tool call fails with a correctable error
// rather than poisoning every future request with an unserializable payload.
func RepairArguments(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "{}"
	}
	if json.Valid([]byte(trimmed)) {
		return trimmed
	}
	if fixed := escapeControlsInStrings(trimmed); json.Valid([]byte(fixed)) {
		return fixed
	}
	return `{"_malformed_arguments": true}`
}

// escapeControlsInStrings rewrites literal control characters that sit inside a
// JSON string literal into their escape sequences, leaving the rest of the
// document (and any already-correct escapes) alone.
func escapeControlsInStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)

	const backslash = 0x5c
	inString := false
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			// The previous rune was a backslash; this one completes the escape.
			b.WriteRune(r)
			escaped = false
			continue
		case inString && r == backslash:
			b.WriteRune(r)
			escaped = true
			continue
		case r == '"':
			inString = !inString
			b.WriteRune(r)
			continue
		}

		if inString && r < 0x20 {
			switch r {
			case '\n':
				b.WriteString(`\n`)
			case '\t':
				b.WriteString(`\t`)
			case '\r':
				b.WriteString(`\r`)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			default:
				const hex = "0123456789abcdef"
				b.WriteString(`\u00`)
				b.WriteByte(hex[(r>>4)&0xf])
				b.WriteByte(hex[r&0xf])
			}
			continue
		}
		if r == utf8.RuneError {
			// Drop undecodable bytes rather than shipping them upstream.
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
