// Package mention expands @file references in a user's message by attaching the
// referenced file's contents (or a directory listing) as context for the agent.
//
// True per-keystroke path autocomplete needs raw terminal mode, which would break
// this tool's line-based, zero-dependency design. Instead, @mentions are resolved
// on submit: an exact path attaches silently, and an ambiguous one prompts the
// user to pick from the matching files (the "suggestion" step).
package mention

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sridevi14/claude-mini/internal/ignore"
	"github.com/sridevi14/claude-mini/internal/ui"
)

const (
	maxFileBytes  = 50_000 // cap per attached file to bound token usage
	maxCandidates = 8      // how many options to show when a mention is ambiguous
	maxScan       = 5000   // safety cap on files walked
	maxSuggest    = 12     // how many live dropdown suggestions to surface
)

// Matches returns up to maxSuggest project paths matching the partial @token
// currently being typed, ranked: basename-prefix first, then path-prefix, then
// substring. Directories carry a trailing "/" so the user can drill into them.
// An empty token returns the first entries (top of the tree). This feeds the
// live autocomplete dropdown, so it must be cheap and allocation-light.
func (r *Resolver) Matches(token string) []string {
	token = strings.ToLower(filepath.ToSlash(token))
	type scored struct {
		p    string
		rank int
	}
	var hits []scored
	for _, e := range r.entries() {
		el := strings.ToLower(e)
		base := strings.ToLower(path.Base(strings.TrimSuffix(el, "/")))
		switch {
		case token == "":
			hits = append(hits, scored{e, 0})
		case strings.HasPrefix(base, token):
			hits = append(hits, scored{e, 0})
		case strings.HasPrefix(el, token):
			hits = append(hits, scored{e, 1})
		case strings.Contains(el, token):
			hits = append(hits, scored{e, 2})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		return hits[i].p < hits[j].p
	})
	if len(hits) > maxSuggest {
		hits = hits[:maxSuggest]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.p
	}
	return out
}

// entries lists project files and directories (dirs marked with a trailing "/"),
// ignored paths skipped, capped for safety.
func (r *Resolver) entries() []string {
	var out []string
	_ = filepath.WalkDir(r.root, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == r.root {
			return nil
		}
		if r.ign.Ignored(p, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, e := filepath.Rel(r.root, p)
		if e != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			rel += "/"
		}
		out = append(out, rel)
		if len(out) >= maxScan {
			return filepath.SkipAll
		}
		return nil
	})
	return out
}

// Resolver expands @mentions against the project tree.
type Resolver struct {
	root string
	ign  *ignore.Matcher
}

// New builds a resolver rooted at the working directory.
func New(root string, ign *ignore.Matcher) *Resolver {
	return &Resolver{root: root, ign: ign}
}

// Expand returns the message with any resolved @file contents appended. The
// user's original text is preserved verbatim; attachments are added in fenced
// blocks the model can read. If nothing resolves, the input is returned unchanged.
func (r *Resolver) Expand(input string) string {
	tokens := parseMentions(input)
	if len(tokens) == 0 {
		return input
	}
	var attached []string
	seen := map[string]bool{}
	for _, tok := range tokens {
		abs, ok := r.resolveToken(tok)
		if !ok || seen[abs] {
			continue
		}
		seen[abs] = true
		block, label := r.attach(abs)
		if block != "" {
			attached = append(attached, block)
			ui.Info("  ↳ attached %s", label)
		}
	}
	if len(attached) == 0 {
		return input
	}
	return input + "\n\n--- referenced files ---\n" + strings.Join(attached, "\n")
}

// parseMentions extracts @tokens that start at a word boundary.
func parseMentions(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		if s[i] == '@' && (i == 0 || isSpace(s[i-1])) {
			j := i + 1
			for j < len(s) && !isSpace(s[j]) {
				j++
			}
			tok := strings.TrimRight(s[i+1:j], ",;:)")
			if tok != "" {
				out = append(out, tok)
			}
			i = j
			continue
		}
		i++
	}
	return out
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// resolveToken maps an @token to an absolute path inside the project, asking the
// user to disambiguate when several files match equally well.
func (r *Resolver) resolveToken(tok string) (string, bool) {
	tok = filepath.ToSlash(tok)

	// Fast path: an exact existing path relative to root.
	abs := filepath.Join(r.root, filepath.FromSlash(tok))
	if r.within(abs) != "" {
		if _, err := os.Stat(abs); err == nil {
			return abs, true
		}
	}

	type scored struct {
		rel  string
		tier int
	}
	var matches []scored
	for _, rel := range r.candidates() {
		relSlash := filepath.ToSlash(rel)
		switch {
		case relSlash == tok:
			matches = append(matches, scored{rel, 0})
		case filepath.Base(relSlash) == tok:
			matches = append(matches, scored{rel, 1})
		case strings.HasSuffix(relSlash, tok):
			matches = append(matches, scored{rel, 2})
		case strings.Contains(relSlash, tok):
			matches = append(matches, scored{rel, 3})
		}
	}
	if len(matches) == 0 {
		ui.Info("  ↳ no file matches @%s", tok)
		return "", false
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].tier != matches[j].tier {
			return matches[i].tier < matches[j].tier
		}
		return matches[i].rel < matches[j].rel
	})

	best := matches[0].tier
	var top []string
	for _, m := range matches {
		if m.tier == best {
			top = append(top, m.rel)
		}
	}
	if len(top) == 1 {
		return filepath.Join(r.root, filepath.FromSlash(top[0])), true
	}
	if len(top) > maxCandidates {
		top = top[:maxCandidates]
	}
	choice := strings.TrimSpace(ui.AskUser("multiple files match @"+tok+" — which one?", top))
	for _, rel := range top {
		if rel == choice {
			return filepath.Join(r.root, filepath.FromSlash(rel)), true
		}
	}
	if choice != "" && choice != tok {
		return r.resolveToken(choice) // user typed a different path; try once more
	}
	return "", false
}

// candidates lists project files (ignored paths skipped), capped for safety.
func (r *Resolver) candidates() []string {
	var files []string
	_ = filepath.WalkDir(r.root, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == r.root {
			return nil
		}
		if r.ign.Ignored(p, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			if rel, e := filepath.Rel(r.root, p); e == nil {
				files = append(files, rel)
			}
		}
		if len(files) >= maxScan {
			return filepath.SkipAll
		}
		return nil
	})
	return files
}

// attach builds the fenced context block (and a short label) for a resolved path.
func (r *Resolver) attach(abs string) (block, label string) {
	rel := r.within(abs)
	if rel == "" {
		return "", ""
	}
	rel = filepath.ToSlash(rel)
	info, err := os.Stat(abs)
	if err != nil {
		return "", ""
	}
	if info.IsDir() {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return "", ""
		}
		var names []string
		for _, e := range entries {
			if r.ign.Ignored(filepath.Join(abs, e.Name()), e.IsDir()) {
				continue
			}
			n := e.Name()
			if e.IsDir() {
				n += "/"
			}
			names = append(names, n)
		}
		sort.Strings(names)
		return "[directory: " + rel + "]\n" + strings.Join(names, "\n") + "\n",
			rel + "/ (" + strconv.Itoa(len(names)) + " entries)"
	}

	b, err := os.ReadFile(abs)
	if err != nil {
		return "", ""
	}
	if isBinary(b) {
		return "[file: " + rel + "] (binary — contents not shown)\n", rel + " (binary, skipped)"
	}
	truncated := false
	if len(b) > maxFileBytes {
		b = b[:maxFileBytes]
		truncated = true
	}
	body := "[file: " + rel + "]\n```\n" + string(b) + "\n```\n"
	if truncated {
		body += "(truncated at 50KB)\n"
	}
	return body, rel
}

// within returns the clean relative path if abs is inside root, else "".
func (r *Resolver) within(abs string) string {
	rel, err := filepath.Rel(r.root, filepath.Clean(abs))
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return rel
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
