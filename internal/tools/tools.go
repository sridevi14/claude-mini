package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"minicode/internal/ignore"
	"minicode/internal/llm"
	"minicode/internal/session"
)

// Tool is a single capability the agent can invoke.
type Tool interface {
	Def() llm.Tool
	// Mutating tools require user permission before Run.
	Mutating() bool
	// Preview returns a human-facing description (e.g. a diff) shown in the
	// permission prompt. Non-mutating tools may return "".
	Preview(args map[string]any) (string, error)
	Run(ctx context.Context, args map[string]any) (string, error)
}

// Registry holds all tools and shared dependencies.
type Registry struct {
	root    string
	sess    *session.Session
	ign     *ignore.Matcher
	byName  map[string]Tool
	order   []string
	servers []*serverProc
}

// New constructs the registry with every built-in tool.
func New(root string, sess *session.Session, ign *ignore.Matcher) *Registry {
	r := &Registry{
		root:   root,
		sess:   sess,
		ign:    ign,
		byName: map[string]Tool{},
	}
	r.register(&readFile{r})
	r.register(&listDir{r})
	r.register(&search{r})
	r.register(&writeFile{r})
	r.register(&editFile{r})
	r.register(&runBash{r})
	r.register(&runServer{r})
	r.register(&askUser{r})
	return r
}

func (r *Registry) register(t Tool) {
	name := t.Def().Function.Name
	r.byName[name] = t
	r.order = append(r.order, name)
}

// Defs returns the tool schemas for the API request.
func (r *Registry) Defs() []llm.Tool {
	defs := make([]llm.Tool, 0, len(r.order))
	for _, n := range r.order {
		defs = append(defs, r.byName[n].Def())
	}
	return defs
}

// Get looks up a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Names returns registered tool names in order.
func (r *Registry) Names() []string { return r.order }

// Cleanup terminates any background servers started this session.
func (r *Registry) Cleanup() {
	for _, s := range r.servers {
		s.stop()
	}
}

// --- shared helpers ---

func (r *Registry) resolve(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(r.root, p)
	}
	abs = filepath.Clean(abs)
	// keep operations inside the working directory
	rel, err := filepath.Rel(r.root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q is outside the working directory", p)
	}
	return abs, nil
}

func getStr(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getBool(args map[string]any, key string) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func obj(props map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func arr(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       map[string]any{"type": "string"},
	}
}

// getStrSlice reads a JSON string array argument (decoded as []any) into []string.
func getStrSlice(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// --- read_file ---

type readFile struct{ r *Registry }

func (t *readFile) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "read_file",
		Description: "Read the contents of a text file, relative to the working directory.",
		Parameters:  obj(map[string]any{"path": str("file path to read")}, "path"),
	}}
}
func (t *readFile) Mutating() bool                         { return false }
func (t *readFile) Preview(map[string]any) (string, error) { return "", nil }
func (t *readFile) Run(_ context.Context, args map[string]any) (string, error) {
	abs, err := t.r.resolve(getStr(args, "path"))
	if err != nil {
		return "", err
	}
	const max = 200_000
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if len(b) > max {
		return string(b[:max]) + "\n… (file truncated at 200KB)", nil
	}
	if len(b) == 0 {
		return "(file is empty)", nil
	}
	return string(b), nil
}

// --- list_dir ---

type listDir struct{ r *Registry }

func (t *listDir) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "list_dir",
		Description: "List files and subdirectories of a directory (ignored paths are skipped).",
		Parameters:  obj(map[string]any{"path": str("directory path; defaults to '.'")}),
	}}
}
func (t *listDir) Mutating() bool                         { return false }
func (t *listDir) Preview(map[string]any) (string, error) { return "", nil }
func (t *listDir) Run(_ context.Context, args map[string]any) (string, error) {
	p := getStr(args, "path")
	if p == "" {
		p = "."
	}
	abs, err := t.r.resolve(p)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, e := range entries {
		full := filepath.Join(abs, e.Name())
		if t.r.ign.Ignored(full, e.IsDir()) {
			continue
		}
		if e.IsDir() {
			lines = append(lines, e.Name()+"/")
		} else {
			lines = append(lines, e.Name())
		}
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return "(empty or all entries ignored)", nil
	}
	return strings.Join(lines, "\n"), nil
}
