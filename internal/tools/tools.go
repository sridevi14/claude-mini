package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sridevi14/claude-mini/internal/ignore"
	"github.com/sridevi14/claude-mini/internal/llm"
	"github.com/sridevi14/claude-mini/internal/session"
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

// Read paging defaults: enough to see a whole file of ordinary size in one call,
// bounded so a huge or minified file cannot flood the context.
const (
	defaultReadLines = 400
	maxReadLineWidth = 2000
)

func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// getInt reads a numeric argument. JSON numbers decode as float64, and some
// models send them as strings, so both are accepted.
func getInt(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

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
		Name: "read_file",
		Description: "Read a text file, relative to the working directory. Output is line-numbered " +
			"(the numbers are display only - never copy them into edit_file strings). Long files come " +
			"back one page at a time: pass offset (1-based first line) and limit (how many lines) to " +
			"read just the part you need, for example the single function you located with search.",
		Parameters: obj(map[string]any{
			"path":   str("file path to read"),
			"offset": integer("first line to read, 1-based (default 1)"),
			"limit":  integer("how many lines to return (default 400)"),
		}, "path"),
	}}
}
func (t *readFile) Mutating() bool                         { return false }
func (t *readFile) Preview(map[string]any) (string, error) { return "", nil }
func (t *readFile) Run(_ context.Context, args map[string]any) (string, error) {
	abs, err := t.r.resolve(getStr(args, "path"))
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if len(b) == 0 {
		return "(file is empty)", nil
	}

	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	// A trailing newline yields a final empty element that is not a real line.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total := len(lines)

	offset := getInt(args, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	if offset > total {
		return fmt.Sprintf("offset %d is past the end of %s (%d lines)", offset, getStr(args, "path"), total), nil
	}
	limit := getInt(args, "limit", defaultReadLines)
	if limit < 1 {
		limit = defaultReadLines
	}
	last := offset - 1 + limit
	if last > total {
		last = total
	}

	var sb strings.Builder
	for i := offset - 1; i < last; i++ {
		ln := lines[i]
		if len(ln) > maxReadLineWidth {
			ln = ln[:maxReadLineWidth] + "… (line truncated)"
		}
		fmt.Fprintf(&sb, "%6d\t%s\n", i+1, ln)
	}
	if offset > 1 || last < total {
		fmt.Fprintf(&sb, "… showing lines %d-%d of %d. Call read_file again with offset/limit for another section.\n", offset, last, total)
	}
	return sb.String(), nil
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
