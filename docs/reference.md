# Little Control Room Reference

This page keeps the command, keybinding, and configuration details that are useful once you are already up and running.

## Detector Notes

Provider artifact and detector-footprint notes live in:

- [`codex_cli_footprint.md`](codex_cli_footprint.md)
- [`claude_code_footprint.md`](claude_code_footprint.md)
- [`repository_root_integrity.md`](repository_root_integrity.md)
- [`tui_design_rules.md`](tui_design_rules.md)

## CLI Commands

- `lcroom tui` opens the interactive dashboard and normally follows the saved mobile settings; `--listen <host:port>` is a one-run address and enablement override, with pairing required for non-loopback listeners
- `lcroom scan` rescans artifacts and refreshes the local store
- `lcroom classify` scans and drains the latest-session AI classification queue
- `lcroom doctor` prints a diagnostic report from the current cached store
- `lcroom doctor --scan` rescans first, then prints the diagnostic report
- `lcroom screenshots` renders the curated docs screenshot set from a screenshot config
- `lcroom mockups` renders static high-level UI mockups without scanning projects or launching the TUI
- `lcroom demo record`, `demo edit`, `demo play`, and `demo export` capture compact text-frame sessions, select and replay clips, and export asciicast files
- `lcroom scope` shows the effective include and exclude scope for this run
- `lcroom serve` explicitly starts the standalone read-only REST and WebSocket server even when TUI mobile auto-start is disabled; it uses the saved address unless `--listen <host:port>` overrides it

Mobile access, pairing, address modes, and the `/mobile` panel are documented in
[Mobile Preview](mobile.md). Detail worth repeating here: `mobile_input_enabled`
is false by default, a live channel's composer unlocks only when that setting is
on, and pairing adds no TLS, so direct HTTP exposure should stay on a trusted LAN.

`lcroom classify` requires a configured AI backend. That can be Codex, OpenCode, Claude Code, MLX, Ollama, or a direct API backend such as OpenAI, OpenRouter, DeepSeek, Moonshot, or Xiaomi. The TUI will open `/setup` automatically until you pick one.

Official GitHub release builds perform a throttled stable-release check when the TUI starts. The check runs at most once every 24 hours and caches GitHub's ETag and latest release metadata under `~/.little-control-room/updates/`. When an update exists, the top bar shows bright `/update <version>` text. `/update` requires an explicit `Update & restart` confirmation before downloading anything. Installation verifies the GitHub SHA-256 digests and `checksums.txt`, verifies Apple Developer signatures on macOS, stages both `lcroom` and `lcagent`, replaces them with rollback protection, journals active embedded turns, releases the database runtime lease, and restarts the same command. Source builds and non-GitHub distributions skip automatic checks. `LCR_DISABLE_UPDATE_CHECKS=true` disables automatic checks while preserving explicit `/update` checks.

Open Chat from the main TUI with backtick or `/chat`, including from an embedded provider pane. It appears as a centered overlay over the dashboard and receives a compact app-state brief, so the same conversation can explain LCR, inspect projects and tasks, propose confirmable controls, delegate work, and report completion without replacing the dashboard with a second project view. Questions that depend on repository files use a bounded LCAgent Repository Scout: workspace-only reads, no commands or edits, project `AGENTS.md` instructions, mechanically recorded read ranges, and a durable JSONL trace. Scout uses an explicit LCAgent route first when one is configured; otherwise it inherits the existing Chat utility model, then Chat main, then compatible project-analysis inference. Chat answers append the route, fallback, evidence, and trace receipt automatically. If no route can inspect the repository, Chat remains available but must report that inspection was unavailable instead of treating missing evidence as proof that a plan, document, or implementation does not exist. Project-list organization is a separate control boundary: a request to add an existing folder to a named category such as Private registers that existing folder if needed and assigns the category, with no TODO, worktree, engineer session, Git initialization, or repository-content changes. Work requests for an existing loaded project default to one confirmed tracked launch: LCR creates a project TODO, prepares a dedicated worktree, and starts a fresh engineer there. Press `q` in that confirmation to add the TODO without starting work. A brand-new repository work request uses a distinct confirmation that names the exact target path and discloses directory creation, Git initialization, project registration, TODO/worktree creation, and engineer launch; an existing untracked Git repository can use the same tracked-work path without reinitializing Git. A follow-up that defines or clarifies the just-created project continues that creation TODO in its recorded worktree instead of creating a sibling; while the root repository still has no commit, LCR also refuses a second linked worktree until the first is continued or integrated. LCR records both the starting state and the final launch or staged partial-failure result in the Chat transcript, never falls back from failed worktree preparation into the root checkout, and does not treat an idle root engineer turn as proof that its task is finished. `Esc` or backtick hides the overlay while in-flight replies continue; `/new [prompt]` and `Ctrl+L` start a fresh Chat session. Transcripts are Markdown files under `~/.little-control-room/help-chat-sessions/`, and recall also searches legacy `boss-sessions/` files. The existing `boss_chat_backend`, `boss_helm_model`, `boss_utility_model`, `boss_chat_model`, and `LCROOM_BOSS_MODEL` names remain compatibility settings.

## Config File

- Preferred default path: `~/.little-control-room/config.toml`
- Override path: `--config /path/to/config.toml`
- Example file: [`config.example.toml`](config.example.toml)
- Supported format: TOML

Use `/setup` for the Getting Started settings: project-report AI, Chat, optional LCAgent worker/Scout overrides, mobile access, and the shared provider keys or local endpoint fields those choices need. Repository Scout does not require separate LCAgent credentials when Chat already has a compatible API or local inference route. The full `/settings` modal keeps that first-run section and adds AI/model details, MLX/Ollama endpoint/model overrides, project scope, experimental LCAgent launch settings, mobile startup/address controls, browser behavior, refresh timing, and advanced toggles. Project discovery paths live in Project Scope rather than quick setup. Mobile changes apply after restart. The Browser section exposes a simplified `Browser windows` field with plain-language choices such as `Only when needed`, `Always show`, and `Classic browser behavior`, while the config file still stores the raw Playwright policy keys below:

In `Only when needed`, newly launched embedded Codex, OpenCode, and Claude Code sessions route Playwright through an LCR-managed wrapper with a persistent browser profile. Codex gets a session-local `CODEX_HOME` overlay and OpenCode gets a session-local `XDG_CONFIG_HOME` overlay, both shadowing only the managed skills available for that launch so the user's real global installs are unchanged. Claude Code receives the managed Playwright and LCR runtime servers through its inline MCP config, with the corresponding tool namespaces pre-approved and the browser-handoff contract appended to its system prompt. After Playwright reaches login, MFA, consent, CAPTCHA, or another human-only step, the assistant can send LCR a structured browser-attention instruction. LCR shows a centered dialog for the same managed browser context—even when the embedded session is already visible—and keeps the wait actionable in the Browser sidebar after dismissal. Claude Code keeps its stream and MCP children alive while that handoff is pending, so the next user message continues with the same browser context. On macOS, revealing that browser targets its PID directly and is time-bounded, so auth stays in the Playwright session the embedded assistant is actually driving. Existing embedded sessions still need to be reopened or reconnected before they pick up the new launch path.

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
- `conflict_resolver_provider`
- `engineer_todo_capture_mode`
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
- `playwright_state_retention`
- `playwright_state_cleanup_interval`
- `playwright_state_disk_ceiling_bytes`
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
# /resolve preselects this provider independently of ordinary launch defaults.
# Confirming another provider remembers it for next time.
# Values: codex (default), opencode, claude_code, or lcagent.
conflict_resolver_provider = "codex"
# Embedded engineer TODO capture: off, explicit_only (default), or
# explicit_and_clear_deferrals.
engineer_todo_capture_mode = "explicit_only"
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
playwright_state_retention = "720h"
playwright_state_cleanup_interval = "6h"
playwright_state_disk_ceiling_bytes = 2147483648
```

Managed Playwright state cleanup runs once when a long-lived `tui` or `serve`
runtime starts and then every `playwright_state_cleanup_interval`. The defaults
retain inactive session metadata, output, and profiles for 30 days and cap their
combined logical size at 2 GiB. Cleanup removes age-expired inactive sessions
first, then evicts the oldest remaining inactive state until it reaches the
ceiling. A zero retention period disables age-based expiry; a zero ceiling
disables size-based eviction. The cleanup interval must remain greater than
zero.

Live owner, MCP, or browser PIDs protect a session, and every profile referenced
by a live session is protected with it. A live Chromium `SingletonLock` also
protects a profile even when its session metadata is incomplete. Active state is
never removed to force the ceiling, so a cleanup record can report the ceiling
as temporarily unsatisfied. Each pass writes a JSONL result to
`~/.little-control-room/browser/playwright/cleanup.log`; the log rotates at 1
MiB and is outside the state-usage total. Changes to these three cleanup values
take effect when the long-lived runtime restarts.

### Embedded engineer TODO capture

`engineer_todo_capture_mode` lets LCR-managed Codex, OpenCode, Claude Code, and LCAgent sessions add items to the current repository's LCR TODO list. It applies only to sessions embedded by LCR; an unrelated provider process opened in another terminal does not receive these tools. Codex, OpenCode, and Claude Code use the embedded `lcr_runtime` MCP server, while LCAgent uses an equivalent native host broker.

The default, `explicit_only`, acts only when the user directly asks to remember or add work for later. `explicit_and_clear_deferrals` also permits an unambiguous user decision to postpone concrete work. `off` disables writes. Neither enabled mode permits an engineer to capture its own suggestions, code comments, tool output, or ambiguous ideas. The agent must list open TODOs first, compare them for semantic duplicates, pass the returned review revision when adding, and report whether the item was created, already existed, or needs review again because the list changed.

Scope is derived from the embedded session's trusted launch path; the tool accepts no project-path override. A session in a linked worktree or repository subdirectory writes to the loaded main repository's TODO list. If Git/LCR scope cannot be resolved unambiguously, the write fails closed. Inserts are serialized across MCP processes, and exact retries are duplicate-safe even when two engineers race.

Policy downgrades and `off` are enforced against already-running calls through the live policy. Newly enabled tools or an expanded clear-deferral schema require reopening or `/reconnect` for an already-initialized Codex, OpenCode, Claude Code, or LCAgent session. LCAgent receives the same contract through its per-run native tools.

### Conflict resolver provider

`/resolve` always opens a provider chooser before starting repair. `conflict_resolver_provider` controls which option is highlighted initially: `codex`, `opencode`, `claude_code`, or `lcagent`. It defaults to `codex`. Pressing Enter launches the highlighted provider and remembers that choice here for the next `/resolve`; canceling leaves the default unchanged.

The resolver default is intentionally independent of the selected project's recent sessions and the provider last used for ordinary tasks. Starting a resolver also does not change the ordinary-task default. The chooser shows known setup or availability problems before launch. If the confirmed provider cannot start, repair fails visibly without silently falling back to another provider, and the Git conflicts remain untouched.

Embedded Codex keeps the filesystem reach and approval behavior selected by
`codex_launch_preset`; the default remains `yolo`. Little Control Room adds a
narrow destructive-command seatbelt to every LCR-managed embedded Codex
session. Its PATH-pinned `rm` shim permits plain `rm -rf /tmp/<name>` only when
every operand is unambiguous and its parent resolves below `/tmp`; other named
`rm` calls are rejected. Codex exec-policy rules separately forbid absolute
executables and common wrapper forms that could bypass the shim. This does not
confine reads or ordinary writes to the current project. Embedded Claude Code
receives an LCR-owned Bash `PreToolUse` hook that denies direct `rm` before
execution in every permission mode, including YOLO's `bypassPermissions`.
LCAgent keeps its stricter policy and denies every direct `rm` invocation.

The guard is deliberately narrower than a security sandbox. Other deletion
mechanisms, an absolute executable hidden inside a script, or deliberate PATH
replacement can bypass it. Keep normal backups and filesystem protections in
place; use the `/tmp` exception only for disposable temporary trees, use
targeted file/patch tools for agent edits, and run other intentional bulk
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
conflict_resolver_provider = "codex"
playwright_management_mode = "managed"
playwright_default_browser_mode = "headless"
playwright_login_mode = "promote"
playwright_isolation_scope = "task"
playwright_state_retention = "720h"
playwright_state_cleanup_interval = "6h"
playwright_state_disk_ceiling_bytes = 2147483648

interval = "60s"
active-threshold = "20m"
stuck-threshold = "4h"
```

## Demo recordings

LCR can record the rendered Bubble Tea view as compact text frames, edit clips
without modifying the source session, and export a selected clip as an
[asciicast v3](https://docs.asciinema.org/manual/asciicast/v3/) file:

```sh
lcroom demo record walkthrough.lcrdemo
lcroom demo edit walkthrough.lcrdemo
lcroom demo play walkthrough.lcrdemo --clip 1
lcroom demo export walkthrough.lcrdemo --clip 1 --output walkthrough.cast
```

`demo record` launches the regular TUI and accepts the same configuration flags
after the optional output path. When the path is omitted, LCR creates a
timestamped `.lcrdemo` directory in the current directory. Existing paths are
never overwritten.

The source recording is not a pixel video. It contains complete Bubble Tea
views encoded as independently readable, one-minute gzip chunks. The first
frame in each chunk is complete; subsequent frames store changed text lines.
Identical views are omitted, so a long interval with an unchanged view takes
essentially no frame space. Compression and file writes run outside Bubble
Tea's update/render path.
If the process is interrupted, completed chunks remain editable; only the
currently open chunk may be incomplete.

The editor is also a Bubble Tea TUI:

- `Left`/`Right` seek by one second; `Shift+Left`/`Shift+Right` seek by ten.
- `PageUp`/`PageDown` seek by one minute; `Ctrl+Left`/`Ctrl+Right` seek by ten minutes.
- `[`/`]` jump to the previous/next coarse interaction marker.
- `Space` previews the selection.
- `i` and `o` set source-timeline in/out points.
- `n` starts a new selection; `s` saves or updates it.
- `Tab` cycles saved clips; `Backspace` deletes the selected clip.
- `d` cycles the ordinary idle-gap cap; `t` toggles smart timing.
- `f` toggles a clean full-frame preview.
- `e` exports the current selection to asciicast v3.

New selections default to smart timing. This editing-time transform leaves the
source recording untouched. It replays localized text growth or deletion at a
steady short cadence, accelerates low-information changes such as counters and
marquees, and inserts a brief readability hold when recent interactive text is
followed by a large screen transition. Bulk paste or dictation appears as one
update and receives the same final hold; LCR does not fabricate or store
individual pasted keystrokes. The classifier uses rendered-frame geometry plus
the existing coarse interaction timestamps, never key identities or values.
Saved clips retain their timing choice.

Saved selections live in `edits.json` beside the recording and are only edit
decisions; the captured chunks are unchanged. Exported `.cast` files contain
full text frames and can be played with `asciinema play` or rendered to a GIF
with `agg`. Smart exports write the edited event delays explicitly; ordinary
exports retain the asciicast idle-time-limit behavior.

`demo play` shows either the whole recording or a selected clip as a clean
full-frame playback. Press `Space` to pause/resume, `Left`/`Right` to seek by a
second, `Home` to restart the selection, and `q` to exit.

Key values and entered text are never recorded. LCR keeps at most one coarse
interaction timestamp every two seconds to make long recordings navigable; it
does not retain which key or mouse button produced the marker. When a private
project-category tab is selected, or an embedded session for a private project
is visible, the saved frame is replaced before capture with the fixed message
`PRIVATE VIEW — NOT RECORDED`; the operator's live TUI remains unchanged. Other
visible surfaces can still contain project names, prompts, diffs, paths, or
sensitive output, so review every clip before sharing it.

The equivalent direct TUI flag is:

```sh
lcroom tui --demo-record walkthrough.lcrdemo
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
- `Alt+O` opens transcript links, inspected images, or generated artifacts when an embedded pane advertises available links; image rows include a large inline preview, streaming turns append newly discovered rows without resetting the current selection, and `Alt+F` reveals the selected file in its folder when supported, otherwise opens the containing folder
- `PgUp/PgDn/Home/End` fast scrolling in long project lists
- `Tab` or `Shift+Tab` switch focus between list, detail, and runtime
- `f` open the temporary project-name filter dialog
- `a` cycle the project-list tabs: Main, any custom categories, and Archived
- `o` toggle sort mode between `recent activity` (the default, minute-grouped with alphabetical ties) and `attention`
- `p` pin toggle
- `I` inspect a repository-root branch mismatch for any selected member of the repository family
- `q` quit
- While the runtime pane is focused, `Left` and `Right` choose the highlighted runtime action and `Enter` runs it; `c` copies the selected process's captured output directly

While the embedded Codex, Claude Code, or OpenCode pane is visible:

- `Enter` sends a prompt when idle and steers the active turn when the embedded session is busy
- `Alt+Enter` or `ctrl+j` inserts a newline
- `ctrl+v` attaches a clipboard image when available
- `Backspace` on an inline `[Image #n]` marker removes that attachment
- Consecutive identical command and file-change blocks collapse into one row with an `×N` count; `Alt+L` cycles dense command, file, and tool transcript blocks through hidden output, preview, and full detail, revealing every collapsed occurrence in full mode
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

### Sessions and projects

- `/chat` (alias `/help`): Open Chat over the dashboard. Backtick is the shortcut; it prompts for setup when the Chat backend is not configured yet.
- `/codex [prompt]`, `/claude [prompt]`, `/opencode [prompt]`, `/lcagent [prompt]`: Resume the selected project's latest session for that provider, or start one.
- `/new-codex [prompt]`, `/new-claude [prompt]`, `/new-opencode [prompt]`, `/new-lcagent [prompt]`: Start a fresh embedded session.
- `/todo` (`t`): Open the selected project's TODO list. Add items, toggle done, and start a fresh embedded session from any item.
- `/new-project [--assistant codex|opencode|claude|lcagent]`: Create a project folder, or use path suggestions / paste an existing project path to add it directly. The dialog also chooses which assistant `Enter` opens first for the new item, defaulting to the last embedded provider you used when available.
- `/clone-project [--assistant …]`: Clone an HTTPS, SSH, or local Git repository into a selected parent folder and add it as a project. The repository name becomes the folder name; an existing destination is avoided with `-2`, `-3`, and later suffixes. Also reachable from the tab-focusable **Clone a Git repository…** action in `/new-project`.
- `/new-task [--assistant …] [request]`: Create a scratch task folder under the default task root. Optional request text seeds the temporary task name.
- `/task-actions`: Open archive/delete actions for the selected scratch task.
- `/open`: Open the selected project's folder in the system browser.
- `/terminal`: Open a system terminal in the selected project's folder.
- `/refresh`: Rescan projects and retry failed assessments.

### Git

- `/diff`: Open the full-screen git diff.
- `/commit [message]`: Preview a commit for the selected project. `Alt+Enter` also pushes when available.
- `/push`: Push the selected project's branch.
- `/pull [cancel]`: Fetch and safely fast-forward the selected project's branch. The project row shows elapsed time and Git/Git LFS transfer progress; active transfers have no wall-clock deadline and stop only after 60 seconds without observable progress. `/pull cancel` explicitly cancels an active pull — if its fetch already completed, LCR keeps the fetched remote-tracking state and reports whether the local fast-forward is still pending.
- `/resolve`: Choose an agent, then resolve selected repo merge conflicts in a separate background engineer session. The last confirmed resolver choice is preselected next time and stays independent of ordinary agent launches. Progress stays visible on the project row, followed by a fresh Git-status check after the agent verifies and commits the resolution or reports a blocker. If the resolver needs input, fails, or leaves conflicts behind, `Enter` on that project opens the exact saved resolver conversation; run `/resolve` again to retry in a fresh background session.
- `/integrity` (`I`): Inspect a repository-root branch mismatch, hand it to a fresh engineer, acknowledge it, update the expected branch, or apply a conservative linked-worktree repair.
- `/wt restore` (`/wt undelete`): List Codex sessions whose recorded LCR worktree is gone, recreate the original checkout when Git evidence makes that safe, and resume the selected conversation.
- `/wt update`, `/wt merge`, `/wt remove`, `/wt prune`: Update, integrate, remove, or prune linked worktrees in the selected repository family.

### Runtimes, ports, and processes

- `/run [command]` (alias `/start`): Start the selected project's managed runtime.
- `/restart`: Restart the selected project's managed runtime.
- `/run-edit`: Edit the saved runtime command.
- `/runtime`: Focus the runtime pane.
- `/stop`: Stop the selected project's managed runtime.
- `/ports`: Inspect project-local TCP listeners, see which project owns each port, and confirm-stop external ones.
- `/cpu`: Inspect top CPU processes, including ones orphaned under PID 1.

### Organization and display

- `/setup`: Open the Getting Started settings for first-run AI roles. Runs automatically on launch until you pick a backend.
- `/settings`: Full preferences: Getting Started, Providers & Models, LCAgent, Project Scope, Mobile, Browser, and Advanced.
- `/mobile`: Open the mobile access panel with the current listener, detected LAN phone URL, pairing code, and a direct jump to Mobile setup.
- `/filter [text|clear]` (`f`): Temporarily narrow the whole dashboard to matching project names.
- `/sort <attention|recent>` (`o`): Change project and agent-task ordering. Recent activity is the default; it groups activity by minute and orders ties alphabetically.
- `/tab [main|archived|toggle|category]` (`a`): Switch between the Main, custom category, and Archived project-list tabs.
- `/category create|remove|move|clear [name]`: Create categories, or move the selected item between category tabs.
- `/non-ai-folders <on|off>`: Show or hide folders that have no AI activity yet.
- `/focus <list|detail|runtime>`: Move focus between panes.
- `/pin` (`p`): Toggle pin on the selected project.
- `/read [all]`: Mark the selected project, or all visible projects, as read.
- `/unread`: Mark the selected project's latest completed assessment as unread.
- `/snooze [duration|off]`, `/unsnooze` (alias `/clear-snooze`): Snooze the selected project, or clear it.
- `/sessions <on|off|toggle>`: Show or hide the Sessions section.
- `/events <on|off|toggle>`: Show or hide Recent events.
- `/archive`: Move the selected regular project to the Archived tab, or archive the selected scratch task out of the active task list.
- `/unarchive`: Move the selected archived project back to Main when it is in scope.
- `/ignore`: Hide the selected project's exact name.
- `/ignored`: Review ignored names and paths, then restore them.
- `/remove` (aliases `/delete`, `/forget`): Confirm, then make the selected item go away safely. For regular projects, hides only the selected path.
- `/privacy on|off|toggle|settings`: Toggle demo privacy mode or open privacy settings.

### Diagnostics and maintenance

- `/ai`: Internal AI stats dialog, including observed output speed in tokens per second and Ollama context metadata when exposed.
- `/perf`: Internal responsiveness and wait tracker.
- `/errors`: Recent error log.
- `/skills`: Review Codex skills and local duplicates that may be stale.
- `/repair-terminal` (`Ctrl+L`): Reinitialize alternate-screen, cursor, mouse, and bracketed-paste modes after external terminal-state corruption.
- `/update`: Check for a newer stable GitHub release and, after explicit confirmation, verify, install, and restart into it.
- `/quit`: Quit the TUI.

### Inside an embedded Codex, Claude Code, or OpenCode pane

Embedded providers expose LCR's local command subset, not every slash command
from the native provider CLIs. Use the standalone provider CLI when you need a
provider-native command that LCR has not wired into the pane yet. Project
commands `/run`, `/start`, `/restart`, `/run-edit`, `/stop`, and `/commit` also
work here and target the project shown in the pane.

- `/new`: Start a fresh session for the current provider.
- `/sessions [session-id]` (aliases `/resume`, `/session`): Open this project's session-history picker or jump to a saved session.
- `/reconnect`: Restart the embedded provider helper and reconnect to the current session.
- `/pause` (alias `/suspend`): Interrupt the active turn locally without sending another model request. Use this when you need to stop immediately or are about to go offline.
- `/model`: Change the model and reasoning settings for this and future embedded sessions of the same tool, including after restarting LCR. LCAgent uses the same provider → model → reasoning flow as TODO launch; press `r` on the provider step to expand complete recent choices.
- `/status`, `/context`: Show provider/session status, including context usage when the provider reports it. In the embedded Session sidebar, Claude keeps deduplicated token totals across compaction and shows Claude.ai five-hour/weekly usage when subscription credentials are active.
- `/compact [instructions]`: Compact conversation history when supported. Embedded Claude Code forwards optional focus instructions to Claude's native compaction flow and reports whether a compaction boundary actually occurred.
- `/review`: Ask embedded Codex to review uncommitted changes.
- `/permissions [low|medium]`: LCAgent only. Explain or change the current session's next-turn autonomy.
- `/chat`: Hide the embedded pane and open Chat over the main dashboard.

### Inside Chat

- `Enter`: Send a message or confirm a proposed action.
- `Esc` or backtick: Hide Chat and return to the dashboard; in-flight replies keep running. When `/log` is open, `Esc` closes that window first.
- `/new [prompt]`: Start a fresh Chat session, optionally with the first prompt.
- `/log`: Open a separate scrollable window of recent AI engineer events.
- `Ctrl+L`: Start a fresh empty Chat session.
- `Alt+Enter`: Add a newline without sending.

Chat sessions are saved as grep-friendly Markdown transcripts under the app data
directory, for example `~/.little-control-room/help-chat-sessions/`. Recall
searches those transcripts and still includes legacy `boss-sessions/` history.
Launch, progress, completion, and failure receipts are saved as `Log` entries and
shown in the separate `/log` window; they stay out of the visible conversation,
Chat recall, and model context.

Chat can inspect the current dashboard and project/task context, propose
confirmable actions, delegate work, and report completions. Project-list
organization stays separate from project work: a request to add an existing folder
to a named category such as Private gets one confirmation that registers the
folder if needed and assigns the category, without creating a TODO, worktree,
engineer session, Git repository, or repository content. For work in an existing
loaded project, the default confirmation creates a tracked TODO, prepares a
dedicated worktree, and starts a fresh engineer there; press `q` in that
confirmation to add the TODO without starting it. Work in a brand-new or existing
untracked Git repository instead uses a repository-setup confirmation before the
same tracked TODO, worktree, and engineer launch.

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
- `--playwright-state-retention 720h`
- `--playwright-state-cleanup-interval 6h`
- `--playwright-state-disk-ceiling-bytes 2147483648`
- `--db "~/.little-control-room/little-control-room.sqlite"`
- `--interval 60s`
- `--active-threshold 20m`
- `--stuck-threshold 4h`

## Notes

- `Enter` on the selected project opens that project's latest embedded provider inside Little Control Room; fresh items use any assistant chosen during creation, otherwise Codex.
- `/open` opens the selected project's folder in the system browser.
- `/archive` moves the selected regular project to the Archived tab, or moves the selected scratch task into the scratch archive folder and out of the active task list. `/unarchive` restores an archived regular project to Active when the project is still in scope. The `a` key and `/tab [active|archived|toggle]` switch between the Active and Archived tabs.
- `/remove` asks for confirmation, then makes the selected item go away using the safest matching action: it opens scratch-task archive/delete actions, cleans up linked worktrees, removes missing folders from the dashboard, or hides a regular project's exact path from the list. `/delete` and `/forget` are aliases.
- `/wt restore` opens a repository-family recovery dialog for Codex conversations whose recorded linked-worktree path is missing. Candidates come from retained LCR worktree/session evidence and Codex's global `state_5.sqlite` thread index. A restore recreates the exact old path from its local branch, or recreates a missing branch at the thread's recorded Git commit, then applies normal worktree preparation, reconnects an open origin TODO when known, and resumes that Codex thread. Existing paths, branches checked out elsewhere, locked or mismatched stale registrations, invalid branch names, and unavailable fallback commits are shown as blocked instead of being modified. Recovery reconstructs committed Git state; it cannot restore uncommitted files that existed only in the deleted checkout. `/wt undelete` is an alias.
- Linked worktree creation hydrates Git submodules by default. Repos can use `.lcroom/worktrees.toml` to opt out or define custom preparation profiles; see [`worktree_prep.md`](worktree_prep.md).
- LCR stores an evidence-backed expected branch for repository families and warns when the canonical checkout is found on another branch. Press `I` or run `/integrity` for incident details, a fresh investigation-first engineer handoff, exact-state acknowledgment, explicit policy update, or conservative repair. The default is warn-only; see [`repository_root_integrity.md`](repository_root_integrity.md).
- `/ignore` hides the selected project's exact name inside Little Control Room, which is handy for Codex-generated worktrees or other old projects that share a stable folder name.
- `/snooze [duration|off]` snoozes the selected project for a period, and `/unsnooze` clears any active snooze.
- `f` opens a live project-name filter dialog for the whole dashboard; `/filter <text>` applies the same temporary filter from the command palette, and `/filter clear` removes it.
- `/ignored` opens a reversible picker of hidden project names and paths; press `Enter` there to restore one.
- `/run` starts the selected project's saved managed runtime. If no command is saved yet, Little Control Room opens a small dialog with an auto-suggested command when it can infer one from common files like `bin/dev`, `package.json`, `Makefile`, `justfile`, Jekyll's `_config.yml`, a simple Go entrypoint, or a Unity project at the root or in one unambiguous nested project folder.
- The run-command dialog shows matching project-derived completions. It offers all detected package scripts, Make/Just targets, and Go entrypoints without making the automatic default choice less conservative. It also provides project-scoped path completion without requiring a leading `./`: start typing a relative directory name, use Up/Down to select a directory or executable, and use `Tab` or `Enter` on the highlighted match. Directory completion continues into the selected folder; `Enter` on a highlighted command or file saves and optionally runs that completed value, and paths used as command arguments can include regular files. Discovery stays beneath the selected project and never blocks the TUI update/render path.
- `/start` is an alias for `/run`.
- `/run <command>` saves that command as the selected project's default runtime command and starts it immediately.
- `/restart` restarts the selected project's managed runtime with the saved command, or with the active runtime command when one is already known.
- `/run-edit` opens the saved runtime command for editing without starting it.
- `/runtime` focuses the runtime pane for the selected project.
- `/ports` opens a port-focused inspector for tracked project listeners, including managed runtimes, external listeners, orphaned PID 1 listeners, and detected conflicts. Press `s` on an external project-local listener to open a stop confirmation.
- `/stop` stops the selected project's managed runtime when one is running.
- `/codex` resumes the selected project's latest known Codex session when available, otherwise it starts a new one.
- `/new-codex` always starts a fresh Codex session.
- `/claude` resumes the selected project's latest known Claude Code session when available, otherwise it starts a new one.
- `/new-claude` always starts a fresh Claude Code session.
- `/opencode` resumes the selected project's latest known OpenCode session when available, otherwise it starts a new one.
- `/new-opencode` always starts a fresh OpenCode session.
- `/lcagent` resumes the selected project's latest known LCAgent session when available, otherwise it starts a new one-shot run with the configured experimental provider.
- `/new-lcagent` always starts a fresh LCAgent run. LCAgent is experimental and currently supports prompt turns, curated model selection plus custom model entry, local read/edit tools, in-pane approval for denied low-permission commands, a Medium shortcut for the current run, `/permissions` to explain or change session permissions, `/review` for read-only current-diff review, `/compact` for a Markdown handoff summary from the latest JSONL trace, and structured JSONL artifacts; attachments are not wired yet.
- The fresh-session commands were previously named `/codex-new`, `/claude-new`, `/opencode-new`, and `/lcagent-new`. Those names still work as hidden aliases, alongside `/codex-start`, `/cc-start`, `/oc-start`, and `/lca-start`; only the `/new-*` form is listed in help and completion, where it groups with `/new-project` and `/new-task`.
- While an embedded Codex, Claude Code, OpenCode, or LCAgent pane is visible, local slash commands include `/new`, `/sessions` (`/resume` and `/session` aliases), `/reconnect`, `/pause` (`/suspend` alias), `/model`, `/status`, `/context`, `/permissions`, `/compact [instructions]`, `/review`, and `/chat`. `/pause` interrupts the active turn locally without sending another model request, so it is safe to use when the connection is about to disappear. `/context` opens the provider status report with current context usage when available. Project commands `/run`, `/start`, `/restart`, `/run-edit`, `/stop`, and `/commit [message]` are also available and always target the project shown in the embedded pane. Run-command, external-stop, and commit-preview dialogs render over the live session; `/runtime` hides the session and focuses that project's runtime pane. Embedded providers expose LCR's local command subset, not every native slash command from the provider CLI.
- `/model` changes the model and reasoning for the current embedded tool and carries that choice forward to future embedded sessions of the same tool, including after restarting LCR.
- `/sessions` with no session ID opens a picker for saved sessions from the current project and provider; `/sessions <session-id>` jumps straight to that session.
- `/reconnect` restarts the current embedded provider helper and reconnects to the same session when possible, which is useful after refreshing `codex login` or other provider auth outside Little Control Room.
- `/review` starts an embedded Codex review of uncommitted changes and streams the review-mode transcript into the pane.
- While Chat is visible, `Enter` sends or confirms a proposal, `Alt+Enter` adds a newline, `/new [prompt]` starts a fresh session, `Ctrl+L` clears into a fresh session, and `Esc` or backtick hides the overlay.
- When Little Control Room itself is started through `go run`, it pins the disposable Go-cache executable under `<data-dir>/embedded-helpers/` before registering LCR-owned MCP servers or Claude Code safety hooks. The pin is a hard link when the cache and data directory share a filesystem, with a byte-for-byte copy as the cross-filesystem fallback. Clearing the Go build cache can therefore remove its original path without breaking newly opened embedded sessions.
- Embedded Claude Code runs through Claude Code's `claude -p` stream flow. Prompt/response turns, session resume, `/model`, context-usage reporting, and native `/compact [instructions]` are wired, while other unsupported in-pane actions fall back to the local command subset above. LCR only reports successful Claude compaction after receiving Claude's structured compaction boundary; a short conversation can therefore return a clear no-op result. LCR disables Claude Code's native background-task mode for these embedded processes because Claude cleans up background tasks when its owning CLI exits; shell commands and tests therefore remain foreground-owned until they finish. LCR's per-process settings also disable Claude Code's automatic commit and pull-request attribution. Structured background-task evidence from restored or externally owned sessions is still shown under **Active Processes**, and a provider exit before a terminal notification is reported as lost work.
- If LCR inherited a non-empty `ANTHROPIC_API_KEY`, it pauses before starting any embedded Claude Code process (including a Claude conflict-resolver lane) and warns that Claude Code may prioritize pay-as-you-go API billing over subscription limits. **Cancel launch** remains the default. **Continue anyway** acknowledges the warning for the current LCR process only; the warning becomes active again after LCR restarts.
- The main list uses `RUN` for the saved or active managed runtime summary, and `!` inside `RUN` when Little Control Room detects a managed port conflict.
- The project detail pane keeps project metadata only, while the dedicated runtime pane shows runtime command, state, ports, URL, conflicts or errors, and the captured output tail. When output is available, **Copy output** places that selected process's plain-text output on the clipboard, while **Add TODO** opens a prefilled, editable failure report under the repository-scoped project; press `Ctrl+S` there to save it.
- `codex_launch_preset` controls how Codex is launched. The default is `yolo`.
- `conflict_resolver_provider` controls which provider the `/resolve` chooser preselects. The default is `codex`, and confirming another choice remembers it.
- CLI flags override config file values.
