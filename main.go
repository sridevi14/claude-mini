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

	firstRun := isFirstRun()
	cfg := resolveSettings()
	// A key is mandatory for scripted/non-interactive use; in a real terminal we let
	// the user in anyway so they can pick a provider (e.g. free local Ollama) or add
	// a key with /login from inside the app.
	if cfg.APIKey == "" && !ui.Interactive() {
		fmt.Fprintln(os.Stderr, ui.Red+"No API key provided for "+cfg.BaseURL+"."+ui.Reset)
		fmt.Fprintln(os.Stderr, ui.Gray+"  set "+envAPIKey+"=… (with "+envBaseURL+" / "+envModel+"), "+
			"or run in a terminal to set one up interactively."+ui.Reset)
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
	client := llm.New(cfg.BaseURL, cfg.APIKey, cfg.Model)
	reg := tools.New(root, sess, ign)
	defer reg.Cleanup()

	cost := agent.NewCost()
	ag := agent.New(client, reg, sess, cost, root)
	mentions := mention.New(root, ign)

	// On a real terminal, let Esc pause the agent mid-stream (see escWatcher).
	if ui.Interactive() {
		ag.SetInterruptWatcher(escWatcher)
	}

	printWelcome(client, root, firstRun)
	if cfg.APIKey == "" {
		ui.Errorf("No API key yet — run /provider (pick Ollama for free, no key) or /login to add one.")
	}

	ctx := context.Background()
	hintedEmpty := false
	for {
		input, ok := ui.ReadTask(mentions.Matches)
		if !ok {
			break // EOF (Ctrl+D)
		}
		input = strings.TrimSpace(input)
		if input == "" {
			// Nudge first-timers who press Enter at the blank prompt.
			if !hintedEmpty {
				ui.Info("  Type a task in plain English (e.g. \"create hello.txt\"), or /help.")
				hintedEmpty = true
			}
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

// printWelcome renders the startup screen: a friendly header, the current setup in
// plain words, a few example tasks (so a newcomer isn't facing a blank prompt), and
// the handful of commands they'll actually use. The very first run adds a one-line,
// jargon-free explanation of what the tool is.
func printWelcome(client *llm.Client, root string, firstRun bool) {
	b, c, g, r, grn := ui.Bold, ui.Cyan, ui.Gray, ui.Reset, ui.Green

	fmt.Println()
	fmt.Println("  " + b + c + "◆ claude-mini" + r + g + "  ·  your terminal coding assistant" + r)

	if firstRun {
		fmt.Println()
		fmt.Println("  " + b + "Welcome! 👋" + r)
		fmt.Println(g + "  Tell it what you want in plain English and it reads and edits files in" + r)
		fmt.Println(g + "  this folder to do it — like a teammate who can also run commands." + r)
	}

	fmt.Println()
	fmt.Println("  " + grn + "✓ Ready" + r + g + "  using " + r + client.Model + g + "  (" + r + providerLabel(client.BaseURL) + g + ")" + r)
	fmt.Println(g + "  folder " + r + root)

	fmt.Println()
	fmt.Println("  " + b + "Try typing" + r + g + " (then press Enter):" + r)
	for _, ex := range []string{
		"create a file hello.txt that says hi",
		"explain what main.go does",
		"add a test for the add function and run it",
	} {
		fmt.Println(g + "    • " + r + ex)
	}

	fmt.Println()
	fmt.Printf("  %sCommands%s   %s/help%s all  ·  %s/provider%s change AI  ·  %s/model%s change model  ·  %s/exit%s quit\n",
		b, r, c, r, c, r, c, r, c, r)
	fmt.Println(g + "  Shortcuts  " + r + "Esc" + g + " pause   " + r + "@" + g + " attach a file   " + r + "/undo" + g + " undo last change" + r)
	fmt.Println(g + "  " + strings.Repeat("─", 60) + r)
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
		ui.Info("Just type what you want in plain English — e.g. \"add error handling to main.go\".")
		ui.Info("")
		ui.Info("Commands:")
		ui.Info("  /provider  switch AI service (OpenAI, OpenRouter, Ollama, other…)")
		ui.Info("  /model     change the model — or type any model name")
		ui.Info("  /login     add or change your API key")
		ui.Info("  /config    show your current setup")
		ui.Info("  /undo      undo the last file change")
		ui.Info("  /resume    continue your previous session")
		ui.Info("  /cost      show token usage and estimated cost")
		ui.Info("  /tools     list what the agent can do")
		ui.Info("  /exit      quit  (or press Ctrl+D)")
		ui.Info("")
		ui.Info("Shortcuts:  Esc pause the agent   ·   @ attach a file to your message")
	case "/model", "/models":
		switchModel(client)
	case "/provider", "/providers":
		switchProvider(client)
	case "/config":
		showConfig(client)
	case "/login", "/key":
		key, ok := ui.ReadSecret(ui.Bold + "  " + providerLabel(client.BaseURL) + " API key › " + ui.Reset)
		if !ok || key == "" {
			ui.Info("  unchanged.")
			return false
		}
		client.APIKey = key
		if err := saveKeyFor(client.BaseURL, key); err != nil {
			ui.Errorf("key updated for now, but couldn't save it: %v", err)
		} else {
			ui.Success("Key saved for %s.", providerLabel(client.BaseURL))
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
	default:
		ui.Errorf("unknown command %q (try /help)", cmd)
	}
	return false
}

// switchProvider switches the active endpoint. The user picks a known preset
// (openai, openrouter, ollama, …) or "custom" to enter any OpenAI-compatible base
// URL. It then ensures a key exists for that provider (reusing a saved one or
// prompting), suggests a default model, and persists the choice.
func switchProvider(client *llm.Client) {
	presets := providerPresets()
	var labels []string
	for _, p := range presets {
		labels = append(labels, fmt.Sprintf("%-12s %s", p.Label, p.Blurb))
	}
	labels = append(labels, fmt.Sprintf("%-12s %s", "Other…", "paste any other AI service URL"))

	q := fmt.Sprintf("Which AI service do you want to use?  (now: %s)", providerLabel(client.BaseURL))
	choice := strings.TrimSpace(ui.AskUser(q, labels))
	base, ok := providerBaseFromChoice(choice, presets)
	if !ok || base == "" {
		ui.Info("  provider unchanged.")
		return
	}
	base = normalizeBase(base)
	client.BaseURL = base

	// Reuse a saved/env key for this provider, fall back to its default (Ollama),
	// otherwise prompt for one.
	key := firstNonEmpty(os.Getenv(envAPIKey), savedKeyFor(base))
	if key == "" {
		if p, found := providerFor(base); found {
			key = p.DefaultKey
		}
	}
	if key == "" {
		key = promptForKey(base)
	}
	if key != "" {
		client.APIKey = key
	} else {
		ui.Errorf("no API key set for %s — use /login before sending a task.", base)
	}

	// Land on a model that exists for this provider.
	if p, found := providerFor(base); found && len(p.Models) > 0 {
		client.Model = p.Models[0]
	}
	if err := saveActive(base, client.Model); err != nil {
		ui.Errorf("switched for now, but couldn't save it: %v", err)
	}
	ui.Success("Now using %s  ·  model %s", providerLabel(base), client.Model)
	ui.Info("  Type /model to change the model, or just start typing a task.")
}

// providerBaseFromChoice maps an AskUser result to a base URL: a preset name/label,
// a typed URL, or the "custom" option (which prompts for the URL).
func providerBaseFromChoice(choice string, presets []provider) (string, bool) {
	if choice == "" {
		return "", false
	}
	for _, p := range presets {
		// Accept either the friendly label ("OpenAI …") or the short name ("openai").
		if strings.EqualFold(choice, p.Name) || strings.EqualFold(choice, p.Label) ||
			strings.HasPrefix(choice, p.Label+" ") || strings.HasPrefix(choice, p.Name+" ") {
			return p.BaseURL, true
		}
	}
	lc := strings.ToLower(choice)
	if strings.HasPrefix(lc, "other") || strings.HasPrefix(lc, "custom") {
		url, ok := ui.ReadLine(ui.Bold + "  Paste the service URL (e.g. https://api.example.com/v1): " + ui.Reset)
		url = strings.TrimSpace(url)
		if !ok || url == "" {
			return "", false
		}
		return url, true
	}
	if strings.Contains(choice, "://") { // user typed a base URL directly
		return choice, true
	}
	return "", false
}

// switchModel shows a provider-aware shortlist and switches the active model. The
// user may also type any model id. The change is persisted and applies next turn.
func switchModel(client *llm.Client) {
	models := modelChoices(client.BaseURL, client.Model)
	q := fmt.Sprintf("Pick a model, or type any model name  (now: %s)", client.Model)
	choice := strings.TrimSpace(ui.AskUser(q, models))
	if choice == "" || choice == client.Model {
		ui.Info("  Keeping %s.", client.Model)
		return
	}
	client.Model = choice
	if err := saveModelPref(choice); err != nil {
		ui.Success("Now using %s (this session)", choice)
	} else {
		ui.Success("Now using %s", choice)
	}
}

// modelChoices returns the shortlist for /model: the CLAUDE_MINI_MODELS override if
// set, else the active provider's suggested models, else just the current model.
// The current model is always selectable.
func modelChoices(base, current string) []string {
	var list []string
	if v := strings.TrimSpace(os.Getenv(envModels)); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				list = append(list, p)
			}
		}
	} else if p, ok := providerFor(base); ok {
		list = append(list, p.Models...)
	}
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

// showConfig prints the active provider, base URL, model and a masked key.
func showConfig(client *llm.Client) {
	ui.Info("Your current setup:")
	ui.Info("  AI service  %s", providerLabel(client.BaseURL))
	ui.Info("  model       %s", client.Model)
	ui.Info("  API key     %s", maskKey(client.APIKey))
	ui.Info("  endpoint    %s", client.BaseURL)
	if p, err := configPath(); err == nil {
		ui.Info("  saved in    %s", p)
	}
}
