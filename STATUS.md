# Little Control Room Status

Last updated: 2026-03-19 11:31 JST (JST)

## Current State

Implemented milestone:

1. Phase 0 footprint discovery doc + fixtures
2. Phase 1 monitoring foundation (`scan`, `doctor`, SQLite store, attention scoring, event bus)
3. Phase 2 TUI (`tui`) with refresh, filters, pin, snooze, note, command palette, git workflow actions, a first managed per-project runtime lane, and an animated pixel-art idle control-room vignette in the runtime pane
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
- Observed `thread/read` can still report `status.type = active` after the steerable turn is already gone, so embedded turn recovery should trust the presence or absence of an in-progress turn in `thread.turns[]` more than `status.type` alone when deciding whether a follow-up should steer or start a fresh turn.
- Observed Codex session rollouts can end a turn with `event_msg.payload.type == "turn_aborted"` (seen with `reason:"interrupted"`) without a later `task_complete`, so both artifact scanners and embedded-session lifecycle handling should treat `turn_aborted` as a terminal turn event rather than waiting for a separate completion marker.
- Observed interrupted turns can later read back through `thread/read` / `thread/resume` as idle with no active turn even when the JSONL tail ends in `turn_aborted`, so stale-busy recovery should prefer the live thread turn list over any missing `task_complete` marker.
- The installed schema also emits `thread/status/changed` plus streamed `plan`, `reasoning`, and `mcpToolCall` notifications, but it still does not expose a single authoritative "all visible output has settled" event, so embedded turn tracking should model `running`, `finishing`, and `reconciling` instead of a binary busy/idle flag.
- Embedded `codex app-server` stdout frames can exceed the prior 1 MiB scanner cap during tool-heavy turns (observed around MCP/browser screenshot activity), so the embedded transport must tolerate large JSON-RPC messages and treat stdout decode failures as fatal session breakage rather than a recoverable transcript-only warning.
- Embedded `codex app-server` sessions now launch in their own process group on Unix, and Little Control Room tears down that whole group on close, idle-timeout cleanup, and transport failure so long-lived child tool processes (for example `vite preview`) do not survive the embedded session.
- Observed ChatGPT-backed `403 Forbidden` failures on `backend-api/codex/responses` can reject both the websocket path and the later HTTP fallback even while `codex login status` still reports logged in, so Little Control Room should treat that pattern as an auth/account-side Codex failure rather than a websocket-only transport bug.

Current OpenCode transport assumption:

- The installed `opencode` CLI on this machine (observed `1.2.24`) exposes both `serve` and `acp`; `serve` publishes an HTTP/OpenAPI + SSE surface with session, message, status, event, permission, and question endpoints, and Little Control Room now uses that live surface for embedded OpenCode sessions.
- Observed `opencode.db` session parts are structured rather than text-only, including `text`, `reasoning`, `tool`, `patch`, `file`, `step-start`, and `step-finish`, so OpenCode transcript extraction and the embedded pane should preserve that structure instead of flattening it to plain text.
- Observed `prompt_async` behavior accepts a follow-up prompt while the session is still busy (returning `204` and later appending a second user/assistant turn), so the embedded pane can treat Enter as a steer/follow-up path much like embedded Codex.
- OpenCode `FilePartInput` accepts structured file parts and the current embedded implementation sends local image attachments as `data:` URLs; transport support is confirmed, but end-to-end image robustness should still be treated as provisional until more real image cases are exercised.

Current screenshot workflow assumption:

- `make screenshots` currently defaults to the repo-root `screenshots.local.toml` unless `SCREENSHOT_CONFIG` is overridden; the committed demo config remains available at `docs/screenshots.example.toml`.
- Screenshot capture scale is now configurable via `capture_scale`, and the default browser-rendered PNG export path uses `1.5x` capture scale for sharper text.
- Screenshot config also supports `live_runtime_project`, which renders a focused runtime-pane screenshot using a screenshot-only managed-runtime snapshot for the chosen project (defaulting to `selected_project` when unspecified).
- Screenshot export now preserves truecolor terminal escapes instead of forcing ANSI256 quantization, so the committed docs images can match the live TUI palette more closely.
- Screenshot export now also renders block-only ANSI pixel-art runs as explicit CSS pixel cells instead of font glyphs, avoiding hollow-edge “screen door” seams in the control-room vignette PNGs.
- The committed docs screenshot set now includes `main-panel.png`, `main-panel-live-runtime.png`, `codex-embedded.png`, `diff-view.png`, `diff-view-image.png`, and `commit-preview.png`.

## What Works

- Fast startup scan path for Codex using `~/.codex/state_5.sqlite` threads metadata
- Modern and legacy Codex JSONL session parsing
- OpenCode session detection from `~/.local/share/opencode/opencode.db`
- Artifact-first project detection from Codex/OpenCode metadata, with git discovery retained for repo metadata and move detection
- Attention ranking with transparent reasons plus latest-session assessment categories
- Repo-health surfacing for dirty worktrees and remote sync state
- Scope-aware persistence via path filters and project-name filters
- Cached `doctor` by default, with `doctor --scan` for a fresh rescan
- TUI main view with focusable project, detail, and runtime panes, scrolling viewports, compact help/settings overlays, and command palette
- Top-level `/open` slash command to open the selected project's folder in the system browser
- Project notes via `/note` or `n`, with a multiline modal editor, wrapped detail-pane notes, a list badge when a project has saved notes, and clipboard copy actions for the whole note or an explicit marked selection
- Managed per-project run commands via `/run` (`/start` alias), `/restart`, `/run-edit`, `/runtime`, and `/stop`, with persisted default commands, first-pass command suggestions from common project files, a small run-command editor overlay, a dedicated selectable runtime pane with output/actions, and best-effort listening-port detection for LCR-managed runtimes
- Animated runtime-pane empty state that shows a small ANSI truecolor pixel-art "Control Room" scene with a blinking operator, console, and subtle motion when the pane is wide/tall enough, while falling back to the older text-only empty-state summary in cramped layouts
- Git workflow actions in the TUI for full-screen diff preview, commit preview, finish, and push
- Embedded Codex pane via `codex app-server`, with multiline compose, per-project drafts, inline `[Image #n]` clipboard image markers, large clipboard-text placeholders, backspace-based marker removal, local embedded slash commands for `/new`, `/resume` (`/session` alias), `/model`, and `/status`, visible slash autocomplete/suggestions in the composer, a provider-specific saved-session resume picker with lightweight title/summary previews and current-session markers, live model/reasoning/context-left metadata under the transcript, a local model+reasoning picker backed by `model/list`, `Enter`/`/codex`/`/codex-new`, `Esc` or `Alt+Up` hide from the embedded pane with `Enter` reopening from the project list, `Alt+Down` session picker/history, `Alt+[`/`Alt+]` live-session stepping, wrapped transcript blocks, shaded echoed user transcript blocks that reuse the composer shell styling, denser command/tool/file blocks with `Alt+L` expand/collapse, label-free user/assistant transcript rendering, manager-side update coalescing, inline approvals/input requests, and busy-elsewhere rechecks when a read-only embedded session is reopened or restored
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

## Latest Update (2026-03-19 11:31 JST)

- Taught both the Codex artifact detector and the session-classifier lifecycle recovery to treat `event_msg.payload.type == "turn_aborted"` as a terminal turn event, which fixes the real interrupted-turn footprint seen in `2026_03_mothers_farm` where no later `task_complete` was written.
- Tightened embedded Codex session recovery so stale busy follow-ups refresh `thread/read` before steering, normalize `thread/read` results with no in-progress turns to idle even when `status.type` still says `active`, and handle live `turn/aborted` notifications as an interrupted terminal turn.
- Updated `docs/codex_cli_footprint.md` and the embedded transport assumptions above to record the newly confirmed interrupted-turn behavior.
- Verified against the live LCR DB after `make scan`: the `project_sessions` row for `/Users/davide/Library/CloudStorage/Dropbox/Family Room/Media/2026_03_mothers_farm` now shows `latest_turn_state_known=1` and `latest_turn_completed=1`, and the newest `session_classifications` row for that project is `completed/completed`.

Verification snapshot:

- `gofmt -w internal/codexapp/session.go internal/codexapp/types.go internal/codexapp/session_test.go internal/detectors/codex/detector.go internal/detectors/codex/detector_test.go internal/sessionclassify/extract.go internal/sessionclassify/extract_test.go internal/model/model.go` passed.
- `go test ./internal/codexapp -count=1` passed.
- `go test ./internal/detectors/codex -count=1` passed.
- `go test ./internal/sessionclassify -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-19T11:30:37+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 2`, `queued classifications: 77`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T11:30:47+09:00` (`projects: 133`).
- `sqlite3 ~/.little-control-room/little-control-room.sqlite "SELECT session_id,format,latest_turn_state_known,latest_turn_completed,datetime(latest_turn_started_at,'unixepoch','localtime'),datetime(last_event_at,'unixepoch','localtime') FROM project_sessions WHERE project_path='/Users/davide/Library/CloudStorage/Dropbox/Family Room/Media/2026_03_mothers_farm';"` returned `019d0331-5efa-7602-86ab-4416313f5a16|modern|1|1||2026-03-19 11:23:51`.
- `sqlite3 ~/.little-control-room/little-control-room.sqlite "SELECT session_id,status,category,summary,updated_at,completed_at FROM session_classifications WHERE project_path='/Users/davide/Library/CloudStorage/Dropbox/Family Room/Media/2026_03_mothers_farm' ORDER BY updated_at DESC LIMIT 3;"` returned the newest row as `019d0331-5efa-7602-86ab-4416313f5a16|completed|completed|...`.

Next concrete tasks:

- Check the next real interrupted embedded Codex turn in the TUI to confirm the new `turn/aborted` notification handling clears the live “Working …” banner immediately instead of waiting for stale-busy reconciliation.
- If any stale-busy reports remain after this, capture the raw embedded notification sequence so the remaining gap can be narrowed to a missing live protocol event instead of the artifact scanner path.

## Latest Update (2026-03-19 10:40 JST)

- Switched the AI commit-subject and untracked-file recommendation default model from `gpt-5-mini` to `gpt-5.4-mini`, and changed that shared commit-help path from `reasoning.effort = "minimal"` to `reasoning.effort = "low"` so it stays GPT-5.4-compatible while keeping the request lightweight.
- Tightened the focused `internal/gitops` tests so they now assert the outgoing reasoning effort is `low` and the returned tracked model is a GPT-5.4 mini snapshot, which keeps the new commit-help configuration covered instead of only changing production constants.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/gitops/message.go internal/gitops/message_test.go` passed.
- `go test ./internal/gitops -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-19T10:40:04+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 2`, `queued classifications: 77`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T10:40:13+09:00` (`projects: 133`).

Next concrete tasks:

- Watch a few real commit-preview suggestions under `gpt-5.4-mini` to confirm the quality bump is worth the higher per-call cost versus `gpt-5.4-nano`.
- If commit subjects still feel over-detailed, tighten the prompt contract before changing models again.

## Latest Update (2026-03-19 10:25 JST)

- Changed the session-classifier retry plan from `medium -> minimal` to `medium -> low`, keeping the primary assessment effort unchanged while making the fallback compatible with GPT-5.4 models such as `gpt-5.4-nano`, which reject `minimal`.
- Switched the session classifier default model from `gpt-5-mini` to `gpt-5.4-nano`, so new assessment runs now target the cheaper GPT-5.4 nano path by default while still using `medium` reasoning on the first attempt.
- Kept the retry behavior otherwise unchanged: the fallback still only applies on retryable transport/API failures or incomplete outputs, so normal classifier requests still run at `medium`.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- No verification rerun in this step, per user request.

Next concrete tasks:

- Re-run the live multi-project assessment bake-off later, after a bit of real usage, so cost and quality are measured on the actual production model and retry plan.
- Decide whether to migrate commit-subject generation separately after enough footer cost data has accumulated under the new classifier model.

## Latest Update (2026-03-19 08:34 JST)

- Centralized the current OpenAI Responses API access behind a shared `internal/llm` transport layer, so classifier, commit-subject suggestion, and untracked-file recommendation prompts now all use the same request/response path instead of each feature building its own HTTP client flow.
- Added a shared LLM usage tracker in `service` and wired both classifier and commit-help clients through it, so the footer cost estimate now includes commit-help traffic as well as session classification instead of remaining classifier-only.
- Kept the footer/session-usage surface compatible by preferring the new centralized usage snapshot when present while falling back to the older classifier snapshot path for empty/test scenarios, and added commit-path regressions proving tracked usage cost now increments on both commit-subject and untracked-review requests.
- Updated the README cost note to describe the footer estimate as covering the current summary/classification and commit-help paths.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/llm/usage.go internal/llm/responses.go internal/service/service.go internal/gitops/message.go internal/gitops/message_test.go internal/gitops/untracked.go internal/sessionclassify/client.go` passed.
- `go test ./internal/llm ./internal/gitops ./internal/sessionclassify ./internal/service -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-19T08:34:13+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T08:34:13+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-central-usage-check CONFIG=/tmp/lcroom-cost-estimator-config.toml DB=/tmp/lcroom-central-usage.sqlite INTERVAL=1h` reached the TUI sandbox and exited via `q`.

Next concrete tasks:

- Switch the session classifier from `gpt-5-mini` to `gpt-5.4-nano` with `reasoning.effort = "medium"` now that the shared cost tracker covers both assessment and commit-help paths.
- Decide whether the older classifier-local usage tracker should now be removed entirely, since the service-level shared tracker is the new source for footer accounting.

## Latest Update (2026-03-19 08:02 JST)

- Replaced the footer's raw `tok in/out` classifier usage badge with a cost estimate badge, so the TUI now shows approximate tracked OpenAI spend instead of only token volume.
- Added a small GPT-5 pricing table for the currently relevant classifier candidates (`gpt-5-mini`, `gpt-5.4-mini`, `gpt-5-nano`, and `gpt-5.4-nano`) plus snapshot-alias handling, and started recording per-call estimated USD cost alongside token totals when classifier responses come back with usage data.
- Switched the footer usage pulse to react to estimated cost increases instead of token-count increases, added focused pricing tests, and refreshed the README costs copy to describe the footer as an estimate rather than telling users to infer cost from tokens alone.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/model/model.go internal/model/llm_pricing.go internal/model/llm_pricing_test.go internal/sessionclassify/client.go internal/tui/app.go internal/tui/app_test.go` passed.
- `go test ./internal/model ./internal/sessionclassify ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-19T08:01:37+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T08:01:37+09:00` (`projects: 132`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-cost-estimator-check CONFIG=/tmp/lcroom-cost-estimator-config.toml DB=/tmp/lcroom-cost-estimator.sqlite INTERVAL=1h` reached the TUI sandbox and exited via `q`.

Next concrete tasks:

- Switch the session classifier from `gpt-5-mini` to `gpt-5.4-nano` with `reasoning.effort = "medium"` now that the footer can show a pricing estimate for that model.
- Decide whether the footer cost estimate should stay aligned with the old classifier-only usage scope for now or be broadened to also aggregate commit-help usage before we start mixing models across more workflows.

## Latest Update (2026-03-19 06:48 JST)

- Tightened the embedded fresh-session open path so top-level `/codex-new` and embedded `/new` now automatically retry once when a forced fresh launch comes back on the same prior thread instead of immediately showing that stale session.
- Added a focused TUI regression for the specific first-open failure mode: no live embedded session exists, the first forced-new launch reopens the previous saved thread, and the automatic retry lands on a genuinely fresh thread.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/codex_pane.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -run 'Test(LaunchCodexForSelectionForceNewRetriesWhenPreviousThreadReopensFirst|LaunchCodexForSelectionForceNewWarnsWhenActiveSessionIsReopenedReadOnly|VisibleCodexSlashNewStartsFreshSession)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-19T06:47:26+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T06:47:26+09:00` (`projects: 132`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-parallel-codex-new-retry-check` reached the TUI sandbox and exited via `q`.

Next concrete tasks:

- Run a live embedded Codex repro against the real `codex app-server` the next time the stale first-open case appears, to confirm the automatic retry eliminates the user-visible failure outside the fake-session path too.
- If Codex eventually exposes an explicit fresh-session guarantee or error for “reopened existing thread,” replace this retry-on-same-thread recovery with that provider signal.

## Latest Update (2026-03-18 17:31 JST)

- Updated the screenshot HTML exporter so block-only ANSI runs (`█`, `▀`, `▄`) are no longer rendered through the browser font stack; they now render as explicit CSS pixel cells, which removes the hollow-edge “screen door” gaps from the control-room vignette in exported PNGs.
- Added a focused screenshot regression that locks in the pixel-cell path for block-glyph runs and regenerated the committed docs screenshots, including `docs/screenshots/main-panel.png`, with the crisper control-room scene.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/screenshots.go internal/tui/screenshots_test.go` passed.
- `go test ./internal/tui -run 'TestRenderTerminalHTMLDocument(IncludesEscapedTextAndColors|IncludesTrueColorEscapes|UsesPixelCellsForBlockGlyphRuns)' -count=1` passed.
- `make screenshots` passed and rewrote the committed PNG set in `docs/screenshots/`.
- `make test` passed.
- `make scan` passed at `2026-03-18T17:31:03+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T17:31:03+09:00` (`projects: 132`).
- A zoomed visual check of the regenerated `docs/screenshots/main-panel.png` confirmed that the control-room sprite now renders as solid pixel blocks instead of hollow-edged font blocks.

Next concrete tasks:

- If other screenshot scenes start using additional Unicode block-drawing characters, extend the pixel-cell renderer beyond `█`, `▀`, and `▄` before relying on those glyphs in docs art.
- If the terminal screenshot typography ever moves away from the current browser/font path, keep this pixel-cell shortcut for sprite-like runs so the control-room art stays crisp regardless of font metrics.

## Latest Update (2026-03-18 17:22 JST)

- Tightened embedded fresh-session launch feedback so `/codex-new` and embedded `/new` now detect when a supposed fresh Codex launch lands back on the same thread and that thread is already active in another process, instead of pretending a new session opened successfully.
- The open-status copy now tells the user that Little Control Room could not start a fresh embedded session because the existing session is active elsewhere and that the app is showing that session read-only instead.
- Added focused TUI regressions for both entry points: top-level `/codex-new` from the project list and embedded `/new` inside an existing Codex pane.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/codex_pane.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -run 'Test(VisibleCodexSlashNewWarnsWhenActiveSessionIsReopenedReadOnly|LaunchCodexForSelectionForceNewWarnsWhenActiveSessionIsReopenedReadOnly|VisibleCodexSlashNewStartsFreshSession|VisibleOpenCodeSlashNewFailureKeepsClosedSessionVisible|LaunchCodexForSelectionShowsOpeningStateInsteadOfPreviousSession)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T17:22:07+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T17:22:14+09:00` (`projects: 132`).
- `env -u OPENAI_API_KEY COLUMNS=112 LINES=31 make tui` correctly refused to share the main DB because another TUI runtime was already active (`pid 88777`), so the UI smoke check was rerun safely in parallel.
- `env -u OPENAI_API_KEY COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-parallel-codex-new-busy-elsewhere-check` reached the TUI sandbox and exited via `q`.

Next concrete tasks:

- If Codex eventually exposes a first-class “fresh session blocked by active external thread” signal, switch this detection from thread-ID comparison to that explicit protocol signal.
- Consider surfacing the same fresh-session failure copy inside the read-only transcript notice block, not only in the global status line.

## Latest Update (2026-03-18 17:08 JST)

- Softened the README startup copy so it simply says the OpenAI key is required at startup and saved via Settings, without explicitly calling out environment-variable lookup behavior.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `sed -n '44,68p' README.md` confirmed the startup instructions now say the key is required and saved via `/settings`, without the extra env-var sentence.
- No tests were run for this docs-only wording change.

Next concrete tasks:

- If we keep refining onboarding copy, check that `README.md`, `docs/reference.md`, and the in-app Settings blocker stay phrased at the same level of detail.

## Latest Update (2026-03-18 16:57 JST)

- Moved the OpenAI API key from environment-only wiring into persisted settings/config via a new `openai_api_key` field, and updated both latest-session classification and AI commit-help clients to read from that stored value instead of `OPENAI_API_KEY`.
- Made the TUI demand a saved key on startup: when the config lacks `openai_api_key`, launch now opens the Settings overlay immediately with blocker copy explaining that the key is required for session summaries, classifications, and commit help, and `Esc` no longer dismisses that blocker until a key is saved.
- Added a dedicated `OpenAI API key` field at the top of Settings, hid the stored value behind password masking, showed only a short suffix hint like `...12345`, and tightened settings saves so blank keys are rejected and config files are written with `0600` permissions.
- Reworked the service/session-classifier plumbing so the saved key applies live inside the running TUI without needing an env var restart, while keeping the background classifier manager reusable when the key is configured later.
- Updated user-facing help/docs/examples so the repo now describes the config-backed key flow instead of the old env-var setup.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/config/config.go internal/config/editable.go internal/config/config_test.go internal/sessionclassify/client.go internal/sessionclassify/sessionclassify.go internal/gitops/message.go internal/service/service.go internal/cli/run.go internal/tui/settings.go internal/tui/app.go internal/tui/app_test.go internal/commands/commands.go` passed.
- `go test ./internal/config ./internal/sessionclassify ./internal/gitops ./internal/service ./internal/tui ./internal/commands -count=1` passed.
- `go test ./internal/cli ./internal/tui -count=1` passed after restoring the needed `sessionclassify` import in `internal/cli/run.go`.
- `make test` passed.
- `make scan` passed at `2026-03-18T16:56:33+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T16:56:40+09:00` (`projects: 132`).
- `env COLUMNS=112 LINES=31 make tui DATA_DIR=/tmp/lcroom-openai-key-check CONFIG=/tmp/lcroom-openai-key-check/config.toml DB=/tmp/lcroom-openai-key-check/little-control-room.sqlite INTERVAL=1h` opened the TUI on an isolated temp config with no key, landed directly in the Settings blocker, showed the masked `OpenAI API key` field and explanatory copy, and was then terminated from the shell after the smoke check.

Next concrete tasks:

- Decide whether replacing an already-saved API key should get a tiny confirmation affordance or “test key” check so users can catch typos before leaving Settings.
- Consider whether the settings overlay should expose a dedicated clear/revoke action for the saved key instead of relying on freeform editing now that the field is mandatory on startup.

## Latest Update (2026-03-18 08:14 JST)

- Normalized user-requested managed runtime stops so `/stop` no longer leaves the runtime snapshot in an error-looking `signal: terminated` / `exit -1` state after Little Control Room sends `SIGTERM` to the runtime process group.
- The runtime manager now records an explicit stop request before terminating the process and clears the later exit-code/error bookkeeping for that requested shutdown path, while still preserving normal non-stop failures; `CloseAll()` now marks those shutdowns the same way so quit-time cleanup is consistent too.
- Updated runtime status rendering so an intentionally stopped runtime now reads as `stopped` instead of surfacing a raw negative exit code, and added focused regressions for both the manager snapshot normalization and the TUI status copy.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/projectrun/manager.go internal/projectrun/manager_test.go internal/tui/runtime_view.go internal/tui/runtime_view_test.go` passed.
- `go test ./internal/projectrun ./internal/tui -run 'Test(StopNormalizesUserRequestedTermination|RenderRuntimeStatusValueShowsStoppedForUserStoppedRuntime|QuitKeyStopsManagedRuntimes)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T08:14:45+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 2`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T08:14:45+09:00` (`projects: 132`).

Next concrete tasks:

- Run a quick live TUI pass with a real long-lived dev server and confirm the runtime pane now settles on `stopped` after `/stop`, especially in restart-heavy workflows.
- Decide whether the inspector should eventually distinguish `stopped by user` from other graceful stops, or whether the shorter neutral `stopped` label is the right long-term copy.

## Latest Update (2026-03-18 03:05 JST)

- Broadened the operator head again so the face now extends one pixel farther on each side, keeping the eyes more inset from the hairline and making the sprite read closer to a simple square pixel-art head.
- Lengthened the idle scene cycle to 32 beats and turned the right-terminal dwell into a true pause: while parked there, the operator now stays in one typing pose with only a brief blink instead of bobbing or alternating poses.
- Updated the runtime-flair regression to assert that the right-terminal hold keeps the same position and pose across the dwell window, so future tweaks do not reintroduce the “touch and go” feel.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/runtime_flair.go internal/tui/runtime_flair_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T03:04:35+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T03:04:42+09:00` (`projects: 132`).
- `env -u OPENAI_API_KEY COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-parallel-flair-check6` reached the TUI in a no-token sandbox, showed the wider head plus the steadier right-terminal pause, and exited via `q`.

Next concrete tasks:

- If the head still feels too wide in some terminal fonts, try moving the eyes inward by one pixel before adding any new facial detail.
- Consider calming the desk monitor flicker a bit during the right-terminal pause so the stillness reads even more clearly from a quick glance.

## Latest Update (2026-03-18 02:52 JST)

- Fixed another embedded Codex stale-turn edge case: if `turn/steer` now comes back with `no active turn to steer`, Little Control Room re-reads the thread and starts a fresh turn whenever there is no in-progress turn left, instead of surfacing the stale-turn error back to the user.
- Tightened stale-busy recovery so `thread/read` no longer keeps the session in `Working` just because `thread.status.type` still says `active`; if there is no in-progress turn in `thread.turns[]`, the session now reconciles back to idle.
- Tightened resumed-session hydration the same way, so reopening a thread that has no live turn no longer revives it as a bogus busy/external session.
- Softened the embedded send confirmation copy from “Steer sent…” to “Follow-up sent…” so the UI stays accurate whether the follow-up actually steered a live turn or recovered by starting a new one.
- Added focused `internal/codexapp` regressions for all three cases: resumed active-without-turn threads, steer recovery from `no active turn to steer`, and stale-busy reconciliation when `thread/read` says `active` but exposes no in-progress turns.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/session.go internal/codexapp/session_test.go internal/tui/codex_pane.go` passed.
- `go test ./internal/codexapp ./internal/tui` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T02:51:30+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 2`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T02:51:38+09:00` (`projects: 132`).
- `env -u OPENAI_API_KEY COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-parallel-stale-turn-check` reached the TUI in a no-token sandbox and was closed via `q`.

Next concrete tasks:

- Reproduce the original stuck `Working` / `no active turn to steer` scenario in a live embedded Codex turn and verify the footer now falls back to idle soon after the turn really ends.
- If the app-server keeps producing more “thread says active but no turn exists” cases, consider surfacing a tiny transcript/debug breadcrumb so future protocol oddities are easier to diagnose from inside the pane.

## Latest Update (2026-03-18 03:19 JST)

- Updated the `/new-project` flow so it accepts pasted shell-quoted paths such as macOS-style single-quoted folder paths, normalizing matching outer quotes before path resolution in both the TUI preview and the service layer.
- Added a path-only add mode for existing folders: when the user manually edits the path field, leaves `Name` blank, and the path already exists as a directory, Little Control Room now derives the project name from the final path segment and adds that folder directly.
- Kept the existing parent-plus-name creation flow safe by avoiding automatic name derivation for the dialog's default parent path or recent-path shortcuts unless the user manually edits the path field first, preventing accidental adds of a parent directory on a blank-name Enter press.
- Surfaced the derived-name behavior in the dialog/status copy and command summary, and added focused regressions for quoted existing paths, missing-name validation, derived-name preview hints, and the default-parent-path guardrail.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/service/project_create.go internal/service/project_create_test.go internal/tui/new_project.go internal/tui/new_project_test.go internal/tui/app.go` passed.
- `go test ./internal/service ./internal/tui -run 'Test(CreateOrAttachProject|NewProject)' -count=1` passed.
- `go test ./internal/commands -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T03:19:00+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T03:19:00+09:00` (`projects: 132`).
- `env -u OPENAI_API_KEY COLUMNS=112 LINES=31 make tui DATA_DIR=/tmp/lcroom-new-project-check CONFIG=/tmp/lcroom-new-project-check/config.toml DB=/tmp/lcroom-new-project-check/little-control-room.sqlite INTERVAL=1h` reached the TUI on an isolated temp DB and exited cleanly via `q`.

Next concrete tasks:

- Consider whether the dialog should offer a small inline affordance such as `Paste full path` or `Use folder name` so the path-only mode is more discoverable than a status hint alone.
- If we later want to support shell-escaped pasted paths with embedded quotes or backslashes beyond simple outer quoting, add that deliberately with explicit tests rather than broad path munging.

## Latest Update (2026-03-18 02:36 JST)

- Simplified the operator head again by removing the tiny headset-side accents from the face area entirely and reducing the face to a clearer hair-frame, eyes, jaw, and neck shape so the sprite reads less like it has extra facial features.
- Slowed the idle loop further by increasing the scene cycle length and reducing pose changes to a calmer cadence, with the right terminal now getting the longest dwell in the route before the operator starts the return walk.
- Slowed the narrow-pane desk-only fallback too, so even the simplified scene keeps the more deliberate pacing.
- Updated the walk-state regression expectations to the longer 30-step cycle and the extended right-terminal hold.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/runtime_flair.go internal/tui/runtime_flair_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T02:30:33+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T02:30:44+09:00` (`projects: 132`).
- `env -u OPENAI_API_KEY COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-parallel-flair-check5` reached the TUI, rendered the updated idle scene in a no-token sandbox, and was closed via `q`.

Next concrete tasks:

- If the face still feels too ambiguous in your terminal font, the next move should be a genuinely smaller head sprite rather than more detail: fewer pixels usually read better than more at this scale.
- If the route still feels unclear, consider turning the left panel into a more terminal-like object so the “walk between two stations” idea reads even in a static frame.

## Latest Update (2026-03-18 02:30 JST)

- Tweaked the operator face again by moving the small headset/mic accent out of the face area so the turned sprite no longer reads like it has an extra eye or mouth tucked under one eye.
- Lengthened the idle cycle so the operator now lingers longer at each station, with a noticeably longer dwell at the right-hand desk terminal before starting the return walk.
- Slowed the narrow-pane desk-only loop as well, so even the fallback scene keeps the calmer pacing instead of flipping between typing frames too quickly.
- Updated the operator-state regression to assert the longer station holds and the shifted walk timing across the expanded cycle.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/runtime_flair.go internal/tui/runtime_flair_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T02:30:33+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T02:30:44+09:00` (`projects: 132`).
- `env -u OPENAI_API_KEY COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-parallel-flair-check3` reached the TUI, exercised the slower linger-at-terminal loop in a no-token sandbox, and exited via `q`.

Next concrete tasks:

- If the face still reads oddly in some terminal fonts, try a narrower face width or closer-set eyes before adding any more facial detail.
- Consider turning the left status panel into a more explicit workstation silhouette so the “two terminals” route is legible even when the user catches only a single frame.

## Latest Update (2026-03-18 02:26 JST)

- Tuned the control-room operator again after a live visual pass: the face now drops the extra dark lower-face pixel that was reading like extra eyes, keeping the expression closer to a simple two-eye sprite.
- Slowed the walk loop down by expanding the animation cycle and repeating each travel step so the operator now clearly pauses at one terminal, walks across, spends a beat at the other terminal, and only then heads back.
- Updated the scene timing so the terminal lights and monitor flicker stay in sync with the longer cycle rather than flashing at the earlier faster cadence.
- Extended the walk-state unit test coverage to assert the new linger-at-terminal behavior and the slower multi-step return path.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/runtime_flair.go internal/tui/runtime_flair_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T02:26:11+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 2`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T02:26:24+09:00` (`projects: 132`).
- `env -u OPENAI_API_KEY COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-parallel-flair-check2` reached the TUI, exercised the slower idle cycle in a no-token sandbox, and exited via `q`.

Next concrete tasks:

- If the face still feels off after another real screenshot pass, consider a slightly narrower head or closer-set eyes rather than adding more facial detail.
- Decide whether the left-side panel should become a more explicit terminal/workstation so the character's two-stop loop reads immediately even without motion.

## Latest Update (2026-03-18 02:12 JST)

- Polished the runtime-pane control-room animation by replacing the original single-sprite arm layering with explicit inspect, walk, and typing poses so the operator no longer shows overlapping raised/down arms.
- Simplified the face pixels to remove the heavier internal outline look, added a lighter face-shadow cue plus blink frames, and nudged the desk farther right so the operator has more room to read as a character instead of a static centered icon.
- Added a small back-and-forth walk cycle between the side panel and the main desk terminal when the pane is wide enough, while keeping narrower panes on a desk-only typing loop so cramped layouts still read cleanly.
- Added focused unit coverage for the operator state machine so the left-to-right walk path and the narrow-pane desk-only fallback stay intentional as the scene evolves.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/runtime_flair.go internal/tui/runtime_flair_test.go internal/tui/app_test.go internal/tui/runtime_inspector.go` passed.
- `go test ./internal/tui -run 'TestRenderRuntimePane(ShowsControlRoomFlairWhenEmpty|ControlRoomFlairAnimates|FallsBackToTextWhenTooNarrowForFlair)' -count=1` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T02:11:52+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T02:12:02+09:00` (`projects: 132`).
- `env -u OPENAI_API_KEY COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-parallel-flair-check` reached the TUI, exercised the updated idle scene in a no-token sandbox, and exited via `q`.

Next concrete tasks:

- Decide whether the operator should react to runtime state next, for example pausing at the desk for a longer typing loop when a run command exists or switching to a warning-light pose when the latest runtime ended with an error.
- Consider whether the scene should gain one or two tiny environmental accents that do not compete with the character, for example a cable/floor shadow pass or a second blinking desk indicator for the large-pane layout.

## Latest Update (2026-03-18 01:58 JST)

- Added `make tui-parallel`, a dedicated debug/smoke-launch target for opening a second TUI alongside the main one without sharing the live SQLite state.
- The new target uses its own config and DB under `/tmp/lcroom-parallel-<user>/`, copies the main config only on first launch for convenience, and intentionally avoids `--allow-multiple-instances` so the normal single-instance runtime guard still protects the day-to-day TUI.
- This makes it practical to test visual/runtime changes, including the new control-room idle scene, in a throwaway parallel sandbox without disturbing the primary dashboard session.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `make test` passed.
- `make scan` passed at `2026-03-18T01:58:19+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 3`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T01:58:29+09:00` (`projects: 132`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-parallel-smoke` reached the TUI, rendered normally, and exited via `q`.

Next concrete tasks:

- Decide whether `tui-parallel` should stay as a Make-only convenience or also get a documented CLI alias/script for users who do not rely on the repo Makefile.
- If we add a flair settings toggle next, use the parallel sandbox path for iterative UI/smoke checks so the primary TUI session can stay attached to the real working DB.

## Latest Update (2026-03-18 01:53 JST)

- Added a new runtime-pane empty state that renders an original low-res ANSI truecolor "Control Room" scene with a tiny animated operator, monitor wall, desk, and blinking lights instead of the plain `Output` box when the selected project has no saved/run-time runtime data and the pane is large enough.
- Kept the effect low-risk by rendering it only in that no-runtime state, by driving animation from the existing spinner tick, and by falling back automatically to the previous text-only runtime summary whenever the pane is too cramped for the scene.
- Added focused TUI regressions for the new scene, including coverage for the animated frames and the narrow-pane fallback, and refreshed older layout tests so they accept either the legacy runtime empty state or the new control-room rendering.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/runtime_flair.go internal/tui/runtime_inspector.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -run 'TestRenderRuntimePane(ShowsControlRoomFlairWhenEmpty|ControlRoomFlairAnimates|FallsBackToTextWhenTooNarrowForFlair)|TestRuntimePaneShowsRuntimeOutputAndActions' -count=1` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T01:51:49+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T01:51:50+09:00` (`projects: 132`).
- `env COLUMNS=112 LINES=31 make tui DB=/tmp/lcroom-runtime-flair-smoke.sqlite` reached the TUI, rendered the new runtime-pane control-room scene during a live smoke run, and exited via `q`.

Next concrete tasks:

- Decide whether the control-room scene should get a small settings toggle (`off` / `subtle` / `full`) before we add more ambient motion.
- If the scene stays, consider teaching it a few more runtime-aware poses, for example a typing loop when a run command exists but no output has arrived yet, or a warning-light variant for runtime errors.
- Decide whether the committed screenshot/demo flow should get a dedicated idle-runtime showcase asset so the new visual identity appears in docs without replacing the existing live-runtime screenshot.

## Latest Update (2026-03-18 01:06 JST)

- Refined the session-classifier prompt so dashboard summaries now explicitly write from the implicit assistant point of view, omit leading scaffolding like `Assistant is ...`, and avoid falling into a new stock opener.
- Tightened the classifier JSON schema description for `summary` to match that style, clarifying that brief fragments are acceptable and that the assistant should stay implicit rather than named as the subject.
- Updated the focused `internal/sessionclassify` regression so it captures the outbound Responses request and asserts both the system prompt and the schema ask for implicit-assistant, non-templated summary wording.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/sessionclassify/client.go internal/sessionclassify/client_test.go` passed.
- `go test ./internal/sessionclassify -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T01:06:54+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 2`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T01:06:55+09:00` (`projects: 132`).

Next concrete tasks:

- Decide whether older persisted session summaries that already start with `Assistant is ...` should be reclassified once or simply age out naturally.
- Watch fresh classifier outputs in the TUI/doctor flow to confirm the revised prompt consistently yields implicit-assistant wording without converging on a different canned opener.

## Latest Update (2026-03-18 00:55 JST)

- Tightened the embedded Codex `403` diagnosis copy specifically for the status/footer path: the transcript and `LastSystemNotice` still keep the longer auth/account explanation, but the live `snapshot.Status` string now collapses to the short label `Codex auth/session rejected (HTTP 403)` so the embedded turn/status bar stays readable.
- Adjusted the focused `internal/codexapp` regression to assert the shorter status label while preserving the existing longer transcript notice behavior.
- No Codex/OpenCode footprint assumptions changed beyond the already-documented `403` diagnosis behavior, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/session.go internal/codexapp/session_test.go` passed.
- `go test ./internal/codexapp -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T00:55:11+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T00:55:11+09:00` (`projects: 132`).

Next concrete tasks:

- If we see other long diagnostic strings crowding the embedded footer, consider introducing an explicit short-status versus long-notice pattern for a few more high-noise failures instead of only this `403` case.
- Decide whether session-picker summaries should prefer the compact status label or the richer `LastSystemNotice` when the active issue is already visible in the transcript.

## Latest Update (2026-03-18 00:41 JST)

- Added a one-shot embedded Codex diagnosis for ChatGPT-backed `403 Forbidden` failures on `backend-api/codex/responses`: Little Control Room still preserves the raw `codex stderr` or turn error text, but now also appends a clearer system notice that points toward ChatGPT auth/session or entitlement trouble instead of implying a generic local transport failure.
- Covered the new behavior with focused `internal/codexapp` regressions that verify the diagnosis appears for the websocket-stderr form of the failure and that the higher-signal notice is only appended once even if a later HTTP fallback also fails with the same `403`.
- Confirmed from the local Codex CLI log that this failure pattern is not websocket-only on this machine: the client retries websocket streaming, falls back to HTTP, and then still receives `403 Forbidden` from `https://chatgpt.com/backend-api/codex/responses`, while the public status page also reported a live ChatGPT sign-in/account incident during the same window.
- No Codex/OpenCode footprint assumptions changed beyond the embedded transport diagnosis note above, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/session.go internal/codexapp/session_test.go` passed.
- `go test ./internal/codexapp -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-18T00:40:32+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-18T00:40:32+09:00` (`projects: 132`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-auth403-diagnosis-smoke.sqlite` reached the TUI and exited via `q`.

Next concrete tasks:

- Decide whether the embedded Codex pane should also surface the same auth/account diagnosis when the failure arrives only through a structured app-server notification and not through stderr.
- If more auth-side Codex failures appear in the wild, consider grouping the repeated raw retry chatter into a compact local summary so the embedded transcript stays readable during multi-retry outages.

## Latest Update (2026-03-17 21:21 JST)

- Hardened embedded Codex shutdown on Unix by launching `codex app-server` in its own process group and terminating that whole group on explicit close, idle reaping, and transport failure, which prevents long-lived child tool processes from being orphaned when the embedded session goes away.
- Added a Unix-only regression test that starts a background child process under a shell, invokes the new terminator, and asserts the child dies with the parent process group instead of lingering.
- No Codex/OpenCode footprint assumptions changed beyond the embedded transport cleanup behavior above, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/session.go internal/codexapp/process_unix.go internal/codexapp/process_other.go internal/codexapp/process_unix_test.go` passed.
- `go test ./internal/codexapp -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T21:21:32+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T21:21:39+09:00` (`projects: 132`).

Next concrete tasks:

- Consider applying the same process-group cleanup helper to the embedded OpenCode server path, which still uses direct parent-process kills today.
- Run one live embedded Codex smoke check against a real long-lived tool command and confirm the descendant process disappears immediately after the pane is explicitly closed or reaped for inactivity.

## Latest Update (2026-03-17 19:41 JST)

- Reduced embedded Codex/OpenCode pane redraw cost for long sessions by caching the latest per-project session snapshot in the TUI and reusing a rendered transcript body keyed by project, width, dense-block mode, and transcript revision instead of rebuilding it on every normal redraw.
- Updated the visible embedded-pane flow to refresh that cache when sessions open, reopen, or emit `codexUpdateMsg`, while keeping ordinary typing and spinner redraws on the already-rendered transcript whenever the session text has not changed.
- Added focused TUI regressions that verify status-only snapshot changes do not invalidate the transcript revision and that a cache-primed embedded pane no longer rereads the session snapshot while typing.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/codex_pane.go internal/tui/codex_composer.go internal/tui/codex_picker.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -run 'Test(StoreCodexSnapshotOnlyInvalidatesTranscriptRevisionWhenTranscriptChanges|VisibleCodexViewUsesCachedSnapshotWhileTyping)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T19:41:10+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 2`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T19:41:19+09:00` (`projects: 132`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-codex-transcript-cache-smoke.sqlite` reached the TUI, rendered the main view, and exited via `q`.

Next concrete tasks:

- Watch whether the remaining redraw cost is now mostly in lower-pane/footer recomposition and, if so, decide whether that area needs its own lightweight cache or spinner-specific throttling.
- Decide whether the same snapshot-caching approach should also be applied to other live embedded/session surfaces that still call `session.Snapshot()` directly outside the visible-pane hot path.

## Latest Update (2026-03-17 19:13 JST)

- Reordered the help card's quick-action row to emphasize `note`, `sort/view`, `pin`, and `Ctrl+V image`, and swapped the slash-command examples to the more useful set: `/codex`, `/opencode`, `/settings`, `/commit`, `/diff`, and `/run`.
- Removed the old dev-only `x`/`e` section-toggle shortcuts from normal mode instead of merely hiding them in the help text, so the help and the actual keybindings now match.
- Kept the compact overlay help card structure from the prior pass, including the preserved background rendering and the explicit slash-command palette explanation.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T19:12:52+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T19:12:59+09:00` (`projects: 132`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-help-overlay-smoke.sqlite` reached the TUI, toggled help via `?`, and exited via `q`.

Next concrete tasks:

- Decide whether the slash-command examples in the help card should eventually become context-aware, for example preferring runtime commands when the runtime pane is focused.
- If section toggles are no longer part of the day-to-day workflow, decide whether `/sessions` and `/events` themselves should also leave the public command surface or stay available as explicit commands only.

## Latest Update (2026-03-17 18:59 JST)

- Refined the help overlay into a denser, color-coded card with clearer sections for palette usage, navigation, quick actions, compose/status controls, and the `AGENT`/`N`/`RUN`/`!` legend.
- Made the help copy explicitly explain the slash-command palette, including `Tab` completion there and concrete examples such as `/refresh`, `/run`, `/restart`, `/codex`, and `/help`.
- Fixed the help rendering path so it now uses the same overlay compositor as the other modals, keeping the project list/detail/runtime panes visible behind the card instead of replacing the whole body.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T18:59:42+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T18:59:49+09:00` (`projects: 132`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-help-overlay-smoke.sqlite` reached the TUI, opened help via `?`, and exited via `q`.

Next concrete tasks:

- Decide whether the same compact, color-coded structure should also be applied to the command palette and settings overlays so the modal family feels more unified.
- If the help card keeps growing, consider a second context-specific help view instead of turning the global overlay back into a long inventory.

## Latest Update (2026-03-17 18:46 JST)

- Added a user-visible `/start` command as a top-level alias for `/run`, keeping the canonical parsed form as `/run` so command handling and saved-command behavior stay unified.
- Added a top-level `/restart` command for the selected project's managed runtime, reusing the existing runtime restart path and matching the runtime-pane behavior when a saved or active command is available.
- Extended slash-command coverage in parser/TUI tests and synced the README plus `docs/reference.md` so the new runtime commands are discoverable from both the palette and the docs.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/commands/commands.go internal/commands/commands_test.go internal/tui/app.go internal/tui/app_test.go internal/tui/runtime_inspector.go` passed.
- `go test ./internal/commands ./internal/tui -count=1` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T18:47:46+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T18:47:56+09:00` (`projects: 132`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-start-restart-smoke.sqlite` reached the TUI and exited via `q`.

Next concrete tasks:

- Decide whether `/restart` should remain strict when no saved or active run command exists, or eventually fall back to the same run-command editor flow as `/run`.
- Keep the README and `docs/reference.md` command inventories aligned as more runtime-lane slash commands land, unless it becomes worth generating them from the command specs.

## Latest Update (2026-03-17 18:29 JST)

- Fixed a project-note persistence bug in the store merge path: refresh/scan upserts no longer overwrite the existing saved note for an already tracked project, which prevents an older scan snapshot from resurrecting note text the user already removed.
- Added a focused store regression test that clears a note, applies a stale follow-up `UpsertProjectState`, and asserts the cleared note stays cleared while scan-derived status fields still refresh.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/store -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T18:29:25+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 2`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T18:29:25+09:00` (`projects: 137`).

Next concrete tasks:

- Decide whether the same “scan must not replay stale user state” protection should also be applied to other user-managed project fields such as pin/snooze if similar reports show up.
- Run a short live TUI smoke pass around note editing if more reports come in, to confirm there is no separate front-end-only state issue beyond the store merge fix.

## Latest Update (2026-03-17 18:20 JST)

- Replaced the oversized multi-card help overlay with a single compact essentials card, so `?` and `/help` now show the same short, predictable cheat sheet instead of a long command inventory that could fall off moderate terminals.
- Dropped the dynamic LLM/status block and the verbose runtime/session prose from the help panel, keeping only the highest-signal global shortcuts plus a one-line compact legend for `AGENT`, `N`, `RUN`, and `!`.
- Tightened TUI coverage around the new behavior by asserting the help content stays compact and that the old verbose hints do not quietly reappear.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T18:20:19+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 2`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T18:20:28+09:00` (`projects: 137`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-help-minimal-smoke.sqlite` reached the TUI, opened the trimmed help overlay via `?`, and exited via `q`.

Next concrete tasks:

- Watch for any shortcuts that now feel too hidden and decide whether they belong in the footer or a contextual secondary help view instead of growing the global help modal again.
- If the help panel stays stable, consider reusing the same compact-copy discipline for other overlays that have started to accrete status/detail text.

## Latest Update (2026-03-17 17:43 JST)

- Added a concise slash-command section to the README with one-line descriptions for the main TUI palette plus the embedded Codex/OpenCode pane commands, so new users can scan the command surface without leaving the front page.
- Synced the command reference inventory with the actual command set by adding the missing `/new-project`, `/opencode`, and `/opencode-new` entries to `docs/reference.md`.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- Docs-only change; no code/test commands were rerun.

Next concrete tasks:

- Keep the README and `docs/reference.md` command lists in sync as new slash commands land, and only consider generating them from command specs if manual drift becomes annoying in practice.

## Latest Update (2026-03-17 17:29 JST)

- Added an internal ignored-project-name registry in the store, so stale project families can be hidden without relying on config excludes; normal project lists now suppress any tracked project whose exact name is in that registry.
- Added `/ignore` to hide the selected project's exact name and `/ignored` to open a reversible picker of hidden names where `Enter` restores the selected one.
- Kept the user's intentionally-disabled config excludes untouched and seeded the live store with `projects_control_center` in the new ignore registry, which hides the old Codex-generated project family while preserving an explicit path to bring it back later.
- Made CLI surfaces match the new behavior by filtering ignored names in `doctor` and `snapshot`, and documented `/ignore` plus `/ignored` in the README and command reference.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/store ./internal/commands ./internal/tui ./internal/cli -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T17:29:03+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- Inserted `projects_control_center` into the live `ignored_project_names` table in `/Users/davide/.little-control-room/little-control-room.sqlite`.
- `make doctor` passed on the cached snapshot dated `2026-03-17T17:29:19+09:00` (`projects: 132` after ignored-name filtering).
- `go run ./cmd/lcroom doctor | rg -n "projects:|projects_control_center"` returned only `projects: 132`, confirming that `projects_control_center` no longer appears in filtered output even with config excludes still disabled.
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-ignore-registry-smoke.sqlite` reached the TUI with a temp DB and exited via `q`.

Next concrete tasks:

- Decide whether the internal ignore registry should grow a second mode for exact paths, or whether exact-name ignore is the right default for Codex/OpenCode worktree cleanup.
- Consider surfacing matched hidden-project counts or sample paths more prominently inside `/ignored` so users can see exactly what each hidden name currently covers.

## Latest Update (2026-03-17 11:56 JST)

- Added large clipboard-text placeholders in the embedded composer so `Ctrl+V` and bracketed terminal paste now collapse oversized pasted text into compact markers like `[Paste #1: 2739 characters]` instead of flooding the visible composer.
- Hidden pasted text now stays in the per-project draft state and expands back to the real prompt only at submit time, so the model still receives the full text while the user-facing draft stays readable.
- Backspace now removes those pasted-text markers as a unit, the help copy mentions the shared image/text paste flow, and oversized echoed user transcript blocks now collapse to a short `[N characters]` placeholder so both embedded Codex and echoed OpenCode-style user messages stay compact after send.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T11:55:54+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 2`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T11:56:02+09:00` (`projects: 137`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-large-paste-smoke.sqlite` reached the TUI with a temp DB and exited via `q`.

Next concrete tasks:

- Decide whether the large-paste collapse threshold should stay hard-coded or move into a user setting once there is real usage feedback on what counts as “too large”.
- Consider adding a transient “peek expanded pasted text” affordance in the embedded pane if the compact placeholder proves too opaque for prompt review before sending.

## Latest Update (2026-03-17 11:36 JST)

- Changed the live assessment presentation so the project list and detail pane keep showing the last completed assessment while a new one is queued or running, instead of replacing it with internal scheduler/debug states.
- Added a short completed-assessment highlight in the main list and a footer usage pulse when total token usage increases, so the UI surfaces important changes without noisy status text.
- Fixed a store summary-query regression uncovered by `make test`: `GetProjectSummaryMap` now returns the same latest-completed assessment columns as the other project summary paths, keeping scans and rescans aligned with the calmer assessment UI.
- Extended screenshot sanitization/regressions so screenshot-only text cleanup also rewrites the persisted last-completed assessment summary, then regenerated the docs screenshots; the curated gallery now avoids transient assessment messages like `queued`/`waiting for model`.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/store ./internal/service ./internal/tui ./internal/config -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T11:35:58+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 2`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T11:36:07+09:00` (`projects: 137`).
- `make screenshots` passed and wrote `docs/screenshots/main-panel.png`, `docs/screenshots/main-panel-live-runtime.png`, `docs/screenshots/codex-embedded.png`, `docs/screenshots/diff-view.png`, `docs/screenshots/diff-view-image.png`, and `docs/screenshots/commit-preview.png`.
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-assessment-flash-smoke.sqlite` reached the TUI with a temp DB and exited via `q`.

Next concrete tasks:

- Decide whether the same “keep last completed assessment visible while refreshing” treatment should also be applied anywhere outside the main list/detail views, such as future REST/serve surfaces.
- Tune the strength/duration of the assessment flash and token-usage pulse after a bit of daily usage feedback, now that both cues are live.

## Latest Update (2026-03-17 11:11 JST)

- Tightened the docs screenshot organization by dropping the near-duplicate `main-panel-live-cx` asset and making `main-panel` the single dashboard-overview screenshot.
- Curated the overview screenshot itself so it now prefers a project with a stable user-facing assessment in the detail pane, while the list shows more simultaneous live AGENT timers with longer durations.
- Screenshot sanitization now strips non-completed assessment scheduling states (`queued`, `waiting for model`, similar internal progress) so the generated docs images emphasize user-facing categories instead of internal classifier bookkeeping.
- Regenerated the screenshot set and refreshed the README gallery so the committed assets are now `main-panel`, `main-panel-live-runtime`, `codex-embedded`, `diff-view`, `diff-view-image`, and `commit-preview`.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui ./internal/config -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T11:08:16+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T11:08:16+09:00` (`projects: 137`).
- `make screenshots` passed and wrote `docs/screenshots/main-panel.png`, `docs/screenshots/main-panel-live-runtime.png`, `docs/screenshots/codex-embedded.png`, `docs/screenshots/diff-view.png`, `docs/screenshots/diff-view-image.png`, and `docs/screenshots/commit-preview.png`.
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-screenshot-curation-smoke.sqlite` reached the TUI with a temp DB and exited via `q`.

Next concrete tasks:

- Decide whether the screenshot pipeline should eventually get an explicit “showcase selection” config knob instead of auto-picking a stable detail-pane project when the configured one is too internal/noisy.
- Consider whether the real live TUI should also hide or relabel queued classifier states, now that the screenshot curation made it obvious they read as internal scheduling rather than user-facing status.

## Latest Update (2026-03-17 10:57 JST)

- Extended the screenshot pipeline so it can render a focused runtime-pane scenario via a screenshot-only managed-runtime snapshot, which lets the generated docs set show a running project without depending on a live in-memory TUI session.
- Added `live_runtime_project` to the screenshot config, set the local docs config to target `FractalMech`, documented the new field, and regenerated the committed PNG set with a new `main-panel-live-runtime.png` asset.
- Refreshed the README screenshot gallery so the runtime-pane shot is visible alongside the existing dashboard, embedded session, diff, and commit-preview examples.
- Added focused regression coverage for parsing `live_runtime_project` and for rendering the runtime pane from a screenshot runtime snapshot override.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui ./internal/config -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T10:54:26+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T10:54:42+09:00` (`projects: 137`).
- `make screenshots` passed and wrote `docs/screenshots/main-panel.png`, `docs/screenshots/main-panel-live-runtime.png`, `docs/screenshots/codex-embedded.png`, `docs/screenshots/diff-view.png`, `docs/screenshots/diff-view-image.png`, and `docs/screenshots/commit-preview.png`.
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-screenshot-runtime-smoke.sqlite` reached the TUI with a temp DB and exited via `q`.

Next concrete tasks:

- Decide whether the runtime screenshot should eventually pull from persisted provider-neutral runtime state instead of the current screenshot-only synthetic snapshot if `doctor`/`serve` gain runtime reporting.
- Consider whether the screenshot set should also include a runtime pane state with multiple URLs or an error/conflict scenario now that the runtime lane has its own dedicated panel.

## Latest Update (2026-03-17 10:46 JST)

- Increased the vertical expansion for the focused lower row so the detail and runtime panes gain more height when selected, instead of mainly feeling wider.
- Kept the same three-pane structure and minimum list-height guardrails, so small terminals still keep the project list usable while the focused lower panes get a stronger emphasis.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui ./internal/commands -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T10:45:48+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T10:45:57+09:00` (`projects: 137`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-runtime-pane-vertical-smoke.sqlite` reached the TUI, rendered the taller focused-pane layout, and exited via `q`.

Next concrete tasks:

- Decide whether the focused lower row should grow even more on very tall terminals while keeping the current smaller-terminal floor unchanged.
- Consider a small user setting for list/detail/runtime balance if the preferred ratios turn out to vary a lot between projects and terminal sizes.

## Latest Update (2026-03-17 03:56 JST)

- Reworked the main TUI into a three-pane layout: project list on top, detail pane bottom-left, and a dedicated runtime pane bottom-right that expands horizontally when focused.
- Replaced the old modal runtime inspector and the inline detail-pane runtime preview with a persistent runtime pane that keeps runtime command, state, ports + URL, conflicts or errors, and the captured output tail visible at all times.
- `Tab` and `Shift+Tab` now cycle focus across list, detail, and runtime; `/runtime` now focuses the runtime pane instead of opening an overlay; runtime actions are selected with `Left` and `Right` and triggered with `Enter`.
- Updated footer/help/docs copy and focused TUI regressions to match the new pane model, including runtime-pane rendering, focus cycling, and `/runtime` command behavior.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui ./internal/commands -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-17T03:56:54+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T03:57:04+09:00` (`projects: 137`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-runtime-pane-smoke.sqlite` reached the TUI, rendered the new three-pane empty state, and exited via `q`.

Next concrete tasks:

- Decide whether the runtime pane should offer a small multi-URL chooser when a process announces more than one URL.
- Consider whether the runtime pane should remember a deeper output history than the current in-memory tail now that it is visible all the time.

## Latest Update (2026-03-17 03:37 JST)

- Compactified the selected-project runtime presentation so the shared detail pane now keeps runtime command, state, ports, URL, conflicts, errors, and a short output teaser instead of appending the full runtime tail inline.
- Added a dedicated runtime inspector opened with `r` or `/runtime`, with scrollable captured output plus quick `restart`, `stop`, and `open URL` actions wired into the same overlay/footer pattern as the rest of the TUI.
- Tightened the compact runtime summary further by packing related fields such as `Ports` + `URL` onto the same row when the pane is wide enough, so the runtime block spends fewer lines before the attention and session sections.
- Added an inline boxed runtime-output preview in the detail pane with quick `open output` and `open URL` hints, and fixed a quit-path bug where plain `q` / `Ctrl+C` could exit the TUI without calling `runtimeManager.CloseAll()`.
- `CloseAll()` now also waits briefly for managed runtimes to report stopped, which should reduce fast-restart races where a repo falls back to a secondary port like `3002` because the old listener is still winding down.
- Added the runtime restart flow, generalized browser opening for raw runtime URLs, refreshed help/docs copy, and added focused regressions for the runtime command/key path, compact detail rendering, runtime inspector rendering, browser URL opening, and quit-time runtime shutdown.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/commands ./internal/tui -count=1` passed for the initial runtime-panel pass, and `go test ./internal/tui -count=1` passed again after the compact row packing + inline preview + quit-shutdown follow-up.
- `make test` passed.
- `make scan` passed at `2026-03-17T03:35:15+09:00` (`activity projects: 86`, `tracked projects: 137`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-17T03:35:25+09:00` (`projects: 137`).
- `env COLUMNS=110 LINES=30 make tui DB=/tmp/lcroom-runtime-inspector-smoke.sqlite` reached the TUI with a temp DB, rendered the updated main layout after the inline preview + quit fix, and exited via `q`.

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
