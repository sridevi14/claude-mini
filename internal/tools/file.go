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

// --- write_file ---

type writeFile struct{ r *Registry }

func (t *writeFile) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "write_file",
		Description: "Create a file or overwrite it entirely with new content. Shows a diff and requires approval.",
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
	t.r.sess.RecordWrite(abs)
	content := getStr(args, "content")
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %s (%d bytes)", getStr(args, "path"), len(content)), nil
}

// --- edit_file ---

type editFile struct{ r *Registry }

func (t *editFile) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "edit_file",
		Description: "Make a targeted edit by replacing an exact string with a new one. old_string must appear exactly once unless replace_all is true. Shows a diff and requires approval.",
		Parameters: obj(map[string]any{
			"path":        str("file path to edit"),
			"old_string":  str("exact text to find and replace"),
			"new_string":  str("replacement text"),
			"replace_all": boolean("replace every occurrence instead of requiring a unique match"),
		}, "path", "old_string", "new_string"),
	}}
}

func (t *editFile) Mutating() bool { return true }

func (t *editFile) compute(args map[string]any) (abs, oldContent, newContent string, err error) {
	abs, err = t.r.resolve(getStr(args, "path"))
	if err != nil {
		return
	}
	b, rerr := os.ReadFile(abs)
	if rerr != nil {
		err = fmt.Errorf("cannot read %s: %w", getStr(args, "path"), rerr)
		return
	}
	oldContent = string(b)
	oldStr := getStr(args, "old_string")
	newStr := getStr(args, "new_string")
	if oldStr == "" {
		err = fmt.Errorf("old_string must not be empty")
		return
	}
	n := strings.Count(oldContent, oldStr)
	if n == 0 {
		err = fmt.Errorf("old_string not found in %s", getStr(args, "path"))
		return
	}
	if n > 1 && !getBool(args, "replace_all") {
		err = fmt.Errorf("old_string appears %d times; pass replace_all or add more context to make it unique", n)
		return
	}
	if getBool(args, "replace_all") {
		newContent = strings.ReplaceAll(oldContent, oldStr, newStr)
	} else {
		newContent = strings.Replace(oldContent, oldStr, newStr, 1)
	}
	return
}

func (t *editFile) Preview(args map[string]any) (string, error) {
	abs, oldC, newC, err := t.compute(args)
	if err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(t.r.root, abs)
	header := fmt.Sprintf("%sedit%s %s\n", ui.Bold, ui.Reset, filepath.ToSlash(rel))
	return header + ui.Diff(oldC, newC), nil
}

func (t *editFile) Run(_ context.Context, args map[string]any) (string, error) {
	abs, _, newC, err := t.compute(args)
	if err != nil {
		return "", err
	}
	t.r.sess.RecordWrite(abs)
	if err := os.WriteFile(abs, []byte(newC), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", getStr(args, "path")), nil
}
