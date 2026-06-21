package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eiannone/keyboard"

	"github.com/sridevi14/claude-mini/internal/agent"
	"github.com/sridevi14/claude-mini/internal/ignore"
	"github.com/sridevi14/claude-mini/internal/llm"
	"github.com/sridevi14/claude-mini/internal/mention"
	"github.com/sridevi14/claude-mini/internal/session"
	"github.com/sridevi14/claude-mini/internal/tools"
	"github.com/sridevi14/claude-mini/internal/ui"
)

const (
	baseURL = "https://api.openadapter.in/v1"
	model   = "deepseek-v3"
)

// loadDotEnv reads simple KEY=VALUE lines from a .env file (if present) and
// sets them in the environment without overriding already-set variables.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// strip surrounding quotes
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if key != "" {
			if _, exists := os.LookupEnv(key); !exists {
				os.Setenv(key, val)
			}
		}
	}
}

func main() {
	loadDotEnv(".env")

	apiKey := resolveAPIKey()
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, ui.Red+"No OpenAdapter API key provided."+ui.Reset)
		fmt.Fprintln(os.Stderr, ui.Gray+"  run again in a terminal to enter it, or set "+envKeyName+"=sk-… for scripted use."+ui.Reset)
		os.Exit(1)
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot determine working directory:", err)
		os.Exit(1)
	}

	sess, err := session.New(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot start session:", err)
		os.Exit(1)
	}
	defer sess.Close()

	ign := ignore.New(root)
	client := llm.New(baseURL, apiKey, model)
	reg := tools.New(root, sess, ign)
	defer reg.Cleanup()

	cost := agent.NewCost()
	ag := agent.New(client, reg, sess, cost, root)
	mentions := mention.New(root, ign)

	// On a real terminal, let Esc pause the agent mid-stream (see escWatcher).
	if ui.Interactive() {
		ag.SetInterruptWatcher(escWatcher)
	}

	ui.Banner(client.Model, root)

	ctx := context.Background()
	for {
		input, ok := ui.ReadTask(mentions.Matches)
		if !ok {
			break // EOF (Ctrl+D)
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if strings.HasPrefix(input, "/") {
			if quit := handleCommand(input, sess, reg, cost, ag, client, root); quit {
				break
			}
			continue
		}
		// Attach any @file mentions, then run the task. ag.Run manages its own
		// cancelable context; Esc pauses it via the installed watcher.
		ag.Run(ctx, mentions.Expand(input))
	}
	ui.Info("\n  bye.")
}

// escWatcher listens for the Esc key while the agent is streaming and cancels the
// turn when pressed. It returns a release func that stops listening once streaming
// ends — keeping stdin free for the tool phase's permission/ask prompts. If the
// keyboard can't be opened (e.g. not a TTY), it is a no-op.
func escWatcher(cancel context.CancelFunc) func() {
	if err := keyboard.Open(); err != nil {
		return func() {}
	}
	go func() {
		for {
			_, key, err := keyboard.GetKey()
			if err != nil {
				return // keyboard.Close() unblocks GetKey and lands here
			}
			if key == keyboard.KeyEsc {
				ui.Info("\n  ⏸ pausing…")
				cancel()
				return
			}
			// ignore all other keys while the agent works
		}
	}()
	return func() { _ = keyboard.Close() }
}

func handleCommand(cmd string, sess *session.Session, reg *tools.Registry, cost *agent.Cost, ag *agent.Agent, client *llm.Client, root string) (quit bool) {
	switch strings.Fields(cmd)[0] {
	case "/exit", "/quit", "/q":
		return true
	case "/help":
		ui.Info("commands:")
		ui.Info("  /model   list coding models and switch the active one")
		ui.Info("  /login   enter or change your OpenAdapter API key")
		ui.Info("  /resume  reload the previous session's conversation")
		ui.Info("  /undo    revert the last file write")
		ui.Info("  /cost    show token usage and estimated cost")
		ui.Info("  /tools   list available tools")
		ui.Info("  /getkey    get apikey")
		ui.Info("  /exit    quit")
		ui.Info("type @ to pick a file from the live dropdown and attach its contents.")
		ui.Info("shortcut: press Esc while the agent is working to pause, then type more instructions to continue.")
		ui.Info("anything else is sent to the agent as a task.")
	case "/model", "/models":
		switchModel(client)
	case "/login", "/key":
		key, ok := ui.ReadSecret(ui.Bold + "  OpenAdapter API key › " + ui.Reset)
		if !ok || key == "" {
			ui.Info("  unchanged.")
			return false
		}
		client.APIKey = key
		if err := saveKey(key); err != nil {
			ui.Errorf("key updated for this session, but couldn't save it: %v", err)
		} else {
			ui.Success("key updated and saved")
		}
	case "/resume":
		prev := session.LastTranscript(root, sess.Path())
		if prev == "" {
			ui.Info("  no previous session to resume.")
			return false
		}
		recs, err := session.ReadTranscript(prev)
		if err != nil {
			ui.Errorf("could not read previous session: %v", err)
			return false
		}
		n := ag.Resume(recs)
		if n == 0 {
			ui.Info("  previous session had nothing to resume.")
			return false
		}
		ui.Success("resumed %d messages from %s", n, filepath.Base(prev))
	case "/undo":
		if !sess.CanUndo() {
			ui.Info("  nothing to undo.")
			return
		}
		path, err := sess.Undo()
		if err != nil {
			ui.Errorf("undo failed: %v", err)
			return
		}
		ui.Success("reverted %s", path)
	case "/cost":
		fmt.Println(cost.Line())
	case "/tools":
		ui.Info("available tools:")
		for _, n := range reg.Names() {
			ui.Info("  • %s", n)
		}
	case "/getkey":
		ui.Info("ApiKey:")
		apiKey := resolveAPIKey()
		fmt.Println(apiKey, "apiKey")

	default:
		ui.Errorf("unknown command %q (try /help)", cmd)
	}
	return false
}

// switchModel shows a shortlist of coding models and switches the active one.
// The change applies to the next message (the client model is read per request).
func switchModel(client *llm.Client) {
	models := modelChoices(client.Model)
	q := fmt.Sprintf("select a coding model (current: %s) — pick a number, or type any model id", client.Model)
	choice := strings.TrimSpace(ui.AskUser(q, models))
	if choice == "" || choice == client.Model {
		ui.Info("  keeping %s", client.Model)
		return
	}
	client.Model = choice
	ui.Success("model switched to %s — applies to your next message", choice)
}

// modelChoices returns the curated coding-model shortlist. Override the list with
// the MINI_MODELS env var (comma-separated). The current model is always included.
func modelChoices(current string) []string {
	list := []string{
		"deepseek-v3",
		"glm-4.5",
		"qwen2.5-coder-32b-instruct",
		"kimi-k2-instruct",
		"deepseek-coder-v2",
	}
	if v := strings.TrimSpace(os.Getenv("MINI_MODELS")); v != "" {
		list = nil
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				list = append(list, p)
			}
		}
	}
	// Ensure the current model is selectable.
	found := false
	for _, m := range list {
		if m == current {
			found = true
			break
		}
	}
	if !found && current != "" {
		list = append([]string{current}, list...)
	}
	return list
}
