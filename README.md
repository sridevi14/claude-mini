# mini-code

A tiny CLI coding agent in Go — a mini Claude Code. It reads your current
directory, takes natural-language tasks, and uses tools to complete them:
streaming its reasoning live, showing a diff and asking permission before any
write or command, and self-correcting out loud when a tool errors instead of
crashing.

It talks to an **OpenAI-compatible** chat completions API using OpenAI-style
tool/function calling.

## Install (Windows)

Open **Command Prompt (CMD)** and paste this one line:

```bat
curl -L -o "%TEMP%\claude-mini-install.cmd" https://raw.githubusercontent.com/sridevi14/claude-mini/main/install.cmd && "%TEMP%\claude-mini-install.cmd"
```

This downloads and runs [`install.cmd`](install.cmd), which:

1. downloads the latest `claude-mini.exe` from GitHub Releases into `%USERPROFILE%\bin`,
2. creates that folder if needed,
3. adds it to your user `PATH` permanently.

**Close CMD and open a new one**, then run it from any directory:

```bat
claude-mini
```

On first run it asks for your OpenAdapter API key (input hidden) and saves it to
`%AppData%\mini-code\credentials`, so you only enter it once. Use `/login` to
change it later.

To **update**, just run the same one-line command again — it overwrites the
binary with the latest release.

## Build from source

```sh
go build -o claude-mini .
./claude-mini                           # run inside the project you want to work on
```

- Base URL: `https://api.openadapter.in/v1`
- Model: `deepseek-v3`
- The API key is prompted on first run (or set `OPENADAPTER_API_KEY=sk-...` for
  scripted/CI use).
- Optional cost overrides (USD per 1M tokens): `MINI_PRICE_IN`, `MINI_PRICE_OUT`

## How it works

1. You type a task at the `›` prompt.
2. The model streams its reasoning (dim gray) and answer, then emits tool calls.
3. For read-only tools (`read_file`, `list_dir`, `search`) it just runs them.
4. For mutating tools (`write_file`, `edit_file`, `run_bash`, `run_server`) it
   shows a colored diff or the command and asks: `[y]es / [a]llow session / [N]o`.
5. If a tool errors, the error is fed back to the model so it self-corrects.
6. The loop repeats until the model stops calling tools.

## Tools

| Tool | Mutating | Purpose |
|------|----------|---------|
| `read_file` | no | read a text file |
| `list_dir` | no | list a directory (respects ignore rules) |
| `search` | no | regex search across files (respects ignore rules) |
| `write_file` | yes | create/overwrite a file (diff + approval) |
| `edit_file` | yes | exact-string replace edit (diff + approval) |
| `run_bash` | yes | run a shell command (approval) |
| `run_server` | yes | start a background process, logs to a file (approval) |

## REPL commands

- `/undo` — revert the last file write
- `/cost` — show token usage and estimated cost
- `/tools` — list available tools
- `/help` — show help
- `/exit` — quit (also Ctrl+D)

## Safety

- All file operations are confined to the working directory.
- `.git/` and `.mini_agent/` are always ignored; `.gitignore` and `.agentignore`
  are honored for listing/searching.
- Every write is recorded so it can be undone (`/undo`).
- Background servers started via `run_server` are killed when mini-code exits.
- The full transcript is logged to `.mini_agent/sessions/<timestamp>.jsonl`.

## Layout

```
main.go                     REPL + wiring + slash commands
internal/llm/               OpenAI-compatible client, SSE streaming, tool schema
internal/agent/             agent loop, system prompt, cost tracker
internal/tools/             tool implementations + registry
internal/ui/                ANSI rendering, streaming, colored diff, prompts
internal/ignore/            .gitignore / .agentignore matcher
internal/session/           transcript log + undo stack
```

## Notes / limitations

- The ignore matcher implements a practical subset of `.gitignore` (no negation
  with `!`).
- `run_bash`/`run_server` use `bash -c` on Unix/macOS and `powershell` on Windows.
- Cost is an estimate; set `MINI_PRICE_IN`/`MINI_PRICE_OUT` for accuracy.
