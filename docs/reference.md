# Little Control Room Reference

This page keeps the command, keybinding, and configuration details that are useful once you are already up and running.

## Detector Notes

Provider artifact and detector-footprint notes live in:

- [`codex_cli_footprint.md`](codex_cli_footprint.md)
- [`claude_code_footprint.md`](claude_code_footprint.md)
- [`tui_design_rules.md`](tui_design_rules.md)

## CLI Commands

- `lcroom tui` opens the interactive dashboard and normally follows the saved mobile settings; `--listen <host:port>` is a one-run address and enablement override, with pairing required for non-loopback listeners
- `lcroom scan` rescans artifacts and refreshes the local store
- `lcroom classify` scans and drains the latest-session AI classification queue
- `lcroom doctor` prints a diagnostic report from the current cached store
- `lcroom doctor --scan` rescans first, then prints the diagnostic report
- `lcroom screenshots` renders the curated docs screenshot set from a screenshot config
- `lcroom mockups` renders static high-level UI mockups without scanning projects or launching the TUI
- `lcroom scope` shows the effective include and exclude scope for this run
- `lcroom serve` explicitly starts the standalone read-only REST and WebSocket server even when TUI mobile auto-start is disabled; it uses the saved address unless `--listen <host:port>` overrides it

For LAN mobile access, use the Mobile card in `/setup` or the Mobile section in `/settings`, choose `Phones on this LAN`, and restart LCR. This friendly mode derives the technical `0.0.0.0:<port>` listener; `This computer only` derives `127.0.0.1:<port>`, while `Custom address` preserves direct `host:port` control. The top-right `/mobile` badge adds `LAN`, `RESTART`, `SETUP`, `OFF`, or `ERR` when space permits; `RESTART` means the saved listener setup differs from the running listener. `/mobile` opens a status panel with the active listener, detected private IPv4 addresses, phone-ready URL, pairing code, phone-control state, and saved next-launch setup. Press Enter to open the existing Mobile setup drilldown or `c` to copy a reachable phone URL. The TUI-hosted mobile dashboard reports visible live engineer channels and links directly to their Markdown-rendered transcripts; transcript mode can show conversation alone or all command, tool, plan, reasoning, and status activity. `mobile_input_enabled` is false by default. A live channel still shows a disabled composer with directions to Mobile settings; enabling the setting unlocks that composer so it can send, steer, or queue through the shared provider-neutral session manager. Recorded sessions and higher-authority controls remain read-only. A successful pairing stores a 30-day HTTP-only browser cookie signed by `mobile-auth.key` beside the active database. Loopback listeners do not require pairing. Pairing does not add TLS, so direct HTTP exposure should remain on a trusted LAN.

`lcroom classify` requires a configured AI backend. That can be Codex, OpenCode, Claude Code, MLX, Ollama, or an OpenAI API key. The TUI will open `/setup` automatically until you pick one.

Official GitHub release builds perform a throttled stable-release check when the TUI starts. The check runs at most once every 24 hours and caches GitHub's ETag and latest release metadata under `~/.little-control-room/updates/`. When an update exists, the top bar shows bright `/update <version>` text. `/update` requires an explicit `Update & restart` confirmation before downloading anything. Installation verifies the GitHub SHA-256 digests and `checksums.txt`, verifies Apple Developer signatures on macOS, stages both `lcroom` and `lcagent`, replaces them with rollback protection, journals active embedded turns, releases the database runtime lease, and restarts the same command. Source builds and non-GitHub distributions skip automatic checks. `LCR_DISABLE_UPDATE_CHECKS=true` disables automatic checks while preserving explicit `/update` checks.

Open Chat from the main TUI with backtick or `/chat`, including from an embedded provider pane. It appears as a centered overlay over the dashboard and receives a compact app-state brief, so the same conversation can explain LCR, inspect projects and tasks, propose confirmable controls, delegate work, and report completion without replacing the dashboard with a second project view. Questions that depend on repository files use a bounded LCAgent Repository Scout: workspace-only reads, no commands or edits, project `AGENTS.md` instructions, mechanically recorded read ranges, and a durable JSONL trace. Scout uses an explicit LCAgent route first when one is configured; otherwise it inherits the existing Chat utility model, then Chat main, then compatible project-analysis inference. Chat answers append the route, fallback, evidence, and trace receipt automatically. If no route can inspect the repository, Chat remains available but must report that inspection was unavailable instead of treating missing evidence as proof that a plan, document, or implementation does not exist. Work requests for an existing loaded project default to one confirmed tracked launch: LCR creates a project TODO, prepares a dedicated worktree, and starts a fresh engineer there. Press `q` in that confirmation to add the TODO without starting work. A brand-new repository request uses a distinct confirmation that names the exact target path and discloses directory creation, Git initialization, project registration, TODO/worktree creation, and engineer launch. The target must not already exist; LCR creates and registers it before continuing the tracked launch. LCR records both the starting state and the final launch or staged partial-failure result in the Chat transcript, never falls back from failed worktree preparation into the root checkout, and does not treat an idle root engineer turn as proof that its task is finished. `Esc` or backtick hides the overlay while in-flight replies continue; `/new [prompt]` and `Ctrl+L` start a fresh Chat session. Transcripts are Markdown files under `~/.little-control-room/help-chat-sessions/`, and recall also searches legacy `boss-sessions/` files. The existing `boss_chat_backend`, `boss_helm_model`, `boss_utility_model`, `boss_chat_model`, and `LCROOM_BOSS_MODEL` names remain compatibility settings.

## Config File

- Preferred default path: `~/.little-control-room/config.toml`
- Override path: `--config /path/to/config.toml`
- Example file: [`config.example.toml`](config.example.toml)
- Supported format: TOML

Use `/setup` for the Getting Started settings: project-report AI, Chat, optional LCAgent worker/Scout overrides, mobile access, and the shared provider keys or local endpoint fields those choices need. Repository Scout does not require separate LCAgent credentials when Chat already has a compatible API or local inference route. The full `/settings` modal keeps that first-run section and adds AI/model details, MLX/Ollama endpoint/model overrides, project scope, experimental LCAgent launch settings, mobile startup/address controls, browser behavior, refresh timing, and advanced toggles. Project discovery paths live in Project Scope rather than quick setup. Mobile changes apply after restart. The Browser section exposes a simplified `Browser windows` field with plain-language choices such as `Only when needed`, `Always show`, and `Classic browser behavior`, while the config file still stores the raw Playwright policy keys below:

In `Only when needed`, newly launched embedded Codex and OpenCode sessions now route Playwright through an LCR-managed wrapper with a persistent browser profile. Codex gets a session-local `CODEX_HOME` overlay and OpenCode gets a session-local `XDG_CONFIG_HOME` overlay, both shadowing only the `playwright` skill so embedded sessions are guided toward the managed MCP path without changing the user's real global installs. On macOS, LCR backgrounds that managed browser and later reveals the same browser window for login or other human steps, so auth stays in the Playwright session the embedded assistant is actually driving. Existing embedded sessions still need to be reopened or reconnected before they pick up the new launch path, and Codex currently has the more complete browser-attention UX.

Working roadmap for this area: [`browser_automation_working_plan.md`](browser_automation_working_plan.md)

For managed-browser debugging outside the TUI, Little Control Room also exposes:

- `lcroom browser status --session-key <id>`
- `lcroom browser reveal --session-key <id>`

- `openai_api_key`
- `openrouter_api_key`
- `deepseek_api_key`
- `moonshot_api_key`
- `project_reasoning_effort`
- `include_paths`
- `exclude_paths`
- `exclude_project_patterns`
- `codex_launch_preset`
- `embedded_lcagent_model`
- `embedded_lcagent_reasoning_effort`
- `lcagent_path`
- `lcagent_env_file`
- `lcagent_route_preset`
- `lcagent_provider`
- `lcagent_auto`
- `lcagent_tool_profile`
- `lcagent_context_profile`
- `lcagent_request_timeout`
- `playwright_management_mode`
- `playwright_default_browser_mode`
- `playwright_login_mode`
- `playwright_isolation_scope`
- `mobile_enabled`
- `mobile_input_enabled`
- `mobile_listen_address`
- `interval`
- `active-threshold`
- `stuck-threshold`

Minimal config example:

```toml
openai_api_key = "sk-your-openai-api-key"
# Optional direct LCAgent provider keys. OpenAI-backed LCAgent reuses
# openai_api_key; the env file below is only an advanced fallback.
# openrouter_api_key = "sk-or-your-openrouter-key"
# deepseek_api_key = "sk-your-deepseek-key"
# moonshot_api_key = "sk-your-moonshot-key"

include_paths = [
  "~/dev/repos",
]

exclude_paths = []
exclude_project_patterns = []
codex_launch_preset = "yolo"
# LCAgent is experimental. Leave lcagent_path blank to use the bundled binary,
# PATH lookup, or source-checkout go run fallback. Saved provider keys are used before
# process environment variables; lcagent_env_file is an advanced fallback.
# embedded_lcagent_model = "deepseek/deepseek-v4-pro"
# lcagent_env_file = "~/path/to/openrouter.env"
# lcagent_route_preset = "balanced" # optional: balanced, quality, mimo-2.5-pro-low/high/max, cheap-scout
# lcagent_provider = "openrouter"
# lcagent_auto = "low"
# lcagent_tool_profile = "balanced"
# lcagent_context_profile = "balanced" # known model windows adapt packing budgets; unknown models fall back to balanced ~200k chars / large ~600k
# lcagent_request_timeout = "60m"
playwright_management_mode = "managed"
playwright_default_browser_mode = "headless"
playwright_login_mode = "promote"
playwright_isolation_scope = "task"
```

Embedded Codex keeps the filesystem reach and approval behavior selected by
`codex_launch_preset`; the default remains `yolo`. Little Control Room adds a
narrow destructive-command seatbelt to every LCR-managed embedded Codex
session: Codex exec-policy rules forbid direct `rm` invocations, and an LCR
`rm` shim rejects recursive forced deletion when wrappers or child processes
resolve `rm` through that session's `PATH`. This does not confine reads or
ordinary writes to the current project.

The guard is deliberately narrower than a security sandbox. Other deletion
mechanisms, an absolute executable hidden inside a script, or deliberate PATH
replacement can bypass it. Keep normal backups and filesystem protections in
place; use targeted file/patch tools for agent edits and run intentional bulk
cleanup manually outside the embedded agent session.

The architecture, threat model, maintenance invariants, and provider-extension
plan are recorded in
[`destructive_command_safety.md`](destructive_command_safety.md).

LCAgent session JSONL artifacts are replayable in the embedded pane. Opening a previous
LCAgent session loads read-only transcript history; sending a new prompt starts a fresh
one-shot run that continues from a saved model-context snapshot when the prior artifact
has one, with summarized continuation as a labeled fallback for older artifacts. The
new run records `continuation` and `resume_context` events with the parent session,
root session, chain depth, handoff source, context mode, and pending verification/file
state when available.

For direct CLI use, `lcagent presets` lists coding route presets. `lcagent exec
--route-preset balanced|quality|mimo-2.5-pro-low|mimo-2.5-pro-high|mimo-2.5-pro-max|cheap-scout` and `lcagent live-eval
--route-preset balanced|quality|mimo-2.5-pro-low|mimo-2.5-pro-high|mimo-2.5-pro-max|cheap-scout` apply a provider, model, autonomy,
reasoning, tool-profile, context-profile, timeout, and temperature bundle where
the command supports those knobs; any explicit flag such as `--model` or
`--context-profile` still wins. The balanced DeepSeek lane sends explicit high
reasoning.
`lcagent scout <prompt>` is a direct cheap-scout wrapper for bounded read-only
exploration; it records a `delegation_mode` trace event and asks for a compact
handoff with findings, relevant files, next steps, and risks.
Chat's Repository Scout calls the same execution loop in-process for tracked,
privacy-eligible projects, with a smaller read-only tool schema and workspace-only reads. Its Scout behavior profile is
separate from provider/model routing, which lets it inherit configured Chat or
project-analysis inference without copying credentials or requiring a second
LCAgent setup.
Identical provider/model routes are skipped instead of making the same request
twice.
The durable internal Scout trace is linked from Chat but is not counted as new
engineering activity, so merely inspecting a repository does not refresh its
project recency.
Use `lcagent exec --continue-from <session-id-or-jsonl>` to start an explicit
continuation. Newer artifacts replay saved model context; older artifacts fall back to
summarized context. The older `--resume` flag remains as a compatibility alias.
In Little Control Room settings, `lcagent_route_preset` applies the same bundle
to embedded LCAgent launches; leave it blank to use the individual provider,
model, autonomy, tool-profile, and context-profile fields.

LCAgent permission levels are set by `lcagent_auto` or the LCAgent Permissions
field in `/settings`. `off` denies file edits and non-read commands. `low` is
the default: it allows workspace file edits, read-only command inspection, and
recognized verifier commands, while broader commands ask in the embedded pane.
`medium` allows workspace-contained commands without repeated approvals; write
tools still stay inside the workspace unless `lcagent_admin_write` is enabled.
Direct `rm` commands are denied structurally in both `run_command` and
`start_process` at every LCAgent permission level; a Low approval or switch to
Medium cannot override that denial. Targeted LCAgent file and patch tools remain
available.
Persistent user/system configuration mutations through `run_command`, such as
file-association/defaults changes or global package-manager state changes, also
require explicit `admin_scope=system` plus `lcagent_admin_write`.
When a Low run asks for command approval, `a` approves once and `A` switches the
current LCAgent run to Medium.

`lcagent metrics <session.jsonl>...` summarizes trace artifacts and includes a
`continuations` count plus a derived `trace_quality` block with verification
coverage, tool failures, repair pressure, read overlap, cached-token rate, and
estimated cost. The repeatable
`lcagent live-eval` lane reports the same trace-quality score per case.

Current LCAgent status and next work are tracked in
[`lcagent_experimental_handoff.md`](lcagent_experimental_handoff.md).

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
- Backtick or `/chat` opens Chat over the dashboard, or prompts for setup when its backend is not configured; `Esc` or backtick hides it
- `↑/↓` move selection
- `Enter` open or resume the selected project's latest embedded provider; fresh projects and scratch tasks default to Codex unless their create flow preselected another assistant
- `Esc` hide the visible embedded session pane
- `Alt+O` opens transcript links or generated artifacts when an embedded pane advertises available links; in that picker, `Alt+F` reveals the selected file in its folder when supported, otherwise opens the containing folder
- `PgUp/PgDn/Home/End` fast scrolling in long project lists
- `Tab` or `Shift+Tab` switch focus between list, detail, and runtime
- `f` open the temporary project-name filter dialog
- `a` switch between the Active and Archived project-list tabs
- `o` toggle sort mode between `recent activity` (the default, minute-grouped with alphabetical ties) and `attention`
- `p` pin toggle
- `q` quit
- While the runtime pane is focused, `Left` and `Right` choose the highlighted runtime action and `Enter` runs it

While the embedded Codex, Claude Code, or OpenCode pane is visible:

- `Enter` sends a prompt when idle and steers the active turn when the embedded session is busy
- `Alt+Enter` or `ctrl+j` inserts a newline
- `ctrl+v` attaches a clipboard image when available
- `Backspace` on an inline `[Image #n]` marker removes that attachment
- `Alt+L` cycles dense command, file, and tool transcript blocks through hidden output, preview, and full detail
- `ctrl+c` interrupts the active turn when busy and closes the session when idle

While the diff screen is visible:

- The left pane groups `Staged` files first and `Unstaged` files below them
- `-` stages the selected file when it is unstaged, and unstages it when it already has staged changes
- `Up/Down` or `j/k` moves between files when the file list is focused
- `Enter` opens the selected file with the system app, and `Alt+F` reveals it in its folder when supported, otherwise opens its containing folder
- `Right` or `Tab` moves focus into the diff pane
- `Left` or `Tab` moves focus back to the file list
- `PgUp/PgDn/Home/End` pages or jumps within the focused pane
- `Alt+Up` returns to the commit preview when the diff was opened from there, otherwise to the main project list
- `Esc` returns to the commit preview when the diff was opened from there, otherwise closes the diff screen

## Slash Commands

The TUI command palette opens with `/` and supports autocomplete with `Tab`.

- `/chat`
- `/refresh`
- `/update`
- `/sort attention`
- `/sort recent`
- `/non-ai-folders on`
- `/non-ai-folders off`
- `/tab`
- `/tab active`
- `/tab archived`
- `/setup`
- `/settings`
- `/filter`
- `/filter clear`
- `/new-project [--assistant codex|opencode|claude|lcagent]`
- `/new-task [--assistant codex|opencode|claude|lcagent] [request]`
- `/task-actions`
- `/open`
- `/run`
- `/start`
- `/run pnpm dev`
- `/restart`
- `/run-edit`
- `/runtime`
- `/ports`
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
- `/lcagent`
- `/lcagent continue with the next small step`
- `/lcagent-new inspect and patch the failing check`
- `/commit`
- `/commit tighten git status parsing`
- `/push`
- `/pull`
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
- `/archive`
- `/unarchive`
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
- `--lcagent-path "~/bin/lcagent"`
- `--lcagent-env-file "~/path/to/openrouter.env"`
- `--lcagent-auto low`
- `--db "~/.little-control-room/little-control-room.sqlite"`
- `--interval 60s`
- `--active-threshold 20m`
- `--stuck-threshold 4h`

## Notes

- `Enter` on the selected project opens that project's latest embedded provider inside Little Control Room; fresh items use any assistant chosen during creation, otherwise Codex.
- `/open` opens the selected project's folder in the system browser.
- `/archive` moves the selected regular project to the Archived tab, or moves the selected scratch task into the scratch archive folder and out of the active task list. `/unarchive` restores an archived regular project to Active when the project is still in scope. The `a` key and `/tab [active|archived|toggle]` switch between the Active and Archived tabs.
- `/remove` asks for confirmation, then makes the selected item go away using the safest matching action: it opens scratch-task archive/delete actions, cleans up linked worktrees, removes missing folders from the dashboard, or hides a regular project's exact path from the list. `/delete` and `/forget` are aliases.
- Linked worktree creation hydrates Git submodules by default. Repos can use `.lcroom/worktrees.toml` to opt out or define custom preparation profiles; see [`worktree_prep.md`](worktree_prep.md).
- `/ignore` hides the selected project's exact name inside Little Control Room, which is handy for Codex-generated worktrees or other old projects that share a stable folder name.
- `/snooze [duration|off]` snoozes the selected project for a period, and `/unsnooze` clears any active snooze.
- `f` opens a live project-name filter dialog for the whole dashboard; `/filter <text>` applies the same temporary filter from the command palette, and `/filter clear` removes it.
- `/ignored` opens a reversible picker of hidden project names and paths; press `Enter` there to restore one.
- `/run` starts the selected project's saved managed runtime. If no command is saved yet, Little Control Room opens a small dialog with an auto-suggested command when it can infer one from common files like `bin/dev`, `package.json`, `Makefile`, `justfile`, Jekyll's `_config.yml`, a simple Go entrypoint, or a Unity project at the root or in one unambiguous nested project folder.
- `/start` is an alias for `/run`.
- `/run <command>` saves that command as the selected project's default runtime command and starts it immediately.
- `/restart` restarts the selected project's managed runtime with the saved command, or with the active runtime command when one is already known.
- `/run-edit` opens the saved runtime command for editing without starting it.
- `/runtime` focuses the runtime pane for the selected project.
- `/ports` opens a port-focused inspector for tracked project listeners, including managed runtimes, external listeners, orphaned PID 1 listeners, and detected conflicts. Press `s` on an external project-local listener to open a stop confirmation.
- `/stop` stops the selected project's managed runtime when one is running.
- `/codex` resumes the selected project's latest known Codex session when available, otherwise it starts a new one.
- `/codex-new` always starts a fresh Codex session.
- `/claude` resumes the selected project's latest known Claude Code session when available, otherwise it starts a new one.
- `/claude-new` always starts a fresh Claude Code session.
- `/opencode` resumes the selected project's latest known OpenCode session when available, otherwise it starts a new one.
- `/opencode-new` always starts a fresh OpenCode session.
- `/lcagent` resumes the selected project's latest known LCAgent session when available, otherwise it starts a new one-shot run with the configured experimental provider.
- `/lcagent-new` always starts a fresh LCAgent run. LCAgent is experimental and currently supports prompt turns, curated model selection plus custom model entry, local read/edit tools, in-pane approval for denied low-permission commands, a Medium shortcut for the current run, `/permissions` to explain or change session permissions, `/review` for read-only current-diff review, `/compact` for a Markdown handoff summary from the latest JSONL trace, and structured JSONL artifacts; attachments are not wired yet.
- While an embedded Codex, Claude Code, OpenCode, or LCAgent pane is visible, local slash commands include `/new`, `/sessions` (`/resume` and `/session` aliases), `/reconnect`, `/model`, `/status`, `/permissions`, `/compact`, `/review`, and `/chat`. Embedded providers expose LCR's local command subset, not every native slash command from the provider CLI.
- `/model` changes the model and reasoning for the current embedded tool and carries that choice forward to future embedded sessions of the same tool, including after restarting LCR.
- `/sessions` with no session ID opens a picker for saved sessions from the current project and provider; `/sessions <session-id>` jumps straight to that session.
- `/reconnect` restarts the current embedded provider helper and reconnects to the same session when possible, which is useful after refreshing `codex login` or other provider auth outside Little Control Room.
- `/review` starts an embedded Codex review of uncommitted changes and streams the review-mode transcript into the pane.
- While Chat is visible, `Enter` sends or confirms a proposal, `Alt+Enter` adds a newline, `/new [prompt]` starts a fresh session, `Ctrl+L` clears into a fresh session, and `Esc` or backtick hides the overlay.
- Embedded Claude Code runs through Claude Code's `claude -p` stream flow. Prompt/response turns, session resume, and `/model` are wired, while unsupported in-pane actions fall back to the local command subset above.
- The main list uses `RUN` for the saved or active managed runtime summary, and `!` inside `RUN` when Little Control Room detects a managed port conflict.
- The project detail pane keeps project metadata only, while the dedicated runtime pane shows runtime command, state, ports, URL, conflicts or errors, and the captured output tail.
- `codex_launch_preset` controls how Codex is launched. The default is `yolo`.
- CLI flags override config file values.
