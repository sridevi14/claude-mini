package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sridevi14/claude-mini/internal/llm"
)

type search struct{ r *Registry }

func (t *search) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "search",
		Description: "Search file contents for a regular expression across the working directory. Returns matching lines as path:line:text. Respects ignore rules.",
		Parameters: obj(map[string]any{
			"pattern": str("Go/RE2 regular expression to search for"),
			"path":    str("subdirectory to search within; defaults to '.'"),
			"glob":    str("optional filename glob filter, e.g. '*.go'"),
		}, "pattern"),
	}}
}

func (t *search) Mutating() bool                         { return false }
func (t *search) Preview(map[string]any) (string, error) { return "", nil }

func (t *search) Run(_ context.Context, args map[string]any) (string, error) {
	pat := getStr(args, "pattern")
	re, err := regexp.Compile(pat)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	start := getStr(args, "path")
	if start == "" {
		start = "."
	}
	absStart, err := t.r.resolve(start)
	if err != nil {
		return "", err
	}
	globFilter := getStr(args, "glob")

	const maxResults = 200
	var results []string
	count := 0

	walkErr := filepath.WalkDir(absStart, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if t.r.ign.Ignored(path, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if globFilter != "" {
			if ok, _ := filepath.Match(globFilter, d.Name()); !ok {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2_000_000 {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || isBinary(b) {
			return nil
		}
		rel, _ := filepath.Rel(t.r.root, path)
		rel = filepath.ToSlash(rel)
		for i, line := range strings.Split(string(b), "\n") {
			if re.MatchString(line) {
				trimmed := strings.TrimSpace(line)
				if len(trimmed) > 200 {
					trimmed = trimmed[:200] + "…"
				}
				results = append(results, fmt.Sprintf("%s:%d: %s", rel, i+1, trimmed))
				count++
				if count >= maxResults {
					return fmt.Errorf("__stop__")
				}
			}
		}
		return nil
	})
	if walkErr != nil && walkErr.Error() != "__stop__" {
		return "", walkErr
	}

	if len(results) == 0 {
		return "(no matches)", nil
	}
	out := strings.Join(results, "\n")
	if count >= maxResults {
		out += fmt.Sprintf("\n… (stopped at %d matches)", maxResults)
	}
	return out, nil
}

func isBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}
