package mention

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sridevi14/claude-mini/internal/ignore"
)

// buildTree creates a small project layout under a temp dir and returns a Resolver.
func buildTree(t *testing.T) *Resolver {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"main.go",
		"README.md",
		"internal/ui/ui.go",
		"internal/agent/agent.go",
	}
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return New(root, ignore.New(root))
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestMatchesBasenamePrefixRanksFirst(t *testing.T) {
	r := buildTree(t)
	got := r.Matches("main")
	if len(got) == 0 || got[0] != "main.go" {
		t.Fatalf("Matches(\"main\") = %v; want main.go first", got)
	}
}

func TestMatchesFindsNestedFileAndDir(t *testing.T) {
	r := buildTree(t)
	got := r.Matches("ui")
	if !contains(got, "internal/ui/ui.go") {
		t.Errorf("Matches(\"ui\") = %v; want it to include internal/ui/ui.go", got)
	}
	if !contains(got, "internal/ui/") {
		t.Errorf("Matches(\"ui\") = %v; want it to include the internal/ui/ directory", got)
	}
}

func TestMatchesEmptyTokenListsEntries(t *testing.T) {
	r := buildTree(t)
	if got := r.Matches(""); len(got) == 0 {
		t.Error("Matches(\"\") returned nothing; want some entries")
	}
}

func TestMatchesSubstringFallback(t *testing.T) {
	r := buildTree(t)
	// "agent" only appears mid-path / in the basename — substring should still hit.
	if got := r.Matches("agent"); !contains(got, "internal/agent/agent.go") {
		t.Errorf("Matches(\"agent\") = %v; want internal/agent/agent.go", got)
	}
}

func TestMatchesRespectsIgnore(t *testing.T) {
	r := buildTree(t)
	// .git is always ignored; ensure nothing from it ever leaks in.
	if err := os.MkdirAll(filepath.Join(r.root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.root, ".git", "config"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, m := range r.Matches("config") {
		if filepath.ToSlash(m) == ".git/config" {
			t.Errorf("Matches surfaced an ignored path: %s", m)
		}
	}
}
