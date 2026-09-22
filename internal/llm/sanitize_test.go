package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// A tool call carrying raw tabs/newlines inside a string must be repaired into
// valid JSON with its content intact - otherwise the gateway rejects it and
// every later request in the session fails too.
func TestRepairArgumentsFixesRawControlChars(t *testing.T) {
	raw := `{"path": "login.go", "old_string": "@treturn nil@n"}`
	bad := strings.NewReplacer("@t", "\t", "@n", "\n").Replace(raw)

	var m map[string]any
	got := RepairArguments(bad)
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("not repaired: %v (%q)", err, got)
	}
	if m["old_string"] != "\treturn nil\n" {
		t.Fatalf("content lost: %q", m["old_string"])
	}
}

// Already-valid payloads, including ones with legitimate escapes, pass through
// untouched.
func TestRepairArgumentsPreservesValidPayloads(t *testing.T) {
	in := `{"a":"b\\c","d":"e\"f"}`
	if got := RepairArguments(in); got != in {
		t.Fatalf("valid JSON was rewritten: %q", got)
	}
}

func TestRepairArgumentsMarksUnrepairable(t *testing.T) {
	if got := RepairArguments("not json {{{"); got != `{"_malformed_arguments": true}` {
		t.Fatalf("unrepairable payload not marked: %q", got)
	}
	if got := RepairArguments("  "); got != "{}" {
		t.Fatalf("empty payload: %q", got)
	}
}
