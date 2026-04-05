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

## What It Does Not Do Yet

- Many Codex slash-commands are missing.
- Embedded Claude Code support is now available through Claude's headless CLI flow, but it is still an MVP: it now has a usable model picker with saved preferences, while approval parity, compact, and attachments are not fully wired yet.

## Quick Start

Requirements:

- Go 1.25+
- Codex installed locally, capable of running in the terminal.
- OpenCode installed locally if you want embedded OpenCode sessions.
- Claude Code installed locally if you want embedded Claude Code sessions.
- At least one AI backend configured: Codex, OpenCode, Claude Code, MLX, Ollama, or an OpenAI API key.

Build and launch from this repo:

```bash
make build
./lcroom tui
```

Or install the CLI to your Go bin:

```bash
make install
lcroom tui
```

On the first run, LCR opens `/setup` if no AI backend is configured. From there you can use Codex, OpenCode, Claude Code, MLX, Ollama, an OpenAI API key, or continue without AI and come back later. Claude-backed background inference currently defaults to Haiku to keep usage lighter. MLX and Ollama use their OpenAI-compatible local endpoints, with defaults of `http://127.0.0.1:8080/v1` for MLX and `http://127.0.0.1:11434/v1` for Ollama.

<p align="center">
  <a href="docs/screenshots/setup.png">
    <img src="docs/screenshots/setup.png" alt="Little Control Room setup screen showing Codex, OpenCode, Claude Code, MLX, Ollama, OpenAI API key, and disabled AI options" width="850">
  </a>
</p>

## Background AI Backends

LCR separates embedded session providers from the backend used for background work such as summaries, classification, commit help, and TODO worktree suggestions.

- Embedded sessions today are Codex, OpenCode, and Claude Code.
- Background AI can run through Codex, OpenCode, Claude Code, MLX, Ollama, or a direct OpenAI API key.
- MLX and Ollama use OpenAI-compatible local endpoints, so they fit into the same background inference path without a separate integration surface.

For local inference, the practical setup is:

- Pick `MLX` or `Ollama` in `/setup`.
- Leave the endpoint fields blank in `/settings` if you want the defaults.
- Or override them in `/settings` if your local server runs elsewhere.

Default local endpoints:

- MLX: `http://127.0.0.1:8080/v1`
- Ollama: `http://127.0.0.1:11434/v1`

<p align="center">
  <a href="docs/screenshots/settings-local-backends.png">
    <img src="docs/screenshots/settings-local-backends.png" alt="Little Control Room settings screen showing MLX and Ollama local endpoint fields" width="850">
  </a>
</p>

## Slash Commands

The main TUI command palette opens with `/`.

- `/help`: Open the help panel.
- `/refresh`: Rescan projects and retry failed assessments.
- `/sort <attention|recent>`: Change the project ordering.
- `/view <ai|all>`: Switch between AI-linked and all tracked folders.
- `/setup`: Choose and check the AI backend for summaries, classification, commit help, and other background inference.
- `/settings`: Edit scope, API keys, local endpoint overrides, filters, and scan settings.
- `/filter [text|clear]`: Temporarily narrow the whole dashboard to matching project names.
- `/new-project`: Create a project folder, or paste an existing project path to add it directly.
- `/open`: Open the selected project's folder in the system browser.
- `/run [command]`: Start the selected project's managed runtime.
- `/start [command]`: Alias for `/run`.
- `/restart`: Restart the selected project's managed runtime.
- `/run-edit`: Edit the saved runtime command.
- `/runtime`: Focus the runtime pane.
- `/stop`: Stop the selected project's managed runtime.
- `/todo`: Open the TODO list for the selected project. Add items, toggle done, and start a fresh embedded session from any item.
- `/diff`: Open the full-screen git diff.
- `/commit [message]`: Preview a commit for the selected project.
- `/push`: Push the selected project's branch.
- `/codex [prompt]`: Resume the latest Codex session or start one.
- `/codex-new [prompt]`: Start a fresh Codex session.
- `/claude [prompt]`: Resume the latest Claude Code session or start one.
- `/claude-new [prompt]`: Start a fresh Claude Code session.
- `/opencode [prompt]`: Resume the latest OpenCode session or start one.
- `/opencode-new [prompt]`: Start a fresh OpenCode session.
- `/pin`: Toggle pin on the selected project.
- `/read [all]`: Mark the selected project, or all visible projects, as read.
- `/unread`: Mark the selected project's latest completed assessment as unread.
- `/snooze [duration|off]`: Snooze the selected project, or clear snooze with `off`.
- `/unsnooze` (alias: `/clear-snooze`): Clear the selected project's snooze.
- `/sessions <on|off|toggle>`: Show or hide the Sessions section.
- `/events <on|off|toggle>`: Show or hide Recent events.
- `/focus <list|detail|runtime>`: Move focus between panes.
- `/ignore`: Hide the selected project's exact name.
- `/ignored`: Review ignored names and restore them.
- `/forget`: Forget a selected missing folder.
- `/quit`: Quit the TUI.

Inside the embedded Codex, Claude Code, or OpenCode pane:

- `/new`: Start a fresh session for the current provider.
- `/resume [session-id]`: Open the session picker or jump to a saved session.
- `/session [session-id]`: Alias for `/resume`.
- `/reconnect`: Restart the embedded provider helper and reconnect to the current session.
- `/model`: Change the model and reasoning settings for this and future embedded sessions of the same tool, including after restarting LCR.
- `/status`: Show the current provider/session status.

## Core Workflows

1. Start the dashboard with `lcroom tui` or `./lcroom tui`.
2. Move through projects with the arrow keys.
3. Press `Enter` to open or resume the selected project's latest embedded provider.
4. Press `Esc` or `Alt+Up` to hide the embedded session pane while it keeps working, then press `Enter` on that project to reopen it from the list.
5. Press `/` for commands, or `f` to filter the project list instantly.

Most day-to-day use falls into a few buckets:

- **Run and monitor** — Use `/run` or `/start` to launch a saved runtime, `/restart` to bounce it, `/run-edit` to change the command, and `/stop` to shut it down. Press `Tab` or `/runtime` when you want to work directly in the runtime pane.

  [![Runtime pane focused on a running session](docs/screenshots/main-panel-live-runtime.png)](docs/screenshots/main-panel-live-runtime.png)

- **Resume agent work** — Use `/codex`, `/claude`, or `/opencode` to pick up where you left off, and `/codex-new`, `/claude-new`, or `/opencode-new` when you want a fresh session. Inside the embedded pane, `/resume`, `/session`, and `/reconnect` handle switching sessions or reattaching the helper. Claude Code support currently uses a headless CLI MVP, so approvals and attachments are still less complete than Codex/OpenCode, even though `/model` now offers saved Claude aliases and reasoning levels.

  [![Embedded Codex conversation](docs/screenshots/codex-embedded.png)](docs/screenshots/codex-embedded.png)

- **TODO-driven sessions** — Press `t` or use `/todo` to open a per-project TODO list. Add items you want an agent to work on, then press `Enter` on any item to start a fresh embedded session with that task as the prompt. The dialog shows the model that will be used and lets you pick the provider (Codex, Claude Code, or OpenCode).

  [![TODO dialog with per-project task list](docs/screenshots/todo-dialog.png)](docs/screenshots/todo-dialog.png)

- **Review and organize** — Use `/diff` to inspect git changes, `/commit` and `/push` when you are ready to ship, and `/open` to jump to the project folder.

  | Diff View | Commit Preview | Image Diff |
  | --- | --- | --- |
  | [![Diff window](docs/screenshots/diff-view.png)](docs/screenshots/diff-view.png) | [![Commit preview dialog](docs/screenshots/commit-preview.png)](docs/screenshots/commit-preview.png) | [![Image diff with before/after previews](docs/screenshots/diff-view-image.png)](docs/screenshots/diff-view-image.png) |

- **Keep the list clean** — Use `f` or `/filter <text>` to narrow the project list, `/pin` and `/snooze` to control attention, `/ignore` and `/ignored` to hide or restore exact project names, and `/forget` to remove a missing folder.
- **Adjust setup** — Use `/settings` for API keys, MLX/Ollama endpoint overrides, paths, and defaults, and `/new-project` when you want to add something new to the dashboard.

For the full command list and detailed behavior, see [`docs/reference.md`](docs/reference.md).

## Costs

If Codex, OpenCode, Claude Code, MLX, or Ollama is available, LCR can use that local provider path for summaries, classification, commit help, and other background inference. On a flat-rate plan, or when you are running local inference, that usually means no extra LCR API cost from LCR itself. Claude-backed background inference currently defaults to Haiku to keep usage lighter.

If you use an OpenAI API key instead, LCR mainly spends tokens on summaries/classification and commit help. The footer shows a live estimate for that API usage only.

With a few active projects, a full day is often around `$1` to `$2`, but treat that as a rough guide. The OpenAI dashboard is the billing source of truth.

Type `/setup` from the TUI or edit `~/.little-control-room/config.toml` to change the provider.

## Notes

- Local state lives under `~/.little-control-room/`.
- For keys, slash commands, flags, and config details, see [`docs/reference.md`](docs/reference.md).

## Contacts

- Davide Pasca on X: [@109mae](https://x.com/109mae)
- NEWTYPE, Japan: [newtypekk.com](https://newtypekk.com/)

## Contributing

This is a utility that I constantly change to suit some specific needs. For this reason this is not a good candidate for external contributions, however, bug reports are welcome and anyone is free to fork and modify for their own use.
