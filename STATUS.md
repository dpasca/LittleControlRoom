# Little Control Room Status

Last updated: 2026-03-12 19:01 JST (JST)

## Current State

Implemented milestone:

1. Phase 0 footprint discovery doc + fixtures
2. Phase 1 monitoring foundation (`scan`, `doctor`, SQLite store, attention scoring, event bus)
3. Phase 2 TUI (`tui`) with refresh, filters, pin, snooze, note, command palette, and git workflow actions
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

Current screenshot workflow assumption:

- `make screenshots` currently defaults to the repo-root `screenshots.local.toml` unless `SCREENSHOT_CONFIG` is overridden; the committed demo config remains available at `docs/screenshots.example.toml`.
- Screenshot capture scale is now configurable via `capture_scale`, and the default browser-rendered PNG export path uses `1.5x` capture scale for sharper text.

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
- Git workflow actions in the TUI for full-screen diff preview, commit preview, finish, and push
- Embedded Codex pane via `codex app-server`, with multiline compose, per-project drafts, inline `[Image #n]` clipboard image markers in the composer, backspace-based image removal, local embedded slash commands for `/new`, `/model`, and `/status`, visible slash autocomplete/suggestions in the composer, live model/reasoning/context-left metadata under the transcript, a local model+reasoning picker backed by `model/list`, `Enter`/`/codex`/`/codex-new`, `Esc` or `Alt+Up` hide from the embedded pane with `Enter` reopening from the project list, `Alt+Down` session picker/history, `Alt+[`/`Alt+]` live-session stepping, wrapped transcript blocks, shaded echoed user transcript blocks that reuse the composer shell styling, denser command/tool/file blocks with `Alt+L` expand/collapse, label-free user/assistant transcript rendering, manager-side update coalescing, inline approvals/input requests, and busy-elsewhere rechecks when a read-only embedded session is reopened or restored
- Settings-backed Codex launch presets, currently defaulting to the dangerous `yolo` mode
- Programmatic screenshot generation via `lcroom screenshots` and `make screenshots`, using screenshot-config-driven browser-rendered PNG exports from deterministic HTML terminal scenarios

## Current Priorities

- Keep polishing the embedded Codex pane now that the main picker/attach/transcript ergonomics are in place.
- Consider a schema-aware mini form for MCP elicitation instead of the current freeform JSON/text fallback.
- Watch for future `codex app-server` protocol support for true queued turns and adopt it when it exists.
- Add a later generic terminal/dev-server lane beside Codex sessions.

## Status File Policy

- `STATUS.md` should stay short: current state plus the latest active work burst.
- Older historical notes now live in [docs/status_archive.md](docs/status_archive.md).
- If a note is mostly historical and no longer affects implementation, archive it instead of keeping it inline here.

## Latest Update (2026-03-12 19:01 JST)

- Polished the dedicated full-screen `/diff` screen so `Alt+Up` returns to the main project list, the footer uses the same highlighted action-chip style as the embedded Codex pane, and the selected file can be staged or unstaged in place with `-`.
- Split the diff sidebar into staged-first and unstaged-following sections, while keeping the existing single-file selection model and scroll behavior intact.
- Added commit-preview handoff into the same diff screen via `d`, wired stage/unstage support through service and git helpers so the diff preview refreshes after `git add -A --` or `git restore --staged --`, and updated the docs to describe the staged/unstaged grouping plus the `/diff` command.
- No Codex/OpenCode detector assumptions changed; `docs/codex_cli_footprint.md` stayed aligned with the current footprint expectations.

Verification snapshot:

- `go test ./internal/service -run 'TestPrepareDiff|TestToggleDiffFileStage|TestPrepareCommit'` passed.
- `go test ./internal/tui -run 'TestViewWithDiffScreenUsesFullBody|TestRenderDiffFileListSeparatesStagedAndUnstagedSections|TestDiffModeMovesSelectionAndScrollsContent|TestDiffModeAltUpReturnsToMainList|TestDiffModeDashStartsStageToggle|TestCommitPreviewDOpensDiffView|TestDispatchDiffCommandOpensDiffView'` passed.
- `make test` passed.
- `make scan` passed at `2026-03-12T19:00:48+09:00` (`activity projects: 81`, `tracked projects: 135`, `updated projects: 2`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-12T19:00:55+09:00` (`projects: 135`).
- `env COLUMNS=100 LINES=28 make tui` launched and exited cleanly via `q`.

Next concrete tasks:

- Consider whether the diff screen should also accept `s` as an alias for stage/unstage, or keep the more Fugitive-like `-` as the single shortcut.
- Decide whether the diff pane should gain optional per-line colorized text wrapping or richer binary-file handling beyond the current ANSI image preview path.

## Recent Updates

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
