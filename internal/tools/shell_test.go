package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDescribeShellKnownInterpreters(t *testing.T) {
	cases := []struct {
		path   string
		name   string
		chains bool
		first  string
	}{
		{"powershell", "Windows PowerShell 5.1", false, "-NoProfile"},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, "PowerShell 7", true, "-NoProfile"},
		{`C:\Program Files\Git\bin\bash.exe`, "bash", true, "-c"},
		{"cmd.exe", "cmd.exe", true, "/C"},
	}
	for _, c := range cases {
		got := describeShell(c.path)
		if got.name != c.name || got.chains != c.chains {
			t.Errorf("describeShell(%q) = {%s chains=%v}, want {%s chains=%v}",
				c.path, got.name, got.chains, c.name, c.chains)
		}
		if len(got.args) == 0 || got.args[0] != c.first {
			t.Errorf("describeShell(%q) args = %v, want to start with %q", c.path, got.args, c.first)
		}
	}
}

func TestWSLShimIsRejected(t *testing.T) {
	// The WSL launcher shadows Git bash on PATH but runs in a Linux filesystem
	// view, where the Windows working directory this tool sets does not exist.
	for _, p := range []string{
		`C:\WINDOWS\system32\bash.exe`,
		`C:\Windows\System32\bash.exe`,
		"c:/windows/system32/bash.exe",
	} {
		if !isWSLShim(p) {
			t.Errorf("%q should be rejected as the WSL shim", p)
		}
	}
	for _, p := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		"/usr/bin/bash",
	} {
		if isWSLShim(p) {
			t.Errorf("%q is a real bash and should be accepted", p)
		}
	}
}

func TestBashDescriptionWarnsWhenChainingUnsupported(t *testing.T) {
	d := bashDescription(describeShell("powershell"))
	if !strings.Contains(d, "Windows PowerShell 5.1") {
		t.Errorf("description should name the interpreter:\n%s", d)
	}
	if !strings.Contains(d, "does NOT support && or ||") {
		t.Errorf("description must warn that && fails to parse:\n%s", d)
	}
	// The obvious retry is the dangerous one, so it has to be called out.
	if !strings.Contains(d, "not a substitute") {
		t.Errorf("description must warn that ';' changes the semantics:\n%s", d)
	}
}

func TestBashDescriptionStaysQuietWhenChainingWorks(t *testing.T) {
	d := bashDescription(describeShell(`C:\Program Files\Git\bin\bash.exe`))
	if strings.Contains(d, "does NOT support") {
		t.Errorf("bash supports &&; the warning should be absent:\n%s", d)
	}
	if !strings.Contains(d, "executed by bash") {
		t.Errorf("description should still name the interpreter:\n%s", d)
	}
}

func TestWindowsBashCandidatesCoverGitInstallLayout(t *testing.T) {
	got := windowsBashCandidates()
	joined := strings.ToLower(strings.Join(got, "|"))
	for _, want := range []string{
		filepath.Join("git", "bin", "bash.exe"),
		filepath.Join("git", "usr", "bin", "bash.exe"),
	} {
		if !strings.Contains(joined, strings.ToLower(want)) {
			t.Errorf("candidates should include a path ending %s, got %v", want, got)
		}
	}
}
