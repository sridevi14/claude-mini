# claude-mini

A tiny coding agent that lives in your terminal. Tell it what you want in plain
English — it reads your files, makes the changes (showing a diff first), runs
commands, and fixes its own mistakes. A pocket-sized Claude Code, written in Go.

Works with any OpenAI-compatible model: **OpenAI, OpenRouter, local Ollama,
OpenAdapter, or your own endpoint.**

```
  ◆ claude-mini  ·  your terminal coding assistant
  › add input validation to the signup handler
  ⠹ Cooking…  12s · ~1.2k tokens · esc to interrupt
  ✓ baked for 13s
```

## Install

**Windows** — paste into Command Prompt:

```bat
curl -L -o "%TEMP%\claude-mini-install.cmd" https://raw.githubusercontent.com/sridevi14/claude-mini/main/install.cmd && "%TEMP%\claude-mini-install.cmd"
```

**macOS / Linux**:

```sh
curl -fsSL https://raw.githubusercontent.com/sridevi14/claude-mini/main/install.sh | sh
```

Open a new terminal, `cd` into a project, and run `claude-mini`. The first run asks
for an API key (hidden, saved locally — you only enter it once).

Rather build it yourself? `go build -o claude-mini .`

## Using it

Just say what you want:

```
› explain what cache.go does
› add a /health endpoint that returns 200
› write a test for parseConfig and run it
```

It streams its thinking, shows a colored diff before touching a file, and asks
before running anything. Press **Esc** to pause mid-task, **@** to attach a file.

| command | does |
|---|---|
| `/provider` | switch AI service (OpenAI, OpenRouter, Ollama, …) |
| `/model` | change model, or type any model name |
| `/login` | set your API key |
| `/config` | show your current setup |
| `/undo` | undo the last file change |
| `/help` | everything else |

## Pick your model

Run `/provider` and choose one — or point it anywhere with env vars:

```sh
# Local Ollama — free, no key needed
CLAUDE_MINI_BASE_URL=http://localhost:11434/v1 CLAUDE_MINI_MODEL=qwen3:latest claude-mini

# OpenAI
CLAUDE_MINI_BASE_URL=https://api.openai.com/v1 CLAUDE_MINI_API_KEY=sk-... CLAUDE_MINI_MODEL=gpt-4o claude-mini
```

Your choice is saved per provider, so next time it just remembers.

## Under the hood

A small, dependency-light Go program — a streaming OpenAI-compatible client, a
tool loop, a diff-and-approve gate on every change, and an undo stack. Everything
stays inside your working directory, and `.gitignore` / `.agentignore` are
respected.

```
main.go          REPL + commands
internal/agent   the agent loop + prompt
internal/llm     streaming client
internal/tools   read · write · edit · search · run
internal/ui      rendering, diffs, the live status line
```

Made for fun with [Claude Code](https://claude.com/claude-code).
