package ui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	prompt "github.com/c-bata/go-prompt"
	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

// ANSI styles.
const (
	Reset   = "\033[0m"
	Dim     = "\033[2m"
	Bold    = "\033[1m"
	Italic  = "\033[3m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	Gray    = "\033[90m"
)

var stdin = bufio.NewReader(os.Stdin)

// Banner prints the startup header.
func Banner(model, root string) {
	fmt.Println()
	fmt.Println(Bold + Cyan + "  ◆ mini-code" + Reset + Gray + "  — a tiny coding agent" + Reset)
	fmt.Println(Gray + "  model " + Reset + model + Gray + "   dir " + Reset + root)
	fmt.Println(Gray + "  type a task, or /help for commands" + Reset)
	fmt.Println(Gray + "  shortcuts " + Reset + "esc " + Gray + "pause the agent   " +
		Reset + "@ " + Gray + "attach a file   " +
		Reset + "/model " + Gray + "switch model   " +
		Reset + "/resume " + Gray + "reload session" + Reset)
	fmt.Println(Gray + strings.Repeat("─", 60) + Reset)
}

// ReadLine prints prompt and reads one line of input.
func ReadLine(prompt string) (string, bool) {
	fmt.Print(prompt)
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimRight(line, "\r\n"), true
}

// ReadSecret prompts for sensitive input (e.g. an API key) and reads it without
// echoing the characters to the terminal. On a non-TTY stdin (pipes/CI) it falls
// back to a normal visible read so scripted use still works. The bool is false on
// EOF/error.
func ReadSecret(promptText string) (string, bool) {
	fmt.Print(promptText)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		if b, err := term.ReadPassword(fd); err == nil {
			fmt.Println() // ReadPassword swallows the newline; restore it
			return strings.TrimSpace(string(b)), true
		}
		// Hidden read failed on this terminal — fall through to a visible read so
		// the user can still enter the key (rather than being stuck).
	}
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// Interactive reports whether stdin is a real terminal (so rich input is usable).
func Interactive() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// ReadTask reads one task line. On a capable terminal it offers a live @file
// completion dropdown (type @, then letters to filter; arrow keys to navigate;
// Tab/Enter to accept). When stdin isn't a TTY (pipes, CI), when MINI_SIMPLE_INPUT
// is set, or if the rich-input library can't drive this terminal, it falls back
// to plain line input — @mentions still resolve on submit, just without the live
// dropdown. complete(token) returns candidate paths for the @token being typed
// (directories carry a trailing "/").
func ReadTask(complete func(token string) []string) (string, bool) {
	if !Interactive() || simpleInput() {
		return readTaskPlain()
	}
	line, ok := readTaskRich(complete)
	if !ok {
		// The rich input layer couldn't run in this terminal — degrade gracefully
		// for this and every later read instead of leaving the user stuck.
		forceSimpleInput()
		return readTaskPlain()
	}
	return line, true
}

func readTaskPlain() (string, bool) {
	return ReadLine("\n" + Bold + Cyan + "› " + Reset)
}

// readTaskRich runs the go-prompt dropdown. go-prompt can panic on terminals it
// doesn't understand (some Windows consoles, ConPTY, restricted shells); we
// recover so the caller can fall back to plain input rather than crash. ok is
// false when the rich layer failed.
func readTaskRich(complete func(token string) []string) (line string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	completer := func(d prompt.Document) []prompt.Suggest {
		word := d.GetWordBeforeCursor()
		if !strings.HasPrefix(word, "@") {
			return nil
		}
		matches := complete(word[1:])
		sug := make([]prompt.Suggest, 0, len(matches))
		for _, m := range matches {
			desc := "file"
			if strings.HasSuffix(m, "/") {
				desc = "dir"
			}
			sug = append(sug, prompt.Suggest{Text: "@" + m, Description: desc})
		}
		return sug
	}
	fmt.Println()
	line = prompt.Input("› ", completer,
		prompt.OptionPrefixTextColor(prompt.Cyan),
		prompt.OptionCompletionWordSeparator(" "),
	)
	return line, true
}

// simpleInputForced is set once the rich input layer has failed, so we don't keep
// retrying it every prompt.
var simpleInputForced bool

func forceSimpleInput() {
	if !simpleInputForced {
		simpleInputForced = true
		Info("  (switched to simple input — @ still works on submit; set MINI_SIMPLE_INPUT=1 to keep it)")
	}
}

// simpleInput reports whether the plain line reader should be used instead of the
// rich dropdown — either forced by env or after a prior rich-input failure.
func simpleInput() bool {
	if simpleInputForced {
		return true
	}
	v := strings.TrimSpace(os.Getenv("MINI_SIMPLE_INPUT"))
	return v != "" && v != "0" && strings.ToLower(v) != "false"
}

// Info prints a dim informational line.
func Info(format string, a ...any) {
	fmt.Println(Gray + fmt.Sprintf(format, a...) + Reset)
}

// Errorf prints an error/warning line in yellow (used for self-correction).
func Errorf(format string, a ...any) {
	fmt.Println(Yellow + "  ⚠ " + fmt.Sprintf(format, a...) + Reset)
}

// Success prints a green confirmation line.
func Success(format string, a ...any) {
	fmt.Println(Green + "  ✓ " + fmt.Sprintf(format, a...) + Reset)
}

// ToolHeader announces a tool invocation.
func ToolHeader(name, summary string) {
	fmt.Printf("\n%s  ⚙ %s%s %s%s%s\n", Blue+Bold, name, Reset, Gray, summary, Reset)
}

// ToolResult prints a (possibly truncated) tool result preview.
func ToolResult(text string) {
	const max = 1200
	t := text
	truncated := false
	if len(t) > max {
		t = t[:max]
		truncated = true
	}
	for _, ln := range strings.Split(strings.TrimRight(t, "\n"), "\n") {
		fmt.Println(Gray + "  │ " + Reset + ln)
	}
	if truncated {
		fmt.Println(Gray + "  │ … (truncated)" + Reset)
	}
}

// Perm is the outcome of a permission prompt.
type Perm int

const (
	PermNo Perm = iota
	PermYes
	PermAlways
)

// AskPermission shows the action and asks the user to approve it. preview may
// contain a colored diff or the command to run. scopeLabel names what "allow
// session" will cover (e.g. "npm commands" or a file path) so approval is
// understood to be scoped, not a blanket grant for the whole tool.
func AskPermission(action, preview, scopeLabel string) Perm {
	fmt.Println()
	fmt.Println(Yellow + "  ┌─ permission required ─────────────────────────" + Reset)
	fmt.Println(Yellow + "  │ " + Reset + action)
	if preview != "" {
		fmt.Println(Yellow + "  │" + Reset)
		for _, ln := range strings.Split(strings.TrimRight(preview, "\n"), "\n") {
			fmt.Println(Yellow + "  │ " + Reset + ln)
		}
	}
	fmt.Println(Yellow + "  └────────────────────────────────────────────────" + Reset)
	allow := "[a]llow session"
	if scopeLabel != "" {
		allow = fmt.Sprintf("[a]llow %s this session", scopeLabel)
	}
	for {
		ans, ok := ReadLine(Bold + "  approve? [y]es / " + allow + " / [N]o › " + Reset)
		if !ok {
			return PermNo
		}
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "y", "yes":
			return PermYes
		case "a", "all", "allow":
			return PermAlways
		case "", "n", "no":
			return PermNo
		}
	}
}

// AskUser renders a clarifying question (with optional numbered options) and
// returns the user's answer — a chosen option's text, or whatever they type.
func AskUser(question string, options []string) string {
	fmt.Println()
	fmt.Println(Cyan + "  ┌─ the agent needs your input ──────────────────" + Reset)
	fmt.Println(Cyan + "  │ " + Reset + Bold + question + Reset)
	if len(options) > 0 {
		fmt.Println(Cyan + "  │" + Reset)
		for i, opt := range options {
			fmt.Printf("%s  │ %s %s%d.%s %s\n", Cyan, Reset, Bold, i+1, Reset, opt)
		}
		fmt.Println(Cyan + "  │" + Reset)
		fmt.Println(Cyan + "  │ " + Reset + Gray + "enter a number, or type your own answer" + Reset)
	}
	fmt.Println(Cyan + "  └────────────────────────────────────────────────" + Reset)
	for {
		ans, ok := ReadLine(Bold + "  › " + Reset)
		if !ok {
			return "(no answer; user ended input)"
		}
		ans = strings.TrimSpace(ans)
		if ans == "" {
			continue
		}
		if n, err := strconv.Atoi(ans); err == nil && n >= 1 && n <= len(options) {
			return options[n-1]
		}
		return ans
	}
}

// Streamer renders streamed reasoning/content tokens with section headers.
type Streamer struct {
	mode int // 0 none, 1 reasoning, 2 content
}

const (
	modeNone      = 0
	modeReasoning = 1
	modeContent   = 2
)

// Reasoning prints a chain-of-thought token in dim gray.
func (s *Streamer) Reasoning(tok string) {
	if s.mode != modeReasoning {
		s.closeMode()
		fmt.Print("\n" + Gray + "  · thinking ·" + Reset + "\n" + Gray)
		s.mode = modeReasoning
	}
	fmt.Print(tok)
}

// Content prints a visible answer token.
func (s *Streamer) Content(tok string) {
	if s.mode != modeContent {
		s.closeMode()
		fmt.Print("\n" + Bold + "  ●" + Reset + " ")
		s.mode = modeContent
	}
	fmt.Print(tok)
}

func (s *Streamer) closeMode() {
	if s.mode == modeReasoning {
		fmt.Print(Reset)
	}
}

// End finalizes the current stream block.
func (s *Streamer) End() {
	s.closeMode()
	if s.mode != modeNone {
		fmt.Println()
	}
	s.mode = modeNone
}
