# Little Control Room Status Archive

This file holds condensed historical notes pruned out of `STATUS.md` on 2026-03-08.

Use it when an older decision still matters. For exact raw chronology beyond these summaries, rely on git history.

## 2026-03-05 to 2026-03-06

- Built the monitoring foundation around `scan`, `doctor`, SQLite persistence, transparent attention scoring, and the event bus.
- Confirmed the main artifact assumption: Codex sessions live under `~/.codex` and project mapping comes from session metadata `cwd`, not from per-project `.codex` folders.
- Added support for:
  - modern and legacy Codex JSONL parsing
  - OpenCode session detection from `~/.local/share/opencode/opencode.db`
  - artifact-first project discovery with git metadata as supporting context
- Grew the service/store layer to handle:
  - project move detection with path aliases
  - missing-folder detection and forget/hide behavior
  - repo dirtiness and remote-sync state
  - latest-session assessments with structured categories
- Improved the TUI with:
  - relative activity labels
  - AI-only vs all-folder visibility
  - compact status/state presentation
  - clearer repo-health presentation
  - compact token usage in the footer/help
- Created the first GitHub remote for the repo and pushed `master` to `origin/master`.

## 2026-03-07

- Reworked the TUI into a stacked list/detail layout with proper height accounting.
- Added explicit pane focus, scrollable detail content, and focus-aware sizing/hints.
- Added the slash-command system:
  - shared command registry
  - centered command palette overlay
  - autocomplete
  - scrollable suggestion list
- Added workflow commands on top of the palette, including commit preview, finish, and push flows.
- Added AI-assisted commit message generation with tighter prompt guidance so subjects favor the main outcome over changelog-style lists.
- Reduced SQLite lock contention by switching store connections to WAL-oriented pragmas and then made `doctor` cached by default, with `doctor --scan` as the explicit fresh-scan path.
- Renamed the active command/module surface to `lcroom`, migrated old data-dir defaults, and added compatibility migration/aliasing for historical app paths.
- Persisted Codex turn-completion signals so latest-session classification could distinguish finished turns from mid-task commentary more reliably.

## Early 2026-03-08

- Added demo-friendly project-name filtering and made that setting hot-reload in the TUI.
- Removed the legacy `controlcenter` CLI compatibility path from the active surface.
- Tightened the project list for narrow terminals by simplifying markers and reclaiming column width.
- Added first-phase in-app Codex CLI launch:
  - `/codex`
  - `/codex-new`
  - smart resume against the latest known Codex session
  - launch from the selected project
- Added settings-backed Codex launch presets with `yolo` as the default mode and clearer copy about the interaction cost of safer presets.
- Fixed manual refresh so it can retry failed same-snapshot assessments after transient network/API issues.
- Compacted the settings modal by moving long field descriptions into a bottom help area and windowing the visible field rows around the current selection.

## Archive Policy

- Keep `STATUS.md` focused on current state and the latest active work burst.
- Move anything that has become historical context into this archive instead of keeping long raw logs in the primary handoff file.
