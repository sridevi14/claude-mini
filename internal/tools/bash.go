package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"minicode/internal/llm"
	"minicode/internal/ui"
)

type runBash struct{ r *Registry }

func (t *runBash) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "run_bash",
		Description: "Run a shell command in the working directory and return its combined stdout/stderr and exit code. Use for builds, tests, git, etc. Requires approval. Not for long-running servers — use run_server for those.",
		Parameters: obj(map[string]any{
			"command": str("the shell command to execute"),
		}, "command"),
	}}
}

func (t *runBash) Mutating() bool { return true }

func (t *runBash) Preview(args map[string]any) (string, error) {
	return fmt.Sprintf("%srun%s %s%s%s", ui.Bold, ui.Reset, ui.Cyan, getStr(args, "command"), ui.Reset), nil
}

// shellCommand builds an OS-appropriate command. We expose a "bash" tool but
// transparently fall back to the platform shell on Windows.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	}
	return exec.CommandContext(ctx, "bash", "-c", command)
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
		out = out[:max] + "\n… (output truncated)"
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
