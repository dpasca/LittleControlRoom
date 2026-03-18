# Little Control Room

Little Control Room (LCR) is a terminal dashboard for keeping track of AI work across multiple local projects.

It finds recent Codex and OpenCode activity, highlights what needs attention, and lets you jump back into work without bouncing between repos and terminal tabs.

## Screenshots

Click any screenshot to open the full-size PNG on GitHub.

<p align="center">
  <a href="docs/screenshots/main-panel.png">
    <img src="docs/screenshots/main-panel.png" alt="Little Control Room main dashboard overview with live agent activity in the list" width="850">
  </a>
</p>

| Dashboard | Runtime Pane |
| --- | --- |
| [![Little Control Room dashboard list view](docs/screenshots/main-panel.png)](docs/screenshots/main-panel.png) | [![Little Control Room runtime pane focused on a running FractalMech session](docs/screenshots/main-panel-live-runtime.png)](docs/screenshots/main-panel-live-runtime.png) |

| Embedded Session | Diff View |
| --- | --- |
| [![Little Control Room embedded Codex conversation](docs/screenshots/codex-embedded.png)](docs/screenshots/codex-embedded.png) | [![Little Control Room diff window](docs/screenshots/diff-view.png)](docs/screenshots/diff-view.png) |

| Commit Preview | Image Diff |
| --- | --- |
| [![Little Control Room commit preview dialog](docs/screenshots/commit-preview.png)](docs/screenshots/commit-preview.png) | [![Little Control Room diff window showing before and after image previews](docs/screenshots/diff-view-image.png)](docs/screenshots/diff-view-image.png) |

## What It Does

- Finds recent Codex and OpenCode sessions across your local projects
- Shows which projects are active, idle, or worth revisiting
- Lets you open, resume, or switch embedded Codex or OpenCode sessions directly from the dashboard
- Keeps common actions close at hand: refresh, pin, snooze, multiline project notes with list badges, managed per-project run commands with runtime/port badges, diff, commit, and push

## What it doesn't do (yet)

- Many Codex slash-commands are missing.
- Some OpenCode details are still catching up with Codex.

## Quick Start

Requirements:

- Go 1.25+
- Codex installed locally, capable of running in the terminal.
- OpenCode installed locally if you want embedded OpenCode sessions.
- An OpenAI API key saved in `~/.little-control-room/config.toml`. LCR requires this at startup and opens Settings until you save it.

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

On the first run, LCR opens `/settings` and stays there until you save `openai_api_key`. After that, you can keep editing the saved config from the dashboard or create `~/.little-control-room/config.toml` from [`docs/config.example.toml`](docs/config.example.toml).

## Slash Commands

The main TUI command palette opens with `/`.

- `/help`: Open the help panel.
- `/refresh`: Rescan projects and retry failed assessments.
- `/sort <attention|recent>`: Change the project ordering.
- `/view <ai|all>`: Switch between AI-linked and all tracked folders.
- `/settings`: Edit the saved OpenAI key, scope, and scan settings.
- `/new-project`: Create a project folder, or paste an existing project path to add it directly.
- `/open`: Open the selected project's folder in the system browser.
- `/run [command]`: Start the selected project's managed runtime.
- `/start [command]`: Alias for `/run`.
- `/restart`: Restart the selected project's managed runtime.
- `/run-edit`: Edit the saved runtime command.
- `/runtime`: Focus the runtime pane.
- `/stop`: Stop the selected project's managed runtime.
- `/note [clear]`: Edit or clear the selected project's note.
- `/diff`: Open the full-screen git diff.
- `/commit [message]`: Preview a commit for the selected project.
- `/push`: Push the selected project's branch.
- `/finish [message]`: Open the finish/commit flow.
- `/codex [prompt]`: Resume the latest Codex session or start one.
- `/codex-new [prompt]`: Start a fresh Codex session.
- `/opencode [prompt]`: Resume the latest OpenCode session or start one.
- `/opencode-new [prompt]`: Start a fresh OpenCode session.
- `/pin`: Toggle pin on the selected project.
- `/snooze [duration]`: Snooze the selected project.
- `/clear-snooze`: Clear the selected project's snooze.
- `/sessions <on|off|toggle>`: Show or hide the Sessions section.
- `/events <on|off|toggle>`: Show or hide Recent events.
- `/focus <list|detail|runtime>`: Move focus between panes.
- `/ignore`: Hide the selected project's exact name.
- `/ignored`: Review ignored names and restore them.
- `/forget`: Forget a selected missing folder.
- `/quit`: Quit the TUI.

Inside the embedded Codex or OpenCode pane:

- `/new`: Start a fresh session for the current provider.
- `/resume [session-id]`: Open the session picker or jump to a saved session.
- `/session [session-id]`: Alias for `/resume`.
- `/model`: Change the model and reasoning settings.
- `/status`: Show the current provider/session status.

## Everyday Workflow

1. Start the dashboard with `lcroom tui` or `./lcroom tui`.
2. Move through projects with the arrow keys.
3. Press `Enter` to open or resume the selected project's latest embedded provider.
4. Press `Esc` or `Alt+Up` to hide the embedded session pane while it keeps working, then press `Enter` on that project to reopen it from the list.
5. Press `/` to open the command palette for actions like refresh, pin, snooze, note, diff, commit, or push.

Use `/run` or `/start` to start the selected project's saved managed runtime. On the first run, LCR suggests a command from files like `bin/dev`, `package.json`, `Makefile`, `justfile`, or a simple Go entrypoint and lets you confirm or edit it before saving.

Use `/restart` to bounce the managed runtime with the saved or active command, `/run-edit` to change the saved run command later, and `/stop` to stop it entirely.

The main view now keeps a dedicated runtime pane beside the detail pane. Use `Tab` or `/runtime` to focus it, then use `Left` and `Right` to choose `Open URL`, `Restart`, or `Stop`, and `Enter` to run the selected action.

Use `/codex` or `/opencode` to resume the last session.

Use `/codex-new` or `/opencode-new` when you want a fresh session instead of resuming an existing one.

Inside the embedded Codex or OpenCode pane, use `/resume` or `/session` to open a provider-specific session picker for the current project, or `/resume <session-id>` to jump straight to that session.

Use `/settings` when you want to save your OpenAI API key, include or exclude paths, or change the default Codex launch mode.

Use `/ignore` on a selected project when you want to hide that exact project name, including Codex-generated worktrees that share the same folder name.

Use `/ignored` to review the hidden names and restore them later with `Enter`, so cleanup stays reversible.

Use `/open` to open the selected project's folder in your system browser.

Use `/note` to open a multiline note editor for the selected project, or `/note clear` to remove the saved note after confirmation. Projects with saved notes show an `N` badge in the main list. Press `n` for the same editor as a shortcut. Inside the note dialog, `Ctrl+Y` copies the whole current note to the system clipboard, and the `Copy...` action offers either `Whole note` or `Selected text`. In selection mode, press `Space` once to mark the start, move the cursor, and press `Space` again to copy the selected range.

Projects with an active managed runtime show a short summary in the `RUN` column. Detected ports appear inline there as `@3000`, while `!3000` marks a managed port conflict between tracked projects. The detail pane stays focused on project metadata, while the separate runtime pane shows the saved command, runtime state, detected ports and URL, conflicts or errors, and the captured output tail.

Use `/diff` to open a full-screen git diff for the selected project, with staged files listed first on the left, unstaged files below them, and a scrollable text or image preview on the right.

To create a new project, use the command `/new-project`. This will create a new directory or acknowledge an existing one, and add it to the list of projects to track. If you already have a full folder path copied from macOS, you can paste it directly into the path field, leave `Name` blank, and LCR will use the last folder name automatically.

## Costs

Using Codex or OpenCode inside LCR does not add any additional cost beyond what you would normally pay for those tools.

There are some OpenAI API costs from a key the user needs to provide (`openai_api_key` in the config file). These are for LCR to
summarize sessions, classify them, help with commit messages.
These operations are done with cheaper models like `gpt-5-mini` and a relatively small amount of tokens, so the cost should be minimal, but it's important to keep an eye on usage from the OpenAI dashboard, to avoid any surprises.

## Notes

- Local state lives under `~/.little-control-room/`.
- For keys, slash commands, flags, and config details, see [`docs/reference.md`](docs/reference.md).

## Contacts

- Davide Pasca on X: [@109mae](https://x.com/109mae)
- NEWTYPE, Japan: [newtypekk.com](https://newtypekk.com/)

## Contributing

This is a utility that I constantly change to suit some specific needs. For this reason this is not a good candidate for external contributions, however, bug reports are welcome and anyone is free to fork and modify for their own use.
