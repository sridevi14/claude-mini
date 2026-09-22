package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sridevi14/claude-mini/internal/llm"
	"github.com/sridevi14/claude-mini/internal/ui"
)

// envShell forces a specific interpreter for run_bash, overriding detection.
const envShell = "CLAUDE_MINI_SHELL"

// shellInfo describes the interpreter run_bash actually executes through.
type shellInfo struct {
	path   string   // resolved executable
	args   []string // arguments preceding the command string
	name   string   // what to call it when telling the model
	chains bool     // supports && and ||
}

var (
	shellOnce sync.Once
	theShell  shellInfo
)

// shell resolves the command interpreter once per process.
//
// The tool is called run_bash, so the model writes bash - overwhelmingly
// "cd sub && go build ./...". On Windows this ran through Windows PowerShell 5.1,
// which rejects && outright ("not a valid statement separator in this version"),
// so that first call always failed and burned a round trip. The natural retry,
// "cd sub; go build", is not equivalent: ';' runs the second command even when the
// first failed, so a build can silently run in the wrong directory.
//
// So prefer an interpreter that can actually chain. Git for Windows ships bash but
// puts only Git\cmd on PATH, so look beside the git executable before falling back.
// Resolution happens once: the result feeds the tool description, which must stay
// byte-stable across requests or it would invalidate the prompt cache every turn.
func shell() shellInfo {
	shellOnce.Do(func() { theShell = resolveShell() })
	return theShell
}

func resolveShell() shellInfo {
	if custom := strings.TrimSpace(os.Getenv(envShell)); custom != "" {
		return describeShell(custom)
	}
	if runtime.GOOS != "windows" {
		return shellInfo{path: "bash", args: []string{"-c"}, name: "bash", chains: true}
	}
	for _, cand := range windowsBashCandidates() {
		p, err := exec.LookPath(cand)
		if err != nil || isWSLShim(p) {
			continue
		}
		return shellInfo{path: p, args: []string{"-c"}, name: "bash", chains: true}
	}
	if p, err := exec.LookPath("pwsh"); err == nil {
		return describeShell(p)
	}
	return describeShell("powershell")
}

// windowsBashCandidates lists where Git for Windows keeps bash. A default install
// exports only Git\cmd (git.exe), so the sibling directories are probed directly,
// including ones derived from wherever git itself was found.
func windowsBashCandidates() []string {
	c := []string{"bash"}
	if git, err := exec.LookPath("git"); err == nil {
		root := filepath.Dir(filepath.Dir(git)) // ...\Git\cmd\git.exe -> ...\Git
		c = append(c,
			filepath.Join(root, "bin", "bash.exe"),
			filepath.Join(root, "usr", "bin", "bash.exe"))
	}
	return append(c,
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`)
}

// isWSLShim reports whether a resolved bash is the WSL launcher that Windows
// keeps in System32. That bash runs inside a Linux filesystem view, where the
// Windows working directory this tool sets does not exist, so every command would
// fail on a missing path. It shadows Git bash on PATH, so it must be rejected by
// location rather than simply ordered after it.
func isWSLShim(path string) bool {
	return strings.Contains(strings.ToLower(filepath.ToSlash(path)), "/windows/system32/")
}

// describeShell infers how to invoke an interpreter from its name.
func describeShell(path string) shellInfo {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), ".exe"))
	psArgs := []string{"-NoProfile", "-NonInteractive", "-Command"}
	switch base {
	case "pwsh":
		return shellInfo{path: path, args: psArgs, name: "PowerShell 7", chains: true}
	case "powershell":
		return shellInfo{path: path, args: psArgs, name: "Windows PowerShell 5.1", chains: false}
	case "cmd":
		return shellInfo{path: path, args: []string{"/C"}, name: "cmd.exe", chains: true}
	default:
		return shellInfo{path: path, args: []string{"-c"}, name: base, chains: true}
	}
}

type runBash struct{ r *Registry }

// bashDescription names the interpreter the model is actually writing for. Naming
// it is the whole point: a tool called run_bash invites bash syntax, and when the
// interpreter cannot honour that the model needs to know before it spends a call
// finding out.
func bashDescription(sh shellInfo) string {
	desc := "Run a shell command in the working directory and return its combined stdout/stderr " +
		"and exit code. Use for builds, tests, git, etc. Requires approval. Not for long-running " +
		"servers - use run_server for those. Commands are executed by " + sh.name + "."
	if !sh.chains {
		desc += " IMPORTANT: this interpreter does NOT support && or ||, and a command using them " +
			"fails to parse. To run a second command only if the first succeeds, write " +
			"`cd sub; if ($?) { go build ./... }` - a bare ';' is not a substitute, because it runs " +
			"the next command even when the previous one failed."
	}
	return desc
}

func (t *runBash) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "run_bash",
		Description: bashDescription(shell()),
		Parameters: obj(map[string]any{
			"command": str("the shell command to execute"),
		}, "command"),
	}}
}

func (t *runBash) Mutating() bool { return true }

func (t *runBash) Preview(args map[string]any) (string, error) {
	return fmt.Sprintf("%srun%s %s%s%s", ui.Bold, ui.Reset, ui.Cyan, getStr(args, "command"), ui.Reset), nil
}

// shellCommand builds the OS-appropriate command for the resolved interpreter.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	sh := shell()
	argv := append(append([]string{}, sh.args...), command)
	return exec.CommandContext(ctx, sh.path, argv...)
}

func (t *runBash) Run(ctx context.Context, args map[string]any) (string, error) {
	command := getStr(args, "command")
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := shellCommand(cctx, command)
	cmd.Dir = t.r.root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()

	out := buf.String()
	const max = 30_000
	if len(out) > max {
		out = out[:max] + "\n... (output truncated)"
	}
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return out, fmt.Errorf("failed to run command: %w", err)
		}
	}
	result := fmt.Sprintf("exit code: %d\n%s", exit, out)
	if out == "" {
		result = fmt.Sprintf("exit code: %d (no output)", exit)
	}
	return result, nil
}
