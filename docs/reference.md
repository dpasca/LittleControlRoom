# Little Control Room Reference

This page keeps the command, keybinding, and configuration details that are useful once you are already up and running.

## Detector Notes

Provider artifact and detector-footprint notes live in:

- [`codex_cli_footprint.md`](codex_cli_footprint.md)
- [`claude_code_footprint.md`](claude_code_footprint.md)
- [`tui_design_rules.md`](tui_design_rules.md)

## CLI Commands

- `lcroom tui` opens the interactive dashboard
- `lcroom scan` rescans artifacts and refreshes the local store
- `lcroom classify` scans and drains the latest-session AI classification queue
- `lcroom doctor` prints a diagnostic report from the current cached store
- `lcroom doctor --scan` rescans first, then prints the diagnostic report
- `lcroom screenshots` renders the curated docs screenshot set from a screenshot config
- `lcroom mockups` renders static high-level UI mockups without scanning projects or launching the TUI
- `lcroom boss` opens the chat-first high-level assistant UI directly, mostly useful for development and smoke checks
- `lcroom scope` shows the effective include and exclude scope for this run
- `lcroom serve` starts the optional read-only REST and WebSocket server

`lcroom classify` requires a configured AI backend. That can be Codex, OpenCode, Claude Code, MLX, Ollama, or an OpenAI API key. The TUI will open `/setup` automatically until you pick one.

The usual way to reach boss mode is from the classic TUI with `/boss`. It opens a minimal boss chat layer over the dashboard, gives each turn a compact plain-text app-state brief, and uses its own `boss_chat_backend` setting so live chat can use direct API inference through OpenAI API, MLX, or Ollama while project analysis keeps using Codex, OpenCode, Claude Code, MLX, Ollama, or another configured backend. Boss chat resumes the latest saved boss session on open; `/sessions` opens a saved-session picker, and `/sessions <session-id>` jumps directly. Boss Chat transcripts are Markdown text files under the app data directory, for example `~/.little-control-room/boss-sessions/`. Codex, OpenCode, and Claude Code work-session transcripts are called engineer sessions in Boss Chat, and the internal context lookup keeps Boss Chat recall separate from engineer transcript recall. If boss chat is not configured yet, `/boss` shows a setup prompt with a direct jump to the Boss chat card in `/setup`. Press `Esc`, submit `/boss off`, or run `/boss off` from the command palette after returning to the dashboard to hide it; in-flight Boss replies keep running in the background. Set `boss_chat_model` or `LCROOM_BOSS_MODEL` to override the default boss-mode chat model.

## Config File

- Preferred default path: `~/.little-control-room/config.toml`
- Override path: `--config /path/to/config.toml`
- Example file: [`config.example.toml`](config.example.toml)
- Supported format: TOML

Use `/setup` for AI roles, API keys, and MLX/Ollama endpoint/model overrides. The TUI `/settings` modal is split into sections for the remaining app configuration, including project scope, browser behavior, refresh timing, and advanced toggles. The Browser section exposes a simplified `Browser windows` field with plain-language choices such as `Only when needed`, `Always show`, and `Classic browser behavior`, while the config file still stores the raw Playwright policy keys below:

In `Only when needed`, newly launched embedded Codex and OpenCode sessions now route Playwright through an LCR-managed wrapper with a persistent browser profile. Codex gets a session-local `CODEX_HOME` overlay and OpenCode gets a session-local `XDG_CONFIG_HOME` overlay, both shadowing only the `playwright` skill so embedded sessions are guided toward the managed MCP path without changing the user's real global installs. On macOS, LCR backgrounds that managed browser and later reveals the same browser window for login or other human steps, so auth stays in the Playwright session the embedded assistant is actually driving. Existing embedded sessions still need to be reopened or reconnected before they pick up the new launch path, and Codex currently has the more complete browser-attention UX.

Working roadmap for this area: [`browser_automation_working_plan.md`](browser_automation_working_plan.md)

For managed-browser debugging outside the TUI, Little Control Room also exposes:

- `lcroom browser status --session-key <id>`
- `lcroom browser reveal --session-key <id>`

- `openai_api_key`
- `include_paths`
- `exclude_paths`
- `exclude_project_patterns`
- `codex_launch_preset`
- `playwright_management_mode`
- `playwright_default_browser_mode`
- `playwright_login_mode`
- `playwright_isolation_scope`
- `interval`
- `active-threshold`
- `stuck-threshold`

Minimal config example:

```toml
openai_api_key = "sk-your-openai-api-key"

include_paths = [
  "~/dev/repos",
]

exclude_paths = []
exclude_project_patterns = []
codex_launch_preset = "yolo"
playwright_management_mode = "managed"
playwright_default_browser_mode = "headless"
playwright_login_mode = "promote"
playwright_isolation_scope = "task"
```

Saved-from-TUI example:

```toml
openai_api_key = "sk-your-openai-api-key"

include_paths = [
  "~/dev/repos",
]

exclude_paths = []
exclude_project_patterns = [
  "client-*",
  "archive-*",
]
codex_launch_preset = "yolo"
playwright_management_mode = "managed"
playwright_default_browser_mode = "headless"
playwright_login_mode = "promote"
playwright_isolation_scope = "task"

interval = "60s"
active-threshold = "20m"
stuck-threshold = "4h"
```

## Screenshots

- `make screenshots` renders a curated set of fixed-size PNG terminal screenshots for docs.
- `make mockups` renders static high-level UI mockups to `/tmp/lcroom-mockups` by default.
- `make boss` opens the chat-first high-level assistant UI directly for development smoke checks.
- Default local config path: `./screenshots.local.toml`
- Override the screenshot config path with `lcroom screenshots --screenshot-config /path/to/screenshots.local.toml`
- Override the output directory with `lcroom screenshots --output-dir /tmp/lcroom-shots`
- Mockups also accept `--screenshot-config` and `--output-dir`, but do not require a config file.
- Committed example file: [`screenshots.example.toml`](screenshots.example.toml)

Screenshot config fields:

- `demo_data` (when `true`, render built-in sample data instead of your local project scan)
- `terminal_width`
- `terminal_height`
- `capture_scale` (browser device scale factor for higher-resolution PNGs; default `1.5`)
- `output_dir`
- `browser_path` (optional absolute path or command name for Chrome/Chromium/Brave/Edge)
- `project_filters`
- `selected_project`
- `live_codex_project`
- `live_runtime_project` (optional; defaults to `selected_project` and renders a focused runtime-pane screenshot with a screenshot-only running-session snapshot)

Minimal screenshot example:

```toml
demo_data = true
terminal_width = 112
terminal_height = 31
capture_scale = 1.5
output_dir = "screenshots"
# browser_path = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

selected_project = "LittleControlRoom"
live_codex_project = "LittleControlRoom"
live_runtime_project = "LittleControlRoom"
```

The generated set currently includes:

- `main-panel.png`
- `main-panel-live-runtime.png`
- `codex-embedded.png`
- `diff-view.png`
- `diff-view-image.png`
- `commit-preview.png`
- `todo-dialog.png`
- `setup.png`
- `settings-local-backends.png`

`project_filters`, `selected_project`, `live_codex_project`, and `live_runtime_project` match against the project name, the repo directory name, and simple acronyms such as `LCR` for `LittleControlRoom`.
Use `demo_data = true` when you want a reproducible sample set, or a local config file when you want screenshots from your own curated projects.

## TUI Keys

- `/` open the command palette
- `/boss` opens the chat-first boss layer, or prompts for setup if boss chat is not configured; `Esc` hides Boss Chat and returns to the classic TUI
- `B` opens the chat-first boss layer, with the same setup prompt as `/boss` when needed
- `↑/↓` move selection
- `Enter` open or resume the selected project's latest embedded provider
- `Esc` hide the visible embedded session pane
- `Alt+Up` hide the visible embedded session pane
- `Alt+Down` open the embedded session picker and history overlay
- `Alt+[` jump to the previous live embedded session
- `Alt+]` jump to the next live embedded session
- `PgUp/PgDn/Home/End` fast scrolling in long project lists
- `Tab` or `Shift+Tab` switch focus between list, detail, and runtime
- `f` open the temporary project-name filter dialog
- `o` toggle sort mode between `attention` and `recent activity`
- `p` pin toggle
- `q` quit
- While the runtime pane is focused, `Left` and `Right` choose the highlighted runtime action and `Enter` runs it

While the embedded Codex, Claude Code, or OpenCode pane is visible:

- `Enter` sends a prompt when idle and steers the active turn when the embedded session is busy
- `Alt+Enter` or `Ctrl+J` inserts a newline
- `Ctrl+V` attaches a clipboard image when available
- `Backspace` on an inline `[Image #n]` marker removes that attachment
- `Alt+L` cycles dense command, file, and tool transcript blocks through hidden output, preview, and full detail
- `Ctrl+C` interrupts the active turn when busy and closes the session when idle

While the diff screen is visible:

- The left pane groups `Staged` files first and `Unstaged` files below them
- `-` stages the selected file when it is unstaged, and unstages it when it already has staged changes
- `Up/Down` or `j/k` moves between files when the file list is focused
- `Enter`, `Right`, or `Tab` moves focus into the diff pane
- `Left` or `Tab` moves focus back to the file list
- `PgUp/PgDn/Home/End` pages or jumps within the focused pane
- `Alt+Up` returns to the commit preview when the diff was opened from there, otherwise to the main project list
- `Esc` returns to the commit preview when the diff was opened from there, otherwise closes the diff screen

## Slash Commands

The TUI command palette opens with `/` and supports autocomplete with `Tab`.

- `/help`
- `/refresh`
- `/sort attention`
- `/sort recent`
- `/view ai`
- `/view all`
- `/setup`
- `/settings`
- `/filter`
- `/filter clear`
- `/new-project`
- `/new-task`
- `/task-actions`
- `/open`
- `/run`
- `/start`
- `/run pnpm dev`
- `/restart`
- `/run-edit`
- `/runtime`
- `/stop`
- `/diff`
- `/codex`
- `/codex continue from the last breakpoint`
- `/codex-new sketch a plan for this repo`
- `/claude`
- `/claude continue from the last breakpoint`
- `/claude-new sketch a plan for this repo`
- `/opencode`
- `/opencode continue from the last breakpoint`
- `/opencode-new sketch a plan for this repo`
- `/commit`
- `/commit tighten git status parsing`
- `/push`
- `/pin`
- `/read`
- `/read all`
- `/unread`
- `/snooze [duration|off]`
- `/unsnooze`
- `/clear-snooze`
- `/sessions toggle`
- `/events off`
- `/focus detail`
- `/focus runtime`
- `/ignore`
- `/ignored`
- `/remove`
- `/quit`

## Common Flags

- `--config "~/.little-control-room/config.toml"`
- `--include-paths "~/dev/repos,~/work/client-repos"`
- `--exclude-paths "~/dev/repos/archive,~/dev/repos/tmp"`
- `--exclude-project-patterns "client-*,archive-*"`
- `--codex-launch-preset "yolo"`
- `--codex-home "~/.codex"`
- `--opencode-home "~/.local/share/opencode"`
- `--db "~/.little-control-room/little-control-room.sqlite"`
- `--interval 60s`
- `--active-threshold 20m`
- `--stuck-threshold 4h`

## Notes

- `Enter` on the selected project opens that project's latest embedded provider inside Little Control Room.
- `/open` opens the selected project's folder in the system browser.
- `/remove` asks for confirmation, then makes the selected item go away using the safest matching action: it opens scratch-task archive/delete actions, cleans up linked worktrees, removes missing folders from the dashboard, or hides a regular project's exact path from the list. `/delete` and `/forget` are aliases.
- `/ignore` hides the selected project's exact name inside Little Control Room, which is handy for Codex-generated worktrees or other old projects that share a stable folder name.
- `/snooze [duration|off]` snoozes the selected project for a period, and `/unsnooze` clears any active snooze.
- `f` opens a live project-name filter dialog for the whole dashboard; `/filter <text>` applies the same temporary filter from the command palette, and `/filter clear` removes it.
- `/ignored` opens a reversible picker of hidden project names and paths; press `Enter` there to restore one.
- `/run` starts the selected project's saved managed runtime. If no command is saved yet, Little Control Room opens a small dialog with an auto-suggested command when it can infer one from common files like `bin/dev`, `package.json`, `Makefile`, `justfile`, or a simple Go entrypoint.
- `/start` is an alias for `/run`.
- `/run <command>` saves that command as the selected project's default runtime command and starts it immediately.
- `/restart` restarts the selected project's managed runtime with the saved command, or with the active runtime command when one is already known.
- `/run-edit` opens the saved runtime command for editing without starting it.
- `/runtime` focuses the runtime pane for the selected project.
- `/stop` stops the selected project's managed runtime when one is running.
- `/codex` resumes the selected project's latest known Codex session when available, otherwise it starts a new one.
- `/codex-new` always starts a fresh Codex session.
- `/claude` resumes the selected project's latest known Claude Code session when available, otherwise it starts a new one.
- `/claude-new` always starts a fresh Claude Code session.
- `/opencode` resumes the selected project's latest known OpenCode session when available, otherwise it starts a new one.
- `/opencode-new` always starts a fresh OpenCode session.
- While an embedded Codex, Claude Code, or OpenCode pane is visible, local slash commands include `/new`, `/resume` (`/session` alias), `/reconnect`, `/model`, `/status`, `/compact`, and `/review`.
- `/model` changes the model and reasoning for the current embedded tool and carries that choice forward to future embedded sessions of the same tool, including after restarting LCR.
- `/resume` with no session ID opens a picker for saved sessions from the current project and provider; `/resume <session-id>` jumps straight to that session.
- `/reconnect` restarts the current embedded provider helper and reconnects to the same session when possible, which is useful after refreshing `codex login` or other provider auth outside Little Control Room.
- `/review` starts an embedded Codex review of uncommitted changes and streams the review-mode transcript into the pane.
- While boss chat is visible, `Alt+1` through `Alt+8` jump from the bottom attention list into the matching embedded engineer session. Local slash commands include `/new [prompt]`, `/sessions [session-id]`, `/session [session-id]`, `/resume [session-id]`, `/help`, and `/boss off`. `/sessions` opens a picker with `Up`/`Down`, `Enter`, and `Esc`; adding a session ID switches directly. Boss slash commands use the same Tab and Shift+Tab autocomplete behavior as embedded slash commands.
- Embedded Claude Code currently runs through Claude's headless CLI flow. It works for prompt/response turns and session resume, and `/model` now offers a curated Claude alias picker with saved reasoning preferences. Compact, in-pane approvals, and attachments are still MVP-level.
- The main list uses `RUN` for the saved or active managed runtime summary, and `!` inside `RUN` when Little Control Room detects a managed port conflict.
- The project detail pane keeps project metadata only, while the dedicated runtime pane shows runtime command, state, ports, URL, conflicts or errors, and the captured output tail.
- `codex_launch_preset` controls how Codex is launched. The default is `yolo`.
- CLI flags override config file values.
