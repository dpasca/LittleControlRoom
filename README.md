# Little Control Room

Little Control Room (LCR) is a terminal control room I built for my own agent-heavy workflow across many repos.

I use it to keep Codex, OpenCode, and Claude Code sessions visible, jump back into work quickly, start fresh sessions from TODOs, review diffs, and ship changes without bouncing between tools. For background AI work, it can also route through local MLX or Ollama servers.

It is also used internally, but this is not a commercial product. It is an opinionated open source tool that grew out of day-to-day use.

<p align="center">
  <a href="docs/screenshots/main-panel.png">
    <img src="docs/screenshots/main-panel.png" alt="Little Control Room main dashboard overview with live agent activity in the list" width="850">
  </a>
</p>

## Why I Built It

- Too many repos and too many live agent sessions are hard to coordinate from separate terminals and tabs.
- I wanted one terminal-first place to see what is active, what is worth revisiting, and what I can ship next.
- I wanted TODOs, diffs, commit help, and embedded sessions to stay close to each other instead of being spread across several tools.

## What It Helps With

- Finding recent Codex, OpenCode, and Claude Code sessions across local projects
- Seeing which projects are active, idle, or worth revisiting
- Opening, resuming, or switching embedded Codex, OpenCode, or Claude Code sessions directly from the dashboard
- Reopening Claude Code sessions that are already running in another terminal
- Keeping common actions close at hand: refresh, pin, snooze, per-project TODO lists, managed per-project run commands with runtime/port badges, diff, commit, and push

## Quick Start

```bash
curl -fsSL https://raw.githubusercontent.com/dpasca/LittleControlRoom/master/install.sh | bash
lcroom tui
```

On first run, LCR opens `/setup` so you can pick a backend: Codex, OpenCode, Claude Code, MLX, Ollama, or an OpenAI API key.

The installer puts `lcroom` and `lcagent` in `~/.local/bin` and prints a PATH hint when needed.

<details>
<summary>Manual download</summary>

You can also download an archive directly from the [Releases page](https://github.com/dpasca/LittleControlRoom/releases):

| Platform | Release asset |
| --- | --- |
| macOS Apple Silicon | `lcroom_Darwin_arm64.zip` |
| macOS Intel | `lcroom_Darwin_x86_64.zip` |
| Linux ARM64 | `lcroom_Linux_arm64.tar.gz` |
| Linux x86_64 | `lcroom_Linux_x86_64.tar.gz` |

```bash
# Example: Linux x86_64
curl -L -o lcroom.tar.gz https://github.com/dpasca/LittleControlRoom/releases/latest/download/lcroom_Linux_x86_64.tar.gz
tar -xzf lcroom.tar.gz
./lcroom tui
```

Release archives include `lcroom` and the sibling `lcagent` helper binary used by the experimental embedded LCAgent provider. Move both binaries to a directory on your `PATH` if you want to run `lcroom` from anywhere.

Little Control Room is not published through Homebrew, apt, Snap, Flatpak, Nix, or other package managers yet.

</details>

Official GitHub release builds check for a newer stable release when the TUI starts, at most once every 24 hours. A new version appears as bright `/update <version>` text in the top bar; run `/update` to review it. The updater does not download or install anything until you explicitly highlight `Update & restart` and confirm. It then verifies GitHub's SHA-256 asset digests and the published checksum file, also verifies the Apple Developer signatures on macOS, replaces `lcroom` and `lcagent` together with rollback protection, saves active engineer turns, and restarts the TUI using the new binary.

Source/development builds do not contact GitHub for updates. Build metadata also keeps the updater out of package-manager-owned installations when those distributions arrive. Set `LCR_DISABLE_UPDATE_CHECKS=true` to disable the once-daily automatic check in an official GitHub build; `/update` remains available for an explicit manual check.

<details>
<summary>Build from source</summary>

Requires Go 1.25+.

```bash
make build
./lcroom tui
```

To build both local binaries:

```bash
make build-all
./lcroom tui
```

Or install the CLI to your Go bin:

```bash
make install
lcroom tui
```

</details>

## Local Mobile Preview

The monitor-first mobile client starts with the main TUI by default and shares its live store, service configuration, and update events:

```bash
lcroom tui
```

Open `http://127.0.0.1:7777` to use the project/category dashboard, project detail, and active/recent engineer transcripts. The portrait dashboard follows the main TUI's familiar project-first scan pattern: each compact row keeps the project name and summary prominent, with narrow assessment, agent, and flag columns beside it. TUI-hosted dashboards also include a live engineer-channel rack for jumping directly into working, waiting, stalled, or input-needed sessions. Live transcript revisions arrive over a dedicated event stream and update individual entries in place, with periodic refresh retained only as a connection fallback. Transcript views render Markdown, offer `Conversation` and `All activity` monitoring modes, and preserve live-follow state while you read older entries. Live channels always show their composer state: mobile session messages are off by default, with an explanation that points to `Session messages` in Mobile settings; enabling that setting unlocks the draft-preserving composer for the current live channel. Recorded sessions, approvals, interrupts, model changes, and session creation remain read-only. The stable top-right indicator advertises `/mobile` and, when space permits, its `LAN`, `RESTART`, `SETUP`, `OFF`, or `ERR` state. `RESTART` means the saved mobile listener setup differs from the running listener. Run `/mobile` for the full access panel: current listener, detected private LAN address, usable phone URL, pairing code, phone-control state, and any saved setup waiting for restart. Press Enter there to jump to the authoritative Mobile setup fields. If the port is already occupied, the TUI keeps running and reports the mobile server failure in its top status line and the Mobile panel.

Use the Mobile card in `/setup` or the Mobile section in `/settings` to disable TUI auto-start, opt into `Session messages`, and choose `This computer only`, `Phones on this LAN`, or `Custom address`. The message permission applies immediately after saving. LAN mode is recommended for phone use; it derives the technical listener `0.0.0.0:<port>` and shows the detected phone-ready URL separately. Local mode derives `127.0.0.1:<port>`. Listener changes apply on the next LCR launch, while Custom address preserves direct `host:port` control for advanced setups.

Pass an explicit LAN address for a one-run override, for example `lcroom tui --listen 192.168.0.6:7777`. An explicit `--listen` also starts the mobile client for that run when saved auto-start is disabled. Non-loopback listeners require mobile pairing: run `/mobile` in the TUI to see the phone-ready URL and current six-digit code, then enter it on the phone. Press `c` in that panel to copy the phone URL. Pairing grants that browser a 30-day HTTP-only device pass which remains valid across LCR restarts; the signing key is stored as `mobile-auth.key` beside the active database with owner-only permissions.

`lcroom serve` remains available for a standalone preview and accepts the same `--listen` flag. It prints the LAN pairing code at startup. It can read recorded engineer transcripts from detected artifacts, but only the TUI-hosted client can overlay the richer in-memory live transcript. A standalone preview also needs its own database runtime lease.

Pairing authenticates the browser but does not encrypt plain HTTP traffic. Keep direct LAN exposure on a trusted network; transport encryption or a private overlay network is still required against local traffic interception.

<p align="center">
  <a href="docs/screenshots/setup.png">
    <img src="docs/screenshots/setup.png" alt="Little Control Room setup screen showing Getting Started settings for project reports, Chat, and optional LCAgent details" width="850">
  </a>
</p>

## Background AI Backends

LCR separates embedded session providers from the backend used for background work such as summaries, classification, commit help, and TODO worktree suggestions.

- Embedded sessions today are Codex, OpenCode, and Claude Code.
- Background AI can run through Codex, OpenCode, Claude Code, MLX, Ollama, or direct OpenAI API.
- Chat has its own `boss_chat_backend` compatibility setting, so interactive high-level chat can use direct API inference through OpenAI API, MLX, or Ollama without forcing summaries/classification off Codex, OpenCode, Claude Code, MLX, or Ollama. If it is not configured yet, `/chat` offers to jump straight to the Chat setup card.
- Claude Code usage follows the local `claude` CLI authentication mode. Current Anthropic docs say Pro/Max plan terminal usage counts against plan limits when Claude Code is authenticated with Claude credentials, while `ANTHROPIC_API_KEY` or explicit usage-credit continuation can bill separately at API rates.
- MLX uses its OpenAI-compatible local endpoint. Ollama discovery still uses its OpenAI-compatible model list, while background generation uses Ollama's native generate endpoint so thinking models can return usable JSON/text with thinking disabled.
- Ollama thinking stays off by default for background automation and structured helper calls. When Chat uses Ollama, native `think: true` is on by default for answer text only; its setup panel includes a Chat Ollama thinking toggle if you want final-content-only responses.

For local inference, the practical setup is:

- Pick `MLX` or `Ollama` in `/setup`.
- Leave the endpoint fields blank in `/settings` if you want the defaults.
- Or edit the MLX/Ollama endpoint fields in `/settings` if your local server runs elsewhere.

Default local endpoints:

- MLX: `http://127.0.0.1:8080/v1`
- Ollama: `http://127.0.0.1:11434/v1`

To smoke-test a local model against common LCR usage without touching any repo state, run:

```bash
lcroom model-eval --backend ollama --model gemma4:12b-mlx
```

The check covers plain summary text, LCR session-assessment JSON, advice-follow-up classification, and commit-subject JSON. Passing and failing cases are both useful: local models may be good enough for commit help or free-form summaries while still failing stricter dashboard assessment schemas. The `/ai` dialog also reports observed output speed in tokens per second after successful calls, plus Ollama model context metadata when the server exposes it.

<p align="center">
  <a href="docs/screenshots/settings-local-backends.png">
    <img src="docs/screenshots/settings-local-backends.png" alt="Little Control Room settings screen showing MLX and Ollama local endpoint fields" width="850">
  </a>
</p>

## Slash Commands

The main TUI command palette opens with `/`.

Main workflow:

- `/chat`: Open Chat over the dashboard.
- `/filter [text|clear]` (`f`): Temporarily narrow the whole dashboard to matching project names.
- `/todo` (`t`): Open the TODO list for the selected project. Add items, toggle done, and start a fresh embedded session from any item.
- `/open`: Open the selected project's folder in the system browser.
- `/new-project [--assistant codex|opencode|claude|lcagent]`: Create a project folder, or use path suggestions/paste an existing project path to add it directly. The dialog also lets you choose which assistant `Enter` should open first for the new item, defaulting to the last embedded provider you used when available.
- `/new-task [--assistant codex|opencode|claude|lcagent] [request]`: Create a scratch task folder under the default task root. Optional request text seeds the temporary task name, and the assistant flag preselects the first embedded provider for `Enter`; without a flag, the task picker defaults to the last embedded provider you used when available.
- `/codex [prompt]`, `/opencode [prompt]`, `/claude [prompt]`, `/lcagent [prompt]`: Resume the latest session for that provider, or start one.
- `/codex-new [prompt]`, `/opencode-new [prompt]`, `/claude-new [prompt]`, `/lcagent-new [prompt]`: Start a fresh embedded session.
- `/refresh`: Rescan projects and retry failed assessments.
- `/repair-terminal`: Reinitialize alternate-screen, cursor, mouse, and bracketed-paste modes after external terminal-state corruption. `Ctrl+L` is the immediate shortcut.
- `/update`: Check for a newer stable GitHub release and, after explicit confirmation, verify, install, and restart into it.

Repo and runtime actions:

- `/diff`: Open the full-screen git diff.
- `/commit [message]`: Preview a commit for the selected project.
- `/push`: Push the selected project's branch.
- `/pull`: Pull the selected project's branch.
- `/resolve`: Start a fresh engineer session to resolve selected repo merge conflicts.
- `/run [command]`: Start the selected project's managed runtime.
- `/start [command]`: Alias for `/run`.
- `/restart`: Restart the selected project's managed runtime.
- `/run-edit`: Edit the saved runtime command.
- `/runtime`: Focus the runtime pane.
- `/ports`: Inspect project-local TCP listeners and confirm-stop external ones.
- `/stop`: Stop the selected project's managed runtime.

Organization, display, and cleanup:

- `/setup`: Open the Getting Started settings for first-run AI roles. Runs automatically on launch until you pick a backend.
- `/settings`: Full preferences with Getting Started first, then Providers & Models, LCAgent, Project Scope, Mobile, Browser, and Advanced.
- `/mobile`: Open the mobile access panel with the current listener, detected LAN phone URL, pairing code, and a direct jump to Mobile setup.
- `/sort <attention|recent>` (`o`): Change the project and agent-task ordering. Recent activity is the default; it groups activity by minute and orders ties alphabetically.
- `/tab [active|archived|toggle]` (`a`): Switch the project list between Active and Archived tabs.
- `/non-ai-folders <on|off>`: Show or hide folders that have no AI activity yet.
- `/focus <list|detail|runtime>`: Move focus between panes.
- `/pin` (`p`): Toggle pin on the selected project.
- `/read [all]`: Mark the selected project, or all visible projects, as read.
- `/unread`: Mark the selected project's latest completed assessment as unread.
- `/snooze [duration|off]`: Snooze the selected project, or clear snooze with `off`.
- `/unsnooze` (alias: `/clear-snooze`): Clear the selected project's snooze.
- `/sessions <on|off|toggle>`: Show or hide the Sessions section.
- `/events <on|off|toggle>`: Show or hide Recent events.
- `/task-actions`: Open archive/delete actions for the selected scratch task.
- `/archive`: Move the selected regular project to the Archived tab, or archive the selected scratch task out of the active task list.
- `/unarchive`: Move the selected archived project back to Active when it is in scope.
- `/ignore`: Hide the selected project's exact name.
- `/ignored`: Review ignored names and paths, then restore them.
- `/remove`: Confirm, then make the selected item go away safely. For regular projects, hides only the selected path. Aliases: `/delete`, `/forget`.
- `/quit`: Quit the TUI.

Inside the embedded Codex, Claude Code, or OpenCode pane:

Embedded providers expose LCR's local command subset, not every slash command from the native Codex, Claude Code, or OpenCode CLIs. Use the standalone provider CLI when you need a provider-native command that LCR has not wired into the pane yet.

- `/new`: Start a fresh session for the current provider.
- `/sessions [session-id]`: Open this project's session-history picker or jump to a saved session.
- `/resume [session-id]` and `/session [session-id]`: Aliases for `/sessions`.
- `/reconnect`: Restart the embedded provider helper and reconnect to the current session.
- `/model`: Change the model and reasoning settings for this and future embedded sessions of the same tool, including after restarting LCR.
- `/status`: Show the current provider/session status.
- `/compact`: Compact the embedded Codex conversation history when supported.
- `/review`: Ask embedded Codex to review uncommitted changes.
- `/chat`: Hide the embedded pane and open Chat over the main dashboard.

Inside Chat:

- `Enter`: Send a message or confirm a proposed action.
- `Esc` or backtick: Hide Chat and return to the dashboard; in-flight replies keep running.
- `/new [prompt]`: Start a fresh Chat session, optionally with the first prompt.
- `Ctrl+L`: Start a fresh empty Chat session.
- `Alt+Enter`: Add a newline without sending.

Chat sessions are saved as grep-friendly Markdown transcripts under the app data directory, for example `~/.little-control-room/help-chat-sessions/`. Recall searches those transcripts and continues to include legacy `boss-sessions/` history. Chat can inspect the current dashboard and project/task context, propose confirmable actions, delegate work, and report completions without assigning human names to AI work sessions. Project-list organization stays separate from project work: a request to add an existing folder to a named category such as Private gets one confirmation that registers the folder if needed and assigns the category, without creating a TODO, worktree, engineer session, Git repository, or repository content. For work in an existing loaded project, the default confirmation creates a tracked TODO, prepares a dedicated worktree, and starts a fresh engineer there; press `q` in that confirmation to add the TODO without starting it. Work in a brand-new or existing untracked Git repository instead uses a repository-setup confirmation before the same tracked TODO, worktree, and engineer launch. Starting and final launch/failure receipts are kept in the Chat transcript.

## Core Workflows

1. Start the dashboard with `lcroom tui` or `./lcroom tui`.
2. Move through projects with the arrow keys.
3. Press `Enter` to open or resume the selected project's latest embedded provider. Fresh projects and scratch tasks use the assistant chosen in their create dialog, which defaults to the last embedded provider you used when available.
4. Press `Esc` to hide the embedded session pane while it keeps working, then press `Enter` on that project to reopen it from the list.
5. Press `/` for commands, backtick or `/chat` for Chat, `f` to filter the project list instantly, or `a` to switch Active/Archived project tabs.

Most day-to-day use falls into a few buckets:

- **Run and monitor** — Use `/run` or `/start` to launch a saved runtime, `/restart` to bounce it, `/run-edit` to change the command, and `/stop` to shut it down. The command editor visibly lists project-derived completions, including package scripts, Make/Just targets, and Go entrypoints. Type a prefix and press `Tab` to complete it; use Up/Down to cycle matches. Press `Tab` or `/runtime` from the dashboard when you want to work directly in the runtime pane.

  [![Runtime pane focused on a running session](docs/screenshots/main-panel-live-runtime.png)](docs/screenshots/main-panel-live-runtime.png)

- **Resume agent work** — Use `/codex`, `/claude`, `/opencode`, or experimental `/lcagent` to pick up where you left off, and `/codex-new`, `/claude-new`, `/opencode-new`, or `/lcagent-new` when you want a fresh session. Inside the embedded pane, `/sessions`, `/resume`, `/session`, and `/reconnect` handle project-local session history or reattaching the helper. LCAgent also supports `/permissions` to explain Off/Low/Medium and `/permissions medium` or `/permissions low` to change the current session's next-turn autonomy. Embedded providers do not mirror every native provider slash command; the pane exposes the LCR commands above plus each provider's wired capabilities. LCAgent is a one-shot LCR-native worker with provider-backed tool calls and structured local JSONL artifacts.

  [![Embedded Codex conversation](docs/screenshots/codex-embedded.png)](docs/screenshots/codex-embedded.png)

- **Tune LCAgent permissions** — In `/settings`, Low lets LCAgent edit workspace files and run read-only or recognized verification commands, then asks before broader commands. Medium lets it run workspace-contained commands without repeated approvals. When a Low run asks for command approval, press `a` to approve once or `A` to switch that LCAgent run to Medium.

- **Keep broad access with a narrow deletion seatbelt** — LCR-managed embedded Codex sessions keep the selected launch preset's cross-directory access, including YOLO. Their guarded `rm` allows plain `rm -rf /tmp/<name>` only when every target's parent resolves below `/tmp`; other `rm` uses, absolute executables, and common wrapper forms remain blocked. LCAgent still denies direct `rm` through both bounded commands and managed-process launches at every permission level. This is protection against the common accidental command, not a complete deletion sandbox; targeted editing tools and other filesystem APIs still work, so backups remain important. See the [destructive-command safety design](docs/destructive_command_safety.md) for the threat model and known limits.

- **TODO-driven sessions** — Press `t` or use `/todo` to open a per-project TODO list. Add items you want an agent to work on, then press `Enter` on any item to start a fresh embedded session with that task as the prompt. The dialog shows the model that will be used and lets you pick the provider (Codex, Claude Code, OpenCode, or experimental LCAgent). New linked worktrees inherit the source project's saved run command and prepare Git submodules by default; repos can use [`.lcroom/worktrees.toml`](docs/worktree_prep.md) only when they need to opt out or customize preparation.

  [![TODO dialog with per-project task list](docs/screenshots/todo-dialog.png)](docs/screenshots/todo-dialog.png)

- **Review and organize** — Use `/diff` to inspect git changes, `/commit`, `/push`, and `/pull` when you are ready to sync or ship, and `/open` to jump to the project folder.

  | Diff View | Commit Preview | Image Diff |
  | --- | --- | --- |
  | [![Diff window](docs/screenshots/diff-view.png)](docs/screenshots/diff-view.png) | [![Commit preview dialog](docs/screenshots/commit-preview.png)](docs/screenshots/commit-preview.png) | [![Image diff with before/after previews](docs/screenshots/diff-view-image.png)](docs/screenshots/diff-view-image.png) |

- **Keep the list clean** — Use `a` or `/tab` to switch between Active and Archived project tabs, `/archive` and `/unarchive` to move regular projects between them, `f` or `/filter <text>` to narrow the project list, and `/pin` or `/snooze` to control attention. Archiving or unarchiving a repository root moves its linked worktrees with it. On scratch tasks, `/archive` moves the task into the scratch archive folder and out of the active task list. Use `/remove` when an item should go away by its safest matching action, `/ignore` for an exact-name hide rule, and `/ignored` to restore hidden names or paths.
- **Adjust setup** — `/setup` jumps to the Getting Started settings; `/settings` is the full preferences panel. Getting Started covers project-report AI, Chat, LCAgent, and mobile access through focused setup panels. Shared provider connection fields are reused inside those panels, so the same OpenAI/MLX/Ollama settings and LCAgent provider keys are edited from whichever feature needs them. Providers & Models stays compact: connection status plus global launch/display defaults. Project Scope controls include/exclude paths; category privacy is managed from `/category`. Mobile controls TUI auto-start, monitor-only or live-session message access, local or LAN reachability, port, and an optional advanced custom address. Browser sets the Playwright window policy. Advanced holds refresh thresholds and low-level tuning knobs. For embedded Codex and OpenCode sessions, LCR can isolate Playwright per session so browser-heavy work multitasks more cleanly in parallel, then surface the right managed browser window only when a human step is actually needed. Switch to `Classic browser behavior` if you want the original provider-owned flow, then use `/new-project` for repo-backed work and `/new-task` for quick scratch work.

For the full command list and detailed behavior, see [`docs/reference.md`](docs/reference.md).

## Costs

If Codex, OpenCode, Claude Code, MLX, or Ollama is available, LCR can use that local provider path for summaries, classification, commit help, and other background inference. MLX and Ollama run locally, so they do not create external API charges. Codex, OpenCode, and Claude Code follow whatever plan, key, or provider billing mode their own CLIs are using.

Claude Code is the subtle one. LCR invokes the local `claude` CLI for both embedded Claude sessions and Claude-backed background inference. Anthropic currently says Claude Pro/Max include Claude Code terminal usage when authenticated with Claude credentials, but `ANTHROPIC_API_KEY` makes Claude Code use API billing instead, and usage-credit continuation after plan limits is billed separately at standard API rates. See Anthropic's [Claude Code plan billing](https://support.claude.com/en/articles/11145838-use-claude-code-with-your-pro-or-max-plan) and [Claude Code cost](https://code.claude.com/docs/en/costs) docs for the current rules. Claude-backed background inference defaults to Haiku to keep usage lighter.

If you use an OpenAI API key for background analysis, LCR mainly spends tokens on summaries/classification and commit help. Chat can also use direct API inference through its separate `boss_chat_backend` compatibility setting; keep that in mind when reading cost estimates, since the project-analysis footer is not meant to be the full billing ledger for interactive chat.

With a few active projects, a full day is often around `$1` to `$2`, but treat that as a rough guide. The OpenAI dashboard is the billing source of truth.

Type `/setup` from the TUI or edit `~/.little-control-room/config.toml` to change the provider.

## Release Engineering

macOS release binaries are signed and notarized by the release workflow. A tagged release must have the Apple Developer credentials configured; otherwise the release fails instead of publishing unsigned macOS artifacts. The installer verifies published signatures locally; notarization acceptance is enforced during the GitHub release job.

GoReleaser marks official archives with `distribution=github`; that build metadata is what enables the in-app updater. Future package-manager builds should set their own distribution value so update ownership remains with the package manager. Release archives must continue to contain both `lcroom` and `lcagent`, `checksums.txt`, and GitHub-provided SHA-256 asset digests because the updater refuses incomplete or unverifiable releases.

Required GitHub secrets:

- `MACOS_SIGN_P12`: base64 contents of a Developer ID Application `.p12` certificate, or a path when running GoReleaser locally
- `MACOS_SIGN_PASSWORD`: password for the `.p12`
- `MACOS_NOTARY_KEY`: base64 contents of the App Store Connect API `.p8` key, or a path when running locally
- `MACOS_NOTARY_KEY_ID`: App Store Connect API key ID
- `MACOS_NOTARY_ISSUER_ID`: App Store Connect issuer UUID

For local archive smoke checks, run:

```bash
make release-snapshot
```

Snapshot archives under `dist/` are for local verification only, not public distribution.

## Notes

- Local state lives under `~/.little-control-room/`.
- For keys, slash commands, flags, and config details, see [`docs/reference.md`](docs/reference.md).

## Contacts

- Davide Pasca on X: [@109mae](https://x.com/109mae)
- NEWTYPE, Japan: [newtypekk.com](https://newtypekk.com/)

## Contributing

This is a utility that I constantly change to suit some specific needs. For this reason this is not a good candidate for external contributions, however, bug reports are welcome and anyone is free to fork and modify for their own use.
