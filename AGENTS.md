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

Interactive TUI verification needs a real PTY-backed terminal. In non-interactive command runners, `make tui` or `make tui-parallel` may fail trying to open `/dev/tty`; treat that as an environment limitation unless the same failure reproduces from an actual terminal session.

## TUI Responsiveness Guardrails

- Treat the Bubble Tea `Update`/render path as a UI thread: do not do blocking disk I/O, git calls, DB work, process inspection, or lock-contended session reads there.
- In TUI code, prefer cached state or non-blocking snapshot APIs. If fresh data may block, return cached data and queue an async refresh instead.
- Route potentially slow work through `tea.Cmd`, background services, or dedicated workers. Do not call slow service or session methods directly from render helpers just because they are "usually fast".
- Coalesce refreshes and invalidations by project or panel. Prefer "mark stale and reload once" over stacking duplicate reload commands.
- Every mutating dialog/action should have explicit `Busy`/`Submitting` state and ignore repeat activation while work is already in flight.
- When a UI feature needs derived data from live subsystems, design a small view-model/cache boundary first instead of letting the UI reach directly into the subsystem.
- When touching refresh loops, event fan-out, or embedded session state, check `/perf` and treat spinner-gap / UI-stall signals as real regressions.
- Make failure states explicit in the UI. Never leave a panel looking like it is still loading if the real state is an error or blocked action.

## Debugging Multi-Instance Runs

- By default, long-lived `lcroom` modes such as `tui`, `serve`, and `classify` now refuse to share the same DB with another active runtime.
- For intentional short-lived local debugging overlap only, re-run with `--allow-multiple-instances`.
- Treat `--allow-multiple-instances` as a temporary escape hatch for dev/debug sessions, not the normal way to run Little Control Room day to day.
- Prefer `make tui-parallel` for isolated TUI experiments, and run `make tui-parallel-clean` periodically so stale `/tmp/lcroom-parallel-*` sandboxes do not pile up and confuse later debugging.
- When touching startup, project loading, or refresh flows, make failure states explicit in the UI/status text. Do not leave the app looking like it is still "Loading..." when the real issue is an error.
