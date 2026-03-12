# Little Control Room Reference

This page keeps the command, keybinding, and configuration details that are useful once you are already up and running.

## CLI Commands

- `lcroom tui` opens the interactive dashboard
- `lcroom scan` rescans artifacts and refreshes the local store
- `lcroom classify` scans and drains the latest-session AI classification queue
- `lcroom doctor` prints a diagnostic report from the current cached store
- `lcroom doctor --scan` rescans first, then prints the diagnostic report
- `lcroom screenshots` renders the curated docs screenshot set from a screenshot config
- `lcroom scope` shows the effective include and exclude scope for this run
- `lcroom serve` starts the optional read-only REST and WebSocket server

`lcroom classify` requires `OPENAI_API_KEY`.

## Config File

- Preferred default path: `~/.little-control-room/config.toml`
- Override path: `--config /path/to/config.toml`
- Example file: [`config.example.toml`](config.example.toml)
- Supported format: TOML

The TUI `/settings` modal writes these values:

- `include_paths`
- `exclude_paths`
- `exclude_project_patterns`
- `codex_launch_preset`
- `interval`
- `active-threshold`
- `stuck-threshold`

Minimal config example:

```toml
include_paths = [
  "~/dev/repos",
]

exclude_paths = []
exclude_project_patterns = []
codex_launch_preset = "yolo"
```

Saved-from-TUI example:

```toml
include_paths = [
  "~/dev/repos",
]

exclude_paths = []
exclude_project_patterns = [
  "client-*",
  "archive-*",
]
codex_launch_preset = "yolo"

interval = "60s"
active-threshold = "20m"
stuck-threshold = "4h"
```

## Screenshots

- `make screenshots` renders a curated set of fixed-size PNG terminal screenshots for docs.
- Default local config path: `./screenshots.local.toml`
- Override the screenshot config path with `lcroom screenshots --screenshot-config /path/to/screenshots.local.toml`
- Override the output directory with `lcroom screenshots --output-dir /tmp/lcroom-shots`
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
```

The generated set currently includes:

- `main-panel.png`
- `main-panel-live-cx.png`
- `codex-embedded.png`
- `diff-view.png`
- `diff-view-image.png`
- `commit-preview.png`

`project_filters`, `selected_project`, and `live_codex_project` match against the project name, the repo directory name, and simple acronyms such as `LCR` for `LittleControlRoom`.
Use `demo_data = true` when you want a reproducible sample set, or a local config file when you want screenshots from your own curated projects.

## TUI Keys

- `/` open the command palette
- `↑/↓` move selection
- `Enter` open or resume Codex for the selected project
- `Esc` hide the visible Codex pane
- `Alt+Up` hide the visible Codex pane
- `Alt+Down` open the Codex session picker and history overlay
- `Alt+[` jump to the previous live Codex session
- `Alt+]` jump to the next live Codex session
- `PgUp/PgDn/Home/End` fast scrolling in long project lists
- `Tab` or `Shift+Tab` switch focus between list and detail
- `o` toggle sort mode between `attention` and `recent activity`
- `p` pin toggle
- `s` snooze for 1 hour
- `S` clear snooze
- `n` add or edit note
- `q` quit

While the Codex pane is visible:

- `Enter` sends a prompt when idle and steers the active turn when Codex is busy
- `Alt+Enter` or `Ctrl+J` inserts a newline
- `Ctrl+V` attaches a clipboard image when available
- `Backspace` on an inline `[Image #n]` marker removes that attachment
- `Alt+L` expands or collapses dense command, file, and tool transcript blocks
- `Ctrl+C` interrupts the active turn when busy and closes the session when idle

While the diff screen is visible:

- The left pane groups `Staged` files first and `Unstaged` files below them
- `-` stages the selected file when it is unstaged, and unstages it when it already has staged changes
- `Up/Down` or `j/k` moves between files when the file list is focused
- `Enter`, `Right`, or `Tab` moves focus into the diff pane
- `Left` or `Tab` moves focus back to the file list
- `PgUp/PgDn/Home/End` pages or jumps within the focused pane
- `Alt+Up` returns to the main project list
- `Esc` closes the diff screen

## Slash Commands

The TUI command palette opens with `/` and supports autocomplete with `Tab`.

- `/help`
- `/refresh`
- `/sort attention`
- `/sort recent`
- `/view ai`
- `/view all`
- `/settings`
- `/diff`
- `/codex`
- `/codex continue from the last breakpoint`
- `/codex-new sketch a plan for this repo`
- `/commit`
- `/commit tighten git status parsing`
- `/push`
- `/finish`
- `/finish polish palette and ship`
- `/pin`
- `/snooze 4h`
- `/clear-snooze`
- `/sessions toggle`
- `/events off`
- `/focus detail`
- `/forget`
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

- `Enter` on the selected project opens an embedded Codex pane inside Little Control Room.
- `/codex` resumes the selected project's latest known Codex session when available, otherwise it starts a new one.
- `/codex-new` always starts a fresh session.
- `codex_launch_preset` controls how Codex is launched. The default is `yolo`.
- CLI flags override config file values.
