package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotFoundErrorNamesWhitespaceMismatch(t *testing.T) {
	// The real failure: a Go file indented with tabs, and a model that retyped the
	// line using spaces. Six identical retries came out of a bare "not found".
	content := "func Login() {\n\tpwd := user.Password\n\treturn nil\n}\n"
	want := "    pwd := user.Password" // spaces, not a tab

	err := notFoundError("login.go", content, want)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()

	for _, fragment := range []string{
		"line 2",        // where it actually is
		"TAB",           // what differs
		"byte-for-byte", // what to do about it
	} {
		if !strings.Contains(msg, fragment) {
			t.Errorf("error missing %q:\n%s", fragment, msg)
		}
	}
	// The file's real text must be shown with tabs made visible, or the model
	// cannot see what it got wrong.
	if !strings.Contains(msg, `\tpwd := user.Password`) {
		t.Errorf("error should quote the real line with a visible tab:\n%s", msg)
	}
}

func TestNotFoundErrorReportsIndentWidthMismatch(t *testing.T) {
	// Same characters, different amount of indentation.
	content := "if x {\n        doThing()\n}\n"
	msg := notFoundError("a.go", content, "    doThing()").Error()

	if !strings.Contains(msg, "line 2") {
		t.Errorf("should locate the near-match:\n%s", msg)
	}
	if !strings.Contains(msg, "indentation or spacing differs") {
		t.Errorf("should name spacing as the cause:\n%s", msg)
	}
}

func TestNotFoundErrorWhenNothingResembles(t *testing.T) {
	// No near-match at all: say so plainly rather than inventing a line number,
	// and steer away from the other common mistake.
	msg := notFoundError("a.go", "package main\n", "totally unrelated text").Error()

	if strings.Contains(msg, "matches once whitespace is ignored") {
		t.Errorf("should not claim a near-match that does not exist:\n%s", msg)
	}
	if !strings.Contains(msg, "no line matches") {
		t.Errorf("should say plainly that nothing resembles it:\n%s", msg)
	}
	if !strings.Contains(msg, "line numbers") {
		t.Errorf("should warn against pasting read_file line numbers:\n%s", msg)
	}
}

func TestCollapseWS(t *testing.T) {
	cases := map[string]string{
		"\tfoo  bar":    "foo bar",
		"    foo bar  ": "foo bar",
		"foo\t\tbar":    "foo bar",
		"":              "",
		"   ":           "",
	}
	for in, want := range cases {
		if got := collapseWS(in); got != want {
			t.Errorf("collapseWS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstNonBlankLine(t *testing.T) {
	if got := firstNonBlankLine("\n\n  real line\nnext"); got != "  real line" {
		t.Errorf("firstNonBlankLine = %q, want the first line with content", got)
	}
	if got := firstNonBlankLine("\n  \n"); got != "" {
		t.Errorf("firstNonBlankLine = %q, want empty for an all-blank block", got)
	}
}

// --- whitespace-tolerant matching ---

// newEdit builds an edit_file tool rooted at a temp dir holding one file.
func newEdit(t *testing.T, name, content string) *editFile {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return &editFile{&Registry{root: dir}}
}

func TestEditAppliesMatchThatDiffersOnlyInIndentation(t *testing.T) {
	// The transcript failure: a tab-indented Go file, and a model that retyped the
	// block with four spaces. This used to cost three failed calls and a re-read.
	src := "func Register() {\n\tif err != nil {\n\t\treturn\n\t}\n\tbad()\n\treturn\n}\n"
	e := newEdit(t, "auth.go", src)

	p, err := e.plan(map[string]any{
		"path":       "auth.go",
		"old_string": "    bad()\n    return", // spaces, file has tabs
		"new_string": "    good()",
	})
	if err != nil {
		t.Fatalf("expected the edit to apply, got: %v", err)
	}
	want := "func Register() {\n\tif err != nil {\n\t\treturn\n\t}\n\tgood()\n}\n"
	if p.newContent != want {
		t.Errorf("newContent =\n%q\nwant\n%q", p.newContent, want)
	}
	// The replacement must adopt the file's tabs, not the model's spaces.
	if strings.Contains(p.newContent, "    good()") {
		t.Error("replacement kept the model's spaces; it should be re-indented to tabs")
	}
	if !strings.Contains(p.note, "re-indented") || !strings.Contains(p.note, "tabs") {
		t.Errorf("note should say the replacement was re-indented to tabs, got: %q", p.note)
	}
}

func TestEditPrefersExactMatchAndAddsNoNote(t *testing.T) {
	e := newEdit(t, "a.go", "package main\n\tx := 1\n")
	p, err := e.plan(map[string]any{
		"path": "a.go", "old_string": "\tx := 1", "new_string": "\tx := 2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.note != "" {
		t.Errorf("an exact match should not be annotated, got: %q", p.note)
	}
	if !strings.Contains(p.newContent, "\tx := 2") {
		t.Errorf("exact edit did not apply:\n%q", p.newContent)
	}
}

func TestEditRefusesAmbiguousWhitespaceTolerantMatch(t *testing.T) {
	// Two candidate regions: guessing one would be worse than failing.
	src := "if a {\n\tdo()\n}\nif b {\n\t\tdo()\n}\n"
	e := newEdit(t, "a.go", src)
	_, err := e.plan(map[string]any{
		"path": "a.go", "old_string": "    do()", "new_string": "    done()",
	})
	if err == nil {
		t.Fatal("expected an error for an ambiguous match")
	}
	if !strings.Contains(err.Error(), "separate places") {
		t.Errorf("error should explain the ambiguity, got: %v", err)
	}
}

func TestEditStillFailsWhenNothingResembles(t *testing.T) {
	e := newEdit(t, "a.go", "package main\n")
	_, err := e.plan(map[string]any{
		"path": "a.go", "old_string": "totally unrelated", "new_string": "x",
	})
	if err == nil || !strings.Contains(err.Error(), "no line matches") {
		t.Errorf("should fall through to the explanatory error, got: %v", err)
	}
}

func TestEditDeletionLeavesNoBlankLine(t *testing.T) {
	e := newEdit(t, "a.go", "a\n\tdropMe\nb\n")
	p, err := e.plan(map[string]any{
		"path": "a.go", "old_string": "  dropMe", "new_string": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.newContent != "a\nb\n" {
		t.Errorf("deletion left a stray line: %q", p.newContent)
	}
}

// --- what the model is told after a write ---

func TestAppliedReturnsADiffNotJustAConfirmation(t *testing.T) {
	out := applied("edited a.go", "", "line1\nline2\nline3", "line1\nCHANGED\nline3")
	for _, want := range []string{"edited a.go", "- line2", "+ CHANGED"} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %q:\n%s", want, out)
		}
	}
	// ANSI colour would be tokens the model pays for and cannot use.
	if strings.Contains(out, "\033[") {
		t.Errorf("tool result must not carry ANSI colour:\n%q", out)
	}
}

func TestAppliedTruncatesAHugeDiff(t *testing.T) {
	big := strings.Repeat("some line of content\n", 2000)
	out := applied("wrote big.txt", "", "", big)
	if len(out) > maxDiffInResult+300 {
		t.Errorf("diff was not truncated: %d chars", len(out))
	}
	if !strings.Contains(out, "diff truncated") {
		t.Error("truncation should be announced")
	}
}
