package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sridevi14/claude-mini/internal/llm"
	"github.com/sridevi14/claude-mini/internal/ui"
)

// maxDiffInResult bounds the diff handed back to the model after a write: it
// needs to see what changed, not the whole file.
const maxDiffInResult = 4000

// applied reports a completed write to the model as a diff of what actually
// landed on disk. Returning only "edited <path>" left the model no way to check
// its own work except reading the whole file back - several times the tokens of a
// diff, and re-sent on every later turn of the conversation.
func applied(headline, note, oldC, newC string) string {
	var sb strings.Builder
	sb.WriteString(headline + "\n")
	if note != "" {
		sb.WriteString(note + "\n")
	}
	d := strings.TrimRight(ui.DiffPlain(oldC, newC), "\n")
	if d == "" {
		sb.WriteString("(no change: the file already contained exactly this)")
		return sb.String()
	}
	if len(d) > maxDiffInResult {
		d = d[:maxDiffInResult] + "\n... (diff truncated)"
	}
	sb.WriteString("diff of what changed:\n" + d)
	return sb.String()
}

// --- write_file ---

type writeFile struct{ r *Registry }

func (t *writeFile) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "write_file",
		Description: "Create a file or overwrite it entirely with new content. Requires approval, and returns a diff of what changed - you do not need to read the file back afterwards.",
		Parameters: obj(map[string]any{
			"path":    str("file path to write"),
			"content": str("the full new file content"),
		}, "path", "content"),
	}}
}

func (t *writeFile) Mutating() bool { return true }

func (t *writeFile) Preview(args map[string]any) (string, error) {
	abs, err := t.r.resolve(getStr(args, "path"))
	if err != nil {
		return "", err
	}
	old, _ := os.ReadFile(abs)
	newContent := getStr(args, "content")
	header := fmt.Sprintf("%swrite%s %s\n", ui.Bold, ui.Reset, getStr(args, "path"))
	return header + ui.Diff(string(old), newContent), nil
}

func (t *writeFile) Run(_ context.Context, args map[string]any) (string, error) {
	abs, err := t.r.resolve(getStr(args, "path"))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	old, _ := os.ReadFile(abs)
	t.r.sess.RecordWrite(abs)
	content := getStr(args, "content")
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	headline := fmt.Sprintf("wrote %s (%d bytes)", getStr(args, "path"), len(content))
	return applied(headline, "", string(old), content), nil
}

// --- edit_file ---

type editFile struct{ r *Registry }

func (t *editFile) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "edit_file",
		Description: "Make a targeted edit by replacing an exact string with a new one. old_string must appear exactly once unless replace_all is true. If it does not match byte-for-byte, a block differing only in indentation is accepted and the replacement is re-indented to the file's own style. Requires approval, and returns a diff of what changed - you do not need to read the file back afterwards.",
		Parameters: obj(map[string]any{
			"path":        str("file path to edit"),
			"old_string":  str("exact text to find and replace"),
			"new_string":  str("replacement text"),
			"replace_all": boolean("replace every occurrence instead of requiring a unique match"),
		}, "path", "old_string", "new_string"),
	}}
}

func (t *editFile) Mutating() bool { return true }

// editPlan is a resolved, not-yet-written edit: the exact bytes that will replace
// the file, plus a note when the match needed help.
type editPlan struct {
	abs        string
	oldContent string
	newContent string
	note       string // non-empty when the whitespace-tolerant path was taken
}

func (t *editFile) plan(args map[string]any) (editPlan, error) {
	path := getStr(args, "path")
	abs, err := t.r.resolve(path)
	if err != nil {
		return editPlan{}, err
	}
	b, rerr := os.ReadFile(abs)
	if rerr != nil {
		return editPlan{}, fmt.Errorf("cannot read %s: %w", path, rerr)
	}
	oldContent := string(b)
	oldStr := getStr(args, "old_string")
	newStr := getStr(args, "new_string")
	if oldStr == "" {
		return editPlan{}, fmt.Errorf("old_string must not be empty")
	}
	all := getBool(args, "replace_all")

	if n := strings.Count(oldContent, oldStr); n > 0 {
		if n > 1 && !all {
			return editPlan{}, fmt.Errorf("old_string appears %d times; pass replace_all or add more context to make it unique", n)
		}
		newContent := strings.Replace(oldContent, oldStr, newStr, 1)
		if all {
			newContent = strings.ReplaceAll(oldContent, oldStr, newStr)
		}
		return editPlan{abs: abs, oldContent: oldContent, newContent: newContent}, nil
	}
	return planFuzzy(abs, path, oldContent, oldStr, newStr, all)
}

// planFuzzy retries a failed exact match ignoring indentation. Nearly every miss
// is whitespace - the model retypes a tab-indented line with spaces - and turning
// that into an error costs a full round trip to re-read the file and guess again,
// usually several in a row. When the block is unambiguous once indentation is
// normalized, apply it, re-indent the replacement to the file's own convention,
// and say so in the result, so the substitution is visible rather than silent.
func planFuzzy(abs, path, oldContent, oldStr, newStr string, all bool) (editPlan, error) {
	lines := strings.Split(oldContent, "\n")
	wantLines := strings.Split(oldStr, "\n")
	regions := fuzzyRegions(lines, wantLines)
	switch {
	case len(regions) == 0:
		return editPlan{}, notFoundError(path, oldContent, oldStr)
	case len(regions) > 1 && !all:
		return editPlan{}, fmt.Errorf(
			"old_string is not in %s byte-for-byte, and matches %d separate places once "+
				"indentation is ignored. Add more surrounding context to make it unique, "+
				"or pass replace_all if every occurrence should change.", path, len(regions))
	}

	fileUnit := indentUnit(lines[regions[0][0]:regions[0][1]])
	modelUnit := indentUnit(wantLines)
	// An empty new_string is a deletion: splitting it would insert a blank line.
	var replacement []string
	if newStr != "" {
		replacement = strings.Split(reindent(newStr, modelUnit, fileUnit), "\n")
	}

	out := make([]string, 0, len(lines))
	prev := 0
	for _, r := range regions {
		out = append(out, lines[prev:r[0]]...)
		out = append(out, replacement...)
		prev = r[1]
	}
	out = append(out, lines[prev:]...)

	note := fmt.Sprintf(
		"note: old_string did not match byte-for-byte; matched at line %d ignoring indentation",
		regions[0][0]+1)
	if modelUnit != fileUnit && modelUnit != "" && fileUnit != "" {
		note += fmt.Sprintf(", and the replacement was re-indented to this file's style (%s)", indentName(fileUnit))
	}
	note += ". Check the diff below, and copy text byte-for-byte next time."

	return editPlan{abs: abs, oldContent: oldContent, newContent: strings.Join(out, "\n"), note: note}, nil
}

// fuzzyRegions returns the non-overlapping line ranges matching want once every
// run of whitespace is collapsed, so tabs, spaces and indent width compare equal.
// A block of only blank lines matches nothing: it would otherwise hit everywhere.
func fuzzyRegions(lines, wantLines []string) [][2]int {
	if len(wantLines) == 0 || len(wantLines) > len(lines) {
		return nil
	}
	norm := func(in []string) []string {
		out := make([]string, len(in))
		for i, l := range in {
			out[i] = collapseWS(l)
		}
		return out
	}
	a, b := norm(lines), norm(wantLines)
	substantive := false
	for _, l := range b {
		if l != "" {
			substantive = true
			break
		}
	}
	if !substantive {
		return nil
	}

	var out [][2]int
	for i := 0; i+len(b) <= len(a); {
		match := true
		for j := range b {
			if a[i+j] != b[j] {
				match = false
				break
			}
		}
		if match {
			out = append(out, [2]int{i, i + len(b)})
			i += len(b)
			continue
		}
		i++
	}
	return out
}

// indentOf returns the leading whitespace of a line.
func indentOf(s string) string { return s[:len(s)-len(strings.TrimLeft(s, " \t"))] }

// indentUnit infers one level of indentation from a block: a tab if anything in it
// is tab-indented, otherwise the narrowest non-empty run of spaces.
func indentUnit(lines []string) string {
	width := 0
	for _, l := range lines {
		ind := indentOf(l)
		if strings.Contains(ind, "\t") {
			return "\t"
		}
		if n := len(ind); n > 0 && (width == 0 || n < width) {
			width = n
		}
	}
	if width == 0 {
		return ""
	}
	return strings.Repeat(" ", width)
}

// indentDepth reports how many units of indentation a line carries.
func indentDepth(line, unit string) int {
	ind := indentOf(line)
	if unit == "\t" {
		return len(ind) - len(strings.TrimLeft(ind, "\t"))
	}
	if unit == "" {
		return 0
	}
	return len(ind) / len(unit)
}

// reindent rewrites each line of text to carry the file's indentation unit at the
// depth the model expressed with its own. Without this, accepting a spaces-for-tabs
// match would write spaces into a tab-indented file: a fix that lands correctly but
// leaves the file misformatted.
func reindent(text, fromUnit, toUnit string) string {
	if fromUnit == "" || toUnit == "" || fromUnit == toUnit {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = strings.Repeat(toUnit, indentDepth(l, fromUnit)) + strings.TrimLeft(l, " \t")
	}
	return strings.Join(lines, "\n")
}

// indentName describes an indent unit in the words an error message needs.
func indentName(unit string) string {
	if unit == "\t" {
		return "tabs"
	}
	return fmt.Sprintf("%d spaces", len(unit))
}

// notFoundError explains WHY an exact match failed, instead of only that it did.
// A bare "old_string not found" gives the model nothing to act on, so it re-reads
// the file and guesses again — the single most expensive failure loop this agent
// has. Nearly every miss is whitespace: the model retypes a line with spaces
// where the file uses tabs, or normalizes indentation width. So we re-search with
// whitespace collapsed and, when that hits, say exactly what differs and where.
func notFoundError(path, content, want string) error {
	lines := strings.Split(content, "\n")
	wantFirst := collapseWS(firstNonBlankLine(want))
	if wantFirst != "" {
		for i, line := range lines {
			if collapseWS(line) != wantFirst {
				continue
			}
			detail := "indentation or spacing differs"
			if strings.Contains(line, "\t") && !strings.Contains(want, "\t") {
				detail = "this file indents with TAB characters, and old_string uses spaces"
			}
			return fmt.Errorf(
				"old_string not found in %s, but line %d matches once whitespace is ignored — %s.\n"+
					"The file has:\n%s\n"+
					"Copy the text byte-for-byte from read_file (a tab must be written \\t in JSON), "+
					"or use write_file to replace the whole region.",
				path, i+1, detail, quoteLine(line))
		}
	}
	return fmt.Errorf(
		"old_string not found in %s, and no line matches even ignoring whitespace. "+
			"Re-read the relevant part of the file and copy the text exactly; "+
			"do not include read_file's line numbers.", path)
}

// firstNonBlankLine returns the first line of s with any content, which is the
// most reliable anchor to search for when the full block did not match.
func firstNonBlankLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// collapseWS reduces every run of whitespace to a single space and trims the
// ends, so tabs, spaces and indent width all compare equal.
func collapseWS(s string) string { return strings.Join(strings.Fields(s), " ") }

// quoteLine renders a line with tabs made visible, so the difference the message
// describes is actually readable in the model's context.
func quoteLine(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\r"), "\t", "\\t")
}

func (t *editFile) Preview(args map[string]any) (string, error) {
	p, err := t.plan(args)
	if err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(t.r.root, p.abs)
	header := fmt.Sprintf("%sedit%s %s\n", ui.Bold, ui.Reset, filepath.ToSlash(rel))
	if p.note != "" {
		header += ui.Yellow + "  " + p.note + ui.Reset + "\n"
	}
	return header + ui.Diff(p.oldContent, p.newContent), nil
}

func (t *editFile) Run(_ context.Context, args map[string]any) (string, error) {
	p, err := t.plan(args)
	if err != nil {
		return "", err
	}
	t.r.sess.RecordWrite(p.abs)
	if err := os.WriteFile(p.abs, []byte(p.newContent), 0o644); err != nil {
		return "", err
	}
	return applied("edited "+getStr(args, "path"), p.note, p.oldContent, p.newContent), nil
}
