package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIgnore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n*.log\nbuild/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(dir)

	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"node_modules", true, true},
		{"node_modules/pkg/index.js", false, true},
		{"app.log", false, true},
		{"src/main.go", false, false},
		{".git/config", false, true},
		{".mini_agent/sessions/x.jsonl", false, true},
		{"build/out", true, true},
	}
	for _, c := range cases {
		got := m.Ignored(filepath.Join(dir, c.path), c.isDir)
		if got != c.want {
			t.Errorf("Ignored(%q)=%v, want %v", c.path, got, c.want)
		}
	}
}
