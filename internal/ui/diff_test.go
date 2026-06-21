package ui

import "strings"

import "testing"

func TestDiffAddAndRemove(t *testing.T) {
	old := "line1\nline2\nline3"
	neu := "line1\nCHANGED\nline3"
	out := Diff(old, neu)
	if !strings.Contains(out, "- line2") {
		t.Errorf("expected removed line2, got:\n%s", out)
	}
	if !strings.Contains(out, "+ CHANGED") {
		t.Errorf("expected added CHANGED, got:\n%s", out)
	}
}

func TestDiffNewFile(t *testing.T) {
	out := Diff("", "hello\nworld")
	if !strings.Contains(out, "+ hello") || !strings.Contains(out, "+ world") {
		t.Errorf("expected both lines added, got:\n%s", out)
	}
}
