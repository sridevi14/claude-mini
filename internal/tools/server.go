package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/sridevi14/claude-mini/internal/llm"
	"github.com/sridevi14/claude-mini/internal/ui"
)

// serverProc tracks one background process.
type serverProc struct {
	name string
	cmd  *exec.Cmd
	log  string
}

func (s *serverProc) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

type runServer struct{ r *Registry }

func (t *runServer) Def() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "run_server",
		Description: "Start a long-running process (e.g. a dev server) in the background. Returns immediately with the PID and a log file path; it does NOT block. Use run_bash to read the log afterwards. Requires approval.",
		Parameters: obj(map[string]any{
			"command": str("the command to start, e.g. 'npm run dev'"),
			"name":    str("short name for this server, used for the log filename"),
		}, "command"),
	}}
}

func (t *runServer) Mutating() bool { return true }

func (t *runServer) Preview(args map[string]any) (string, error) {
	return fmt.Sprintf("%sstart server%s %s%s%s (background)", ui.Bold, ui.Reset, ui.Cyan, getStr(args, "command"), ui.Reset), nil
}

func (t *runServer) Run(_ context.Context, args map[string]any) (string, error) {
	command := getStr(args, "command")
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	name := getStr(args, "name")
	if name == "" {
		name = "server"
	}

	logDir := filepath.Join(t.r.root, ".mini_agent", "servers")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", err
	}
	logPath := filepath.Join(logDir, name+"-"+time.Now().Format("150405")+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return "", err
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.Command("bash", "-c", command)
	}
	cmd.Dir = t.r.root
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return "", fmt.Errorf("failed to start: %w", err)
	}
	t.r.servers = append(t.r.servers, &serverProc{name: name, cmd: cmd, log: logPath})

	relLog, _ := filepath.Rel(t.r.root, logPath)
	return fmt.Sprintf("started '%s' (pid %d). Logs streaming to %s. It will be killed when mini-code exits.",
		name, cmd.Process.Pid, filepath.ToSlash(relLog)), nil
}
