package ignore

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Matcher decides whether a path should be skipped during search/listing.
// It loads a subset of .gitignore + .agentignore semantics (enough for a
// learning tool, not a full git-spec implementation).
type Matcher struct {
	root     string
	patterns []pattern
}

type pattern struct {
	glob    string // pattern body, no leading/trailing slash
	dirOnly bool
	rooted  bool // pattern contained a slash -> match against full rel path
}

// New loads ignore rules rooted at dir.
func New(root string) *Matcher {
	m := &Matcher{root: root}
	// always-ignore defaults
	for _, p := range []string{".git/", ".mini_agent/"} {
		m.add(p)
	}
	for _, f := range []string{".gitignore", ".agentignore"} {
		m.load(filepath.Join(root, f))
	}
	return m
}

func (m *Matcher) load(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		m.add(line)
	}
}

func (m *Matcher) add(line string) {
	p := pattern{}
	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	line = strings.TrimPrefix(line, "/")
	p.rooted = strings.Contains(line, "/")
	p.glob = line
	if p.glob != "" {
		m.patterns = append(m.patterns, p)
	}
}

// Ignored reports whether the given path (absolute or relative to root)
// should be ignored.
func (m *Matcher) Ignored(path string, isDir bool) bool {
	rel := path
	if filepath.IsAbs(path) {
		if r, err := filepath.Rel(m.root, path); err == nil {
			rel = r
		}
	}
	rel = filepath.ToSlash(rel)
	segs := strings.Split(rel, "/")
	last := len(segs) - 1

	for _, p := range m.patterns {
		if p.rooted {
			// pattern with a slash matches against the full relative path.
			if p.dirOnly && !isDir {
				continue
			}
			if ok, _ := filepath.Match(p.glob, rel); ok {
				return true
			}
			continue
		}
		// non-rooted: match any path segment.
		for i, seg := range segs {
			ok, _ := filepath.Match(p.glob, seg)
			if !ok {
				continue
			}
			if i < last {
				// matched an ancestor directory -> everything under it is ignored.
				return true
			}
			// matched the final segment.
			if !p.dirOnly || isDir {
				return true
			}
		}
	}
	return false
}
