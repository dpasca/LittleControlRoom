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

The first read-only mobile slice starts with the main TUI and shares its live store, service configuration, and update events:

```bash
lcroom tui
```

Open `http://127.0.0.1:7777` to use the project/category dashboard, project detail, and read-only active/recent engineer transcripts. Use `/mobile` in the TUI to check the URL. If the port is already occupied, the TUI keeps running and reports the mobile server failure in its top status line.

Pass an explicit LAN address to keep the live session manager and mobile client in the same process, for example `lcroom tui --listen 192.168.0.6:7777`. There is no authentication yet, so only expose it on a trusted network.

`lcroom serve` remains available for a standalone preview and accepts the same `--listen` flag. It can read recorded engineer transcripts from detected artifacts, but only the TUI-hosted client can overlay the richer in-memory live transcript. A standalone preview also needs its own database runtime lease.

<p align="center">
  <a href="docs/screenshots/setup.png">
    <img src="docs/screenshots/setup.png" alt="Little Control Room setup screen showing Getting Started settings for project reports, boss chat, and optional LCAgent details" width="850">
  </a>
</p>

## Background AI Backends

LCR separates embedded session providers from the backend used for background work such as summaries, classification, commit help, and TODO worktree suggestions.

- Embedded sessions today are Codex, OpenCode, and Claude Code.
- Background AI can run through Codex, OpenCode, Claude Code, MLX, Ollama, or direct OpenAI API.
- Boss chat has its own `boss_chat_backend`, so interactive high-level chat can use direct API inference through OpenAI API, MLX, or Ollama without forcing summaries/classification off Codex, OpenCode, Claude Code, MLX, or Ollama. If it is not configured yet, `/boss` offers to jump straight to the Boss chat setup card.
- Claude Code usage follows the local `claude` CLI authentication mode. Current Anthropic docs say Pro/Max plan terminal usage counts against plan limits when Claude Code is authenticated with Claude credentials, while `ANTHROPIC_API_KEY` or explicit usage-credit continuation can bill separately at API rates.
- MLX uses its OpenAI-compatible local endpoint. Ollama discovery still uses its OpenAI-compatible model list, while background generation uses Ollama's native generate endpoint so thinking models can return usable JSON/text with thinking disabled.
- Ollama thinking stays off by default for background automation and structured helper calls. When Boss chat uses Ollama, native `think: true` is on by default for Boss answer text only; its setup panel includes a Boss Ollama thinking toggle if you want final-content-only responses.

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

- `/help` (`?`): Open the help panel.
- `/filter [text|clear]` (`f`): Temporarily narrow the whole dashboard to matching project names.
- `/todo` (`t`): Open the TODO list for the selected project. Add items, toggle done, and start a fresh embedded session from any item.
- `/open`: Open the selected project's folder in the system browser.
- `/new-project [--assistant codex|opencode|claude|lcagent]`: Create a project folder, or use path suggestions/paste an existing project path to add it directly. The dialog also lets you choose which assistant `Enter` should open first for the new item, defaulting to the last embedded provider you used when available.
- `/new-task [--assistant codex|opencode|claude|lcagent] [request]`: Create a scratch task folder under the default task root. Optional request text seeds the temporary task name, and the assistant flag preselects the first embedded provider for `Enter`; without a flag, the task picker defaults to the last embedded provider you used when available.
- `/codex [prompt]`, `/opencode [prompt]`, `/claude [prompt]`, `/lcagent [prompt]`: Resume the latest session for that provider, or start one.
- `/codex-new [prompt]`, `/opencode-new [prompt]`, `/claude-new [prompt]`, `/lcagent-new [prompt]`: Start a fresh embedded session.
- `/refresh`: Rescan projects and retry failed assessments.

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
- `/settings`: Full preferences with Getting Started first, then Providers & Models, LCAgent, Project Scope, Browser, and Advanced.
- `/sort <attention|recent>` (`o`): Change the project ordering.
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

Inside boss chat:

- `Alt+1` through `Alt+8`: Open the matching marked Boss Desk task or project in an embedded engineer session.
- `Alt+Up`: Hide Boss Chat and return to the classic TUI; in-flight replies keep running.
- `/new [prompt]`: Start a fresh boss chat session, optionally with the first prompt.
- `/sessions [session-id]`: Open the saved-session picker, or switch directly by ID.
- `/session [session-id]` and `/resume [session-id]`: Aliases for `/sessions`.
- `/help`: Show boss chat slash commands.
- `/boss off`: Hide Boss Chat and return to the classic TUI; in-flight replies keep running.

Boss chat sessions are saved as grep-friendly Markdown transcripts under the app data directory, for example `~/.little-control-room/boss-sessions/`. The `/sessions` picker uses those files for human session switching, and Boss Chat recall searches the same transcripts. Codex, OpenCode, and Claude Code transcripts are called engineer sessions in Boss Chat so they stay distinct from Boss Chat's own transcript.

## Core Workflows

1. Start the dashboard with `lcroom tui` or `./lcroom tui`.
2. Move through projects with the arrow keys.
3. Press `Enter` to open or resume the selected project's latest embedded provider. Fresh projects and scratch tasks use the assistant chosen in their create dialog, which defaults to the last embedded provider you used when available.
4. Press `Esc` to hide the embedded session pane while it keeps working, then press `Enter` on that project to reopen it from the list.
5. Press `/` for commands, `f` to filter the project list instantly, `a` to switch Active/Archived project tabs, or `b` for Boss Chat.

Most day-to-day use falls into a few buckets:

- **Run and monitor** — Use `/run` or `/start` to launch a saved runtime, `/restart` to bounce it, `/run-edit` to change the command, and `/stop` to shut it down. Press `Tab` or `/runtime` when you want to work directly in the runtime pane.

  [![Runtime pane focused on a running session](docs/screenshots/main-panel-live-runtime.png)](docs/screenshots/main-panel-live-runtime.png)

- **Resume agent work** — Use `/codex`, `/claude`, `/opencode`, or experimental `/lcagent` to pick up where you left off, and `/codex-new`, `/claude-new`, `/opencode-new`, or `/lcagent-new` when you want a fresh session. Inside the embedded pane, `/sessions`, `/resume`, `/session`, and `/reconnect` handle project-local session history or reattaching the helper. LCAgent also supports `/permissions` to explain Off/Low/Medium and `/permissions medium` or `/permissions low` to change the current session's next-turn autonomy. Embedded providers do not mirror every native provider slash command; the pane exposes the LCR commands above plus each provider's wired capabilities. LCAgent is a one-shot LCR-native worker with provider-backed tool calls and structured local JSONL artifacts.

  [![Embedded Codex conversation](docs/screenshots/codex-embedded.png)](docs/screenshots/codex-embedded.png)

- **Tune LCAgent permissions** — In `/settings`, Low lets LCAgent edit workspace files and run read-only or recognized verification commands, then asks before broader commands. Medium lets it run workspace-contained commands without repeated approvals. When a Low run asks for command approval, press `a` to approve once or `A` to switch that LCAgent run to Medium.

- **TODO-driven sessions** — Press `t` or use `/todo` to open a per-project TODO list. Add items you want an agent to work on, then press `Enter` on any item to start a fresh embedded session with that task as the prompt. The dialog shows the model that will be used and lets you pick the provider (Codex, Claude Code, OpenCode, or experimental LCAgent). Linked worktrees prepare Git submodules by default; repos can use [`.lcroom/worktrees.toml`](docs/worktree_prep.md) only when they need to opt out or customize preparation.

  [![TODO dialog with per-project task list](docs/screenshots/todo-dialog.png)](docs/screenshots/todo-dialog.png)

- **Review and organize** — Use `/diff` to inspect git changes, `/commit`, `/push`, and `/pull` when you are ready to sync or ship, and `/open` to jump to the project folder.

  | Diff View | Commit Preview | Image Diff |
  | --- | --- | --- |
  | [![Diff window](docs/screenshots/diff-view.png)](docs/screenshots/diff-view.png) | [![Commit preview dialog](docs/screenshots/commit-preview.png)](docs/screenshots/commit-preview.png) | [![Image diff with before/after previews](docs/screenshots/diff-view-image.png)](docs/screenshots/diff-view-image.png) |

- **Keep the list clean** — Use `a` or `/tab` to switch between Active and Archived project tabs, `/archive` and `/unarchive` to move regular projects between them, `f` or `/filter <text>` to narrow the project list, and `/pin` or `/snooze` to control attention. On scratch tasks, `/archive` moves the task into the scratch archive folder and out of the active task list. Use `/remove` when an item should go away by its safest matching action, `/ignore` for an exact-name hide rule, and `/ignored` to restore hidden names or paths.
- **Adjust setup** — `/setup` jumps to the Getting Started settings; `/settings` is the full preferences panel. Getting Started covers project-report AI, boss chat, and LCAgent through focused setup panels. Shared provider connection fields are reused inside those panels, so the same OpenAI/MLX/Ollama settings and LCAgent provider keys are edited from whichever feature needs them. Providers & Models stays compact: connection status plus global launch/display defaults. Project Scope controls include/exclude paths; category privacy is managed from `/category`. Browser sets the Playwright window policy. Advanced holds refresh thresholds and low-level tuning knobs. For embedded Codex and OpenCode sessions, LCR can isolate Playwright per session so browser-heavy work multitasks more cleanly in parallel, then surface the right managed browser window only when a human step is actually needed. Switch to `Classic browser behavior` if you want the original provider-owned flow, then use `/new-project` for repo-backed work and `/new-task` for quick scratch work.

For the full command list and detailed behavior, see [`docs/reference.md`](docs/reference.md).

## Costs

If Codex, OpenCode, Claude Code, MLX, or Ollama is available, LCR can use that local provider path for summaries, classification, commit help, and other background inference. MLX and Ollama run locally, so they do not create external API charges. Codex, OpenCode, and Claude Code follow whatever plan, key, or provider billing mode their own CLIs are using.

Claude Code is the subtle one. LCR invokes the local `claude` CLI for both embedded Claude sessions and Claude-backed background inference. Anthropic currently says Claude Pro/Max include Claude Code terminal usage when authenticated with Claude credentials, but `ANTHROPIC_API_KEY` makes Claude Code use API billing instead, and usage-credit continuation after plan limits is billed separately at standard API rates. See Anthropic's [Claude Code plan billing](https://support.claude.com/en/articles/11145838-use-claude-code-with-your-pro-or-max-plan) and [Claude Code cost](https://code.claude.com/docs/en/costs) docs for the current rules. Claude-backed background inference defaults to Haiku to keep usage lighter.

If you use an OpenAI API key for background analysis, LCR mainly spends tokens on summaries/classification and commit help. Boss chat can also use direct API inference through its separate `boss_chat_backend`; keep that in mind when reading cost estimates, since the project-analysis footer is not meant to be the full billing ledger for interactive chat.

With a few active projects, a full day is often around `$1` to `$2`, but treat that as a rough guide. The OpenAI dashboard is the billing source of truth.

Type `/setup` from the TUI or edit `~/.little-control-room/config.toml` to change the provider.

## Release Engineering

macOS release binaries are signed and notarized by the release workflow. A tagged release must have the Apple Developer credentials configured; otherwise the release fails instead of publishing unsigned macOS artifacts. The installer verifies published signatures locally; notarization acceptance is enforced during the GitHub release job.

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
