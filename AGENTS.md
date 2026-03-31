# Little Control Room Agent Notes

## Purpose

Little Control Room is a control center for AI tasks with multi-project visibility and prioritization.

Core intent for this repo:

1. Keep monitoring logic UI-agnostic.
2. Treat Codex disk artifacts as source-of-truth inputs.
3. Keep scoring reasons transparent and inspectable.

## Project Snapshot Protocol

1. Read `STATUS.md` first at the start of each session for the current project snapshot.
2. Treat `STATUS.md` as durable orientation, not a running work log.
3. Update `STATUS.md` only when project-wide goals, architecture assumptions, or long-lived priorities materially change.
4. Record branch-specific or session-specific implementation history in git commits, PR descriptions, TODOs, or dedicated docs instead of `STATUS.md`.
5. Keep `docs/codex_cli_footprint.md` in sync if detector assumptions change.

## Codex Artifact Assumption

Current default assumption:

- Codex artifacts are primarily under `~/.codex` (user-home global directory), not per-project.
- Project mapping is derived from session metadata `cwd` fields in session logs.
- OpenCode artifacts are primarily under `~/.local/share/opencode` (not per-project), mapped via session/project metadata.

Do not silently switch to project-local `.codex` assumptions unless validated and documented.

## Validation

Use these checks before finishing:

- `make test`
- `make scan`
- `make doctor`

Run `make tui` for interactive verification when UI behavior is touched.

## Debugging Multi-Instance Runs

- By default, long-lived `lcroom` modes such as `tui`, `serve`, and `classify` now refuse to share the same DB with another active runtime.
- For intentional short-lived local debugging overlap only, re-run with `--allow-multiple-instances`.
- Treat `--allow-multiple-instances` as a temporary escape hatch for dev/debug sessions, not the normal way to run Little Control Room day to day.
- Prefer `make tui-parallel` for isolated TUI experiments, and run `make tui-parallel-clean` periodically so stale `/tmp/lcroom-parallel-*` sandboxes do not pile up and confuse later debugging.
- When touching startup, project loading, or refresh flows, make failure states explicit in the UI/status text. Do not leave the app looking like it is still "Loading..." when the real issue is an error.
