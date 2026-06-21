# mini-code

A tiny CLI coding agent in Go — a mini Claude Code. It reads your current
directory, takes natural-language tasks, and uses tools to complete them:
streaming its reasoning live, showing a diff and asking permission before any
write or command, and self-correcting out loud when a tool errors instead of
crashing.

It talks to an **OpenAI-compatible** chat completions API using OpenAI-style
tool/function calling.

## Install

After installing, open a **new** terminal and run `claude-mini` from any directory.
On first run it asks for your OpenAdapter API key (input hidden) and saves it to
your user config dir, so you only enter it once. Use `/login` to change it later.
Re-run the installer any time to update to the latest release.

### Windows (CMD)

Paste this one line into **Command Prompt**:

```bat
curl -L -o "%TEMP%\claude-mini-install.cmd" https://raw.githubusercontent.com/sridevi14/claude-mini/main/install.cmd && "%TEMP%\claude-mini-install.cmd"
```

[`install.cmd`](install.cmd) downloads the right `claude-mini-windows-<arch>.exe`
into `%USERPROFILE%\bin` (as `claude-mini.exe`), creates that folder if needed,
and adds it to your user `PATH`. Then **close CMD, open a new one**, and run
`claude-mini`.

### macOS / Linux

Paste this one line into your terminal:

```sh
curl -fsSL https://raw.githubusercontent.com/sridevi14/claude-mini/main/install.sh | sh
```

[`install.sh`](install.sh) detects your OS/arch, downloads the matching
`claude-mini-<os>-<arch>` binary into `~/.local/bin`, marks it executable, and
adds that folder to your `PATH` (via `~/.profile`/`~/.bashrc`/`~/.zshrc`) if it
isn't already. Open a new terminal (or `source ~/.profile`) and run `claude-mini`.

> Override the install location with `CLAUDE_MINI_INSTALL_DIR`, e.g.
> `curl -fsSL …/install.sh | CLAUDE_MINI_INSTALL_DIR=/usr/local/bin sh`.

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

## Releasing (maintainers)

Binaries are pure-Go (`CGO_ENABLED=0`), so one machine cross-compiles every
platform into `./dist`, with the exact names the installers expect:

```
claude-mini-linux-amd64     claude-mini-darwin-amd64    claude-mini-windows-amd64.exe
claude-mini-linux-arm64     claude-mini-darwin-arm64    claude-mini-windows-arm64.exe
```

### Automated (recommended)

A GitHub Action ([`.github/workflows/release.yml`](.github/workflows/release.yml))
builds all six binaries and publishes them as a Release whenever you push a
**version tag**:

```sh
git tag v1.0.0
git push origin v1.0.0
```

It runs `build.sh` on a single Linux runner and uploads `dist/*`. Day-to-day
pushes to `main` don't trigger a release — only tags do. The installers fetch
from `releases/latest/download/<asset>`, so a new tag instantly becomes what
users get.

### Manual (fallback)

Build locally and upload every file in `./dist` to a Release yourself:

```sh
sh build.sh                                          # macOS / Linux / Git Bash
```
```powershell
powershell -ExecutionPolicy Bypass -File build.ps1   # Windows
```

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
