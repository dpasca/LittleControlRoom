# Little Control Room Status

Last updated: 2026-03-17 03:10 JST (JST)

## Current State

Implemented milestone:

1. Phase 0 footprint discovery doc + fixtures
2. Phase 1 monitoring foundation (`scan`, `doctor`, SQLite store, attention scoring, event bus)
3. Phase 2 TUI (`tui`) with refresh, filters, pin, snooze, note, command palette, git workflow actions, and a first managed per-project runtime lane
4. Optional Phase 3 skeleton (`serve`) with read-only REST + WS stream

## Confirmed Footprint Assumption

Codex artifacts are currently observed under the user-home global path:

- `~/.codex`

Project linkage is inferred from session metadata (`cwd`) in session logs and persisted into the Little Control Room store.

No project-local `.codex` footprint has been observed on this machine so far.

Current embedded Codex transport assumption:

- The installed `codex app-server` schema supports structured user input beyond plain text, including local image attachments, and Little Control Room now uses that richer input path for the embedded pane.
- The installed schema on this machine exposes `turn/start`, `turn/steer`, and `turn/interrupt`, but no separate queued-turn method; Little Control Room therefore supports explicit steer of the active turn, not a distinct queued-next-turn action.
- The installed schema's `turn/start` params also support per-turn-and-subsequent-turn overrides for `model`, `effort`, `serviceTier`, and related thread settings, so embedded model changes can be applied without mutating the user's global Codex config.
- The installed schema on this machine also exposes additional thread and utility RPCs such as `thread/fork`, `thread/read`, `thread/compact/start`, `review/start`, `model/list`, `app/list`, `skills/list`, and `account/rateLimits/read`, and Little Control Room now uses `thread/read` both as a stale-busy sanity check and as a steer-recovery fallback when the app-server reports that the active turn id has already advanced.
- The installed schema also emits `thread/status/changed` plus streamed `plan`, `reasoning`, and `mcpToolCall` notifications, but it still does not expose a single authoritative "all visible output has settled" event, so embedded turn tracking should model `running`, `finishing`, and `reconciling` instead of a binary busy/idle flag.
- Embedded `codex app-server` stdout frames can exceed the prior 1 MiB scanner cap during tool-heavy turns (observed around MCP/browser screenshot activity), so the embedded transport must tolerate large JSON-RPC messages and treat stdout decode failures as fatal session breakage rather than a recoverable transcript-only warning.

Current OpenCode transport assumption:

- The installed `opencode` CLI on this machine (observed `1.2.24`) exposes both `serve` and `acp`; `serve` publishes an HTTP/OpenAPI + SSE surface with session, message, status, event, permission, and question endpoints, and Little Control Room now uses that live surface for embedded OpenCode sessions.
- Observed `opencode.db` session parts are structured rather than text-only, including `text`, `reasoning`, `tool`, `patch`, `file`, `step-start`, and `step-finish`, so OpenCode transcript extraction and the embedded pane should preserve that structure instead of flattening it to plain text.
- Observed `prompt_async` behavior accepts a follow-up prompt while the session is still busy (returning `204` and later appending a second user/assistant turn), so the embedded pane can treat Enter as a steer/follow-up path much like embedded Codex.
- OpenCode `FilePartInput` accepts structured file parts and the current embedded implementation sends local image attachments as `data:` URLs; transport support is confirmed, but end-to-end image robustness should still be treated as provisional until more real image cases are exercised.

Current screenshot workflow assumption:

- `make screenshots` currently defaults to the repo-root `screenshots.local.toml` unless `SCREENSHOT_CONFIG` is overridden; the committed demo config remains available at `docs/screenshots.example.toml`.
- Screenshot capture scale is now configurable via `capture_scale`, and the default browser-rendered PNG export path uses `1.5x` capture scale for sharper text.
- Screenshot export now preserves truecolor terminal escapes instead of forcing ANSI256 quantization, so the committed docs images can match the live TUI palette more closely.
- The committed docs screenshot set now includes `main-panel.png`, `main-panel-live-cx.png`, `codex-embedded.png`, `diff-view.png`, `diff-view-image.png`, and `commit-preview.png`.

## What Works

- Fast startup scan path for Codex using `~/.codex/state_5.sqlite` threads metadata
- Modern and legacy Codex JSONL session parsing
- OpenCode session detection from `~/.local/share/opencode/opencode.db`
- Artifact-first project detection from Codex/OpenCode metadata, with git discovery retained for repo metadata and move detection
- Attention ranking with transparent reasons plus latest-session assessment categories
- Repo-health surfacing for dirty worktrees and remote sync state
- Scope-aware persistence via path filters and project-name filters
- Cached `doctor` by default, with `doctor --scan` for a fresh rescan
- TUI stacked layout with focusable detail pane, scrolling, compact settings modal, and command palette
- Top-level `/open` slash command to open the selected project's folder in the system browser
- Project notes via `/note` or `n`, with a multiline modal editor, wrapped detail-pane notes, a list badge when a project has saved notes, and clipboard copy actions for the whole note or an explicit marked selection
- Managed per-project run commands via `/run`, `/run-edit`, `/runtime`, and `/stop`, with persisted default commands, first-pass command suggestions from common project files, a small run-command editor overlay, compact detail-pane runtime summaries, a dedicated runtime inspector with output/actions, and best-effort listening-port detection for LCR-managed runtimes
- Git workflow actions in the TUI for full-screen diff preview, commit preview, finish, and push
- Embedded Codex pane via `codex app-server`, with multiline compose, per-project drafts, inline `[Image #n]` clipboard image markers in the composer, backspace-based image removal, local embedded slash commands for `/new`, `/resume` (`/session` alias), `/model`, and `/status`, visible slash autocomplete/suggestions in the composer, a provider-specific saved-session resume picker with lightweight title/summary previews and current-session markers, live model/reasoning/context-left metadata under the transcript, a local model+reasoning picker backed by `model/list`, `Enter`/`/codex`/`/codex-new`, `Esc` or `Alt+Up` hide from the embedded pane with `Enter` reopening from the project list, `Alt+Down` session picker/history, `Alt+[`/`Alt+]` live-session stepping, wrapped transcript blocks, shaded echoed user transcript blocks that reuse the composer shell styling, denser command/tool/file blocks with `Alt+L` expand/collapse, label-free user/assistant transcript rendering, manager-side update coalescing, inline approvals/input requests, and busy-elsewhere rechecks when a read-only embedded session is reopened or restored
- Embedded OpenCode pane via `opencode serve`, with live SSE transcript updates, resume/new launch from `Enter` and `/opencode` / `/opencode-new`, shared picker/history and model picker, provider-aware banners/footer/help copy, interrupt/status actions, shared approval/question handling, and mixed Codex/OpenCode live-session management per project
- Settings-backed Codex launch presets, currently defaulting to the dangerous `yolo` mode
- Programmatic screenshot generation via `lcroom screenshots` and `make screenshots`, using screenshot-config-driven browser-rendered PNG exports from deterministic HTML terminal scenarios

## Current Priorities

- Keep polishing the embedded Codex/OpenCode pane now that live OpenCode transport and the mixed-session TUI flow are in place.
- Improve OpenCode parity details such as richer attachment confidence, better agent/status presentation, and any remaining approval/question edge cases.
- Polish the new managed runtime lane: multi-URL/runtime-panel ergonomics, better external-port attribution, and stronger cross-platform shell launching.
- Consider a schema-aware mini form for MCP elicitation instead of the current freeform JSON/text fallback.
- Watch for future `codex app-server` protocol support for true queued turns and adopt it when it exists.
- Factor a provider-neutral transcript/session abstraction so Codex and OpenCode stop sharing only by convention.
- Decide whether managed runtime state should graduate from TUI-only memory into a provider-neutral service/runtime abstraction that `doctor` and `serve` can also report.

## Status File Policy

- `STATUS.md` should stay short: current state plus the latest active work burst.
- Older historical notes now live in [docs/status_archive.md](docs/status_archive.md).
- If a note is mostly historical and no longer affects implementation, archive it instead of keeping it inline here.

## Latest Update (2026-03-17 03:10 JST)

- Compactified the selected-project runtime presentation so the shared detail pane now keeps runtime command, state, ports, URL, conflicts, errors, and a short output teaser instead of appending the full runtime tail inline.
- Added a dedicated runtime inspector opened with `r` or `/runtime`, with scrollable captured output plus quick `restart`, `stop`, and `open URL` actions wired into the same overlay/footer pattern as the rest of the TUI.
- Tightened the compact runtime summary further by packing related fields such as `Ports` + `URL` onto the same row when the pane is wide enough, so the runtime block spends fewer lines before the attention and session sections.
- Added the runtime restart flow, generalized browser opening for raw runtime URLs, refreshed help/docs copy, and added focused regressions for the new runtime command/key path, compact detail rendering, runtime inspector rendering, and browser URL opening.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/commands ./internal/tui -count=1` passed for the initial runtime-panel pass, and `go test ./internal/tui -count=1` passed again after the compact row packing follow-up.
- `make test` passed.
- `make scan` passed at `2026-03-17T03:08:59+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T03:08:59+09:00` (`projects: 137`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-runtime-inspector-smoke.sqlite` reached the TUI with a temp DB, rendered the updated main layout after the compaction pass, and exited via `q`.

Next concrete tasks:

- Decide whether the runtime inspector should let users choose among multiple announced URLs instead of always opening the first available URL/port.
- Consider keeping a longer or persisted runtime output history now that the dedicated panel makes the current 8-line tail more noticeable.

## Latest Update (2026-03-16 21:30 JST)

- Refined the project-list column redesign so the old live-turn signal is back under a clearer `AGENT` column instead of being mixed into `RUN`: busy rows now show compact provider+timer labels such as `CX 06:10`, while non-busy saved history still shows dim `CX` or `OC`.
- Merged the separate runtime/port presentation into a single `RUN` summary column that stays dedicated to `/run`-managed processes and can show compact labels like `pnpm`, `pnpm@3000`, or `dev!3000`.
- Added lightweight command-label extraction for saved or active run commands so the list can show a short executable-style runtime summary without exposing the full shell command in every row.
- Updated the list legend/help copy and the focused TUI regressions so the new `AGENT` + `N` + merged `RUN` model is documented and covered by tests.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `make test` passed.
- `make scan` passed at `2026-03-16T21:29:50+09:00` (`activity projects: 85`, `tracked projects: 136`, `updated projects: 2`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-16T21:30:01+09:00` (`projects: 136`).
- `env COLUMNS=110 LINES=30 make tui` correctly refused to start because another real `lcroom tui` runtime already owned the shared DB.
- A short `go run ./cmd/lcroom tui ... --allow-multiple-instances` smoke launch showed the expected existing-owner warning, rendered the project list with the new `AGENT`, `N`, and merged `RUN` columns visible, and exited via `q`.

Next concrete tasks:

- Watch a few real `/run` workflows and see whether the first-pass runtime labels (`pnpm`, `make`, `dev`, `go`, etc.) feel informative enough or need an explicit user-editable short label later.
- Decide whether idle but live embedded sessions should stay visually bright in `AGENT` or whether only busy turns should get the strongest styling once there is more daily usage feedback.

## Latest Update (2026-03-16 18:54 JST)

- Redesigned the main project-list runtime/note area to use explicit columns instead of the cryptic combined badge rail: `N` now shows `*` for saved notes, `RUN` is reserved for active `/run` managed runtimes, and `PORT` is reserved for detected runtime ports.
- Removed the old overloaded list behavior where `RUN` could mean either an AI live timer or a runtime port, and where note/runtime/conflict state was compressed into the short-lived `N$!` badge rail.
- Kept conflict visibility by rendering `PORT` values with a leading `!` when Little Control Room detects a managed port conflict.
- Updated the list legend/help copy and the focused TUI regressions so the new column model is documented and covered by tests.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `make test` passed.
- `make scan` passed at `2026-03-16T18:54:23+09:00` (`activity projects: 85`, `tracked projects: 136`, `updated projects: 2`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-16T18:54:32+09:00` (`projects: 136`).
- `env COLUMNS=110 LINES=30 make tui` correctly refused to start because another real `lcroom tui` runtime already owned the shared DB.
- A short `go run ./cmd/lcroom tui ... --allow-multiple-instances` smoke launch showed the expected existing-owner warning, rendered the project list with the new `N`, `RUN`, and `PORT` headers visible, and exited via `q`.

Next concrete tasks:

- Watch whether `RUN` should remain a simple active/error indicator or also surface a saved-but-idle configured state once the new layout gets some real use.
- If we still want more AI-live visibility in the list, add it under a clearly named column instead of reusing `RUN`.

## Latest Update (2026-03-16 18:43 JST)

- Renamed the project-list badge rail header from `BAD` to `N$!` so the visible label matches the badges actually shown in each row.
- Updated the help legend to spell that rail out as `N` note, `$` shell/runtime, and `!` port conflict.
- Adjusted the focused TUI rendering regression so the header assertion now follows the new `N$!` label.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `make test` passed.
- `make scan` passed at `2026-03-16T18:42:07+09:00` (`activity projects: 85`, `tracked projects: 136`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-16T18:42:14+09:00` (`projects: 136`).
- `env COLUMNS=110 LINES=30 make tui` correctly refused to start because another real `lcroom tui` runtime already owned the shared DB.
- A short `go run ./cmd/lcroom tui ... --allow-multiple-instances` smoke launch showed the expected existing-owner warning, rendered the project list with the `N$!` header visible, and exited via `q`.

Next concrete tasks:

- Decide whether the badge rail should keep symbol-first copy (`N$!`) or grow a slightly wider text label if the list layout changes again.
- If runtime terminology keeps causing confusion, consider whether `$` should stay runtime-flavored in the legend or be renamed more explicitly around shell-launched processes.

## Latest Update (2026-03-15 01:35 JST)

- Added a first managed runtime lane to the TUI: the selected project can now save a default run command, start it with `/run`, edit it with `/run-edit`, and stop it with `/stop`.
- Persisted the default run command on the project record, including schema migration support, summary/detail loading, move/consolidation preservation, service setters, and stored action events.
- Added a new `internal/projectrun` package that manages per-project shell-launched runtimes, captures recent stdout/stderr lines, extracts announced local URLs, and does best-effort listening-port detection via the managed process group.
- Updated the main list and detail pane to surface runtime state: the badge rail (then labeled `BAD`, now `N$!`) carries `N` notes, `$` active runtimes, and `!` managed port conflicts; the `RUN` column can show detected ports like `:3000` or `2p`; the detail pane now shows the saved run command, runtime status, ports, URLs, conflicts, and recent output.
- Added focused regressions for command parsing, run-command persistence and move safety, service event publishing, project-run suggestions, and TUI rendering/command-mode behavior around the new runtime flow.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./...` passed.
- `make test` passed.
- `make scan` passed at `2026-03-15T01:34:32+09:00` (`activity projects: 85`, `tracked projects: 136`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-15T01:34:32+09:00` (`projects: 136`).
- `make tui` correctly refused to start because another real `lcroom tui` runtime already owned the shared DB.
- A short `go run ./cmd/lcroom tui ... --allow-multiple-instances` smoke launch showed the expected existing-owner warning, reached the TUI with the then-new `BAD` header visible, and exited via `q`.

Next concrete tasks:

- Add a restart/open-url action once runtime start/stop settles, so common web-app flows need fewer manual steps after `/run`.
- Improve port attribution for non-LCR-managed listeners and decide whether conflicts should eventually become attention reasons in the shared store rather than a TUI-only badge.

## Latest Update (2026-03-15 00:59 JST)

- Added a top-level `/open` slash command that opens the selected project's directory in the system browser, including parser support, command-palette suggestions, TUI dispatch, and user-facing help/docs updates.
- Added a small cross-platform external-browser helper in the TUI layer that turns the project directory into a `file://` URL and launches it via the platform opener, while keeping the action unit-testable behind a stubbed function variable.
- Added focused regressions for both layers: command parsing/suggestion coverage for the new `/open` entry and TUI tests that confirm the selected project path is routed to the external browser helper without touching the real OS browser during tests.
- No detector or footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/commands -count=1` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-15T00:59:04+09:00` (`activity projects: 85`, `tracked projects: 136`, `updated projects: 2`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-15T00:59:05+09:00` (`projects: 136`).
- `make tui DB=/tmp/lcroom-open-command-smoke.sqlite` reached the TUI and exited via `q`.

Next concrete tasks:

- Decide whether `/open` should remain a plain `file://` handoff on every platform or grow a lightweight local HTTP directory viewer later if browser handling of folders proves inconsistent.
- Consider adding a small keyboard shortcut for the same action if opening the selected project in an external browser becomes a frequent workflow.

## Latest Update (2026-03-15 00:30 JST)

- Fixed the freshness gap in attention scoring for recently idle projects: the soft `recent_activity` bonus now starts as soon as a project leaves the active window instead of waiting until it crosses the stuck threshold.
- Also fixed recent-completion surfacing for idle projects with known completed work: classified `completed` sessions and recovered `latest turn completed` sessions now keep their completion reason during the normal idle window instead of falling back to a generic `Idle for ...` reason.
- Added focused regressions for the exact shape of the bug: a recent idle scorer case, a recent completed-turn scorer case, updated classified-state score expectations, and a service refresh regression that locks in the `session_completed + recent_activity` result for fresh completed work.
- No detector or footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/attention ./internal/service -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-15T00:30:28+09:00` (`activity projects: 84`, `tracked projects: 136`, `updated projects: 6`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-15T00:30:35+09:00` (`projects: 136`).
- `env COLUMNS=110 LINES=30 make tui` correctly refused to start because another `lcroom tui` process already owns the shared DB.
- A short `go run ./cmd/lcroom tui ... --allow-multiple-instances` smoke launch showed the expected existing-owner warning, reached the TUI, and exited via `q`.

Next concrete tasks:

- Watch the live list for a workday and tune the fresh-idle/completed weights if recently touched repos now feel either too sticky or still too easy to bury.
- Consider a small detail-pane distinction between category reasons like `session_completed` and softer modifiers like `recent_activity` so the score makeup stays easy to read.

## Latest Update (2026-03-14 23:55 JST)

- Fixed an embedded OpenCode `/new` failure-path regression where replacing the current session could drop the TUI back to the project list if the fresh session failed to start.
- The embedded session manager now keeps the previous session entry in place until a replacement is created successfully, so a failed force-new launch leaves the closed session visible instead of pruning the pane away.
- Added focused regressions for both layers: a manager test that proves failed replacements keep the prior closed session attached to the project, and a TUI test that exercises embedded OpenCode `/new` failure handling without losing the visible pane.
- Direct local `opencode serve` HTTP smoke checks also confirmed that creating and resuming sessions for `/Users/davide/dev/repos` still works outside the TUI, which supports the conclusion that this was a rollback/state-handling bug rather than a detector-footprint change.
- No detector or footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/codexapp -run 'TestManagerOpen(FailedReplacementKeepsClosedExistingSession|ForceNewReplacesExistingSession)' -count=1` passed.
- `go test ./internal/tui -run 'TestVisible(OpenCodeSlashNewFailureKeepsClosedSessionVisible|CodexSlashNewStartsFreshSession)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-14T23:55:29+09:00` (`activity projects: 84`, `tracked projects: 135`, `updated projects: 2`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T23:55:30+09:00` (`projects: 135`).
- Manual OpenCode HTTP smoke checks passed: `opencode serve --hostname 127.0.0.1 --port 0 --print-logs` accepted `POST /session`, resumed a created session via `GET /session/<id>`, and created a second fresh session both in `LittleControlRoom` and in `/Users/davide/dev/repos`.

Next concrete tasks:

- Re-run the exact real-TUI `repos` -> `Enter` -> embedded OpenCode `/new` flow and confirm the user-visible behavior now stays in the pane even if startup fails.
- Consider surfacing the underlying OpenCode startup error text more prominently in the embedded pane so future failures are easier to distinguish from a successful close/reopen cycle.

## Latest Update (2026-03-14 21:31 JST)

- Fixed an OpenCode live/idle presentation bug in the main project list: `OC` source tags were always rendered in the bright live-looking style, which made OpenCode projects look pre-opened even on cold start despite the embedded manager having no live session for them.
- OpenCode source styling now mirrors the Codex live-state treatment by dimming idle rows and only using the bright accent when there is an actual live embedded session for that project.
- Added a focused TUI regression that proves idle and live OpenCode source tags render differently, so future styling changes cannot silently reintroduce the false-open appearance.
- Investigation of the live store also confirmed there was no broad OpenCode detector/session-state bug behind this specific symptom: stored OpenCode rows in the current DB were not marked as running (`latest_turn_state_known=0` / `latest_turn_completed=0`).
- No detector or footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-14T21:31:11+09:00` (`activity projects: 84`, `tracked projects: 136`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T21:31:23+09:00` (`projects: 136`).
- A short `go run ./cmd/lcroom tui ... --allow-multiple-instances` smoke launch showed the expected existing-owner warning, reached the TUI, and exited via `q`.

Next concrete tasks:

- Do one longer manual OpenCode UI pass inside the real TUI and confirm there is no separate hidden-session/reopen issue beyond the fixed source-tag styling.
- If users still report OpenCode rows reopening unexpectedly, instrument the embedded manager/session counts per provider so future reports can distinguish render-state confusion from a real live-session leak.

## Latest Update (2026-03-14 21:09 JST)

- Fixed a hidden-session reopen regression in the main project list: pressing `Enter` on a project with an already-live hidden embedded Codex/OpenCode session could still go back through the launch/resume path, so stale stored session ids could replace the running session and append a misleading `Resumed...` notice.
- The list reopen path now restores the existing live embedded session for that project/provider directly, and the session-id selection helper now prefers the live embedded thread over older detail/summary metadata when a true relaunch path still needs a resume target.
- Added focused TUI regressions for both behaviors: restoring a hidden live Codex session from the project list without relaunching, and preferring the live embedded session id over stale stored ids.
- No detector or footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-14T21:08:34+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T21:08:34+09:00` (`projects: 138`).
- A short `go run ./cmd/lcroom tui ... --allow-multiple-instances` smoke launch showed the expected existing-owner warning, reached the TUI, and exited via `q`.

Next concrete tasks:

- Reproduce the exact "hide while the reply is streaming, then reopen from the main list" flow in a free debug TUI session and confirm no extra `Resumed...` system notice is appended.
- If the transcript still ever looks partial after this fix, trace whether that is a separate transcript-hydration/reconciliation issue rather than another reopen/replace bug.
- Consider whether the status copy should explicitly say `restored` vs `resumed` in more places so the distinction is clearer during live use.

## Latest Update (2026-03-14 20:42 JST)

- Fixed a resume-picker confusion bug where forked Codex subagent sessions were being listed like normal resumable conversations, which could surface duplicate-looking titles with different session ids and timestamps.
- The picker now inspects Codex `session_meta` headers and hides spawned subagent/thread-fork children from the saved-session resume list, bringing the visible entries closer to the top-level conversations shown by Codex TUI.
- Added a focused regression that builds a parent session plus a forked `explorer` child session and verifies that only the parent remains resumable in the picker.
- No detector or footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-14T20:42:35+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T20:42:42+09:00` (`projects: 138`).
- A short `go run ./cmd/lcroom tui ... --allow-multiple-instances` smoke launch started and exited cleanly via `q`.

Next concrete tasks:

- Sanity-check whether any intentionally user-created non-subagent Codex forks should remain visible later, or whether hiding all spawned child sessions is the right long-term rule for the resume UI.
- Consider whether the picker should explicitly surface parent/child lineage somewhere else for debugging, without cluttering the main resume list.

## Latest Update (2026-03-14 20:24 JST)

- Updated the embedded `/resume` session picker to use each session `Title` as the primary list preview, with the `Summary` demoted to secondary detail text in the About block.
- Made the picker height-aware so it now shows substantially more resume rows on taller terminals instead of the old fixed ~5-row window, while still leaving room for the dialog header and detail area.
- Reworked the left status rail into compact fixed-width slots such as `CX CUR LIVE`, `CX     SAVE`, and `CX     LAST`, which keeps the timestamp and session-id columns aligned even when a row is current/live.
- Added focused TUI regressions for title-first resume rows, compact badge-column alignment, and the taller picker window calculation.
- No detector or footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-14T20:23:22+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T20:23:33+09:00` (`projects: 138`).
- `make tui` correctly refused to start because another `lcroom tui` process already owns the shared DB.
- A short `go run ./cmd/lcroom tui ... --allow-multiple-instances` smoke launch started and exited cleanly via `q`, confirming the override path still boots for temporary UI checks.

Next concrete tasks:

- Reopen the populated `/resume` picker in a free debug TUI session and sanity-check the new row count and compact badge rail against real saved-session data.
- Decide whether the global embedded-session picker should also switch to title-first previews now that the resume picker behavior feels better.

## Latest Update (2026-03-14 19:59 JST)

- Unified the assessment vocabulary between the list and detail pane so they now use the same short canonical labels for completed classified states: `done`, `waiting`, `followup`, `working`, and `blocked`.
- The shared assessment helper no longer mixes short and formal synonyms like `done` vs `completed` or `working` vs `in progress`, which keeps the list and selected-project detail aligned as the same project changes state.
- Also aligned the local `sessionCategoryLabel` helper to the same canonical wording so future TUI surfaces are less likely to reintroduce the old split vocabulary.
- No detector or footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-14T19:59:28+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T19:59:28+09:00` (`projects: 138`).
- A short live TUI launch with `--allow-multiple-instances` confirmed that the same project now moved from `Assessment: preparing snapshot` to `Assessment: working` using the same wording visible in the list.

Next concrete tasks:

- Decide whether `followup` should stay compact ASCII or become `follow-up` with a small column-width adjustment later.
- Consider whether pending/running transient labels like `snapshot` and `model` should also eventually gain a matching dedicated detail shorthand, or remain detail-only as the fuller phrase.

## Latest Update (2026-03-14 19:53 JST)

- Added a small `AGENTS.md` note documenting the new multi-instance runtime guard and the intended debugging-only use of `--allow-multiple-instances`.
- Kept the guidance concise and local to the validation/debugging instructions so future sessions can find the flag quickly without treating it as the default launch mode.
- No product behavior or detector assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `make test` passed.
- `make scan` passed at `2026-03-14T19:53:16+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T19:53:16+09:00` (`projects: 138`).

Next concrete tasks:

- If we keep relying on `--allow-multiple-instances` for short debug launches, consider mirroring the same reminder in a user-facing troubleshooting doc later.
- Keep an eye on whether the runtime-owner warning itself should suggest the most common debug command shapes more explicitly.

## Latest Update (2026-03-14 19:51 JST)

- Replaced the short list `STATE` column with a compact assessment label so the main list now surfaces classifier outcomes like `waiting`, `done`, `followup`, `working`, `snapshot`, and `failed` instead of the coarser activity state.
- Renamed the long right-hand list column from `ASSESSMENT` to `SUMMARY` to better match its actual content: the latest session summary/explanation rather than the short assessment tag.
- Kept the detail pane split intact so the selected project still shows both `Assessment` and `Status`, preserving the broader activity signal without spending scarce list space on it.
- Updated TUI regression coverage for the new compact assessment labels, the renamed list headers, and the normal-body suppression checks that referenced the old `STATE` header text.
- No Codex/OpenCode detector assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-14T19:50:42+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T19:50:51+09:00` (`projects: 138`).
- A short live TUI launch with `--allow-multiple-instances` showed the new `ATTN  ASSESS ... SUMMARY` header and rows such as `snapshot`, `done`, `waiting`, and `working`, then exited cleanly via `q`.

Next concrete tasks:

- Watch whether the compact running-stage labels (`snapshot`, `model`, `working`) feel right in real use or should be tuned further.
- Consider whether the detail pane should also rename `Status` to `Activity` again now that the list has taken over the compact assessment role more explicitly.
- Decide whether later list-density passes should reclaim one more character from `LAST` or `RUN` now that `ASSESS` is carrying more value.

## Latest Update (2026-03-14 18:56 JST)

- Added a runtime-owner guard for long-lived `lcroom` modes (`tui`, `serve`, `classify`) so only one process per DB may own scanning/classification by default.
- The guard uses a per-DB advisory lock plus an OS process-table fallback, so a fresh build can still detect older pre-lock `lcroom` processes that are already running and refuse to compete with them.
- Added a new `--allow-multiple-instances` escape hatch for intentional short-lived dev/debug overlap; the default path now exits with a clear owner report (PID, mode, command) instead of silently sharing the live DB.
- Live verification against the currently running duplicate TUIs now blocks as intended: a fresh `lcroom classify` exits immediately and points at the older `tui` PID/command instead of starting another competing classifier run.
- Embedded Codex/OpenCode cleanup assumptions did not change: the TUI already closes embedded sessions on quit, so the concrete hazard here was multiple full `lcroom` processes, not orphaned embedded helper subprocesses.

Verification snapshot:

- `go test ./internal/runtimeguard ./internal/config ./internal/cli -count=1` passed.
- `make test` passed.
- `go run ./cmd/lcroom classify --config ~/.little-control-room/config.toml --db ~/.little-control-room/little-control-room.sqlite ...` now exits with a conflict message naming the older `tui` process.
- `make scan` passed at `2026-03-14T18:55:42+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 36`, `queued classifications: 28`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T18:55:42+09:00` (`projects: 138`).

Next concrete tasks:

- Decide whether the TUI itself should also render an in-app banner when launched with `--allow-multiple-instances`, since the CLI now blocks by default but the override path is currently terminal-only.
- Restart the two stale pre-lock TUIs once so future launches are protected by the new lease as well.
- Revisit a handful of old `in_progress` assessments like `quickgame_27` and `xpsvr-static`; the requeue bug is fixed, but some historical classifier judgments still look worth recalibrating separately.

## Latest Update (2026-03-14 18:45 JST)

- Fixed a classification-attempt ownership bug that let stale or duplicate workers overwrite newer lifecycle state, which was leaving impossible rows like `status=completed` with `stage=queued` / `waiting_for_model` and helping unchanged sessions get reassessed again.
- Added attempt-scoped store updates plus a model-wait heartbeat: active classifications now refresh their `updated_at` while waiting on the LLM, and stage/complete/fail writes only succeed for the currently claimed running attempt.
- Store open now repairs legacy terminal rows by clearing stray stages from `completed` / `failed` classifications, so older corrupted DB state does not keep confusing the UI after restart.
- Added regressions for stale-attempt overwrite protection, long `waiting_for_model` heartbeats staying fresh past the stale timeout, and the startup repair of broken terminal classification rows.
- Live investigation also found two long-lived `lcroom tui` processes still running pre-fix binaries against `~/.little-control-room/little-control-room.sqlite`; those old processes can still reintroduce churn until they are restarted on the new code.

Verification snapshot:

- `go test ./internal/store ./internal/sessionclassify -count=1` passed.
- `go test ./internal/service -count=1` passed.
- `make test` passed.
- `make doctor` passed on the cached snapshot dated `2026-03-14T18:44:03+09:00` (`projects: 138`).
- A one-shot current-code `doctor` open repaired the live DB from `44` broken terminal classification rows down to `0`.
- `make scan` first pass after the repair queued `27` classifications; an immediate second `make scan` dropped to `1`, which strongly suggests the broad churn was repair/backlog fallout rather than a stable repeat-scan loop in the current code.

Next concrete tasks:

- Restart or close the two stale `lcroom tui` processes still running older binaries against the live DB, then watch whether repeated `classification_updated` events disappear fully.
- Re-check a few previously noisy projects like `okmain`, `quickgame_27`, and `quickgame_30` after those old app instances are gone to confirm they stay stable across background scans.
- Decide whether terminal classifications should expose any failure-stage detail elsewhere now that terminal rows no longer keep `stage` in storage.

## Latest Update (2026-03-14 17:32 JST)

- Fixed the `quickgame_27` misclassification path by recovering Codex `task_started` / `task_complete` lifecycle from rollout transcripts when the fast detector omitted it for older sessions.
- Service scan now reuses stored latest-turn state when the latest session is unchanged, falls back to transcript recovery when needed, and computes snapshot hashes after that recovery so stale pre-recovery hashes do not survive.
- Session classification now also writes the recovered snapshot hash and latest-turn lifecycle back into `project_sessions`, which lets later scans and attention scoring keep that stronger completion signal.
- Simplified the TUI state/assessment split: the main list `STATE` column now stays on project status (`active` / `idle` / `stuck`), the detail pane label is now `Status` instead of `Activity`, and the assessment label no longer aliases `in progress` to `working`.
- Added focused regressions for transcript lifecycle recovery, snapshot-hash refresh after lifecycle recovery, reuse of stored latest-turn state, classifier backfill into `project_sessions`, the `STATE` column ignoring assessment labels, and the renamed detail `Status` field.
- No Codex/OpenCode detector assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/sessionclassify ./internal/service ./internal/tui -count=1` passed.
- `make test` passed.
- `make classify` passed and drained the classification queue with the new lifecycle-backfill path.
- `make scan` passed at `2026-03-14T17:31:04+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 26`, `queued classifications: 20`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T17:31:22+09:00` (`projects: 138`); the cached report now shows `/Users/davide/dev/poncle_repos/quickgame_27` as `status=idle` with latest-session assessment `category=completed`.
- Live DB spot-check after the refresh showed `quickgame_27` with `latest_turn_state_known=1`, `latest_turn_completed=1`, `classify_status=completed`, and project `status=idle`.
- `env COLUMNS=110 LINES=30 make tui` launched; the list showed `STATE` values like `active`, `stuck`, and `idle`, and the detail pane rendered `Assessment: ...  Status: ...`; the app exited cleanly via `q`.

Next concrete tasks:

- Investigate why a fresh broad `make scan` still requeues a batch of older classifications even after the lifecycle backfill now converges `quickgame_27`; decide whether that is expected churn from broader snapshot changes or a remaining hash-reuse issue.
- Decide whether the main list should surface a short explicit assessment-category badge somewhere, now that `STATE` is once again reserved for project status.
- Factor a provider-neutral transcript/session abstraction so Codex and OpenCode stop sharing only by convention.

## Recent Updates

### 2026-03-14 17:02 JST

- Added explicit clipboard support to the project note dialog so users no longer have to rely on terminal text selection that can capture surrounding UI chrome.
- Notes now support a quick `Ctrl+Y` whole-note copy plus a `Copy...` action with just `Whole note` and `Selected text`. Selected-text mode uses a two-step `Space` mark-start / move / `Space` copy flow inside the editor.
- Added a dedicated selection-mode renderer so the currently selected note range is visibly highlighted while the user is choosing it, instead of relying only on status text.
- Added focused TUI regression coverage for the new note-copy and selection-highlight behavior and updated the note-related docs/help copy to advertise the new workflow.
- No Codex/OpenCode detector assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-14T17:01:44+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 11`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T17:01:52+09:00` (`projects: 138`).
- `env COLUMNS=110 LINES=30 make tui` launched and exited cleanly via `q`.

Next concrete tasks:

- Decide whether note copy should also be exposed directly from the main detail pane or command palette, not just inside the note dialog.
- Keep polishing OpenCode parity details such as richer attachment confidence, better agent/status presentation, and any remaining approval/question edge cases.
- Factor a provider-neutral transcript/session abstraction so Codex and OpenCode stop sharing only by convention.

### 2026-03-14 15:07 JST

- Changed the main project list to show the full raw attention score instead of the old score-divided-by-10 shorthand, and widened the `ATTN` column by one character so repo-warning rows still render cleanly with the leading `!`.
- Reworked attention scoring so recency now contributes progressively after the normal stuck threshold instead of only through the separate recent sort. Projects with activity from later the same day, yesterday, or about two days ago now keep a modest `recent_activity` bonus that fades out across a `72h` window.
- Replaced the old flat completed-session window with a tapered completion reason: recently completed work now gradually loses attention over the same `72h` horizon instead of dropping from a fixed recent score to zero at the old `48h` cutoff.
- Extended scorer and TUI regression coverage to lock in the new raw list labels, modal-overlay clipping expectations, explicit `recent_activity` reasons, and the completion-score taper over time.
- No Codex/OpenCode detector assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/attention ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-14T15:06:53+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 9`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-14T15:06:59+09:00` (`projects: 138`).
- `env COLUMNS=110 LINES=30 make tui` launched; the live list showed raw scores such as `95`, `73`, and `43` in the widened `ATTN` column, and the app exited cleanly via `q`.

Next concrete tasks:

- Watch a few live workdays with the new taper and tune the `72h`/weight constants if completed work from about two days ago still feels either too sticky or not sticky enough.
- Decide whether the detail pane should eventually group or visually separate base attention reasons from softer modifiers like `recent_activity`.
- Factor a provider-neutral transcript/session abstraction so Codex and OpenCode stop sharing only by convention.

### 2026-03-13 21:08 JST

- Kept the new wrapped grayscale `Working` footer in `internal/tui/codex_pane.go` and sped its phase loop up again to match the requested feel by moving the wrapped wave down to a `25`-frame cycle. With the existing `120ms` spinner tick, that makes one full pass take about `3.0s`.
- Fixed the hidden cause of the steppy animation in `internal/tui/app.go`: `spinnerFrame` had been wrapped to the 4 spinner glyphs, which meant every "smooth" footer animation only had 4 actual states. The counter now keeps a higher-resolution animation frame and only mods by 4 when selecting the spinner glyph itself.
- Added focused TUI regression coverage in `internal/tui/app_test.go` for both root issues: the wrapped busy gradient now proves it matches exactly at the seam, and the spinner tick keeps advancing past the 4 glyph states so animated gradients are not limited to four phases.
- No Codex/OpenCode detector assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -run 'Test(RenderCodexFooterAnimatesBusyStatus|CodexBusyGradientWrapsContinuously|SpinnerTickKeepsHighResolutionAnimationFrames)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-13T21:08:22+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 2`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-13T21:08:30+09:00` (`projects: 138`).
- `env COLUMNS=110 LINES=30 make tui` launched and exited cleanly via `q`.

Next concrete tasks:

- Re-open the embedded pane against a live busy Codex/OpenCode session and tune the wrapped wave's contrast and speed further if it still feels off compared with Codex CLI in real use.
- Decide whether the same higher-resolution wrapped-gradient treatment should also replace the remaining `Finishing` and `Rechecking turn status` animated footer states for consistency.
- Factor a provider-neutral transcript/session abstraction so Codex and OpenCode stop sharing only by convention.

### 2026-03-13 11:19 JST

- Switched the diff screen's text preview from a unified patch block to a side-by-side renderer in `internal/tui/diff_view.go`, while preserving the existing file list, staged/unstaged grouping, image diff handling, and diff-screen navigation.
- Added a lightweight parser for the preview body's staged/unstaged/untracked sections so removed and added runs are paired into `Before | After` columns, with hunk headers, file headers, and diff metadata still rendered distinctly.
- Added focused TUI regression coverage for the new paired-column rendering and updated the screenshot fixture expectations to lock in the side-by-side layout.
- Regenerated the committed demo screenshot at `docs/screenshots/diff-view.png` so the docs now match the new diff screen.
- No Codex/OpenCode detector or screenshot-footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -run 'Test(RenderDiffEntryBodyUsesSideBySideColumns|RenderDiffFileListSeparatesStagedAndUnstagedSections|ViewWithDiffScreenUsesFullBody|ScreenshotDiffViewFixtureRendersSelectedPatch|DiffModeMovesSelectionAndScrollsContent)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-13T11:18:16+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-13T11:18:24+09:00` (`projects: 138`).
- `env COLUMNS=112 LINES=31 make tui` launched and exited cleanly via `q`.
- `SCREENSHOT_CONFIG=docs/screenshots.example.toml SCREENSHOT_OUTPUT_DIR=docs/screenshots make screenshots` passed and refreshed `docs/screenshots/diff-view.png`.

Next concrete tasks:

- Exercise the side-by-side renderer against more real rename/delete-heavy diffs to tune any edge-case metadata rows.
- Decide whether the diff pane should grow optional line numbers or a narrow-terminal fallback once we have more usage feedback.
- Factor a provider-neutral transcript/session abstraction so Codex and OpenCode stop sharing only by convention.

### 2026-03-13 11:02 JST

- Fixed the OpenCode reassessment loop where unchanged projects could keep re-queueing the LLM classifier just because `opencode.db` was temporarily busy during snapshot extraction.
- Added a dedicated `internal/opencodesqlite` read helper that opens `opencode.db` with a busy timeout and query-only mode, and switched both the detector and OpenCode snapshot/preview extraction onto that helper.
- Tightened classification queuing so supported session formats now require a real transcript-derived `snapshot_hash`; scan/classification no longer silently falls back to a timestamp-based legacy hash when snapshot extraction fails.
- Extended project summaries with the latest session hash and last-event timestamp, then reused that stored hash when the same latest OpenCode session is unchanged across scans. This preserves the stable classification key even if a fresh read is transiently blocked.
- Added focused regression coverage for the new OpenCode SQLite helper and for unchanged OpenCode sessions reusing the previous stable snapshot hash.
- No Codex/OpenCode detector footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/opencodesqlite ./internal/sessionclassify ./internal/service ./internal/detectors/opencode -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-13T11:01:55+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 2`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-13T11:01:56+09:00` (`projects: 138`).
- `sqlite3 ~/.little-control-room/little-control-room.sqlite "SELECT ... WHERE sc.status IN ('pending','running') ..."` showed only two queued `modern` (Codex) sessions after the scan, with no queued OpenCode reassessment entries.

Next concrete tasks:

- Factor a provider-neutral transcript/session abstraction so Codex and OpenCode stop sharing only by convention.
- Keep polishing OpenCode parity details such as agent/status presentation and attachment confidence.
- Consider a small follow-up around persistent OpenCode snapshot failures that are not lock-related, so they surface more clearly in the UI/doctor output.

### 2026-03-13 10:50 JST

- Added embedded `/resume` support, with hidden `/session` as an alias, to the shared Codex/OpenCode slash-command layer. `/resume` with no session ID now opens a picker for saved sessions from the current project and provider, while `/resume <session-id>` jumps straight to that session.
- Expanded the shared embedded session picker into a resume mode that shows saved-session title, lightweight artifact-derived summary, provider tag, last activity, and a `CURRENT` badge for the visible session when present.
- Extended session preview extraction so Codex JSONL and OpenCode transcript artifacts can supply short titles and summaries without any extra LLM pass, which keeps the picker responsive and avoids long-context parsing.
- Added focused regression coverage for slash parsing, picker loading, direct session resume, and the `/session` alias opening an OpenCode session through the embedded pane.
- Updated `README.md` and `docs/reference.md` to mention the new embedded `/resume` flow and removed the outdated claim that switching to other sessions in the same project is not supported.
- No Codex/OpenCode detector footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui ./internal/sessionclassify ./internal/codexapp ./internal/codexslash -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-13T10:48:00+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 7`, `queued classifications: 4`).
- `make doctor` passed on the cached snapshot dated `2026-03-13T10:48:08+09:00` (`projects: 138`).
- `env COLUMNS=110 LINES=30 make tui` launched and exited cleanly via `q`.

Next concrete tasks:

- Factor a provider-neutral transcript/session abstraction so Codex and OpenCode stop sharing only by convention.
- Consider a small screenshot refresh later if we want a captured resume-picker image in the docs once the UI settles.
- Keep polishing OpenCode parity details such as agent/status presentation and attachment confidence.

### 2026-03-13 08:13 JST

- Fixed an embedded OpenCode stability bug reported from the demo project: the shared OpenCode `http.Client` was carrying a global `20s` timeout, which is fine for short RPCs but wrong for the long-lived `/event` SSE stream. The OpenCode transport now leaves the shared client without a global timeout and relies on per-request contexts for normal RPC deadlines, so the event stream can stay open while the model picker or other idle UI is on screen.
- Added a focused regression in `internal/codexapp/opencode_session_test.go` that locks in the no-global-timeout HTTP client used by the embedded OpenCode transport.
- Re-ran the mixed-provider focused suites after the fix; Codex/OpenCode picker, pane, and command tests still pass.
- No Codex/OpenCode detector footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/codexapp ./internal/tui ./internal/commands -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-13T02:00:04+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 4`, `queued classifications: 7`).
- `make doctor` passed on the cached snapshot dated `2026-03-13T02:00:04+09:00` (`projects: 138`).
- `env COLUMNS=110 LINES=30 make tui` launched and exited cleanly via `q`.
- Manual OpenCode smoke test still reproduced a real OpenCode session and assistant reply in `/private/tmp/lcr-oc-smoke.0JlgcW`, and `make doctor` tracked that repo as `format=opencode_db`.

Next concrete tasks:

- Add a provider-neutral transcript/session abstraction above Codex/OpenCode so the TUI no longer relies on Codex-named helpers and duplicated provider conditionals.
- Exercise more real OpenCode image/file prompts to harden attachment behavior and decide whether `data:` URLs are sufficient or whether `file://` fallbacks are needed.
- Improve OpenCode-specific polish around agent/status presentation and any remaining approval/question wording that still feels Codex-shaped.

### 2026-03-13 07:55 JST

- Traced the stale `RUN` timer report to embedded Codex session bookkeeping rather than the assessment classifier: completed turns could stay marked busy because generic metadata activity (`thread/tokenUsage/updated`, rate-limit updates, similar heartbeats) kept refreshing the same timestamp the stale-busy reconciler uses.
- Added a dedicated `LastBusyActivityAt` snapshot field and `lastBusyActivityAt` session clock so the manager now reconciles based on real turn/output activity instead of any session activity.
- Updated embedded Codex turn/item handlers to refresh the busy-activity clock only for turn lifecycle and output-bearing events, while leaving generic metadata updates as ordinary activity that no longer masks stale busy sessions.
- Added focused regression coverage for both sides of the bug: token-usage updates do not refresh the busy-activity clock, and the manager still rechecks a busy session when only generic activity is fresh.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/codexapp` passed.
- `make test` passed.
- `make scan` passed at `2026-03-13T07:52:46+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 7`, `queued classifications: 7`).
- `make doctor` passed on the cached snapshot dated `2026-03-13T07:52:47+09:00` (`projects: 138`).
- `make tui` launched cleanly in a PTY and exited via `Ctrl+C` after a startup smoke check.

### 2026-03-13 02:01 JST

- Landed the first real embedded OpenCode lane in the app: `internal/codexapp/opencode_session.go` now launches `opencode serve`, resumes or creates sessions over HTTP, hydrates transcript history, streams `/event` SSE updates, exposes model list/staging, supports status and interrupt, maps permission/question requests into the shared embedded UI, and sends local image attachments as `FilePartInput` data URLs.
- Lifted the embedded-session stack from Codex-only to mixed-provider behavior. The manager, slash commands, picker/history, banner/footer/help text, status cards, and model picker now understand both `CX` and `OC`, and `Enter` from the project list prefers the selected project's latest provider instead of assuming Codex.
- Added focused regression coverage for OpenCode command parsing, provider-switch session replacement, OpenCode resume from the picker, Enter-opening of the preferred OpenCode session, OpenCode status-card rendering, and provider-specific session-id selection from the detail pane.
- Polished the mixed-provider UX after the first pass: the busy-elsewhere warning now preserves the original provider message when present, the session picker overlay no longer overflows the parent frame, and OpenCode status cards now show `agent:` as a first-class row instead of smuggling it through the Codex `service tier` slot.
- No Codex/OpenCode detector footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/codexapp ./internal/commands ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-13T02:00:04+09:00` (`activity projects: 84`, `tracked projects: 138`, `updated projects: 4`, `queued classifications: 7`).
- `make doctor` passed on the cached snapshot dated `2026-03-13T02:00:04+09:00` (`projects: 138`).
- `env COLUMNS=110 LINES=30 make tui` launched and exited cleanly via `q`.
- Manual OpenCode smoke test passed: created a temporary git repo under `/private/tmp/lcr-oc-smoke.0JlgcW`, started `opencode serve`, created a real session, sent `prompt_async`, confirmed the assistant reply via `/session/{id}/message`, and then confirmed `make doctor` tracked that repo as `format=opencode_db`.

Next concrete tasks:

- Add a provider-neutral transcript/session abstraction above Codex/OpenCode so the TUI no longer relies on Codex-named helpers and duplicated provider conditionals.
- Exercise more real OpenCode image/file prompts to harden attachment behavior and decide whether `data:` URLs are sufficient or whether `file://` fallbacks are needed.
- Improve OpenCode-specific polish around agent/status presentation and any remaining approval/question wording that still feels Codex-shaped.

### 2026-03-11 10:35 JST

- Added an MIT `LICENSE` so the public snapshot has explicit reuse terms before the visibility flip.
- Created a local pre-public backup archive (git bundle plus working-tree tarball) before rewriting the repo history, so the prior private history and local files remain recoverable off-repo.
- Repointed local `master` to the clean public snapshot and kept that rewritten history as a single sanitized commit without reintroducing machine-specific paths or private fixture names.
- Force-pushed the rewritten `master` and flipped the GitHub repository visibility to public; the live public page now serves the sanitized one-commit snapshot with `master` as the default branch.
- No Codex/OpenCode detector assumptions changed; `docs/codex_cli_footprint.md` stayed aligned with the current footprint expectations.

### 2026-03-11 09:00 JST

- Finished the public-readiness cleanup pass before making the repo visible: removed the tracked empty `.littlecontrolroom.db` file and added repo-local ignore rules for `.DS_Store`, `Session.vim`, and `.littlecontrolroom.db`.
- Replaced machine-specific and owner-specific examples in the public docs with generic placeholders, including config paths, screenshot examples, and the observed-host wording in `docs/codex_cli_footprint.md`.
- Added a safe built-in screenshot demo dataset plus `demo_data` screenshot-config support, switched `make screenshots` to the committed demo config, and regenerated the committed PNGs so the docs screenshots no longer expose local project names or paths.
- Anonymized the bundled Codex footprint fixtures and related detector/TUI/service tests so checked-in sample paths and repo URLs no longer point at the maintainer's local machine.
- No Codex/OpenCode detector assumptions changed; `docs/codex_cli_footprint.md` stayed aligned with the current footprint expectations while dropping the machine-specific host path.

### 2026-03-11 08:15 JST

- Tightened the TUI detail-pane move metadata so `Moved from` / `Moved at` now follow the same recent-move policy as the list and status labels instead of lingering forever once a project has a stored move origin.
- Added focused detail-render regressions that cover both sides of the new behavior: recent moves still show their origin/timestamp, while stale moves older than the recent-move window disappear from the detail pane.
- Kept the older long-row layout regression active by updating it to use a still-recent moved project, so we continue covering wrapped `Moved from` / `Moved at` rows under the new visibility rule.
- No Codex/OpenCode detector footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

### 2026-03-10 23:33 JST

- Refined the embedded Codex shortcut move so `picker`, `prev`, `next`, and `blocks` now live on the same banner line as `Codex | <project>` instead of consuming their own top row.
- Kept the footer trimmed to immediate compose/approval actions, so the bottom line stays compact while the transcript regains the row that the temporary top shortcut strip used.
- Updated the focused TUI coverage to assert the banner itself carries those promoted shortcuts and that the footer still omits them.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

### 2026-03-10 23:27 JST

- Moved the embedded Codex pane's session-navigation hints for `picker`, `prev`, `next`, and `blocks` into a dedicated top row directly under the Codex banner so moderately sized terminals keep those shortcuts visible.
- Simplified the embedded footer states to keep only the immediate compose/approval actions at the bottom, which reduces truncation pressure without changing the underlying keybindings.
- Added focused TUI coverage for the promoted top shortcut row and for the footer regression so those hints stay out of the bottom action strip.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

### 2026-03-10 23:19 JST

- Fixed the detail-pane `Session summary` block so long summary text now wraps into real continuation lines instead of being clipped at the pane edge.
- Reused the wrapped bullet rendering for the other summary-status rows in the same section so long progress or failure copy also fits the viewport cleanly.
- Updated the detail renderer to count wrapped lines correctly before viewport fitting, which keeps multiline summary content visible instead of truncating it back down to the old one-line assumption.
- Added a focused TUI regression that renders a narrow detail pane with a long completed-session summary and asserts both wrapping and width limits.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

### 2026-03-10 22:15 JST

- Removed explicit `max_output_tokens` caps from all current structured Responses API calls so classifier, commit-message, and untracked-file prompts no longer risk truncated JSON output just because a guessed cap was too small.
- Kept the classifier hardening from the earlier pass, but simplified the retry path so retries now change only `reasoning.effort` (`medium` primary, `minimal` fallback) instead of also changing a token budget.
- Kept the richer classifier failure text for `status=incomplete` and transient API failures so stored assessment errors still surface useful clues when the model produces no assistant output.
- Updated focused tests to assert that these structured prompts now omit `max_output_tokens` entirely, while preserving coverage for incomplete-response fallback and transient `500` retry handling.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

### 2026-03-10 21:43 JST

- Reworked the commit preview overlay so the commit subject now renders as its own two-line message block, followed by a deliberate blank line before branch/stage metadata.
- Added focused TUI regression coverage to lock in the separated message block and the spacer after the subject line.
- Regenerated the screenshot set and visually rechecked `docs/screenshots/commit-preview.png` so the documented preview matches the new hierarchy.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

### 2026-03-10 21:29 JST

- Added persisted session-classification stages so assessments now distinguish `queued`, `preparing snapshot`, and `waiting for model` instead of collapsing everything into a generic `assessment running`.
- The classifier worker now records stage transitions around local transcript extraction and the Responses API call, and `doctor` now prints stage plus elapsed time for pending/running assessments.
- Bumped the session-classifier Responses request from `reasoning.effort = "minimal"` to `reasoning.effort = "medium"` while keeping the compact structured-output schema and token cap.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

### 2026-03-10 18:54 JST

- Promoted embedded Codex turn tracking from a binary busy/idle flag to explicit phases: `Running`, `Finishing`, `Reconciling`, `External`, `Idle`, and `Closed`.
- Tightened the live output lifecycle so streamed `plan`, `reasoning`, and `mcpToolCall` items keep a turn active alongside agent replies, commands, and file changes, and so the UI can treat the post-`turn/completed` settle window as `Finishing` instead of immediately declaring the turn done.
- Added manager-side stale-busy reconciliation: if a busy embedded session goes quiet for too long, Little Control Room now issues `thread/read` and converts an idle reply into a recovered completion state instead of leaving the UI stuck in `Working`.
- Updated the embedded TUI footer, picker copy, and `Ctrl+C` / `Enter` behavior so users can see whether Codex is still actively running, merely finishing trailing output, or being rechecked for idle recovery.

### 2026-03-10 17:36 JST

- Fixed a main-view layout regression where long selected-project detail rows could wrap inside the framed panes and push the brand/status lines off the top of the terminal.
- The main detail pane now clamps rendered lines to the pane width before viewport slicing, and framed panes now render already-sized content instead of asking Lip Gloss to reflow it a second time.
- Added a focused TUI regression that keeps the header and status visible for the long `Moved from` / `Moved at` case, and reused the same framed-pane helper for the embedded Codex transcript pane.

### 2026-03-10 16:54 JST

- Expanded the screenshot terminal size again to `112x31` so the hidden-live-Codex panel has enough room for the sample follow-up summary without clipping the lower rows or footer.
- Updated the default screenshot config, local/example config, docs example, and embedded Codex fixture text to keep the wider/taller screenshot size consistent everywhere.
- Regenerated the screenshot set and visually rechecked the live hidden-Codex panel after the viewport bump.

### 2026-03-10 12:50 JST

- Adjusted the `main-panel-live-cx` screenshot scenario so it now keeps the hidden live Codex session on `LittleControlRoom` while selecting a sample follow-up project in the detail pane, making the screenshot visibly different from the plain `main-panel` shot.
- This makes the hidden-session story much clearer in the final PNG: one row shows the bright `CX` plus active `RUN` timer while the detail pane shows a separate project in a `followup` state.
- Regenerated the screenshot set and visually rechecked the main panel plus hidden-live-Codex panel side by side after the scenario split.

### 2026-03-10 11:41 JST

- Removed the extra left/right inset between the simulated window frame and the terminal content so the screenshot shell now sits flush against the frame edges while keeping the inner terminal padding intact.
- Bumped the screenshot terminal width from `104` to `110` columns across the default config, example config, local config, docs, and the embedded Codex fixture text so the screenshot set is consistently wider.
- Added a sample follow-up project to the local screenshot allowlist and introduced screenshot-only fake live Codex sessions for two projects, which makes the list show real `MM:SS` timers in the `RUN` column and keeps that sample project visible in a `followup` state.
- Regenerated the screenshot set and visually rechecked the main panel, live hidden-Codex panel, embedded Codex session, and commit preview PNGs after the layout/data pass.

### 2026-03-10 09:11 JST

- Tightened the screenshot renderer so the final PNGs now embed the local Iosevka family directly into the temporary HTML, size the terminal from browser-native monospace metrics (`ch` / line-height) instead of hardcoded cell widths, and keep the rounded titlebar/frame as one clipped structure.
- The browser capture step now intentionally overcaptures and then crops the PNG back to the real non-background bounds, which removed the bottom clipping and extra right-side slack without needing brittle per-browser viewport tuning.
- Fixed screenshot row background painting by letting terminal runs stay `inline-block`, so commit/composer regions with lighter shell backgrounds now render as solid blocks instead of striped rows with dark seams between lines.
- Regenerated the screenshot set and visually rechecked the main panel, live hidden-Codex panel, embedded Codex session, and commit preview PNGs after the renderer/crop pass.

### 2026-03-10 01:04 JST

- Replaced the screenshot pipeline's final export step from browser-sensitive SVG output to browser-rendered PNG output, while keeping the same deterministic TUI scenarios and local allowlist config.
- The renderer now generates a standalone HTML terminal frame and `lcroom screenshots` captures it with a locally detected Chrome/Chromium-family browser, with optional local override via `browser_path` in `screenshots.local.toml` or `LCROOM_SCREENSHOT_BROWSER`.
- `make screenshots` now writes `main-panel.png`, `main-panel-live-cx.png`, `codex-embedded.png`, and `commit-preview.png`, and removes stale legacy `.svg` / `.html` siblings for those assets.
- Added focused coverage for the browser resolver plus the HTML renderer output, and updated docs/example config to describe the new PNG-first workflow.

Verification snapshot:

- `go test ./internal/tui` passed.
- `go test ./internal/cli` passed.
- `go test ./internal/config` passed.
- `make screenshots` passed and refreshed `docs/screenshots/main-panel.png`, `docs/screenshots/main-panel-live-cx.png`, `docs/screenshots/codex-embedded.png`, and `docs/screenshots/commit-preview.png`.
- `make test` passed.
- `make scan` passed at `2026-03-10T01:03:39+09:00` (`activity projects: 80`, `tracked projects: 133`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-10T01:03:39+09:00` (`projects: 133`).
- `env COLUMNS=100 LINES=28 make tui` launched and exited cleanly via `q`.

### 2026-03-10 00:49 JST

- Switched the screenshot SVG renderer to prefer the locally installed Iosevka family (`Iosevka Term` / `Iosevka Fixed`) and disabled browser font synthesis so the generated screenshots stayed closer to terminal cell metrics during the transition away from SVG.
- Regenerated the screenshot set and rechecked the main panel plus embedded Codex views in headless Chrome; the `SRC` badges rendered more cleanly and the detail-pane `Last activity ... Codex` row no longer showed the same browser-side drift seen with the previous fallback font stack.
- Added a renderer assertion so the screenshot output keeps advertising the Iosevka font stack.

### 2026-03-10 00:40 JST

- Narrowed embedded Codex busy-item tracking so only output-bearing item lifecycles (`agentMessage`, `commandExecution`, and `fileChange`) can keep a turn running after `turn/completed`.
- This fixes the opposite regression where harmless non-streaming items such as `userMessage` could pin the embedded session timer in a long-running `Working` state long after Codex had finished answering.
- Added a focused `internal/codexapp` regression that covers `userMessage` start events not blocking turn completion, while keeping the earlier late-output regressions green.

### 2026-03-10 00:03 JST

- Polished the screenshot renderer for browser-facing SVG output: widened the terminal content area slightly so right-edge labels such as `YOLO MODE` no longer clip in Chromium-family browsers.
- Center-aligned the `SRC` list cell so `CX`/`OC` badges sit correctly inside the fixed-width column in generated screenshots.
- Added line-level background inference in the XHTML `<foreignObject>` renderer so full-width shell/composer rows keep their lighter gray background instead of rendering as a few disconnected gray spans.
- Added a focused regression test for the new line-background behavior and regenerated the screenshot assets under `docs/screenshots/`.
- Sanity-checked the generated SVGs in headless Google Chrome after regeneration; the live hidden-Codex view and embedded Codex view now render with the corrected badge alignment and right-edge banner spacing.

### 2026-03-09 22:19 JST

- Fixed an embedded Codex state bug where Little Control Room could mark a session idle as soon as `turn/completed` arrived even though agent or tool items were still streaming output.
- Added active item tracking inside the `codex app-server` session state machine so command, file, and agent deltas keep the session busy until the corresponding item actually completes.
- Added focused `internal/codexapp` regressions covering the bad ordering for streaming agent replies and command-output deltas after turn completion.

### 2026-03-09 21:02 JST

- Gave echoed embedded Codex user messages their own subtle shaded shell so they are easier to distinguish from assistant replies without bringing back explicit sender labels.
- Reused the same gray background family as the embedded composer so the echoed prompt reads like "input-side" UI instead of another assistant paragraph.
- Added focused TUI regression coverage to lock in the user-only shading while keeping assistant transcript blocks unshaded.

### 2026-03-09 20:51 JST

- Added a structured no-changes git outcome so `/commit` and `/finish` no longer collapse into a generic status-line-only failure when the selected repo is already clean.
- Added a visible `Nothing To Commit` dialog in the TUI that shows the selected project, branch, remote status, and an `Enter` push shortcut when the branch is ahead with already-committed work.
- Added regression coverage for the new no-changes service error path plus the TUI dialog rendering and key handling, and documented the new behavior in the README.

### 2026-03-09 18:32 JST

- Reworked the main app footer and embedded Codex footer to use colored key/action hints instead of plain `Keys action` text, using a shared footer-rendering helper so the styling stays consistent.
- Reordered the embedded Codex footer so prompt submission comes first, `Ctrl+C` and `Alt+Up` stand out earlier, and `Alt+L` is pushed to the end as a lower-priority hint.
- Applied the same colored hint treatment to approval-footers and other embedded Codex footer states, and added regression coverage for ANSI-styled footers plus the new Codex footer ordering.

### 2026-03-09 10:54 JST

- Added a first embedded slash-command layer for the Codex pane so `/new` and `/status` now execute locally instead of being forwarded to the model as plain prompts.
- Added visible embedded slash autocomplete with inline suggestions plus Tab/Shift+Tab completion so it is obvious when the composer is in slash-command mode.
- `/status` now reads embedded session configuration and app-server rate-limit/token-usage state when available, then appends the result as a local system transcript block inside the embedded Codex history.
- Added focused TUI regression coverage for embedded slash suggestion rendering, Tab cycling, local `/status`, and fresh-session `/new`.

### 2026-03-09 10:23 JST

- Removed the embedded Codex composer's default `"Message Codex"` placeholder because it was visually cramped and easy to misread in the narrow inline pane.
- Added a styled prompt marker plus a subtle full-width composer shell so the input area stays visibly distinct even when only the first line is in use.
- Added a focused TUI regression test that confirms the empty embedded composer now shows the prompt marker instead of the old placeholder.

### 2026-03-09 08:43 JST

- Fixed duplicated embedded user prompts by binding Little Control Room's optimistic local submission row to the real `userMessage` item once `codex app-server` echoes it back.
- Fixed live transcript reconciliation so completed command, tool, and file-change items replace stale in-progress text instead of leaving `[command inProgress]`-style markers behind.
- Expanded embedded Codex user-message parsing to handle richer content variants such as `input_text`, `local_image`, and `input_image`, which restores the intended user-message styling for those rows.
- Added focused `internal/codexapp` regression tests covering optimistic prompt binding plus command/tool completion status replacement.

### 2026-03-08 16:50 JST

- Replaced the temporary terminal handoff with an embedded Codex `app-server` pane.
- Added live hide/restore with `F2`, inline prompt submission, and inline approval controls.
- Added dedicated tests for the embedded pane and Codex session manager behavior.

### 2026-03-08 16:57 JST

- Fixed a practical resume bug where embedded Codex sessions reopened with only the system banner and no prior conversation content.
- The app now rebuilds the transcript from historical turns/items returned by `thread/resume`, so reopened sessions show prior prompts, replies, and command/file activity immediately.

### 2026-03-08 17:17 JST

- Added a one-hour inactivity reaper for embedded Codex sessions so hidden sessions do not keep background app-server processes alive indefinitely.
- `Ctrl+C` now closes an idle embedded Codex session, while still interrupting active turns.
- Structured transcript entries now drive the Codex pane rendering, enabling clearer role-aware blocks and command/file sections.
- Codex `SRC` badges are now visually dimmed when there is no live embedded Codex process.

### 2026-03-08 17:31 JST

- Moved the embedded Codex `YOLO MODE` warning from its own footer row into the footer/status line itself so it remains visible without consuming an extra line.

### 2026-03-08 11:50 JST

- Manual refresh (`r` and `/refresh`) now rescans and immediately requeues failed same-snapshot session classifications after transient LLM/network failures.
- Forced retry only bypasses the failed-row gate; already-running same-snapshot classifications still stay protected from duplicate requeue.
- The TUI scan status now reports queued classifications so refresh feedback is clearer.
- The settings modal was compacted for shorter terminals by:
  - making each field a single-row label/input pair
  - moving the long description into a bottom help area that follows the selected field
  - windowing visible rows around the current selection when height is tight

### 2026-03-08 11:29 JST

- Added settings-backed Codex launch presets in config and the TUI settings panel.
- `yolo` is now the default preset, but Little Control Room stores and launches the formal Codex flag `--dangerously-bypass-approvals-and-sandbox`.
- `Enter` on the focused project now launches or resumes Codex directly using the selected project's directory.
- Resume flows also pass `-C <project-path>` explicitly.

### 2026-03-08 11:00 JST

- Added first-phase in-app Codex handoff through `/codex` and `/codex-new`.
- `/codex` resumes the selected project's latest known Codex session when possible and otherwise starts a new one.
- `/codex-new` always starts a fresh Codex session in the selected project directory.
- Official integration direction for a future deeper embed is `codex app-server`, which uses bidirectional JSON-RPC 2.0.

### Earlier on 2026-03-08

- Removed the legacy `controlcenter` command surface and aligned README/help with `lcroom`.
- Added project-name filters and hot-reloaded them in the TUI.
- Tightened list density and attention/category behavior in a few smaller UX passes.
- Older milestone context from 2026-03-05 through early 2026-03-08 now lives in [docs/status_archive.md](docs/status_archive.md).
