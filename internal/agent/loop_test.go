package agent

import (
	"strings"
	"testing"

	"github.com/sridevi14/claude-mini/internal/llm"
)

func call(name, args string) llm.ToolCall {
	return llm.ToolCall{Function: llm.FunctionCall{Name: name, Arguments: args}}
}

func TestNoteFailureEscalatesOnRepeat(t *testing.T) {
	a := &Agent{failed: map[string]int{}}
	tc := call("edit_file", `{"path":"a.go","old_string":"x"}`)

	// First failure: the plain error, so the model can simply correct itself.
	first := a.noteFailure(tc, "Error: old_string not found")
	if strings.Contains(first, "STOP") {
		t.Errorf("first failure should not escalate:\n%s", first)
	}
	if !strings.Contains(first, "old_string not found") {
		t.Errorf("first failure must still carry the real error:\n%s", first)
	}

	// Second identical failure: insist on a different approach.
	second := a.noteFailure(tc, "Error: old_string not found")
	if !strings.Contains(second, "STOP") {
		t.Errorf("second identical failure should escalate:\n%s", second)
	}
	if !strings.Contains(second, "write_file") || !strings.Contains(second, "ask_user") {
		t.Errorf("escalation should name concrete alternatives:\n%s", second)
	}
	if !strings.Contains(second, "old_string not found") {
		t.Errorf("escalation must keep the original error:\n%s", second)
	}

	// Third: demand it stop or report BLOCKED.
	third := a.noteFailure(tc, "Error: old_string not found")
	if !strings.Contains(third, "BLOCKED") {
		t.Errorf("third identical failure should demand BLOCKED:\n%s", third)
	}
}

func TestNoteFailureIsPerDistinctCall(t *testing.T) {
	a := &Agent{failed: map[string]int{}}

	// A different argument payload is a genuine new attempt, not a repeat — the
	// model correcting itself must not be punished.
	a.noteFailure(call("edit_file", `{"old_string":"first try"}`), "Error: x")
	second := a.noteFailure(call("edit_file", `{"old_string":"second try"}`), "Error: x")
	if strings.Contains(second, "STOP") {
		t.Errorf("a changed argument is a new attempt, not a repeat:\n%s", second)
	}

	// Same arguments, different tool is also distinct.
	other := a.noteFailure(call("write_file", `{"old_string":"first try"}`), "Error: x")
	if strings.Contains(other, "STOP") {
		t.Errorf("a different tool is a new attempt:\n%s", other)
	}
}

func TestFailureCountsResetPerTask(t *testing.T) {
	// Counts are cleared at the start of each Run, so a retry the user explicitly
	// asks for next turn is not immediately escalated.
	a := &Agent{failed: map[string]int{}}
	tc := call("run_bash", `{"command":"go test"}`)

	a.noteFailure(tc, "Error: x")
	a.noteFailure(tc, "Error: x")
	clear(a.failed)

	if got := a.noteFailure(tc, "Error: x"); strings.Contains(got, "STOP") {
		t.Errorf("counts should reset between tasks:\n%s", got)
	}
}
