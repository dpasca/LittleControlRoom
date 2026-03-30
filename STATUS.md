# Little Control Room Status

Last updated: 2026-03-30 20:19 JST

## Latest Update (2026-03-30 20:19 JST)

- Removed the remaining eager backend detection that was still running during normal TUI startup:
  - assumption update:
    - the earlier service-side fix removed unnecessary backend probing during embedded model saves and AI client configuration
    - startup still felt slow because `Model.Init()` always queued `refreshSetupSnapshotCmd(true)`, which called `aibackend.Detect(...)` across all backends even when AI was already configured
    - that launch-time setup snapshot is only needed to auto-open `/setup` when no backend is configured; it is not needed for normal startup with an existing backend
  - `internal/tui/app.go`
    - `Init()` now only queues startup setup detection when a backend is actually unset
  - `internal/tui/setup.go`
    - added `startupSetupSnapshotCmd()` so the startup-only setup refresh rule is explicit and testable
  - `internal/tui/app_test.go`
    - added focused coverage to prove:
      - configured backends skip the startup setup snapshot
      - unconfigured startup still queues the setup snapshot path
- Verification status:
  - focused startup coverage passed:
    - `go test ./internal/tui -run 'Test(StartupUnconfiguredAIBackendOpensSetupMode|StartupSetupSnapshotCmdSkippedWhenBackendConfigured|StartupSetupSnapshotCmdRunsWhenBackendUnset)' -count=1`
  - repo validation:
    - `make scan` passed at `2026-03-30T20:19:05+09:00`
    - `make doctor` passed using cached report at `2026-03-30T20:19:05+09:00`
    - `make test` still fails only on the same pre-existing unrelated `internal/tui` cases:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
    - `git diff --check` passed
- Next concrete tasks:
  - Restart LCR and compare launch responsiveness before/after this TUI startup change in a real terminal.
  - If startup still feels sticky after restart, profile `ScanOnce(ctx)` and any early project/detail loads next, since backend detection should no longer be the unconditional startup blocker for configured backends.

## Latest Update (2026-03-30 19:42 JST)

- Simplified the TODO launch dialog one more step so it now behaves like a pure hotkey launcher:
  - `internal/tui/todo_dialog.go`
    - expanded the chooser to three side-by-side columns:
      - `Run in`
      - `Agent`
      - `Options`
    - moved `change model` into the new `Options` column, still toggled with `m`
    - removed copy-dialog arrow navigation so the visible launcher keys are now the whole interaction model:
      - `w` toggles run mode
      - `a` cycles agent
      - `m` toggles model-first
      - `x` opens existing worktrees
      - `Enter` launches with the currently visible choices
    - removed the extra per-column hint lines so the only key legend is the color-coded action row at the bottom
    - removed the `Options [m]` header badge because `m` toggles the checkbox itself rather than cycling the whole column
    - kept the model-first flag flowing through start-here, new-worktree, and existing-worktree launches
  - `internal/tui/app_test.go`
    - updated launcher render tests for the new `Options` column
    - updated copy-dialog interaction/render tests so they no longer depend on arrow-key navigation or the older longer option label
    - kept focused coverage for the `m` toggle opening the embedded model picker before the TODO draft is sent
- Verification status:
  - focused TODO launcher coverage passed:
    - `go test ./internal/tui -run 'Test(TodoDialogEnterStartsFreshPreferredProviderWithDraft|TodoDialogModelToggleOpensPickerBeforeDraft|TodoDialogCopyDialogIncludesClaudeAndDefaultsToClaudeProvider|TodoDialogCanStartSelectedTodoInNewWorktree|TodoDialogCanStartSelectedTodoInExistingWorktree|TodoDialogCopyDialogHotkeysChangeRunModeAndProvider|TodoDialogShowsWorktreeSuggestionState|TodoCopyDialogShowsRetryGuidanceForFailedWorktreeSuggestion)' -count=1`
    - `git diff --check` passed
  - latest full-repo validation remains:
    - `make scan` passed at `2026-03-30T19:32:25+09:00`
    - `make doctor` passed using cached report at `2026-03-30T19:32:25+09:00`
    - `make test` still fails only on the same pre-existing unrelated `internal/tui` cases:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Do a real-terminal smoke pass to see whether the three-column `Run in / Agent / Options` launcher reads clearly at common terminal widths.
  - Decide after live use whether `a` should remain a cycling control or eventually become a direct per-agent shortcut.
  - If the launcher now feels stable, refresh the docs/screenshots to show the three-column hotkey-based flow.

## Latest Update (2026-03-30 19:24 JST)

- Tightened the TODO launch dialog so worktree and agent changes are explicit instead of relying on section focus:
  - `internal/tui/todo_dialog.go`
    - removed the old `Tab`-driven section-focus model from the TODO start dialog
    - added dedicated selection keys:
      - `w` toggles `Here` vs `Dedicated worktree`
      - `a` cycles agents forward and `A` cycles backward
      - `←/→` now change worktree target directly
      - `↑/↓` now change agent directly
    - re-laid the chooser into two side-by-side columns (`Run in` and `Agent`) so the dialog uses less vertical space and the two decisions read as separate controls
    - updated on-dialog copy so the hotkeys are visible in the section headers and action legend
  - `internal/tui/app_test.go`
    - updated TODO launcher tests for the new arrow-key behavior and compact copy
    - added focused coverage for the new `w`/`a` hotkeys
- Verification status:
  - focused TODO launcher coverage passed:
    - `go test ./internal/tui -run 'Test(TodoDialogCopyDialogIncludesClaudeAndDefaultsToClaudeProvider|TodoDialogCanStartSelectedTodoInNewWorktree|TodoDialogCanStartSelectedTodoInExistingWorktree|TodoDialogCopyDialogHotkeysChangeRunModeAndProvider|TodoDialogShowsWorktreeSuggestionState|TodoCopyDialogShowsRetryGuidanceForFailedWorktreeSuggestion)' -count=1`
  - `git diff --check` passed
  - `make scan` passed at `2026-03-30T19:24:10+09:00`
  - `make doctor` passed using cached report at `2026-03-30T19:24:10+09:00`
  - `make test` still fails only on the same pre-existing unrelated `internal/tui` cases:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Do a real-terminal smoke pass on the two-column TODO launcher to confirm the `w`/`a` scheme feels obvious at live terminal widths.
  - Decide whether `a/A` is enough for provider cycling or whether direct per-provider shortcuts should be added after manual use.
  - If the launcher layout now feels settled, refresh the docs/screenshots so they show the two-column chooser and hotkey hints.

## Latest Update (2026-03-30 19:12 JST)

- Simplified the TODO launch dialog so it no longer renders the full worktree-provider cross-product:
  - `internal/tui/todo_dialog.go`
    - replaced the old matrix of `Start here/new/existing with <provider>` rows with two smaller sections:
      - `Run in`: `Here` vs `Dedicated worktree`
      - `Agent`: `Codex`, `OpenCode`, `Claude Code`
    - the dialog now defaults focus to the run-mode section while still remembering the repo’s preferred embedded provider
    - generic existing-worktree reuse moved behind the secondary `x` action instead of living in the main choice list
    - existing-worktree picker now returns to the start dialog on `Esc` instead of dropping the user back to the TODO list
    - worktree-only actions (`e` edit names / `r` refresh suggestion) now only appear when `Dedicated worktree` is selected
  - `internal/tui/app_test.go`
    - updated TODO launch tests to cover the new split selection model and the new `x` path for existing worktrees
- Verification status:
  - focused TODO dialog and grouped-list checks passed:
    - `go test ./internal/tui -run 'Test(TodoDialogCopyDialogIncludesClaudeAndDefaultsToClaudeProvider|TodoDialogCanStartSelectedTodoInNewWorktree|TodoDialogCanStartSelectedTodoInExistingWorktree|TodoCopyDialogShowsRetryGuidanceForFailedWorktreeSuggestion|TodoDialogShowsWorktreeSuggestionState)' -count=1`
    - `go test ./internal/tui -run 'Test(RenderFooterShowsWorktreeHintsForRepoFamily|RenderFooterShowsRemoveHintForLinkedWorktree|RenderProjectListCollapsesLinkedWorktreesUnderRepoRow|RenderProjectListShowsExpandedWorktreeChildren|ViewStacksListAndDetailVertically)' -count=1`
  - `git diff --check` passed
  - `make scan` passed at `2026-03-30T19:11:15+09:00`
  - `make doctor` passed using cached report at `2026-03-30T19:11:42+09:00`
  - `make test` still fails only on the same pre-existing unrelated `internal/tui` cases:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
  - `timeout 10s make tui-parallel` still could not complete interactive verification in this environment because opening `/dev/tty` failed (`device not configured`)
- Next concrete tasks:
  - Do a real-terminal smoke pass on the simplified TODO launcher to see whether the `Run in`/`Agent` split feels obvious without extra instructions.
  - Decide whether the advanced `x` existing-worktree path should stay generic or eventually become a more explicit `Resume worktree` affordance tied to a TODO.
  - If the simplified launch dialog feels right in manual use, refresh the docs/screenshots so they show the new two-section chooser instead of the older combined list.

## Latest Update (2026-03-30 11:31 JST)

- Polished the new repo-first worktree UX so the feature is more discoverable and self-explanatory in the TUI:
  - `internal/tui/app.go`
    - footer now advertises the relevant worktree actions when a repo family or linked worktree is selected:
      - `w` for worktree lanes
      - `x` for linked-worktree removal when removal is allowed
      - `P` for prune on repo families
    - child worktree rows now render with a clearer `↳` lane prefix in the project list
  - `internal/tui/worktree_ui.go`
    - added `worktreeFooterActions` so footer hints only show worktree actions when they are actually relevant to the current selection
  - `internal/tui/todo_dialog.go`
    - improved TODO worktree launch details with more actionable copy:
      - ready state now shows `Source` and `Path`
      - edited names now show they are one-off launch overrides
      - missing/queued/failed suggestion states now explain whether to press `r` or `e`
    - existing-worktree flows now show clearer empty-state guidance and include the candidate folder basename in the picker
- Added focused coverage for the polish pass:
  - `internal/tui/app_test.go`
    - added footer coverage for repo-family and linked-worktree worktree hints
    - added dialog-copy coverage for failed suggestion recovery guidance
    - updated grouped-row expectations for the new child-lane prefix
- Verification status:
  - focused polish checks passed:
    - `go test ./internal/tui -run 'Test(RenderFooterShowsWorktreeHintsForRepoFamily|RenderFooterShowsRemoveHintForLinkedWorktree|TodoDialogCopyDialogIncludesClaudeAndDefaultsToClaudeProvider|TodoCopyDialogShowsRetryGuidanceForFailedWorktreeSuggestion|RenderProjectListCollapsesLinkedWorktreesUnderRepoRow|RenderProjectListShowsExpandedWorktreeChildren|TodoDialogCanStartSelectedTodoInExistingWorktree|TodoDialogCanStartSelectedTodoInNewWorktree|TodoDialogShowsWorktreeSuggestionState|ViewStacksListAndDetailVertically)' -count=1`
    - `go test ./internal/service -run 'Test(CreateTodoWorktreeCreatesTrackedSiblingProject|RemoveWorktreeRemovesTrackedLinkedWorktree)' -count=1`
  - `make scan` passed at `2026-03-30T11:31:18+09:00`
  - `make doctor` passed using cached report at `2026-03-30T11:31:18+09:00`
  - `make test` still fails only on the same pre-existing unrelated `internal/tui` cases:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
  - `timeout 10s make tui-parallel` still could not complete interactive verification in this environment because opening `/dev/tty` failed (`device not configured`)
- Next concrete tasks:
  - Do a real-terminal smoke pass to verify the footer hints feel balanced and the existing-worktree picker reads well at live terminal widths.
  - If the new footer hint density feels too high on medium widths, tune the `60+` and `80+` breakpoints after that manual pass.
  - If the worktree model is now stable enough, refresh the docs/screenshots so they reflect grouped lanes and the new TODO launch choices.

## Latest Update (2026-03-30 11:16 JST)

- Completed the main repo-first worktree UX slice so linked worktrees now behave like lanes under one repo instead of noisy duplicate top-level projects:
  - `internal/scanner/discovery.go`
    - discovery now recognizes both `.git` directories and linked-worktree `.git` files
  - `internal/scanner/git.go`
    - added git worktree inspection helpers for current-worktree identity and `git worktree list --porcelain`
  - `internal/model/model.go`
    - added `WorktreeRootPath` and `WorktreeKind` to project state/summary so repo vs linked-worktree identity is explicit
  - `internal/store/store.go`
    - persists worktree root/kind on projects
    - added `ForceQueueTodoWorktreeSuggestion` for explicit user-triggered suggestion refresh
  - `internal/service/service.go`
    - scan/refresh flows now capture worktree metadata, expand discovered repo families via `git worktree list`, and forget removed linked worktrees when Git no longer reports them
  - `internal/service/project_create.go`
  - `internal/service/git_actions.go`
    - manual attach and state rebuild paths now preserve worktree identity
  - `internal/service/worktree.go`
    - expanded TODO worktree creation to support branch/folder overrides
    - added `RegenerateTodoWorktreeSuggestion`
    - added linked-worktree lifecycle operations: `RemoveWorktree` and `PruneWorktrees`
    - added path-variant reconciliation so `/var/...` vs `/private/var/...` worktree families stay grouped on macOS
  - `internal/tui/app.go`
  - `internal/tui/worktree_ui.go`
    - project list now groups linked worktrees under their repo row with expand/collapse behavior
    - detail pane now shows repo-family worktree summaries and lane details
    - added worktree actions: expand/collapse (`w`), remove selected linked worktree (`x`), and prune stale git worktrees (`P`)
  - `internal/tui/todo_dialog.go`
    - TODO launch flow now supports:
      - `Start here`
      - `Start in new worktree`
      - `Start in existing worktree`
    - new-worktree flow now supports cached suggestion display, manual edit (`e`), and regeneration (`r`)
    - existing-worktree flow now opens a picker and launches the embedded provider directly into the selected sibling worktree
- Added focused coverage for the new UX and service behavior:
  - `internal/tui/app_test.go`
    - added grouped-list coverage for collapsed/expanded repo worktree rows
    - added existing-worktree TODO launch coverage
  - `internal/service/service_test.go`
    - added linked-worktree removal coverage
- Kept the earlier new-worktree launch coverage in place while correcting the test expectation to preserve the tracked path variant rather than forcing `EvalSymlinks` output on macOS temp paths
- Verification status:
  - focused worktree checks passed:
    - `go test ./internal/service -run 'Test(CreateTodoWorktreeCreatesTrackedSiblingProject|RemoveWorktreeRemovesTrackedLinkedWorktree)' -count=1`
    - `go test ./internal/tui -run 'Test(RenderProjectListCollapsesLinkedWorktreesUnderRepoRow|RenderProjectListShowsExpandedWorktreeChildren|TodoDialogCanStartSelectedTodoInExistingWorktree|TodoDialogCanStartSelectedTodoInNewWorktree|TodoDialogCopyDialogIncludesClaudeAndDefaultsToClaudeProvider|TodoDialogShowsWorktreeSuggestionState|ViewStacksListAndDetailVertically)' -count=1`
  - `make scan` passed at `2026-03-30T11:15:27+09:00`
  - `make doctor` passed using cached report at `2026-03-30T11:15:27+09:00`
  - `make test` still fails only on the same pre-existing unrelated `internal/tui` cases:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
  - `timeout 10s make tui-parallel` still could not complete interactive verification in this environment because opening `/dev/tty` failed (`device not configured`)
- Next concrete tasks:
  - Decide whether the new worktree keys (`w`, `x`, `P`) should also be surfaced more explicitly in the footer/help affordances.
  - Do an interactive TUI smoke pass in a real terminal once `/dev/tty` access is available, especially around grouped-row selection, existing-worktree launch, and remove/prune confirmations.
  - If repo/worktree grouping remains stable after manual usage, update public/reference docs to show the new grouped-lane model instead of the earlier “duplicate rows for now” limitation.

## Latest Update (2026-03-30 10:33 JST)

- Added the first end-to-end TODO worktree launch flow so a cached AI suggestion can now turn into a real `git worktree` plus an embedded provider session:
  - `internal/service/worktree.go`
    - added `CreateTodoWorktree`, which validates the cached TODO worktree suggestion, creates a sibling worktree via `git worktree add`, and immediately tracks the new path in Little Control Room
    - current managed-path convention is `<repo-parent>/<repo-base>--<worktree-suffix>`
  - `internal/tui/todo_dialog.go`
    - expanded the TODO start dialog to offer both `Start here with ...` and `Start in new worktree with ...` for Codex, OpenCode, and Claude Code
    - when a new-worktree option is highlighted, the dialog now shows the cached branch/folder suggestion or a clear preparation/unavailable state
  - `internal/tui/app.go`
    - handles the new worktree-launch message by restoring the TODO draft under the created worktree path and opening a fresh embedded provider session there
  - `internal/service/service_test.go`
    - added coverage for creating a tracked sibling worktree from a ready TODO suggestion, including branch verification inside the new checkout
  - `internal/tui/app_test.go`
    - added coverage for the new start-dialog options and the end-to-end `Start in new worktree` flow from the TODO overlay
- Current limitation in this slice:
  - newly created worktrees are tracked as separate project rows for now
  - repo/worktree grouping and child-row presentation are still future work
- Verification status:
  - `go test ./internal/service -run 'TestCreateTodoWorktreeCreatesTrackedSiblingProject' -count=1` passed
  - `go test ./internal/tui -run 'Test(TodoDialogCopyDialogIncludesClaudeAndDefaultsToClaudeProvider|TodoDialogCanStartSelectedTodoInNewWorktree|TodoDialogShowsWorktreeSuggestionState)' -count=1` passed
  - `make test` still fails only on the same pre-existing unrelated `internal/tui` cases:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
  - `make scan` passed at `2026-03-30T10:32:12+09:00`
  - `make doctor` passed using cached report at `2026-03-30T10:32:12+09:00`
  - `timeout 10s make tui-parallel` still could not complete interactive verification in this environment because opening `/dev/tty` failed (`device not configured`)
- Next concrete tasks:
  - Add manual edit/regenerate controls to the TODO worktree launch flow before creating the worktree.
  - Add `Start in existing worktree` once there is a good picker for sibling worktrees.
  - Group worktrees under a repo-level row in the main UI so parallel lanes do not appear as noisy duplicate projects.

## Latest Update (2026-03-30 09:06 JST)

- Reframed the public README to position Little Control Room more clearly as a personal open source tool rather than a commercial product:
  - `README.md`
    - intro now uses first-person framing (`I`) and explains that LCR grew out of a personal workflow, while still noting internal use
    - replaced the generic top-level product copy with a `Why I Built It` section and a more practical `What It Helps With` section
    - renamed `Everyday Workflow` to `Core Workflows`
- Verification status for this README/messaging pass:
  - `git diff --check` passed
  - no Go tests were needed in this pass because the only change in this repo was README copy
- Next concrete tasks:
  - Keep the README intro and the public site copy aligned when the external framing of LCR changes again.
  - If the main user-facing workflow shifts further toward TODO-driven work, consider making that even more explicit near the top of the README.

## Latest Update (2026-03-30 02:37 JST)

- Added a dedicated curated `/setup` screenshot so the docs/gallery can double as a quick-start guide:
  - `internal/tui/screenshots.go`
    - added a deterministic `setup` screenshot asset with a guided backend chooser state
    - the setup fixture shows Codex active, Claude Code highlighted, OpenCode ready, the OpenAI API key option, and the Haiku usage hint
  - `internal/tui/screenshots_test.go`
    - added coverage to make sure the setup screenshot render includes the backend choices, the config path, and the Haiku hint
  - `internal/tui/setup.go`
  - `internal/tui/settings.go`
    - compact config-path display now uses `~` for home-relative paths, which keeps both the setup/settings UI and the screenshot output cleaner
  - `docs/screenshots/setup.png`
    - generated and added the new setup guide screenshot
  - `README.md`
    - added the setup screenshot to the quick-start/setup section
  - `docs/reference.md`
    - updated the curated screenshot list to include both `todo-dialog.png` and `setup.png`
- Verification status:
  - `go test ./internal/tui -run 'TestScreenshot|TestRenderSetupHintExplainsClaudeHaikuDefault|TestOpenSetupModeCanPreferReadyClaudeBackend|TestRenderSetupOptionRowDistinguishesActiveAndReadyBackends' -count=1` passed
  - `make screenshots SCREENSHOT_OUTPUT_DIR=/tmp/lcr-setup-shots` passed and generated `setup.png`
  - `make scan` passed at `2026-03-30T02:36:44+09:00`
  - `make doctor` passed using cached report at `2026-03-30T02:36:44+09:00`
  - `make test` still fails only on the same pre-existing unrelated `internal/tui` cases:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
  - `timeout 10s make tui-parallel` still could not complete interactive verification in this environment because opening `/dev/tty` failed (`device not configured`)
- Next concrete tasks:
  - If the `/setup` screen changes materially again, regenerate `docs/screenshots/setup.png` so the guide stays current.
  - Consider whether the setup screenshot should eventually become the Open Graph image for the public LCR page, or stay a secondary guide view behind the main dashboard shot.

## Latest Update (2026-03-30 02:26 JST)

- Synced the Claude Code rollout follow-through so the remaining uncommitted repo state is presentation/docs-only:
  - `README.md`
    - refreshed the public overview, setup, TODO workflow, and cost notes so Claude Code and the Haiku default are documented consistently
  - `docs/reference.md`
    - removed the stale `openai_api_key`-only classify note and updated the embedded-pane wording to include Claude Code
  - `STATUS.md`
    - recorded the completed Claude backend rollout and current verification state
- Checked the current curated screenshot set before deciding whether to regenerate images:
  - `docs/screenshots/`
    - already includes `todo-dialog.png`, so there is no blocker for the public site/gallery to show the TODO-driven workflow right now
  - no new screenshot render was needed in this pass
- Verification status for this docs/presentation sync:
  - no new repo tests were run in this pass because the only local changes are docs/status updates
  - the previously recorded Claude backend verification still stands:
    - focused Go tests passed for the Claude backend wiring and setup copy
    - `make scan` passed at `2026-03-30T02:05:32+09:00`
    - `make doctor` passed at `2026-03-30T02:05:46+09:00`
    - `make test` still only fails on the same pre-existing unrelated `internal/tui` cases already listed below
- Next concrete tasks:
  - Capture a dedicated `/setup` screenshot later if the Claude-first setup flow needs a public-facing image of its own.
  - If the TODO worktree launch flow grows into a more visible feature, consider replacing one of the current gallery shots with that end-to-end path once the UI settles.

## Latest Update (2026-03-30 02:15 JST)

- Added Claude Code as a first-class AI backend for `/setup`, background summaries/classification, commit help, and TODO worktree suggestions:
  - `internal/config/ai_backend.go`
    - added `claude_code` as a valid `ai_backend` value and labeled it `Claude Code`
  - `internal/aibackend/detect.go`
    - added Claude Code readiness detection via `claude auth status --json`
    - surfaces Claude as a `/setup` option alongside Codex and OpenCode
  - `internal/service/service.go`
    - wires the Claude backend into the existing classifier, commit helper, and TODO worktree suggester paths
  - `internal/sessionclassify/client.go`
  - `internal/gitops/message.go`
  - `internal/todoworktree/client.go`
    - added Claude-backed constructors that use a local Claude CLI runner
  - `internal/llm/local_cli.go`
    - added `ClaudePrintRunner`
    - runs `claude -p` with JSON output, JSON Schema validation, tools disabled, and an isolated internal workspace
    - parses Claude `structured_output`, actual model IDs, and usage/cost metadata from the result envelope
    - defaults Claude background inference to the cheaper `haiku` alias
    - skips `--effort` when the requested Claude model is Haiku, since current Claude docs only advertise effort support on Sonnet/Opus families
  - `internal/tui/setup.go`
    - added Claude Code to `/setup`
    - added a hint that Claude-backed background tasks default to Haiku to reduce usage impact
  - `internal/tui/settings.go`
  - `internal/tui/app.go`
    - updated surrounding UI copy and local-backend usage labels to include Claude Code
  - `README.md`
  - `docs/reference.md`
    - synced the user-facing docs so `/setup`, `lcroom classify`, TODO provider choices, and the cost/setup notes all mention Claude Code and the Haiku default
- Validated current assumptions before coding:
  - local `claude --help` confirms headless `-p`, `--model`, `--json-schema`, and output-format controls are available in the installed CLI
  - local `claude auth status --json` works on this machine and is suitable for `/setup` readiness checks
  - current Claude Code docs confirm `haiku` is the fast/efficient alias and that it is appropriate for lighter-weight/background usage
- Added focused coverage:
  - `internal/aibackend/detect_test.go`
    - covers Claude auth-status parsing and readiness detail formatting
  - `internal/config/ai_backend_test.go`
    - covers `claude_code` backend parsing and labeling
  - `internal/llm/local_cli_test.go`
    - covers Claude runner parsing, caching, and model-effort support rules
  - `internal/tui/app_test.go`
    - covers preferring a ready Claude backend in `/setup`
    - covers the Claude/Haiku hint text in `/setup`
    - keeps the OpenAI API key hint behavior covered after the copy update
- Files modified in this pass:
  - `README.md`
  - `docs/reference.md`
  - `internal/aibackend/detect.go`
  - `internal/aibackend/detect_test.go`
  - `internal/config/ai_backend.go`
  - `internal/config/ai_backend_test.go`
  - `internal/gitops/message.go`
  - `internal/llm/local_cli.go`
  - `internal/llm/local_cli_test.go`
  - `internal/service/service.go`
  - `internal/sessionclassify/client.go`
  - `internal/todoworktree/client.go`
  - `internal/tui/app.go`
  - `internal/tui/app_test.go`
  - `internal/tui/settings.go`
  - `internal/tui/setup.go`
  - `STATUS.md`
- Verification status:
  - `go test ./internal/aibackend ./internal/config ./internal/llm -count=1` passed
  - `go test ./internal/sessionclassify ./internal/gitops ./internal/todoworktree ./internal/service ./internal/tui -run 'Test(OpenSetupModeCanPreferReadyClaudeBackend|RenderSetupHintExplainsClaudeHaikuDefault|SettingsAPIKeyHintShowsMaskedSuffix|OpenSetupModePrefersReadyBackendOverUnavailableCurrentBackend|RenderSetupOptionRowDistinguishesActiveAndReadyBackends)' -count=1` passed
  - live Claude sanity check passed:
    - `printf 'Return {"ok":true}.' | claude -p --no-session-persistence --output-format json --json-schema ... --model haiku --tools=`
    - Claude returned `structured_output={"ok":true}` plus model/usage metadata, confirming the local runner contract
  - `make scan` passed at `2026-03-30T02:05:32+09:00`
  - `make doctor` passed using cached report at `2026-03-30T02:05:46+09:00`
  - `make test` still fails in the same pre-existing `internal/tui` coverage unrelated to this Claude-backend change:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
  - `timeout 10s make tui` could not run an interactive verification pass because another live `lcroom tui` runtime already owned the default DB
  - `timeout 10s make tui-parallel` still could not complete interactive verification in this environment because opening `/dev/tty` failed (`device not configured`)
- Next concrete tasks:
  - Decide whether Claude background inference should stay hard-wired to `haiku` or gain an explicit `/setup` or `/settings` override once the default proves out.
  - If Claude exposes richer headless usage metadata in a stable format, thread that through `ClaudePrintRunner` so the footer can report Claude token/cost estimates the same way other runners do.

## Latest Update (2026-03-30 01:40 JST)

- Added current branch visibility to the project details pane even when no explicit worktree UI is active:
  - `internal/model/model.go`
    - added `RepoBranch` to `ProjectState` and `ProjectSummary`
  - `internal/store/store.go`
    - added persisted `projects.repo_branch` schema support and migration
    - threaded `repo_branch` through summary/detail queries, upserts, and project moves
  - `internal/service/service.go`
    - captured branch from live git status during scans and refreshes
    - reused stored branch when live git status is unavailable
    - treated branch changes as project-state updates
  - `internal/service/project_create.go`
    - populated branch metadata for newly attached/manual projects
  - `internal/service/git_actions.go`
    - preserved branch when rebuilding project state from detail snapshots
  - `internal/tui/app.go`
    - folds branch metadata into the existing `Repo:` detail line as `(<branch>)`
- Added focused coverage:
  - `internal/tui/app_test.go`
    - `TestViewStacksListAndDetailVertically` now checks for `Repo: dirty, ahead 2 (master)` and confirms no separate `Branch:` line is rendered
  - `internal/store/store_test.go`
    - `TestMoveProjectPathPreservesData` now verifies `RepoBranch` survives project moves
  - `internal/service/service_test.go`
    - scan/storage expectations now verify the repo branch is persisted end-to-end
- Files modified in this pass:
  - `internal/model/model.go`
  - `internal/store/store.go`
  - `internal/store/store_test.go`
  - `internal/service/service.go`
  - `internal/service/project_create.go`
  - `internal/service/git_actions.go`
  - `internal/service/service_test.go`
  - `internal/tui/app.go`
  - `internal/tui/app_test.go`
  - `STATUS.md`
- Verification status:
  - `go test ./internal/store -count=1` passed
  - `go test ./internal/service -count=1` passed
  - `go test ./internal/tui -run 'TestViewStacksListAndDetailVertically' -count=1` passed
  - `make scan` passed at `2026-03-30T01:39:40+09:00`
  - `make doctor` passed using cached report at `2026-03-30T01:39:40+09:00`
  - `timeout 10s make tui-parallel` could not complete interactive verification in this environment because opening `/dev/tty` failed (`device not configured`)
  - `make test` still fails in the same pre-existing `internal/tui` coverage unrelated to this branch-visibility change:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Decide whether branch should also appear in the main project list summary, or stay detail-pane only until worktree rows land.
  - Continue the worktree launch flow by adding manual edit/regenerate for cached TODO worktree suggestions and the actual `Start in new worktree` path.

## Latest Update (2026-03-29 21:27 JST)

- Investigated the current hard-freeze report that initially looked like a new Claude Code integration loop:
  - the `FractalMech` Claude transcript currently has no active `~/.claude/sessions` PID marker
  - the live stuck `lcroom tui` process had no Claude child process attached
  - sampling the stuck TUI pointed at TUI-side syntax-highlighting/render work rather than Claude session refresh code
- Hardened transcript/diff syntax highlighting so risky blocks fall back to plain text instead of driving Chroma into expensive lexer work:
  - `internal/tui/syntax_highlight.go`
    - skip syntax highlighting for oversized blocks larger than `12 KiB` or `240` lines
    - skip content-only lexer auto-detection when no language hint or filename is available, returning plain text instead
- Added focused regression coverage:
  - `internal/tui/syntax_highlight_test.go`
    - `TestSyntaxHighlightPreparedLexerSkipsContentOnlyInference`
    - `TestSyntaxHighlightPreparedLexerSkipsLargeTypedBlock`
    - `TestSyntaxHighlightBlockFallsBackToPlainTextForLargeTypedBlock`
- Files modified in this pass:
  - `internal/tui/syntax_highlight.go`
  - `internal/tui/syntax_highlight_test.go`
  - `STATUS.md`
- Verification status:
  - `go test ./internal/tui -run 'TestSyntaxHighlight' -count=1` passed
  - `make scan` passed at `2026-03-29T21:26:38+09:00`
  - `make doctor` passed using cached report at `2026-03-29T21:26:39+09:00`
  - `timeout 10s make tui-parallel` still could not complete interactive verification in this environment because opening `/dev/tty` failed (`device not configured`)
  - `make test` still fails in the same pre-existing `internal/tui` coverage unrelated to this syntax-highlighting pass:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Reproduce the original stuck TUI flow interactively after this guardrail lands to confirm the freeze is gone.
  - If a hard freeze still occurs, capture a fresh live sample while the problematic pane is visible so the next pass can distinguish transcript rendering from diff-mode rendering.

## Latest Update (2026-03-29 20:44 JST)

- Implemented the first TODO worktree suggestion slice end-to-end:
  - added persisted TODO worktree suggestion state and queueing in the store
  - added a dedicated model-backed TODO worktree suggestion package and manager
  - wired the service to queue suggestions on TODO create and edit
  - started the TODO worktree suggester alongside the existing background runtime paths
  - fixed the startup backfill path so open TODO suggestions are queued without nesting SQLite queries on the single-connection store
  - surfaced cached suggestion state in the TODO dialog so rows can show either the ready branch name or a preparing/unavailable status
- Files modified in this pass:
  - `internal/model/model.go`
  - `internal/store/store.go`
  - `internal/store/store_test.go`
  - `internal/service/service.go`
  - `internal/cli/run.go`
  - `internal/tui/todo_dialog.go`
  - `internal/tui/app_test.go`
  - `internal/todoworktree/client.go`
  - `internal/todoworktree/manager.go`
  - `internal/todoworktree/manager_test.go`
  - `STATUS.md`
- Verification status:
  - `go test ./internal/store ./internal/todoworktree ./internal/service ./internal/tui -run 'Test(TodoWorktreeSuggestionLifecycleAppearsInTodoList|ClaimNextQueuedTodoWorktreeSuggestionRespectsDebounce|ManagerGeneratesReadySuggestion|TodoDialogShowsWorktreeSuggestionState|TodoDialogSelectedRowHasNoExtraLeadingSpace)' -count=1` passed
  - `go test ./internal/store -count=1` passed
  - `go test ./internal/todoworktree -count=1` passed
  - `make scan` passed at `2026-03-29T20:43:59+09:00`
  - `make doctor` passed using cached report at `2026-03-29T20:44:00+09:00`
  - `timeout 10s make tui-parallel` could not complete interactive verification in this environment because opening `/dev/tty` failed (`device not configured`)
  - `make test` still fails in the same pre-existing `internal/tui` coverage unrelated to this TODO worktree suggestion slice:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Add an explicit regenerate/manual-edit path for TODO worktree suggestions inside the start/launch flow.
  - Implement the actual `Start in new worktree` launch mode so the cached branch/worktree suggestion can create a worktree and open the selected embedded provider inside it.

## Latest Update (2026-03-29 19:21 JST)

- Added a dedicated engineering spec for TODO-driven AI worktree suggestions:
  - `docs/todo_worktree_suggestions_mvp.md`
    - defines the repo-vs-worktree product model for the MVP
    - specifies structured model output for branch/worktree naming suggestions
    - describes cache/debounce/invalidation rules for background suggestion generation
    - covers suggested store schema, service responsibilities, TUI states, failure handling, and rollout order
- Files modified in this pass:
  - `docs/todo_worktree_suggestions_mvp.md`
  - `STATUS.md`
- Verification status:
  - `make scan` passed at `2026-03-29T19:20:39+09:00`
  - `make doctor` passed using cached report at `2026-03-29T19:20:39+09:00`
  - `make test` still fails in the same pre-existing `internal/tui` coverage unrelated to this docs/spec pass:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Review the MVP spec and confirm the first implementation slice order, especially whether store/queue plumbing should land before any TUI affordance changes.
  - Start the first implementation PR by adding the TODO worktree suggestion model/store schema and stale/ready/failed persistence path.

## Latest Update (2026-03-29 18:33 JST)

- Added Claude Code as a first-class TODO dialog launch target:
  - `internal/tui/todo_dialog.go`
    - inserted a third `Start TODO` option for Claude Code alongside Codex and OpenCode
    - mapped the new option to `codexapp.ProviderClaudeCode`
    - updated the default selection logic so projects whose latest session format is `claude_code` preselect Claude Code when starting from a TODO
- Added focused TUI regression coverage:
  - `internal/tui/app_test.go`
    - `TestTodoDialogCopyDialogIncludesClaudeAndDefaultsToClaudeProvider`
    - verifies the dialog renders `Start with Claude Code`, defaults to it for Claude-backed projects, and launches a fresh embedded Claude session with the TODO draft
- Files modified in this pass:
  - `internal/tui/todo_dialog.go`
  - `internal/tui/app_test.go`
  - `STATUS.md`
- Verification status:
  - `go test ./internal/tui -run 'Test(TodoDialogEnterStartsFreshPreferredProviderWithDraft|TodoDialogCopyDialogIncludesClaudeAndDefaultsToClaudeProvider|LaunchClaudeForSelectionUsesClaudeProvider)' -count=1` passed
  - `make scan` passed at `2026-03-29T18:33:13+09:00`
  - `make doctor` passed using cached report at `2026-03-29T18:33:13+09:00`
  - `timeout 10s make tui-parallel` launched the isolated TUI sandbox and rendered the dashboard before the timeout stopped it
  - `make test` still fails in the same pre-existing `internal/tui` coverage unrelated to this TODO-dialog change:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Live-check the TODO dialog manually inside the TUI so we can confirm the new Claude option feels right in the keyboard flow and model-picker path.

## Latest Update (2026-03-29 17:39 JST)

- Fixed the embedded Claude Code launch args so the headless stream-json path matches the currently installed Claude CLI contract:
  - `internal/codexapp/claude_session.go`
    - factored Claude launch flag assembly into `claudeTurnArgs(...)`
    - added `--verbose` alongside `-p --input-format=stream-json --output-format=stream-json`
    - kept existing resume/model/reasoning/permission-mode wiring unchanged
- Added a focused regression in `internal/codexapp/claude_session_test.go`:
  - `TestClaudeTurnArgsIncludeVerboseForStreamJSON`
  - locks in the exact flag set that avoids the reported Claude CLI parse failure
- Updated assumptions in `STATUS.md`:
  - recorded the observed local Claude Code version as `2.1.87`
  - documented that `--output-format=stream-json` now requires `--verbose` when used with `--print`
- Files modified in this pass:
  - `internal/codexapp/claude_session.go`
  - `internal/codexapp/claude_session_test.go`
  - `STATUS.md`
- Verification status:
  - `go test ./internal/codexapp -run 'TestClaude' -count=1` passed
  - `claude --help` confirms `--output-format=stream-json` requires `--verbose` with `--print`
  - `claude -p --verbose --input-format=stream-json --output-format=stream-json --permission-mode acceptEdits --help` exited `0`, confirming the updated flag combination clears the original argument validation error
  - `make scan` passed at `2026-03-29T17:39:25+09:00`
  - `make doctor` passed using cached report at `2026-03-29T17:39:24+09:00`
  - `make test` still fails in the same pre-existing `internal/tui` coverage unrelated to this Claude fix:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Live-check `/claude`, `/claude-new`, `/new`, and `/reconnect` inside `make tui-parallel` against the real local Claude CLI now that the launch flags match the installed CLI behavior.

## Latest Update (2026-03-29 17:18 JST)

- Added a second-stage historical stalled-session view so old disconnected turns stop reading as healthy `in_progress` work in reporting paths:
  - Added `internal/sessionclassify/effective.go` with a shared `DeriveEffectiveAssessment(...)` helper.
  - The helper preserves stored classifier output by default, but upgrades completed `in_progress` assessments to an effective `blocked` view when:
    - the latest structured turn state is known,
    - that turn never completed, and
    - the session has been idle past a derived grace period intended for stale embedded work.
  - The grace period is now shared via `EffectiveAssessmentStallThreshold(...)`:
    - starts from the active threshold,
    - floors at `30m` so we do not flip too aggressively,
    - and caps at the configured stuck threshold if that is lower.
- Wired the shared effective assessment into the paths that matter for user trust:
  - `internal/service/service.go`
    - attention scoring now treats stale incomplete `in_progress` sessions as effective `blocked` sessions
  - `internal/cli/run.go`
    - `doctor` now prints the effective category/summary instead of stale raw `in_progress` wording for wedged turns
  - `internal/tui/app.go`
    - project list/detail assessment labels and summaries now use the same effective blocked/stalled view
- Result on the original repro:
  - after `doctor --scan`, `2025_high_tax` now renders as:
    - reason: `Latest session is blocked; idle for 1h 22m`
    - latest session assessment: `category=blocked`
    - summary: `Last turn never completed; idle 1h 22m, likely stalled or disconnected.`
- Added coverage:
  - `internal/sessionclassify/sessionclassify_test.go`
    - `TestDeriveEffectiveAssessmentMarksStaleInProgressTurnBlocked`
    - `TestDeriveEffectiveAssessmentKeepsFreshInProgressTurn`
  - `internal/service/service_test.go`
    - `TestLatestSessionClassificationTreatsStaleInProgressTurnAsBlocked`
  - `internal/tui/app_test.go`
    - `TestAssessmentStatusLabelAtMarksStaleInProgressBlocked`
    - `TestProjectAssessmentTextAtUsesDerivedStalledSummary`
    - `TestProjectListStatusAtShowsBlockedForStaleInProgressTurn`
- Files modified in this pass:
  - `internal/sessionclassify/effective.go`
  - `internal/sessionclassify/sessionclassify_test.go`
  - `internal/service/service.go`
  - `internal/service/service_test.go`
  - `internal/cli/run.go`
  - `internal/tui/app.go`
  - `internal/tui/app_test.go`
  - `STATUS.md`
- Verification status:
  - `go test ./internal/sessionclassify -run 'Test(DeriveEffectiveAssessment|ExtractSnapshotModernFixture|ManagerProcessOneSanitizesStatusLikeSummaryFromClassifier)' -count=1` passed
  - `go test ./internal/service -run 'Test(LatestSessionClassificationUsesPersistedSessionSnapshotHash|LatestSessionClassificationTreatsStaleInProgressTurnAsBlocked)' -count=1` passed
  - `go test ./internal/tui -run 'Test(AssessmentStatusLabelUsesInProgressName|AssessmentStatusLabelAtMarksStaleInProgressBlocked|ProjectAssessmentTextUsesLatestSummary|ProjectAssessmentTextAtUsesDerivedStalledSummary|ProjectListStatusAtShowsBlockedForStaleInProgressTurn)' -count=1` passed
  - `make scan` passed at `2026-03-29T17:17:18+09:00`
  - `make doctor` passed using cached report at `2026-03-29T17:17:18+09:00`
  - `doctor --scan` confirmed `2025_high_tax` now reports `category=blocked` with the stalled/disconnected summary
  - `make tui-parallel` rendered successfully when launched with a PTY-backed smoke check
  - `make test` still fails in the same pre-existing diff/TUI coverage unrelated to this work:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Decide whether the derived blocked/stalled view should remain a runtime presentation override or be written back into stored classification history as an explicit secondary status.
  - Consider enriching stalled historical detection with structured unfinished-tool-call evidence from the session tail, so summaries can distinguish “waiting on user/tool approval” from “helper disappeared mid-turn.”

## Latest Update (2026-03-29 17:00 JST)

- Tightened embedded Codex stuck-session handling so disconnected helpers stop looking like healthy live turns:
  - Added a distinct embedded session phase `stalled` in `internal/codexapp/types.go`.
  - `internal/codexapp/session.go` now tracks repeated failed busy-state health checks during `thread/read` reconciliation.
  - After two consecutive reconciliation failures for a still-busy embedded session, the session now transitions to `stalled`, keeps the last RPC error, and appends a one-time reconnect guidance notice:
    - `Embedded Codex session seems stuck or disconnected. Use /reconnect.`
- Updated the TUI to present stalled sessions explicitly instead of continuing to show a running timer:
  - `internal/tui/app.go`
    - project agent badges now render `CX stalled` / `OC stalled` / `CC stalled` for live stalled sessions.
  - `internal/tui/codex_picker.go`
    - live picker summaries now say `Live now: embedded helper looks stuck; use /reconnect`.
  - `internal/tui/codex_pane.go`
    - footer status now shows `Stalled; use /reconnect`
    - footer actions now promote `/reconnect recover`
    - Enter/send is blocked while stalled so the pane does not pretend it can steer a disconnected turn
- Added regression coverage:
  - `internal/codexapp/session_test.go`
    - `TestReconcileBusyStateMarksSessionStalledAfterRepeatedHealthCheckFailures`
  - `internal/tui/app_test.go`
    - `TestCodexFooterStatusShowsStalledState`
    - `TestPickerSummaryForStalledLiveSnapshot`
    - `TestProjectAgentDisplayShowsStalledLiveSession`
- Files modified in this pass:
  - `internal/codexapp/types.go`
  - `internal/codexapp/session.go`
  - `internal/codexapp/session_test.go`
  - `internal/tui/app.go`
  - `internal/tui/app_test.go`
  - `internal/tui/codex_pane.go`
  - `internal/tui/codex_picker.go`
  - `STATUS.md`
- Verification status:
  - `go test ./internal/codexapp -run 'Test(ReconcileBusyStateClearsBusyWhenThreadReadShowsNoActiveTurn|ReconcileBusyStateMarksSessionStalledAfterRepeatedHealthCheckFailures)' -count=1` passed
  - `go test ./internal/tui -run 'Test(CodexFooterStatusShowsStalledState|PickerSummaryForStalledLiveSnapshot|ProjectAgentDisplayShowsStalledLiveSession|ProjectAgentDisplayUsesLiveBusyTimer)' -count=1` passed
  - `make scan` passed at `2026-03-29T17:00:12+09:00`
  - `make doctor` passed using cached report at `2026-03-29T17:00:12+09:00`
  - `make tui-parallel` launched successfully in the isolated `/tmp/lcroom-parallel-davide` sandbox and rendered before the smoke-check timeout
  - `make test` still fails in the same pre-existing diff/TUI coverage unrelated to this pass:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Extend historical session/project classification so long-idle interrupted runs can be labeled closer to `stalled/disconnected` instead of only `in_progress` in doctor/reporting views.
  - Consider surfacing the last successful embedded health-check timestamp in the UI so users can tell whether a session is genuinely progressing or just still marked busy.
  - Live-check `/reconnect` recovery flow against a real intentionally wedged embedded session and tune the stall threshold if two failed health checks feels too slow or too eager.

## Latest Update (2026-03-29 09:52 JST)

- Added a practical embedded Claude model picker path so `/model` no longer dead-ends for Claude sessions:
  - `internal/codexapp/claude_session.go` now returns a curated Claude alias list (`sonnet`, `opus`, `haiku`) with reasoning options (`low`, `medium`, `high`, `max`).
  - The picker also prepends the current/pending full Claude model IDs when a session is already using something more specific than an alias.
  - Claude model selections now persist the same way Codex/OpenCode selections do, so the next embedded Claude session reuses the saved model and reasoning choice automatically.
- Added Claude-specific embedded preference persistence in config/TUI state:
  - New saved config fields:
    - `embedded_claude_model`
    - `embedded_claude_reasoning_effort`
    - `recent_claude_models`
  - `internal/tui/codex_model_picker.go` now uses provider-specific recent-model history for Claude instead of falling back to Codex recents.
- Updated docs to reflect the new state of the MVP:
  - `README.md`
  - `docs/reference.md`
  - Claude is still MVP-level for approvals, compact, and attachments, but model selection is now usable and persisted.
- Added/expanded coverage:
  - `internal/codexapp/claude_session_test.go`
  - `internal/config/config_test.go`
  - `internal/tui/app_test.go`
- Files modified in this pass:
  - `internal/codexapp/claude_session.go`
  - `internal/codexapp/claude_session_test.go`
  - `internal/config/config.go`
  - `internal/config/editable.go`
  - `internal/config/config_test.go`
  - `internal/tui/embedded_model_preferences.go`
  - `internal/tui/codex_model_picker.go`
  - `internal/tui/app.go`
  - `internal/tui/settings.go`
  - `internal/tui/app_test.go`
  - `README.md`
  - `docs/reference.md`
  - `STATUS.md`
- Verification status:
  - `go test ./internal/config -run 'TestParseLoadsEmbeddedModelPreferencesFromConfigFile|TestSaveEditableSettingsWritesReadableTOML'` passed
  - `go test ./internal/codexapp -run 'TestClaude'` passed
  - `go test ./internal/tui -run 'TestEmbeddedModelPreferencePersistsAcrossFutureSessionsPerProvider|TestEmbeddedModelPreferenceLoadsFromSavedSettingsOnStartup|TestSettingsSavePreservesEmbeddedModelPreferences|TestLaunchClaudeForSelectionUsesClaudeProvider|TestHelpPanelLinesStayMinimal'` passed
  - `make scan` passed at `2026-03-29T09:52:56+09:00`
  - `make doctor` passed using cached report at `2026-03-29T09:52:56+09:00`
  - `make tui` was attempted but correctly refused because another long-lived TUI runtime is already active for the default DB
  - `make tui-parallel` launched successfully in the isolated `/tmp/lcroom-parallel-davide` sandbox and rendered the dashboard before exit
  - `make test` still fails in the same pre-existing diff/TUI coverage unrelated to Claude:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Run `make tui` and live-check `/claude`, `/claude-new`, `/new`, `/reconnect`, and `/model` against the real local Claude CLI.
  - Add Claude-specific approval / AskUserQuestion handling instead of the current preset-to-permission-mode compromise.
  - Add Claude attachment support.
  - Replace or augment the curated model alias picker if Anthropic exposes a machine-readable local model discovery path later.

## Latest Update (2026-03-29 09:19 JST)

- Started the embedded Claude Code MVP so Claude sessions are no longer hard-coded read-only:
  - Replaced the old `~/.claude` artifact tailer in `internal/codexapp/claude_session.go` with a real headless Claude driver built on `claude -p --input-format stream-json --output-format stream-json`.
  - Embedded Claude sessions now support:
    - opening/resuming existing Claude Code sessions
    - fresh `/new` / force-new Claude sessions that materialize on first prompt
    - prompt submission and transcript updates from structured Claude stream events
    - reconnect/resume using Claude `session_id`
    - interrupting the active Claude turn by killing the current headless Claude process
  - Preset handling currently maps:
    - `yolo` -> Claude `bypassPermissions`
    - `safe` / `full-auto` -> Claude `acceptEdits`
  - This is intentionally documented in-session as an MVP compromise until Claude-specific approval prompts are wired.
- Added first-class dashboard command entry points for Claude:
  - `/claude [prompt]`
  - `/claude-new [prompt]`
  - Embedded `/new` now resolves to `/claude-new` when the visible provider is Claude Code.
- Updated docs to reflect the new MVP support:
  - `README.md`
  - `docs/reference.md`
- Recorded the newly validated Claude transport assumptions below:
  - headless stream-json flow exists on the installed Claude CLI
  - `--resume <session-id>` works with normal Claude disk artifacts
  - current permission handling should be treated as MVP-level rather than approval-parity-complete
- Added focused coverage:
  - `internal/codexapp/claude_session_test.go`
  - command parsing/suggestions for `/claude` and `/claude-new`
  - TUI launch-path coverage for `launchClaudeForSelection(...)`
- Files modified:
  - `internal/codexapp/claude_session.go`
  - `internal/codexapp/claude_session_test.go`
  - `internal/detectors/claudecode/detector.go`
  - `internal/commands/commands.go`
  - `internal/commands/commands_test.go`
  - `internal/tui/app.go`
  - `internal/tui/app_test.go`
  - `internal/tui/codex_pane.go`
  - `README.md`
  - `docs/reference.md`
  - `STATUS.md`
- Verification status:
  - `go test ./internal/codexapp ./internal/commands` passed
  - `go test ./internal/codexapp -run 'TestClaude'` passed
  - `go test ./internal/tui -run 'TestLaunchClaudeForSelectionUsesClaudeProvider|TestHelpPanelLinesStayMinimal|TestViewWithHelpOverlayPreservesBackground'` passed
  - `make scan` passed at `2026-03-29T09:18:39+09:00`
  - `make doctor` passed using cached report at `2026-03-29T09:18:50+09:00`
  - `make test` still fails in pre-existing diff/TUI coverage unrelated to Claude:
    - `TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen`
    - `TestRenderDiffFileRowSelectedUsesCompactCodeSpacing`
    - `TestDiffModeMovesSelectionAndScrollsContent`
- Next concrete tasks:
  - Run `make tui` and live-check `/claude`, `/claude-new`, `/new`, `/reconnect`, and prompt submission in the embedded pane against the real local Claude CLI.
  - Add Claude-specific support for attachments, richer tool-result rendering, and explicit approval / AskUserQuestion flows instead of the current preset-to-permission-mode compromise.
  - Decide whether Claude model discovery should come from the headless CLI, the Agent SDK, or a small curated embedded picker fallback.

## Latest Update (2026-03-26 18:12 JST)

- Added scan sweep behavior in `internal/service/service.go` so periodic scans can refresh projects managed by other agents even when activity appears outside currently configured include paths.
- In `ScanWithOptions`:
  - Added full-scope detector sweep branch (excluding configured excluded paths and internal workspace path) when include paths are configured.
  - Added session recency gate (`recentActivityDiscoveryWindow = 24 * time.Hour`) and merged only recent full-scope discoveries.
  - Switched state upsert to persist `InScope` as `scope.Allows(path)` instead of always `true`.
- Added missing helper functions to complete the refactor:
  - `detectProjectActivities(...)`
  - `isRecentSessionActivity(...)`
  - `mergeDetectorActivities(...)`
  - `finalizeDetectorActivities(...)`
- Files modified:
  - `internal/service/service.go`
  - `STATUS.md`
- Verification status: no test or command verification run in this pass.
- Next concrete tasks:
  - Run interactive verification in `make tui`/`make tui-parallel` and confirm FractalMech's latest session source updates from CC to CX after an external CX update.
  - If needed, tune `recentActivityDiscoveryWindow` duration for how aggressive periodic cross-agent detection should be.

## Latest Update (2026-03-25 19:05 JST)

- Made privacy mode activation persist across launches:
  - Added `privacy_mode` boolean to `config.toml` (default: `false`)
  - Privacy mode now loads from config on startup via `EditableSettings.PrivacyMode`
  - Privacy mode is now persisted when toggled via `/privacy on|off|toggle` command
  - Added `privacyModeSavedMsg` type and handler to handle async config saves
  - Added `savePrivacyModeCmd` function in `settings.go` to persist privacy mode changes
- Files modified:
  - `internal/config/config.go`: Added `PrivacyMode` to `AppConfig`, `fileConfig`, `Default()`, and `applyConfigFile`
  - `internal/config/editable.go`: Added `PrivacyMode` to `EditableSettings`, parsing, and TOML output
  - `internal/config/config_test.go`: Updated `ParseEditableSettings` calls with new parameter
  - `internal/tui/app.go`: Initialize `privacyMode` from config, added `privacyModeSavedMsg` handler
  - `internal/tui/settings.go`: Added `savePrivacyModeCmd` and updated `ParseEditableSettings` call
- `go test ./internal/config` passes
- `make scan` and `make doctor` succeed
- Note: One pre-existing TUI test (`TestViewWithHelpOverlayPreservesBackground`) fails - unrelated to these changes

## Previous Update (2026-03-23 23:25 JST)

- Added additional OpenCode collapse behavior for long streams split into many small transcript chunks.
- In [internal/tui/codex_pane.go](/Users/davide/dev/repos/LittleControlRoom/internal/tui/codex_pane.go):
  - `collapseOpenCodeToolRuns` now also collapses long consecutive `TranscriptAgent` runs (in addition to existing tool runs) using existing summary logic in `collapseOpenCodeLargeCodeBlock`.
  - `collapseOpenCodeLargeCodeBlock` now handles non-fenced, code-like output using a code-likeness heuristic.
- Added regression test in [internal/tui/app_test.go](/Users/davide/dev/repos/LittleControlRoom/internal/tui/app_test.go):
  - `TestRenderCodexTranscriptEntriesCollapsesLongOpenCodeConsecutiveAgentChunks`
- Existing tests for no-fence long block collapse remain intact.
- Validation still not run in this fast-follow pass.

## Latest Update (2026-03-23 23:21 JST)

## Latest Update (2026-03-23 23:21 JST)

- Fixed a gap where long, non-fenced OpenCode code dumps could still appear fully rendered.
- Expanded OpenCode transcript collapsing in [internal/tui/codex_pane.go](/Users/davide/dev/repos/LittleControlRoom/internal/tui/codex_pane.go) to also summarize long code-like agent outputs without fenced blocks.
- Added heuristics for code-likeness so long brace/punctuation/indented output is compacted while preserving a short preview and `Alt+L` full-expansion path.
- Added tests in [internal/tui/app_test.go](/Users/davide/dev/repos/LittleControlRoom/internal/tui/app_test.go):
  - `TestRenderCodexTranscriptEntriesCollapsesLongOpenCodeAgentCodeBlocksWithoutFence`
- Kept existing OpenCode tool-run collapse and compact/dense behavior unchanged for other content.
- No artifact-footprint assumption changes.
- Verification status: not run in this pass.
- Next concrete tasks:
  - Use `make tui` and open the active FractalMech OpenCode session again to confirm the long code loop now collapses to a summary.
  - If needed, tune `openCodeCollapsedAgentCodeLineLimit` and `openCodeCollapsedAgentPreviewRatio`.

## Latest Update (2026-03-23 23:14 JST)

- Reduced oversized OpenCode assistant messages in the embedded transcript to avoid giant code streams in the pane.
- Added OpenCode-only output compaction in [internal/tui/codex_pane.go](internal/tui/codex_pane.go):
  - Non-expanded views now collapse long OpenCode assistant messages that contain large fenced code blocks.
  - Summary now shows total code line count, number of preview lines shown, and remaining hidden lines.
  - Message includes an `Alt+L` hint to expand.
  - In dense-expanded mode (`Alt+L`), OpenCode assistant code blocks remain fully shown.
- Kept existing OpenCode tool-run collapsing behavior and added tests for the new message compaction.
- Added regression coverage in [internal/tui/app_test.go](internal/tui/app_test.go):
  - `TestRenderCodexTranscriptEntriesCollapsesLongOpenCodeAgentCodeBlocks`
  - `TestRenderCodexTranscriptEntriesExpandsLongOpenCodeAgentCodeBlocksWithDenseMode`
  - `TestRenderCodexTranscriptEntriesKeepsCodexAgentCodeBlocksUncollapsed`
- No Codex/OpenCode artifact-footprint assumptions changed.
- Validation status:
  - Not run in this pass to keep this small-touch follow-up interaction fast.
- Next concrete tasks:
  - Run `make tui` and inspect the latest FractalMech OpenCode session with model `zai-coding-plan/glm-5`.
  - Tune the code collapse thresholds if output is still too verbose or too aggressive.

## Latest Update (2026-03-23 22:53 JST)

- Made embedded OpenCode transcript output more user-friendly when a turn emits a long burst of tool chatter.
- `internal/tui/codex_pane.go` now collapses long consecutive `TranscriptTool` runs for `ProviderOpenCode` into a short preview line such as `Tool activity: ... | +N more tool updates`.
- The change is renderer-only:
  - raw transcript entries are preserved in session state
  - Codex provider rendering is unchanged
  - normal short OpenCode tool runs still render densely rather than being collapsed
- Added regression coverage in `internal/tui/app_test.go`:
  - `TestRenderCodexTranscriptEntriesCollapsesLongOpenCodeToolRuns`
  - `TestRenderCodexTranscriptEntriesKeepsCodexToolRunsUncollapsed`
- No Codex/OpenCode artifact-footprint assumptions changed in this pass.
- Verification completed:
  - `make test` passed
  - `make scan` passed at `2026-03-23T22:52:52+09:00` (`updated projects: 1`)
  - `make doctor` passed using cached report at `2026-03-23T22:52:52+09:00`
- Next concrete tasks:
  - Run `make tui` and inspect the latest FractalMech OpenCode session to confirm the collapsed tool summary reads well in the embedded pane.
  - If the final assistant text still feels too code-heavy, add a second presentation rule that collapses oversized assistant code fences behind a concise teaser line.
 
## Latest Update (2026-03-23 14:32 JST)

- Added `decodeJSONOutput` in `internal/gitops/message.go` to sanitize model output before JSON unmarshal.
- Commit message parsing now strips markdown-style code fences and extracts the first JSON object when the model returns wrapped text.
- Reused the same sanitizer for untracked-file recommendation parsing in `internal/gitops/untracked.go`.
- Added unit coverage `TestDecodeJSONOutput` in `internal/gitops/message_test.go`.
- Verification commands were intentionally not run in this pass.
- Next concrete tasks:
  - Run `go test ./internal/gitops`.
  - Manually verify `/commit` AI message fallback in `make tui` when using OpenCode.

## Latest Update (2026-03-23 14:21 JST)

- Added unit coverage for OpenCode reconnect backoff jitter.
  - New tests in `internal/codexapp/opencode_session_test.go`:
    - `TestJitteredReconnectDelayWithinExpectedRange` verifies jitter stays inside ±20% for nominal delays.
    - `TestJitteredReconnectDelaySmallBaseFallsBackToMinimumDelay` verifies low-delay calls are clamped to the minimum reconnect wait.
  - This covers the new `jitteredReconnectDelay` behavior added for embedded session resilience.
- No validation commands were run in this pass.

## Previous Update (2026-03-23 14:20 JST)

- Added jitter to OpenCode event-stream reconnect backoff to prevent thundering-reconnect patterns.
  - `openCodeSession.runEventLoop` now sleeps for a jittered delay (±20%) instead of a fixed backoff value.
  - New helper `jitteredReconnectDelay` in `internal/codexapp/opencode_session.go` applies deterministic time-based jitter around the current backoff.
  - Delay sequence remains capped at 10s and still resets to 500ms after a successful stream cycle.
- No verification commands were run in this pass.

## Previous Update (2026-03-23 14:20 JST)

- Added exponential backoff to embedded OpenCode event-stream reconnect attempts.
  - `runEventLoop` in `internal/codexapp/opencode_session.go` now increases reconnect wait after each consecutive error, doubling from 500ms up to a 10s maximum.
  - On successful stream resume, the delay resets to 500ms.
  - This replaces the prior fixed-delay retry loop and reduces reconnection churn during prolonged network outages while preserving recovery capability.
- No verification commands were run in this pass.

## Previous Update (2026-03-23 14:19 JST)

- Prevented embedded OpenCode sessions from being torn down on transient event-stream transport errors.
  - `internal/codexapp/opencode_session.go` now keeps `runEventLoop` alive after `consumeEventStream` errors and retries every `openCodeReconnectDelay`.
  - Added explicit stream status messaging for UI visibility:
    - `openCodeStreamDisconnectMsg = "OpenCode event stream disconnected; reconnecting..."`
    - `openCodeStreamRecoverMsg = "OpenCode event stream reconnected"`
  - On reconnection, stream errors clear `lastError` and surface a recovery notice so the pane stops being stuck after intermittent network drops.
- No verification commands were run in this continuation pass to keep to the requested minimal-touch workflow.

Next concrete tasks:

- Manually run `make tui` and simulate a short network interruption in an embedded OpenCode session to confirm automatic recovery end-to-end.

## Previous Update (2026-03-23 14:08 JST)

- Added automatic snooze expiration clearing during scan cycle:
  - `ScanWithOptions` now checks if `SnoozedUntil` has passed and clears it automatically
  - Expired snooze states are removed from the database during each scan
  - Active snooze states (not yet expired) are preserved
- Added focused regression tests:
  - `TestScanOnceClearsExpiredSnooze` verifies expired snooze is cleared
  - `TestScanOnceKeepsActiveSnooze` verifies active snooze is preserved
- All tests pass, scan and doctor succeed (`make test`, `make scan`, `make doctor` all green).

Next concrete tasks:

- Manual interactive verification in `make tui` to confirm snooze expiration works as expected.

## Previous Update (2026-03-23 14:01 JST)

- Added a documented incident record for the latest embedded OpenCode continuous-repetition concern after manual analysis:
  - [docs/incidents/opencode-embedded-continuous-repetition-incident-2026-03-23.md](/Users/davide/dev/repos/LittleControlRoom/docs/incidents/opencode-embedded-continuous-repetition-incident-2026-03-23.md)
- Recorded conclusion: transcript evidence did not show repeated identical model text chunks; behavior is more consistent with streaming/rendering perception plus a manual  abort.
- No code changes were made in this step; no artifact-footprint assumptions changed.
- Verification status: no code/test validation was run (documentation-only update).

Next concrete tasks:

- Use the incident document as the starting reference if the behavior returns and attach a raw stream trace to confirm whether duplicates originate from model output or view refresh timing.

## Previous Update (2026-03-23 13:51 JST)

- Optimized space usage in details and runtime panels by combining related fields:
  - Details panel: `Repo` and `Remote` fields now combined into single `Repo: clean, synced` or `Repo: dirty, ahead 2` line
  - Runtime panel: `Runtime` status and `Up` duration combined into `Runtime: running (up 11:24)`
  - Runtime panel: `URL` and `Ports` combined into `URL: http://... (ports: 3000, 3001)`
  - Saves ~3 rows per panel for a more compact, neat display
- All tests pass, scan and doctor succeed (`make test`, `make scan`, `make doctor` all green).

Next concrete tasks:

- Manual interactive verification in `make tui` to confirm the compact panel layout looks clean.

## Previous Update (2026-03-23 13:40 JST)

- Added hide reasoning sections feature for models like GLM-5 that output verbose thinking blocks:
  - New config option `hide_reasoning_sections` in config.toml (default: `true`)
  - When enabled, `TranscriptReasoning` entries are filtered from the embedded transcript view
  - Reasoning blocks now render with dimmed text and subtle background for clearer boundaries
  - Toggle available in `/settings` dialog ("Hide reasoning" field accepts `true` or `false`)
  - `renderCodexReasoningBlock` renders reasoning with `Faint(true)` style and dark background
- All tests pass, scan and doctor succeed (`make test`, `make scan`, `make doctor` all green).

Next concrete tasks:

- Manual interactive verification in `make tui` to confirm reasoning sections are hidden by default and visible when setting is disabled.

## Previous Update (2026-03-23 10:15 JST)

- Committed OpenCode model discovery feature (`efdb0d3`):
  - Uses CLI approach (`opencode models --verbose`) for model discovery
  - Considered HTTP API (`GET /provider` via `opencode serve`) but CLI works standalone without server
  - OpenCode uses Models.dev as upstream source for model info
- Feature complete: model discovery, fallback chain, tier selection in `/setup`

Next concrete tasks:

- Manual interactive verification in `make tui` to confirm OpenCode backend uses free-tier models first and falls back gracefully on rate limiting.

## Previous Update (2026-03-23 09:26 JST)

- Added OpenCode model discovery and automatic fallback chain for summaries/commits:
  - New `internal/llm/opencode_models.go` - discovers available models via `opencode models --verbose`
  - New `internal/llm/fallback.go` - implements fallback runner that tries alternative models on rate limits
  - Model selection prioritizes free-tier models first (mimo-free, minimax-free, nemotron-free)
  - Falls back to cheap models (gpt-5.4-nano, etc.) when free models hit rate limits
  - New config option `opencode_model_tier` in config.toml: `free` (default), `cheap`, or `balanced`
  - Service now uses `OpenCodeDiscovery` to build intelligent model fallback chains
  - Both session classification and commit message generation use the fallback runner
- Added model tier selection to `/setup` dialog (not `/settings`):
  - When OpenCode is selected in `/setup`, press `T` to cycle through tiers: free → cheap → balanced
  - Shows current tier in the OpenCode row detail
  - Saved automatically when Enter is pressed
- Fixed summary re-queuing when switching AI backends:
  - Completed classifications with valid summaries are now preserved regardless of model name changes
  - Previously, switching from Codex to OpenCode would re-queue all summaries because the model name changed
- All tests pass, scan and doctor succeed (`make test`, `make scan`, `make doctor` all green).

## Latest Update (2026-03-23 08:30 JST)

- Added demo/privacy mode feature:
  - New `/privacy on|off|toggle` slash command to toggle privacy mode
  - Privacy patterns configurable via `/settings` (new "Privacy patterns" field)
  - **Privacy patterns field is masked with asterisks** (like API key) to avoid shoulder-surfing in demos
  - **Ctrl+R on privacy patterns field toggles reveal/hide** the patterns for editing
  - When privacy mode is active, projects matching configured patterns are **completely hidden** from the list
  - Privacy mode indicator shown in project list header meta: `(sort=..., view=..., privacy)`
  - Uses existing `wildcardMatch` pattern matching (same as exclude project patterns)
  - `MatchesPrivacyPattern(name, patterns)` function added to `config/project_filters.go`
  - `filterProjectsByPrivacy` filters out matching projects from the project list when privacy mode is on
  - Project list rebuilt automatically when privacy mode is toggled
- All tests pass, scan and doctor succeed (`make test`, `make scan`, `make doctor` all green).

Next concrete tasks:

- Manual interactive verification in `make tui` or `make tui-parallel` to test `/privacy` toggle and verify matching projects are hidden when privacy mode is on.

## Latest Update (2026-03-23 02:22 JST)

- Found and fixed the ROOT CAUSE of `/opencode-new` always reopening the same session:
  - **Bug**: In `initializeSession`, when `sessionID` was non-empty (from `req.ResumeID`, set by `selectedProjectSessionID` to the currently live session's thread ID) AND `ForceNew=true`, the code would:
    1. Skip the resume block (correct, since `ForceNew=true`)
    2. Skip creating a new session (incorrect, since `sessionID != ""`)
    3. Call `ensureFreshSession` on the OLD session ID
    4. But `ensureFreshSession` checks the OLD session on the NEW server - since the new server doesn't have the old session in memory, `getJSON` returns empty messages, so `ensureFreshSession` returns `nil` (treating it as "fresh")!
    5. We then proceeded with `s.sessionID = oldSessionID` - the OLD session ID on the NEW server
  - **Fix**: When `ForceNew=true`, ALWAYS clear `sessionID` first to force creation of a new session, ignoring any `ResumeID`. Added `if req.ForceNew { sessionID = "" }` before the resume logic.
- Also fixed earlier: `ensureFreshSession` returning `nil` for empty `sessionID` (now returns error), and `messages` initialized to `[]openCodeMessage{}` instead of `nil`.
- All tests pass, scan and doctor succeed (`make test`, `make scan`, `make doctor` all green).

Next concrete tasks:

- Manual interactive verification in `make tui` or `make tui-parallel` (with a real TTY) to confirm `/opencode-new` now opens a genuinely fresh session after loading a project.

## Latest Update (2026-03-23 02:05 JST)

- Investigated and fixed a bug in `ensureFreshSession` that could allow a stale/reused session to be treated as fresh:
  - `ensureFreshSession` was returning `nil` (fresh) when `sessionID` was empty after `strings.TrimSpace`, incorrectly treating no session as a fresh session.
  - Fixed by returning an error instead of `nil` when `sessionID` is empty: `return fmt.Errorf("ensureFreshSession called with empty sessionID")`.
  - Also initialized `messages` to `[]openCodeMessage{}` (empty slice) instead of `nil` to ensure proper JSON unmarshal behavior.
- All tests pass, scan and doctor succeed (`make test`, `make scan`, `make doctor` all green).

Next concrete tasks:

- The `/opencode-new` fix is now in place. Manual interactive verification in `make tui` or `make tui-parallel` (with a real TTY) is still needed to confirm end-to-end behavior.

## Latest Update (2026-03-23 01:42 JST)

- Continued `/opencode-new` force-new validation with a fresh full check:
  - `make test` passed
  - `make scan` completed at `2026-03-23T01:40:39+09:00` (`updated projects: 1`)
  - `make doctor` succeeded (`doctor report (cached, 2026-03-23T01:40:46+09:00)`)
- Interactive verification still blocked in this session:
  - `make tui` reports an already-active runtime for the default DB (pid `44068`) and suggests `--allow-multiple-instances` for short-lived overlap.
  - `make tui-parallel` fails in this shell context because no TTY is available (`could not open a new TTY: open /dev/tty: device not configured`).
- No new functional changes were made in this continuation pass; current state is to wait for a terminal-capable session to run the final `/opencode-new` manual flow.

Next concrete tasks:

- Re-run `make tui` in a terminal capable of interactive TTY (or stop the active pid if safe) and execute `/opencode-new` end-to-end for a real project.
- If needed, use a fresh isolated DB with a real PTY (for example via a terminal `env COLUMNS=... LINES=... make tui-parallel`) to complete the manual check once `/dev/tty` is available.

## Latest Update (2026-03-23 01:34 JST)

- Completed end-to-end validation for the `/opencode-new` force-new retry path and kept the max-attempt guard at `maxForceNewEmbeddedOpenAttempts = 3`:
  - `internal/tui/codex_pane.go` now retries with an evolving `threadIDsToAvoid` set built from:
    - current in-memory live session thread
    - `req.ResumeID`
    - each `ThreadID` surfaced via `ForceNewSessionReusedError` returned from manager open attempts
  - `shouldRetryFreshEmbeddedOpen`, `shouldRetryFreshEmbeddedOpenError`, and `extractForceNewReusedThread` now encapsulate reuse/retry logic.
  - `embeddedSessionOpenStatus` now accepts `threadIDsToAvoid` so user-facing status strings describe matched/reused attempts consistently.
- Added regression coverage in `internal/tui/app_test.go` for `/opencode-new` retry behavior:
  - `TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeRejectsFreshSession`
  - `TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeReturnsKnownReusedSession`
- Confirmed OpenCode freshness rejection path in `internal/codexapp/opencode_session.go` via `ensureFreshSession`, returning `ForceNewSessionReusedError` whenever a newly-created session has pre-existing history.

- Verification completed:
  - `make test` passed
  - `make scan` updated 1 project at `2026-03-23T01:31:53+09:00`
  - `make doctor` produced a cached report at `2026-03-23T01:32:01+09:00`

Next concrete tasks:

- Run `make tui` when convenient to manually verify `/opencode-new` in a real embedded session path now that retries are implemented.

## Latest Update (2026-03-23 01:17 JST)

- Hardened `/opencode-new` force-new retry matching so stale/reused threads are tracked across attempts:
  - `internal/tui/codex_pane.go` now seeds the retry-check set with `previousThreadID` and `req.ResumeID`, records thread IDs from `ForceNewSessionReusedError` rejections, and reuses that evolving set to classify subsequent `Open` success snapshots as stale/reused.
  - `openCodexSessionCmd` still enforces `maxForceNewEmbeddedOpenAttempts = 3` and preserves existing status message branches.
- Added a regression test in `internal/tui/app_test.go`:
  - `TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeReturnsKnownReusedSession`
  - verifies a third attempt is triggered when a second attempt returns a known reused session ID that is not `previousThreadID` or `req.ResumeID`.

Verification snapshot:

- `gofmt -w internal/tui/codex_pane.go internal/tui/app_test.go`
- `go test ./internal/tui -run 'TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeRejectsFreshSession|TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeReturnsKnownReusedSession|TestLaunchCodexForSelectionForceNewRetriesWhenCodexRejectsFreshThread' -count=1` (pass)
- `go test ./internal/tui -count=1` (pass)

Next concrete tasks:

- Run `make test`, `make scan`, and `make doctor` before handoff.
- Continue with `make tui` to confirm `/opencode-new` behavior in an interactive OpenCode session.

## Latest Update (2026-03-23 00:55 JST)

- Hardened `sanitize-summaries` to avoid missing classification rows when a project has more than 30 sessions: the command now builds `SessionEvidence` from classification records instead of searching only the first 30 project sessions returned by `GetProjectDetail`.
- Added a validation run with constrained dry-run:
  - `go run ./cmd/lcroom sanitize-summaries --project /Users/davide/dev/repos/LittleControlRoom --dry-run`
  - result: `matched=161 changed=0 skipped=161 failed=0`

- Moved session summary normalization into classification upstream so status-like text (for example `Turn completed`) never persists as a completed summary again. `sessionclassify.Manager.processOne` now sanitizes `result.Summary` with transcript-aware fallback before storing.
- Added a dedicated CLI utility `sanitize-summaries` to backfill existing rows:
  - filters: `--project`, `--session-id`
  - controls: `--apply`, `--dry-run`
  - default is safe dry-run when `--apply` is not set.
- Updated the embedded sessions global picker to show project ownership on each row (`[Project Name]` / path-base fallback) so multi-project session lists are immediately scannable.
- Expanded tests for sanitizer behavior, store query/update helpers, CLI sanitize flow, and picker project hint rendering.

Verification snapshot:

- `go test ./...`
- `make scan` completed at `2026-03-23T00:48:46+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 0`)
- `make doctor` completed with cached report at `2026-03-23T00:48:54+09:00` (exit success)

Next concrete tasks:

- Run `lcroom sanitize-summaries --apply` if we want to backfill any remaining historical mis-summaries in the current DB.

## Latest Update (2026-03-23 00:27 JST)

- Moved summary cleanup to the session-classification layer so status-like text never reaches stored summaries. `sessionclassify` now sanitizes classifier output via `sanitizeClassificationSummary` and uses transcript previews as the primary fallback.
- Removed picker-side status fallback logic so embedded session rows no longer special-case values like `Turn completed` when rendering summaries.
- Updated global embedded picker row rendering to include owning project hints (`[Project Name]` or path base fallback), so cross-project OpenCode sessions are obvious.
- Added/updated tests:
  - `TestSanitizeClassificationSummaryUsesTranscriptPreviewForStatusLikeInput`
  - `TestSanitizeClassificationSummaryKeepsNonStatusText`
  - `TestSanitizeClassificationSummaryFallsBackWhenNoTranscriptPreview`
  - `TestManagerProcessOneSanitizesStatusLikeSummaryFromClassifier`
  - `TestAddPickerProjectHintFallsBackToPathBase`

Verification snapshot:

- `go test ./internal/sessionclassify ./internal/tui ./internal/codexapp`
- `make test`
- `make scan`
- `make doctor`

Next concrete tasks:

- Run `make tui` to confirm the embedded sessions list shows the expected per-project hints and that older pre-migration OpenCode session summaries are visually cleaned up as expected.

## Latest Update (2026-03-22 23:53 JST)

- Fixed embedded sessions picker behavior so OpenCode entries no longer show status strings like `Turn completed` in place of summaries.
- Added a picker fallback so project-bound sessions in the global picker now show summary from `LatestCompletedSessionSummary` when `LatestSessionSummary` is status-like.
- Added row rendering support so global picker rows display the owning project (`[Project Name]` or path base fallback), making cross-project session lists easier to scan.
- Added/updated focused TUI tests:
  - `TestBuildCodexResumeChoicesUsesExtractedSummaryWhenLatestSummaryIsStatusLike`
  - `TestPickerSummaryForProjectFallsBackToCompletedSummaryForTurnCompleted`
  - `TestSummaryLooksLikeSessionStatus`
  - `TestAddPickerProjectHintFallsBackToPathBase`

Verification snapshot:

- `go test ./internal/tui` passed
- `go test ./internal/tui -run 'TestBuildCodexResumeChoicesUsesExtractedSummaryWhenLatestSummaryIsStatusLike|TestPickerSummaryForProjectFallsBackToCompletedSummaryForTurnCompleted|TestSummaryLooksLikeSessionStatus|TestAddPickerProjectHintFallsBackToPathBase|TestAltDownOpensCodexSessionPicker|TestRenderCodexPickerRowUsesCompactSavedBadgeAndTitleInResumeMode|TestRenderCodexPickerRowMarksLatestSavedSessionInResumeMode'` passed
- `make test` passed
- `make scan` completed (`activity projects: 88`, `tracked projects: 138`, `updated projects: 0`, `queued classifications: 0`) at `2026-03-22T23:52:58+09:00`
- `make doctor` returned cached report at `2026-03-22T23:53:05+09:00`

Next concrete tasks:

- Confirm this exact OpenCode picker ordering and label rendering in `make tui` for interactive confirmation.

## Latest Update (2026-03-22 23:41 JST)

- Fixed `/opencode-new` fresh-launch reliability by teaching embedded OpenCode startup to reject a newly created session when the backend hands back a session that already has retained message history.
- Reused the existing forced-fresh retry path by making `ForceNewSessionReusedError` provider-aware and returning it from both Codex and OpenCode fresh-session guards.
- Added a focused OpenCode startup regression in `internal/codexapp/opencode_session_test.go`:
  - `TestOpenCodeInitializeSessionForceNewRejectsReusedSessionWithHistory`
- Added a TUI regression in `internal/tui/app_test.go`:
  - `TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeRejectsFreshSession`
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/session.go internal/codexapp/opencode_session.go internal/codexapp/opencode_session_test.go internal/tui/app_test.go` passed
- `go test ./internal/codexapp -run 'TestOpenCodeInitializeSessionForceNewRejectsReusedSessionWithHistory|TestOpenCodeSessionRefreshReconcilesStalePendingReasoningWhenModelMatches' -count=1` passed
- `go test ./internal/tui -run 'TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeRejectsFreshSession|TestVisibleOpenCodeSlashNewStartsFreshSession' -count=1` passed
- `make test` passed
- `make scan` completed (`updated projects: 1` at `2026-03-22T23:41:23+09:00`)
- `make doctor` returned the cached report dated `2026-03-22T23:41:24+09:00` and completed successfully

Next concrete tasks:

- Live-check `/opencode-new` against the real embedded OpenCode backend in `make tui` and confirm it now lands on a distinct new session even when the latest OpenCode session exists for the same project.

## Latest Update (2026-03-22 23:32 JST)

- Fixed the embedded OpenCode busy-timer reset loop in `internal/codexapp/opencode_session.go` by separating turn-start timing from general activity timing.
- OpenCode sessions now keep a dedicated `busySince` plus `lastBusyActivityAt`, so periodic refreshes and repeated busy events no longer restart the visible `Working 00:xx` timer.
- Added focused OpenCode regressions in `internal/codexapp/opencode_session_test.go`:
  - `TestOpenCodeSessionBusyStatusKeepsExistingBusySince`
  - `TestOpenCodeSessionRefreshBusyKeepsExistingBusySince`
  - extended `TestOpenCodeSessionIdleAfterExternalBusyMarksSessionReady` to verify busy timing clears on idle
- Extended the launch-override reconciliation path in `internal/codexapp/opencode_session.go` so stale launch pending reasoning is also cleared when replayed model matches the pending model, preventing stale `/model` reason from resurfacing as a still-pending `/next` value.
- Added `TestOpenCodeSessionRefreshReconcilesStalePendingReasoningWhenModelMatches` in `internal/codexapp/opencode_session_test.go`.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/opencode_session.go internal/codexapp/opencode_session_test.go` passed
- `go test ./internal/codexapp -count=1` passed
- `go test ./internal/codexapp -run 'TestOpenCodeSessionRefreshReconcilesStalePendingReasoningWhenModelMatches' -count=1` passed
- `make test` passed
- `make scan` completed (`updated projects: 1` at `2026-03-22T23:32:43+09:00`)
- `make doctor` returned the cached report dated `2026-03-22T23:32:43+09:00` and completed successfully

Next concrete tasks:

- Live-check the original stuck OpenCode session flow in `make tui` and confirm the footer timer now keeps climbing instead of snapping back after about one minute.

## Latest Update (2026-03-22 22:40 JST)

- Completed the OpenCode stale-`/model` launch override reconciliation flow by tracking whether pending values came from launch and clearing only those when transcript replay proves the session is already running under a different model.
- Added focused OpenCode session tests in `internal/codexapp/opencode_session_test.go`:
  - `TestOpenCodeSessionRefreshReconcilesStalePendingModelFromReplayedModel`
  - `TestOpenCodeSessionRefreshKeepsLaunchPendingWhenNoReplayedModel`
- Added a TUI regression in `internal/tui/app_test.go`:
  - `TestRenderCodexSessionMetaSkipsNextWhenPendingHasBeenAppliedBeforeOpen`

Verification snapshot:

- `go test ./internal/codexapp -count=1` passed
- `go test ./internal/tui -count=1` passed
- `make test` passed
- `make scan` completed (`updated projects: 1` at `2026-03-22T22:38:51+09:00`)
- `make doctor` returned the cached report and was command- and config-parse successful

Next concrete tasks:

- Optional follow-up: keep `reopen`-path test for this behavior if OpenCode transcript formats change around model metadata.

## Latest Update (2026-03-22 22:00 JST)

- Committed two scoped follow-ups from the current work set:
  - `e87b539` updates `/snooze` slash behavior and docs to support `/snooze off`, `/snooze clear`, `/snooze unsnooze`, and `/unsnooze`.
  - `6a52aab` changes OpenCode launch env construction to replace any existing `OPENCODE_CONFIG_CONTENT` instead of appending another copy.
- Added/updated focused tests to lock both behaviors in `internal/commands` and `internal/codexapp`.

Verification snapshot:

- `gofmt -w internal/commands/commands.go internal/commands/commands_test.go internal/codexapp/opencode_session.go internal/codexapp/opencode_session_test.go`
- `go test ./internal/commands -run 'Test(Parse|Suggestions)' -count=1`
- `go test ./internal/codexapp -run 'TestBuildOpenCodeServerCommandOverridesPreExistingConfigEnv|TestBuildOpenCodeServerCommandInjectsPresetConfig' -count=1`

Next concrete tasks:

- No immediate follow-up required for these scoped cleanups.

## Latest Update (2026-03-22 20:50 JST)

- Removed the remaining TUI quick-key mapping for snooze clear: `S` no longer maps to `m.clearSnoozeCmd` in normal mode.
- Added/updated TUI regression coverage so both `s` and `S` are now explicitly no-ops in `updateNormalMode` when pressed on a project row.
- Updated `docs/reference.md` to remove `S` from the normal TUI key list, matching the command-palette-only snooze workflow.

Verification snapshot:

- `go test ./internal/tui -run 'TestSKeyNoLongerSnoozesProject|TestSUppercaseNoLongerClearsSnooze|TestSuggestionsSnoozeArgumentOff' -count=1`
- `go test ./internal/commands -run 'TestParse|TestSuggestions' -count=1`
- `make test`
- `make scan`
- `make doctor`

Next concrete tasks:

- No immediate follow-up required for this key-remap cleanup; optionally run a quick TUI smoke check if you want a live confirmation.

## Latest Update (2026-03-22 20:44 JST)

- Added a focused regression test for env handling: `TestBuildOpenCodeServerCommandOverridesPreExistingConfigEnv` in `internal/codexapp/opencode_session_test.go`.
- The test verifies pre-existing `OPENCODE_CONFIG_CONTENT` is replaced (not duplicated), and the injected config still reflects safe preset permissions.

Verification snapshot:

- `go test ./internal/codexapp -run 'Test(BuildOpenCodeServerCommandInjectsPresetConfig|TestBuildOpenCodeServerCommandOverridesPreExistingConfigEnv|TestOpenCodePermissionOverrideForPreset)' -count=1`

Next concrete tasks:

- Continue normal cleanup/iteration; no follow-up required for this regression class.

## Latest Update (2026-03-22 20:40 JST)

- Resolved the last `make test` blocker by making OpenCode launch env injection deterministic: `OPENCODE_CONFIG_CONTENT` is now explicitly overridden in `openCode` server env construction (no duplicate/empty stale values from caller env).
- Confirmed that this was the actual cause of the previous preset mismatch failure; nuking the runtime process was unrelated.
- Kept snooze-workflow changes in place: no accidental lowercase `s` snooze, `/snooze` now accepts `off/clear/unsnooze`, and docs/tests reflect `/unsnooze` plus explicit clear behavior.

Verification snapshot:

- `go test ./internal/codexapp -run 'TestBuildOpenCodeServerCommandInjectsPresetConfig|TestOpenCodePermissionOverrideForPreset' -count=1`
- `go test ./internal/commands -run 'TestParse|TestSuggestions' -count=1`
- `go test ./internal/tui -run 'TestSKeyNoLongerSnoozesProject|TestSClearSnoozeStillWorks|TestSuggestionsSnoozeArgumentOff' -count=1`
- `make scan`
- `make doctor`
- `make test`

Next concrete tasks:

- No immediate follow-up required: suite is clean, and the remaining focus is normal iterative polish.

## Latest Update (2026-03-22 20:12 JST)

- Fixed the TODO dialog selected-row spacing by removing the extra horizontal padding from the selected-row style.
- Added a regression test to ensure the selected TODO row renders with a single leading space after the dialog border (no extra blank before the checkbox marker).

Verification snapshot:

- `go test ./internal/tui -run 'TestTodoDialogEnterStartsFreshPreferredProviderWithDraft|TestTodoDialogSelectedRowHasNoExtraLeadingSpace|TestTodoDialogLegendUsesDistinctActionTones|TestRenderDialogPanelRestoresBackgroundAfterStyledResets' -count=1`
- `make test`
- `make scan`
- `make doctor`

Next concrete tasks:

- Confirm the same spacing behavior visually in a full terminal with the latest dialog theme settings.

## Latest Update (2026-03-22 19:53 JST)

- Fixed modal/dialog background rendering across the shared TUI dialog family by routing those overlays through a shared `renderDialogPanel` helper that restores the panel background after nested ANSI style resets and fills each rendered line to the panel width.
- Applied that shared dialog-panel renderer to the TODO, notes, settings, setup, command palette, commit/git-status, picker, filter, ignored-projects, run-command, and new-project overlays so the dark/black gaps seen between styled spans no longer bleed through on modal rows.
- Polished the TODO dialog specifically:
  - Rebalanced the action legend tones so edit/provider read as navigation, done reads as a state-change action, and delete reads as destructive instead of almost everything showing up in the same green key treatment.
  - Made the selected TODO row render at the full dialog width so the highlight reads as one clean row instead of a short badge.
- Added focused TUI regressions covering the dialog background restoration helper and the distinct TODO legend tones.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `go test ./internal/tui -run 'Test(RenderDialogPanelRestoresBackgroundAfterStyledResets|TodoDialogLegendUsesDistinctActionTones|ViewWithSettingsModeRespectsHeight|SettingsModalRendersColoredActionLegend|TodoDialogEnterStartsFreshPreferredProviderWithDraft)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-22T19:52:56+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T19:53:05+09:00` (`projects: 131`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-dialog-bg-smoke INTERVAL=1h` launched the isolated TUI sandbox successfully and exited cleanly via `q`.

Next concrete tasks:

- Live-check the TODO, note, and settings dialogs in the real terminal theme and confirm the shared background-restoration helper fully clears the remaining black-gap artifacts.
- Decide whether the same per-line background restoration should also be applied to any future non-modal chrome that starts combining dense styled spans inside filled panels.

## Latest Update (2026-03-22 19:21 JST)

- Tuned TODO UX polish:
  - Removed the `-` marker for empty TODO count cells in the project list so rows now show blank when open TODO count is zero.
  - Increased TODO dialog footprint (`todoDialogPanelLayout` and a dedicated `todoEditorPanelLayout`) so lists and edits have more room.
  - Switched TODO entry editing from single-line `textinput` to multiline `textarea`.
  - Changed TODO edit save from bare `Enter` to `Ctrl+S` so Enter can create newlines while editing.
  - Added colorized legend/action rows in TODO list and edit overlays using `renderDialogAction`, matching the rest of the modal key legend styling.
- No functional TODO workflow changed beyond this UI polish.

Verification snapshot:

- `make tui-parallel` / `make test` not re-run for this visual-only pass.

Next concrete tasks:

- Decide whether TODO editor save should also accept plain Enter for power users or remain `Ctrl+S` only.

## Latest Update (2026-03-22 19:01 JST)

- Replaced the visible per-project note workflow with first-class TODOs backed by a new `project_todos` store table, while keeping the old `projects.note` column only as a legacy migration source.
- Added TODO counts to project summaries/details so the project list now shows a compact open-task counter and the detail pane shows a `TODO` section with open items instead of the old freeform note block.
- Added automatic legacy-note migration on store open: existing project notes are split by non-empty line into open TODOs, then the legacy note text is cleared so the migration only happens once.
- Added TODO service operations and TUI overlays for listing, adding, editing, toggling, and deleting TODOs, plus a new `/todo` slash command and `t` quick action.
- Wired `Enter` on a selected TODO to open a fresh embedded Codex/OpenCode session using the project's preferred provider, preload the selected TODO text into the composer draft, and leave it unsent for review before submission.
- Kept a hidden `/note` compatibility path for now so older note-oriented tests and workflows do not break abruptly while the visible UX shifts to TODOs.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/model/model.go internal/store/store.go internal/service/service.go internal/commands/commands.go internal/tui/app.go internal/tui/todo_dialog.go internal/store/store_test.go internal/tui/app_test.go` passed.
- `go test ./internal/store -run 'Test(OpenMigratesLegacyProjectNotesIntoTodos)' -count=1` passed.
- `go test ./internal/tui -run 'Test(TodoDialogEnterStartsFreshPreferredProviderWithDraft|RenderProjectListShowsTODOCount|RenderDetailContentShowsTODOSection|HelpPanelLinesStayMinimal)' -count=1` passed.
- `go test ./internal/commands ./internal/store ./internal/tui -count=1` passed.
- `go test ./internal/service -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-22T19:00:23+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T19:00:23+09:00` (`projects: 131`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-todo-smoke-pty INTERVAL=1h` launched successfully in a PTY, rendered the updated dashboard with the new `TODO` list column, and exited cleanly via `q`.

Next concrete tasks:

- Decide whether the hidden `/note` compatibility path should be removed soon now that `/todo` is the primary visible workflow.
- Decide whether TODOs need drag/reorder support or whether append-only ordering is enough for the first real usage cycle.
- Consider whether completed TODOs should get a lighter inline treatment or a collapsible subsection in the TODO overlay once projects accumulate longer histories.

## Latest Update (2026-03-22 13:52 JST)

- Investigated the command-palette bug where typing `/open`, moving the highlight down to `/opencode`, and pressing Enter still executed `/open`.
- Traced the issue to the palette's Enter-resolution order: it accepted an already-valid typed command before checking whether the currently highlighted suggestion was a longer prefix match the user had explicitly selected.
- Fixed the main command palette so Enter now prefers the highlighted suggestion whenever that suggestion extends the typed prefix, which lets `/open` correctly resolve to `/opencode` or `/opencode-new` after cursor selection.
- Applied the same resolution rule to the embedded slash-command path for consistency, even though the current embedded slash command set does not yet expose the same overlapping valid-prefix case.
- Added a focused TUI regression proving that a highlighted `/opencode` selection launched OpenCode instead of the shorter `/open` command.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/codex_slash.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -run 'Test(CommandEnterUsesAutocompleteSuggestion|CommandEnterUsesHighlightedSuggestionOverValidPrefix|VisibleCodexSlashStatusRunsLocally)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-22T13:46:12+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 2`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T13:46:11+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-command-palette-smoke INTERVAL=1h` launched the isolated TUI sandbox successfully and exited cleanly via `q`.

Next concrete tasks:

- Live-check the exact `/open` -> highlighted `/opencode` -> Enter flow in the updated TUI and confirm the visible selection now matches the executed command end to end.
- Decide whether the command palette should eventually mirror some editors and write the highlighted suggestion into the input immediately on arrow movement, instead of only resolving it on Enter or Tab.

## Latest Update (2026-03-22 13:18 JST)

- Investigated why `/run` showed no automatic command suggestion for the `oykgames.com` project and confirmed the runnable app files live under the repo's `src/` subdirectory rather than the project root.
- Extended the managed-runtime suggester so it still prefers root-level markers first, but now falls back to a shallow nested-directory scan when the root has no `bin/dev`, `package.json`, `Makefile`, `justfile`, or Go entrypoint.
- Kept the nested scan intentionally bounded and conservative: it skips obvious build/dependency directories, only searches a shallow depth, and refuses to guess when multiple nested app roots are found.
- Nested suggestions now prefill shell-safe commands such as `cd src && pnpm dev`, with a matching suggestion reason that points at the subdirectory that was detected.
- Taught the project-list/runtime summary label extraction to look past a leading `cd ... &&` prefix so nested commands still render as the real runtime label (`pnpm`, `vite`, etc.) instead of `cd`.
- Added focused regressions for single nested app detection, root-over-nested preference, multi-match ambiguity suppression, and run-label rendering for nested `cd ... &&` commands.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- Confirmed the real `oykgames.com` layout on disk: `/Users/davide/dev/repos/oykgames.com` has no root `package.json`, while `/Users/davide/dev/repos/oykgames.com/src/package.json` contains the runnable `dev`, `build`, and `preview` scripts.
- `gofmt -w internal/projectrun/suggest.go internal/projectrun/suggest_test.go internal/tui/app.go internal/tui/app_test.go` passed.
- `go test ./internal/projectrun -run 'TestSuggest(UsesSingleNestedPackageScript|PrefersRootCandidateOverNestedCandidate|DoesNotGuessAcrossMultipleNestedCandidates|PrefersBinDev|UsesPnpmForDevScript|UsesSingleGoCmdEntrypoint)' -count=1` passed.
- `go test ./internal/tui -run 'TestProjectRunSummary(IncludesCommandAndPort|UsesCommandAfterNestedCdPrefix|ShowsConflictInRunColumn)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-22T13:17:38+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T13:17:39+09:00` (`projects: 133`).

Next concrete tasks:

- Consider whether the run-command dialog should eventually show multiple nested candidates as explicit choices instead of leaving the field empty when more than one plausible app root is found.
- Decide whether managed runtimes should grow an explicit saved working-directory field later, so nested app launches do not need to be encoded as `cd ... && ...` in the saved command text.

## Latest Update (2026-03-22 12:17 JST)

- Investigated the report that the embedded Codex timer restarted after a round had visibly ended and confirmed the restart signal in the real rollout for this repo rather than relying on code inspection alone.
- The rollout showed Codex can emit a bare `task_started` for control/meta churn such as model-switch or developer-context injection before any user-visible activity follows, and Little Control Room was surfacing that provisional start as a real busy turn in the embedded pane and project list.
- Tightened the embedded Codex session state machine so a raw `turn/started` notification no longer lights a fresh busy timer when the pane was idle already; it now keeps the turn ID for correlation and only promotes the turn to busy once real item/output activity arrives, while locally submitted prompts still show immediate busy state.
- Tightened Codex artifact turn-state recovery in both the detector and `sessionclassify` fallback so a trailing control-only `task_started` no longer overwrites the last stable completed state unless meaningful non-control activity follows in that turn.
- Added focused regressions for both sides of the fix: embedded idle `turn/started` stays provisional, and detector/classifier rollout parsing now ignores a control-only `task_started` followed only by `turn_context`, developer messages, and token-count noise.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- Live rollout inspection against `/Users/davide/.codex/sessions/2026/03/22/rollout-2026-03-22T11-43-29-019d136d-1742-7722-9398-7e2cd2b58052.jsonl` confirmed the unexpected restart pattern: a completed turn was followed by a control-only `task_started` plus developer-context churn before the next visible user activity.
- `gofmt -w internal/codexapp/session.go internal/codexapp/session_test.go internal/detectors/codex/detector.go internal/detectors/codex/detector_test.go internal/sessionclassify/extract.go internal/sessionclassify/extract_test.go` passed.
- `go test ./internal/codexapp ./internal/detectors/codex ./internal/sessionclassify -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-22T12:16:53+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T12:17:09+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-control-turn-smoke INTERVAL=1h` launched the isolated TUI sandbox successfully and exited cleanly via `q`.

Next concrete tasks:

- Live-check the same control-turn scenario once a fresh model-switch or similar Codex control event happens again, to confirm the embedded pane no longer shows a ghost busy timer before visible turn activity begins.
- Consider whether the detector should also distinguish separate classes of non-user Codex turns more explicitly in stored session metadata, instead of only suppressing control-only starts from the busy timer path.

## Latest Update (2026-03-22 11:40 JST)

- Fixed embedded OpenCode launch presets so the global `codex_launch_preset` now applies to OpenCode too instead of only Codex.
- OpenCode embedded sessions now carry the preset through launch validation, snapshot state, and banner rendering, so YOLO sessions show the same `YOLO MODE` warning overlay that Codex already had.
- Started embedded OpenCode servers with an `OPENCODE_CONFIG_CONTENT` override derived from the saved preset, mapping `yolo`, `full-auto`, and `safe` to the closest OpenCode permission profile available without mutating the user's disk config.
- Added project-list approval pulsing for pending embedded approvals, and boosted attention scoring/reasons for those projects so an approval request stands out immediately in the dashboard.
- Added focused regressions for the OpenCode preset-to-permission mapping, injected server config, OpenCode YOLO banner rendering, OpenCode launch preset propagation, and the new approval pulse styling.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- Live probe: `OPENCODE_CONFIG_CONTENT='{"permission":{"edit":"allow","bash":"allow","webfetch":"allow","external_directory":"allow","doom_loop":"allow"}}' opencode serve --hostname 127.0.0.1 --port 0 --print-logs` started successfully, `GET /config` reflected the injected permission override, and `GET /agent` showed the effective `build` agent picking up the appended allow rules.
- `gofmt -w internal/codexapp/opencode_session.go internal/codexapp/opencode_session_test.go internal/codexapp/types.go internal/tui/app.go internal/tui/runtime_attention.go internal/tui/codex_pane.go internal/tui/app_test.go` passed.
- `go test ./internal/codexapp -run 'Test(OpenCodePermissionOverrideForPreset|BuildOpenCodeServerCommandInjectsPresetConfig|OpenCodePostJSONNilPayloadSendsEmptyJSONObject|OpenCodePostJSONMarshalsPayloadAsJSON)' -count=1` passed.
- `go test ./internal/tui -run 'Test(ApprovalPulseHighlightsProjectListRow|OpenCodexSessionChoiceLaunchesOpenCodeResume|VisibleCodexViewShowsBannerAndYoloWarning|VisibleOpenCodeViewShowsBannerAndYoloWarning)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-22T11:40:02+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T11:40:03+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-approval-pulse-smoke INTERVAL=1h` launched the isolated TUI sandbox successfully and exited cleanly via `q`.

Next concrete tasks:

- Live-check a real embedded OpenCode approval flow in the dashboard and confirm the project row pulse feels obvious enough once an actual pending approval lands.
- Decide whether approval-pending projects should also swap the list status label to something explicit like `approve`, or whether the new pulse + attention boost is the right level of interruption.

## Latest Update (2026-03-22 11:15 JST)

- Made embedded `/model` choices durable across LCR restarts by storing per-provider model/reasoning preferences in `config.toml` for both Codex and OpenCode.
- Initialized the TUI's embedded model preference cache from saved settings at startup, and auto-save those preferences whenever `/model` changes so a relaunch keeps the same override.
- Preserved these hidden `/model` preferences when saving normal settings, so opening `/settings` later no longer wipes a previously saved model override.
- Updated the embedded `/model` help text in the README, slash-command copy, and reference docs to clarify that the choice now survives restarts.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/config/config.go internal/config/editable.go internal/config/config_test.go internal/service/service.go internal/tui/embedded_model_preferences.go internal/tui/app.go internal/tui/settings.go internal/tui/app_test.go internal/codexslash/commands.go` passed.
- `go test ./internal/config ./internal/tui ./internal/codexslash -run 'Test(ParseLoadsEmbeddedModelPreferencesFromConfigFile|SaveEditableSettingsWritesReadableTOML|EmbeddedModelPreferenceLoadsFromSavedSettingsOnStartup|SettingsSavePreservesEmbeddedModelPreferences|CodexActionMsgPersistsEmbeddedModelPreferencesToConfig|EmbeddedModelPreferencePersistsAcrossFutureSessionsPerProvider|SuggestionsIncludeModelCommand|ParseModelCommand)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-22T11:15:02+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 2`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T11:15:03+09:00` (`projects: 133`).

Next concrete tasks:

- Live-check `/model` end to end for both Codex and OpenCode by setting a non-default model, restarting LCR, and confirming the next embedded session starts with the saved override already staged.
- Decide whether `/model` should also grow an explicit "reset to provider default" action now that preferences are durable, so users have a direct way to clear a saved override instead of replacing it with another one.

## Latest Update (2026-03-22 11:05 JST)

- Investigated the reported `/opencode` provider-switch failure on projects whose last embedded session was Codex, and traced it to fresh OpenCode session creation rather than cross-provider state reuse.
- Live `opencode serve` probing confirmed the current OpenCode `1.2.24` HTTP surface now rejects an empty-body `POST /session` with `Content-Type: application/json`, returning `400 Malformed JSON in request body`, while the same request succeeds with `{}`.
- Fixed the embedded OpenCode HTTP helper so bodyless JSON POSTs now send `{}` instead of an empty body, which covers fresh `/session` creation and any other OpenCode POSTs that do not carry payload fields.
- Added focused `internal/codexapp` regressions covering both the new nil-payload `{}` behavior and the unchanged payload-marshaling path for normal JSON POST requests.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- Live `opencode serve --hostname 127.0.0.1 --port 0 --print-logs` probing in `/Users/davide/dev/repos/LittleControlRoom` reproduced the failure: `POST /session` with an empty body returned `400 Malformed JSON in request body`, while `POST /session` with `--data '{}'` returned `200 OK` and created session `ses_2ecb7d292ffeGzqocSmmmJUfn2`.
- `gofmt -w internal/codexapp/opencode_session.go internal/codexapp/opencode_session_test.go` passed.
- `go test ./internal/codexapp -run 'TestOpenCode(PostJSONNilPayloadSendsEmptyJSONObject|PostJSONMarshalsPayloadAsJSON)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-22T11:05:09+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T11:05:18+09:00` (`projects: 133`).

Next concrete tasks:

- Live-check `/opencode` on the reported `oykgames.com` project now that fresh embedded OpenCode session creation sends valid JSON, and confirm the pane switches cleanly from the previous Codex session instead of surfacing the `POST /session` error.
- If OpenCode keeps tightening its HTTP contract, consider a small transport-level regression around other nil-payload POST endpoints such as `/session/<id>/abort` so future server-side JSON strictness changes stay caught quickly.

## Latest Update (2026-03-22 10:59 JST)

- Investigated the `/diff` flash-and-close behavior on clean repos and confirmed the diff screen was being opened and then immediately torn down when `PrepareDiff` returned `NoDiffChangesError`.
- Kept the diff screen open for clean-worktree cases by preserving an empty diff state with project/branch metadata instead of closing back to the dashboard.
- Updated the diff renderer, status text, footer, and key handling so the empty state now clearly says the worktree is clean, shows the warning inside the diff pane, and hides file-selection/render-mode controls that do not apply without changed files.
- Added a focused TUI regression proving a clean-worktree `/diff` result keeps the screen open and renders the warning instead of bouncing away.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/diff_view.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -run 'Test(ViewWithDiffScreenUsesFullBody|DiffPreviewMsgNoChangesKeepsDiffScreenOpen)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-22T10:58:57+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T10:58:57+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-clean-diff-smoke INTERVAL=1h` launched the isolated TUI sandbox successfully and exited cleanly via `q`.

Next concrete tasks:

- Consider whether the clean-worktree diff state should offer a one-line recovery hint such as `/commit` or `/run` for users who land there expecting the next workflow step.
- If other full-screen tools still collapse on empty states, align them with this "stay open and explain why" pattern for consistency.

## Latest Update (2026-03-22 08:32 JST)

- Added a permanent development note to `AGENTS.md` so the recent startup/debugging lesson is captured in repo guidance instead of only living in session history.
- Documented that parallel TUI experiments should use `make tui-parallel`, that `make tui-parallel-clean` should be run periodically to prune stale `/tmp/lcroom-parallel-*` sandboxes, and that startup/load failures must surface explicit error states rather than leaving the UI looking like it is still loading.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `make test` passed.
- `make scan` passed at `2026-03-22T08:32:20+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T08:32:20+09:00` (`projects: 133`).

Next concrete tasks:

- If more dev-only operational knowledge accumulates, consider a short `## Development Hygiene` section in `AGENTS.md` so sandbox cleanup, runtime-lock expectations, and startup error-reporting guidance stay grouped together.
- If startup can still feel ambiguous, add a more direct recovery hint in the top status line for common failures such as DB lock conflicts or project-list load errors.

## Latest Update (2026-03-22 08:26 JST)

- Investigated the report that `make tui` looked stuck at startup, then confirmed the immediate blocker was a still-running `lcroom tui` already holding the default DB lock while several old `tui-parallel` sandboxes were still lying around under `/tmp`.
- Added `make tui-parallel-clean` and wired `make tui-parallel` to run it first, so stale `/tmp/lcroom-parallel-*` sandboxes that are no longer backing an active `lcroom tui --db ...` process are removed automatically before new parallel dev sessions start.
- Used the new cleanup path to remove the leftover experimental sandboxes from this machine, including prior flair/busy-elsewhere/smoke scratch runs.
- Fixed a startup UX bug in the TUI: when the initial `loadProjectsCmd` fails, the app no longer sits on `Loading projects...`; it now sets an explicit failure status (`Project load failed`, or `Project refresh failed` on later reloads) while still surfacing the underlying error text in the top status line.
- Added focused TUI regressions for both initial project-load failure and later project-refresh failure so these cases keep reporting a real failure state instead of looking hung.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/app_test.go` passed.
- `make tui-parallel-clean` passed and removed stale `/tmp/lcroom-parallel-*` sandboxes not used by active runtimes.
- `go test ./internal/tui -run 'Test(ProjectsMsgClearsInitialLoadingStatusAfterStartupLoad|ProjectsMsgShowsStartupFailureStatusWhenInitialLoadFails|ProjectsMsgShowsRefreshFailureStatusWhenReloadFails)' -count=1` passed.
- `timeout 5s make tui` reached the normal long-lived TUI launch path on the default DB and only exited because the timeout terminated it, confirming the old lock conflict was gone.
- `make test` passed.
- `make scan` passed at `2026-03-22T08:25:51+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 13`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-22T08:25:50+09:00` (`projects: 133`).

Next concrete tasks:

- Consider whether `make tui-parallel-clean` should grow an explicit "stop active parallel sandboxes too" mode for heavier dev cleanup, instead of only pruning stale directories.
- If startup can still feel frozen in real use, add a small retry/help hint in the top status line when `projectsMsg` or the first detail load fails so the recovery action is obvious without reading the full error text.

## Latest Update (2026-03-22 08:10 JST)

- Investigated why LittleControlRoom could still show the previous latest-session assessment after the repo worktree changed, even though commit-message inference for another project proved the session/LLM path itself was healthy.
- Found a stale-snapshot reuse bug in the scan path: when the latest session ID/timestamps/turn state matched the stored session, we reused the prior `LatestSessionSnapshotHash` without checking whether git status had changed, so a clean-to-dirty repo transition could keep the old assessment snapshot hash alive.
- Tightened latest-session snapshot-hash reuse so it now only reuses the old hash when the stored repo dirty/sync/ahead/behind state still matches the current git snapshot.
- Updated `RefreshProjectStatus` to read current git status before recomputing the latest session snapshot hash, so manual/status refreshes do not derive assessment freshness from stale stored repo state.
- Added a focused service regression covering the exact stale-assessment scenario: same latest session metadata, but repo becomes dirty, and the latest session snapshot hash must refresh instead of being reused.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/service/service.go internal/service/service_test.go` passed.
- `go test ./internal/service -run 'Test(ScanOnceRecomputesLatestSessionSnapshotHashWhenGitStatusChanges|ScanOnceReusesLatestOpenCodeSnapshotHashWhenSessionIsUnchanged|ScanOnceRecomputesLatestSessionSnapshotHashWhenTurnStateChanges)' -count=1` passed.
- `make test` passed.
- `make scan` was attempted against the shared live DB but did not complete in this session, so there is no fresh successful scan snapshot from this turn.
- `make doctor` passed on the cached snapshot dated `2026-03-22T08:10:14+09:00` (`projects: 133`).

Next concrete tasks:

- Re-run `make scan` against the intended Little Control Room DB and confirm the project now drops the stale assessment as soon as the worktree dirties, then requeues/replaces it with a fresh assessment.
- Consider adding a higher-level service/store regression that seeds an old completed classification and verifies the visible assessment becomes unknown/refreshing immediately when only git status changes.

## Latest Update (2026-03-22 07:54 JST)

- Investigated the report that running `/opencode` from the project list can open the embedded pane and then immediately drop back to the dashboard, which made provider-switch failures look like Little Control Room could not switch away from Codex.
- Kept failed embedded opens visible by storing a closed provider-specific placeholder snapshot when an OpenCode or Codex launch fails before a real embedded session is registered, instead of clearing the pane outright.
- Taught the embedded pane to fall back to cached closed snapshots when no live session object exists yet, so failed-open placeholders and closed-session views remain renderable and restorable.
- Added a focused TUI regression covering the project-list `/opencode` failure case to ensure the pane now stays open, shows `OpenCode session closed.`, and surfaces the startup error text instead of disappearing.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/codex_pane.go internal/tui/app.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -run 'Test(LaunchOpenCodeForSelectionFailureKeepsErrorPlaceholderVisible|VisibleOpenCodeSlashNewFailureKeepsClosedSessionVisible)' -count=1` passed.
- `go test ./internal/tui -run 'Test(LaunchOpenCodeForSelectionFailureKeepsErrorPlaceholderVisible|LaunchCodexForSelectionShowsOpeningStateInsteadOfPreviousSession|VisibleOpenCodeSlashNewStartsFreshSession|VisibleOpenCodeSlashNewFailureKeepsClosedSessionVisible)' -count=1` passed.
- `opencode --version` returned `1.2.24`.
- `timeout 8s opencode serve --hostname 127.0.0.1 --port 0 --print-logs` reached `opencode server listening on http://127.0.0.1:4096` before the timeout, confirming local OpenCode startup still works in this environment.
- `make test` passed.
- `make scan` was attempted on the shared default DB but did not finish while another long-lived `lcroom tui` process was already using that DB, so there is no fresh successful scan snapshot from this turn.
- `make doctor` passed on the cached snapshot dated `2026-03-22T07:51:28+09:00` (`projects: 133`).

Next concrete tasks:

- Live-check the exact `/opencode` flow on the reported `oykgames.com` project now that startup failures stay visible, and capture the real OpenCode error text if that project still cannot switch.
- Decide whether failed embedded opens should stay visible only until the next explicit hide/reopen, or whether the closed placeholder should also be included in the embedded session picker/history list.

## Latest Update (2026-03-21 20:29 JST)

- Fixed the embedded session meta/footer display after the `/model` persistence work: a fresh `/new` session that only has startup/system entries now shows the carried-forward model as the current effective model instead of rendering it as `Next`.
- Kept the `Next` model badge for real in-session follow-up overrides by only promoting the pending model to current display when the session is still effectively fresh and has no user/agent/tool/command activity yet.
- Added a focused TUI regression covering the fresh-session case alongside the existing pending-override footer test.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/codex_pane.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -run 'Test(RenderCodexSessionMetaShowsModelReasoningContextAndPending|RenderCodexSessionMetaTreatsFreshPendingModelAsCurrent)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-21T20:29:12+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-21T20:29:12+09:00` (`projects: 133`).

Next concrete tasks:

- Live-check the embedded pane with both Codex and OpenCode after `/model` plus `/new` to confirm the fresh-session promotion feels right once the first real prompt is sent and the provider echoes back the actual model.
- If the same confusion appears elsewhere, consider whether the detailed `/status` card should also label launch-seeded model preferences as the current effective model for fresh sessions instead of listing them as a separate staged override.

## Latest Update (2026-03-21 20:22 JST)

- Fixed embedded `/model` so a model/reasoning choice now carries forward to future embedded sessions of the same provider instead of only staging the currently open session.
- Added a small TUI-side embedded model preference store keyed by provider, taught embedded session launches to inject those preferences into new or resumed Codex/OpenCode sessions, and reused the same launch-time preference when a live session is reopened through the manager.
- Unified the pending-model normalization logic in `internal/codexapp` so launch-time model preferences and live-session `/model` changes follow the same "stage only when different from current" behavior.
- Added focused regressions across the TUI and embedded session manager for persisted per-provider model preferences, manager reuse with pending model overrides, and the normalized staging helper.
- Updated the README, slash-command copy, and reference docs so `/model` now explicitly describes the new future-session behavior.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/types.go internal/codexapp/session.go internal/codexapp/opencode_session.go internal/codexapp/session_test.go internal/codexapp/types_test.go internal/tui/app.go internal/tui/codex_model_picker.go internal/tui/codex_pane.go internal/tui/app_test.go internal/tui/embedded_model_preferences.go internal/codexslash/commands.go` passed.
- `go test ./internal/codexapp ./internal/tui ./internal/codexslash -run 'Test(StagedModelOverride|ManagerOpenReusesExistingSessionAppliesPendingModelOverride|EmbeddedModelPreferencePersistsAcrossFutureSessionsPerProvider|VisibleCodexSlashResumeIDOpensRequestedSession|StageModelOverrideUpdatesSnapshot|SuggestionsIncludeModelCommand|ParseModelCommand)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-21T20:22:30+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-21T20:22:40+09:00` (`projects: 133`).

Next concrete tasks:

- Live-verify the embedded Codex and OpenCode `/model` flow in the TUI by setting a non-default model, starting `/new`, and confirming the next fresh session shows the staged choice before the first prompt.
- Decide whether these per-provider embedded model preferences should stay in-memory for the current Little Control Room run or be promoted to durable config/state across app restarts as a separate follow-up.

## Latest Update (2026-03-21 16:36 JST)

- Simplified the README `Everyday Workflow` section so it stays centered on the basic five-step flow instead of expanding into a long sequence of one-off command explanations.
- Replaced the long follow-up prose with five compact emoji-backed buckets for the most common actions: runtimes, agent sessions, list cleanup, review/organizing, and setup.
- Kept the detailed command behavior in `docs/reference.md` instead of repeating that level of detail in the README.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `make test` passed.
- `make scan` passed at `2026-03-21T16:35:57+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-21T16:35:58+09:00` (`projects: 133`).

Next concrete tasks:

- Decide whether the README should keep this lighter emoji-backed tone elsewhere, or reserve it for the onboarding-style workflow section only.
- If the README still feels command-heavy, consider trimming the top-level command inventory next and leaning more on `docs/reference.md` for exhaustive command details.

## Latest Update (2026-03-21 06:43 JST)

- Tightened the embedded Codex/OpenCode transcript layout for tool calls so `TranscriptTool` entries no longer render as a two-line dense block with a separate `Tool` header.
- Tool entries now reuse the tool accent color as a single compact transcript row, and any internal line breaks inside a tool entry are collapsed into one visible summary line with ` | ` separators.
- Consecutive tool entries now render back-to-back with a single newline instead of a blank spacer line, so a burst of tool activity stays dense and scannable while command/file-change blocks keep their existing spacing.
- Added focused TUI regressions covering both behaviors: single-line tool-call rendering and dense spacing for consecutive tool entries.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/codex_pane.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -run 'Test(RenderCodexTranscriptEntryCompactsToolCallsToSingleLine|RenderCodexTranscriptEntriesKeepsConsecutiveToolCallsDense|VisibleCodexViewUsesStructuredTranscriptEntries|RenderCodexTranscriptEntriesWrapLongAgentMessagesWithoutSenderLabels)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-21T06:42:05+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-21T06:42:05+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui` hit the expected active-instance guard on the shared DB, so the UI smoke check used `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-tool-line-smoke INTERVAL=1h`, which reached the TUI sandbox and exited via `q`.

Next concrete tasks:

- Open a live embedded Codex or OpenCode turn that emits several back-to-back tool calls and sanity-check whether the new compact styling feels readable at normal dashboard widths, especially when tool summaries wrap.
- If users like this denser treatment, consider whether other low-signal transcript items such as short file-change notices should get a similar compact-row mode, without making commands or richer status cards harder to scan.

## Latest Update (2026-03-20 17:41 JST)

- Investigated the bad `quickgame_28` dashboard summary that still said commit/push were pending even though the OpenCode turn had already finished them.
- Confirmed this was not stale OpenCode session ingestion: the live `~/.local/share/opencode/opencode.db` rows for `ses_2f643d060ffek6vafOcwIixZ4E` already contained the final assistant text saying the version bump was committed and pushed to `origin/master`, and the project git snapshot in LCR was already clean + synced.
- Confirmed prompt-only classifier guidance was not sufficient. After a `session-v2` reclassification run, the live row still came back `needs_follow_up`, which narrowed the issue to snapshot shaping rather than missing raw transcript data.
- Fixed OpenCode session snapshot extraction so consecutive assistant-only planning/tool messages collapse to the last user-visible assistant reply when one exists, and assistant messages with visible text now prefer that visible reply over same-message reasoning/tool scaffolding for classification purposes.
- Added regressions covering both cases: preserving structured OpenCode parts when no visible assistant reply exists, and preferring the final visible assistant completion text over prior planning/tool chatter.
- Validated the fix in an isolated one-project temp DB for `quickgame_28`: the same session now classifies as `completed` with summary `Bumped to 0.3.7, committed, and pushed the ghost-facing fix.`
- Updated the live cached classification row for `quickgame_28` to the corrected derived `session-v2` result from that isolated run, so the dashboard now reflects the fixed summary immediately.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- Direct SQLite inspection of `~/.local/share/opencode/opencode.db` showed the final assistant message for `ses_2f643d060ffek6vafOcwIixZ4E` already said the change was committed and pushed, while `projects.repo_dirty = 0` and `repo_sync_status = synced` in the LCR store.
- `go test ./internal/sessionclassify -count=1` passed after the prompt update and again after the extractor/run-collapse fix.
- `go run ./cmd/lcroom snapshot --config "/Users/davide/.little-control-room/config.toml" --codex-home "/Users/davide/.codex" --opencode-home "/Users/davide/.local/share/opencode" --db /tmp/lcroom-quickgame28-refresh.sqlite --include-paths "/Users/davide/dev/poncle_repos/quickgame_28" --project "/Users/davide/dev/poncle_repos/quickgame_28" --session-id ses_2f643d060ffek6vafOcwIixZ4E --limit 1` showed the cleaned classification snapshot centered on the visible completion reply instead of the earlier planning-only assistant chatter.
- `go run ./cmd/lcroom classify --config "/Users/davide/.little-control-room/config.toml" --codex-home "/Users/davide/.codex" --opencode-home "/Users/davide/.local/share/opencode" --db /tmp/lcroom-quickgame28-refresh.sqlite --include-paths "/Users/davide/dev/poncle_repos/quickgame_28"` passed and classified the session as `completed`.
- `make test` passed.
- `make scan` passed at `2026-03-20T17:40:49+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 3`, `queued classifications: 77`).
- `make doctor` passed on the cached snapshot dated `2026-03-20T17:40:49+09:00`; `quickgame_28` now shows `latest_session_assessment: status=completed category=completed` with summary `Bumped to 0.3.7, committed, and pushed the ghost-facing fix.`
- A direct `sqlite3 ~/.little-control-room/little-control-room.sqlite` check confirmed the live `session_classifications` row for `ses_2f643d060ffek6vafOcwIixZ4E` now stores the corrected `session-v2` completed summary and matching snapshot hash.

Next concrete tasks:

- Watch the next real OpenCode commit/push-style turn in the live dashboard and confirm the new snapshot shaping prevents the same false `needs_follow_up` outcome without any manual cache correction.
- If more misclassifications remain, consider whether the classifier snapshot should also down-rank or further compress assistant planning-only runs for non-OpenCode transcripts, while keeping in-progress sessions legible.

## Latest Update (2026-03-20 16:33 JST)

- Re-investigated the repeated embedded OpenCode stoppage on `quickgame_28` with a live `opencode serve` probe plus direct inspection of `~/.local/share/opencode/opencode.db`.
- Confirmed the stop was not caused by tool execution itself. The repeated pattern was `step-finish reason=tool-calls` followed immediately by a new assistant message whose `info.error` contained `APIError`, `statusCode: 403`, `providerID: openai`, and `metadata.url: https://api.openai.com/v1/responses`, with `parts: []`.
- Confirmed Little Control Room's embedded OpenCode renderer was dropping exactly those error-only assistant messages because it only rendered `parts` and ignored `info.error`, which is why the pane could misleadingly look like `Turn completed` right after tool activity.
- Fixed the embedded OpenCode session layer so error-only `info.error` messages now render as transcript errors, update the visible embedded status/notice fields, and replace the fake completion state after idle transitions by refreshing the latest session message state when OpenCode goes busy -> idle.
- Added a targeted OpenCode diagnosis for the observed OpenAI Responses `403` case that points users to `opencode auth list`, refreshing OpenCode OpenAI auth or API key if needed, and then `/reconnect` inside Little Control Room.
- Added focused regressions covering both the immediate `message.updated` error path and the real busy -> idle refresh path for an error-only assistant message.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- Live `opencode serve --hostname 127.0.0.1 --port 0 --print-logs` probe in `/Users/davide/dev/poncle_repos/quickgame_28` confirmed `/session/ses_2f643d060ffek6vafOcwIixZ4E/message` returns a final assistant message with `info.error.statusCode = 403`, `providerID = openai`, `metadata.url = https://api.openai.com/v1/responses`, and `parts = []`.
- `gofmt -w internal/codexapp/opencode_session.go internal/codexapp/opencode_session_test.go` passed.
- `go test ./internal/codexapp -run 'Test(NewOpenCodeHTTPClientHasNoGlobalTimeout|OpenCodeSessionStatusIdleMarksTurnCompleted|OpenCodeSessionIdleEventMarksTurnCompleted|OpenCodeSessionIdleAfterExternalBusyMarksSessionReady|OpenCodeSessionIdleRefreshesErrorOnlyMessageIntoTranscript|OpenCodeSessionMessageUpdatedShowsErrorImmediately)' -count=1` passed.
- `go test ./internal/codexapp ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-20T16:32:52+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-20T16:33:00+09:00` (`projects: 133`).

Next concrete tasks:

- Re-run the embedded OpenCode session on `quickgame_28` and confirm the next post-tool `403` now shows up explicitly as an OpenCode/OpenAI error instead of `Turn completed`.
- If the same `403` keeps happening after refreshing OpenCode auth and using `/reconnect`, investigate why this OpenCode environment is hitting OpenAI Responses authorization failures for `gpt-5.4` despite `opencode auth list` showing OpenAI auth configured.

## Latest Update (2026-03-20 16:04 JST)

- Investigated the follow-up auth report where embedded Codex showed `HTTP 403 Forbidden` even after `codex login` succeeded in another shell, and where `quickgame_28` OpenCode still appeared to stop unexpectedly while using Codex-backed access.
- Confirmed the likely restart boundary is the embedded provider helper process, not the whole Little Control Room app: embedded Codex uses a long-lived `codex app-server`, and embedded OpenCode uses a long-lived `opencode serve`, so refreshed external auth can leave the already-running embedded helper stale until that helper is bounced.
- Added a new embedded-only slash command, `/reconnect`, which closes the current embedded Codex/OpenCode helper for the visible project and immediately reopens it against the same provider and session when possible. This gives users an in-place recovery path after refreshing `codex login`, without requiring a full LCR restart.
- Wired `/reconnect` into the embedded slash parser, suggestion list, pane command handling, and user-facing docs, and updated the embedded closed-session guidance so it points to `/reconnect` alongside `/resume` and `/new`.
- Updated the embedded Codex `HTTP 403` diagnosis text so it now explicitly suggests `/reconnect` (or reopening the embedded session) after `codex login` / `codex logout` + `codex login`.
- This does not prove that every `quickgame_28` OpenCode stop was caused by stale Codex auth, but it gives the correct local recovery action for that class of failure. If OpenCode still stops after `/reconnect`, the remaining issue is likely in the OpenCode transport/server-exit path rather than auth-refresh visibility alone.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexslash/commands.go internal/codexslash/commands_test.go internal/tui/codex_pane.go internal/tui/codex_slash.go internal/tui/app_test.go internal/codexapp/session.go` passed.
- `go test ./internal/codexslash -run 'Test(SuggestionsIncludeModelCommand|SuggestionsIncludeReconnectCommand|ParseModelCommand|ParseReconnectCommand|ParseSessionAliasReturnsResumeInvocation|SuggestionsExposeSessionAliasWhenPrefixMatches)' -count=1` passed.
- `go test ./internal/tui -run 'Test(VisibleCodexSlashShowsSuggestions|VisibleCodexSlashTabCyclesSuggestions|VisibleOpenCodeSlashReconnectReopensSameSession|VisibleCodexSlashNewStartsFreshSession|VisibleOpenCodeSlashNewStartsFreshSession|VisibleOpenCodeSlashNewFailureKeepsClosedSessionVisible)' -count=1` passed.
- `go test ./internal/codexapp -run 'Test(ReadStderrAppendsAuth403Diagnosis|ReadStderrCompactsServiceUnavailable503Status|ReadStderrUsesGenericCompactStatusForUnknownStderr|AppendSystemErrorCompactsRateLimitedStatus|CompactCodexStatusLabel|Auth403DiagnosisIsOnlyAppendedOnce)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-20T16:04:36+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-20T16:04:44+09:00` (`projects: 133`).

Next concrete tasks:

- Live-verify the new `/reconnect` flow inside the embedded Codex and OpenCode panes right after a fresh `codex login`, especially on `quickgame_28`, and confirm that a stale-helper auth failure recovers without restarting all of LCR.
- If `quickgame_28` still stops after `/reconnect`, instrument or surface the OpenCode server-exit / event-stream failure path next so silent OpenCode transport failures become explicit in the transcript/footer instead of looking like ambiguous completions.

## Latest Update (2026-03-20 15:05 JST)

- Investigated the embedded OpenCode turn-completion parity gap after the report that OpenCode did not seem to show a clear `Turn completed` state the way embedded Codex does.
- Fixed the OpenCode event-stream idle transition logic so a real busy-to-idle transition now surfaces completion reliably regardless of whether OpenCode sends the state change via `session.status`, `session.idle`, or both in either order.
- Kept the state handling provider-appropriate instead of bolting on transcript heuristics: embedded OpenCode now mirrors Codex-style completion/ready notices in session status fields, while externally busy OpenCode sessions still resolve back to `OpenCode session ready` instead of being mislabeled as local turn completions.
- Added focused `internal/codexapp` regressions covering the OpenCode busy-to-idle completion path, the direct `session.idle` completion path, and the external-busy-to-ready path.
- This patch improves completion signaling parity, but it does not change the OpenCode transport/server-exit path. If `quickgame_28` was truly interrupted rather than merely going idle without a visible completion message, that still needs a separate transport-level investigation.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/opencode_session.go internal/codexapp/opencode_session_test.go` passed.
- `go test ./internal/codexapp -run 'Test(NewOpenCodeHTTPClientHasNoGlobalTimeout|OpenCodeSessionStatusIdleMarksTurnCompleted|OpenCodeSessionIdleEventMarksTurnCompleted|OpenCodeSessionIdleAfterExternalBusyMarksSessionReady)' -count=1` passed.
- `go test ./internal/codexapp ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-20T15:05:01+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-20T15:05:09+09:00` (`projects: 133`).

Next concrete tasks:

- Re-run a real embedded OpenCode prompt on `quickgame_28` and confirm the pane now lands on `Turn completed` reliably even when OpenCode emits idle-related events in a different order.
- If `quickgame_28` still appears to stop without a clear reason, instrument or log the OpenCode server-exit / event-stream failure path next so we can distinguish a real interruption from a previously silent busy-to-idle transition.

## Latest Update (2026-03-20 14:42 JST)

- Investigated the report that embedded OpenCode `/new` "doesn't seem to work" and live-probed the real `opencode serve` API against `quickgame_28`.
- Confirmed the underlying OpenCode transport is creating a genuinely fresh session on that project: a live `POST /session` returned a new session id (`ses_2f643d060ffek6vafOcwIixZ4E`) alongside the older saved `ses_3498ba387ffeXwqvnyCGdEiJ3a`, with the new session starting empty before the first prompt.
- Tightened the embedded fresh-session messaging so successful `/new` flows now explicitly name the new session id in both the pane status text and the provider-side "started new embedded session" transcript notice, which makes OpenCode fresh opens much easier to verify in practice.
- Added the missing successful embedded OpenCode `/new` regression in the TUI suite and updated the fresh-session status expectations for the existing force-new Codex retry coverage.
- Updated the documented OpenCode transport assumptions below to record the newly validated `POST /session` behavior.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- Live `opencode serve` probe in `/Users/davide/dev/poncle_repos/quickgame_28` confirmed `POST /session` created a fresh empty session `ses_2f643d060ffek6vafOcwIixZ4E` instead of reusing `ses_3498ba387ffeXwqvnyCGdEiJ3a`.
- `gofmt -w internal/codexapp/session.go internal/codexapp/opencode_session.go internal/tui/codex_pane.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -run 'Test(VisibleCodexSlashNewStartsFreshSession|VisibleOpenCodeSlashNewStartsFreshSession|VisibleOpenCodeSlashNewFailureKeepsClosedSessionVisible|LaunchCodexForSelectionForceNewRetriesWhenPreviousThreadReopensFirst|LaunchCodexForSelectionForceNewRetriesWhenCodexRejectsFreshThread)' -count=1` passed.
- `go test ./internal/tui ./internal/codexapp -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-20T14:41:50+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-20T14:41:50+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-opencode-new-status-smoke INTERVAL=1h` reached the updated TUI sandbox and exited via `q`.

Next concrete tasks:

- Re-run the actual embedded OpenCode `/new` flow on `quickgame_28` inside the updated TUI and confirm the new session-id confirmation removes the ambiguity that prompted this report.
- If users still report that `/new` feels unreliable after the clearer status copy, the next pass should refresh more project/session metadata immediately after a successful OpenCode fresh open so the detail pane and saved-session list visibly flip to the new row without waiting for scan cadence.

## Latest Update (2026-03-20 14:24 JST)

- Extended the embedded Codex short-status compaction beyond the original `403` and `503` cases so the footer/status bar now stays brief whenever the full raw stderr or error text is already visible in the transcript.
- Added compact status labels for ChatGPT/Codex transport `429` rate limits, `5xx` backend failures (including `502`/`503`/`504`), request timeouts, and connection-failure patterns tied to `backend-api/codex/responses`.
- Added a generic fallback for otherwise-unrecognized `codex stderr:` lines, so noisy raw stderr no longer spills directly into the embedded status line even when the message does not match a known diagnosis pattern.
- Reused the same compact-label logic for both stderr notices and direct embedded Codex error returns, so the footer remains short whether the failure comes from websocket stderr chatter or an HTTP fallback error path.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/session.go internal/codexapp/session_test.go` passed.
- `go test ./internal/codexapp -run 'Test(ReadStderrAppendsAuth403Diagnosis|ReadStderrCompactsServiceUnavailable503Status|ReadStderrUsesGenericCompactStatusForUnknownStderr|AppendSystemErrorCompactsRateLimitedStatus|CompactCodexStatusLabel|Auth403DiagnosisIsOnlyAppendedOnce)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-20T14:23:44+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-20T14:23:55+09:00` (`projects: 133`).

Next concrete tasks:

- Watch the next real embedded Codex failure or recovery in the TUI and confirm the compact footer labels feel clear enough without hiding too much context now that the transcript keeps the raw stderr.
- If we want even less footer churn later, consider splitting snapshot status into an explicit short status field plus the richer long notice instead of continuing to infer that distinction from message content.

## Latest Update (2026-03-20 14:13 JST)

- Investigated an embedded Codex outage-shaped stderr report from `codex_api::endpoint::responses_websocket` and confirmed the underlying failure was an upstream `HTTP 503 Service Unavailable` from `wss://chatgpt.com/backend-api/codex/responses`, which points to temporary Codex/ChatGPT backend unavailability rather than a Little Control Room transport bug.
- Fixed the embedded-status regression where that raw stderr line could spill straight into the embedded footer/status bar: Little Control Room now keeps the raw stderr entry in the transcript and `LastSystemNotice`, but compacts the live status string to `Codex service unavailable (HTTP 503)` for this noisy backend-unavailable pattern.
- Generalized the compact-status hook so known high-noise stderr notices can preserve detailed transcript evidence without overwhelming the embedded footer; the earlier `403` auth/session short label still works through the same path.
- Added a focused `internal/codexapp` regression that covers the new `503 Service Unavailable` stderr shape and asserts that the raw stderr text is retained while the footer-facing status is shortened.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/session.go internal/codexapp/session_test.go` passed.
- `go test ./internal/codexapp -run 'Test(ReadStderrAppendsAuth403Diagnosis|ReadStderrCompactsServiceUnavailable503Status|Auth403DiagnosisIsOnlyAppendedOnce)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-20T14:13:18+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-20T14:13:26+09:00` (`projects: 133`).

Next concrete tasks:

- If more backend-outage Codex failures appear in practice, consider adding a one-shot explanatory system notice for `5xx` failures similar to the existing richer `403` diagnosis, while still keeping the footer copy short.
- Decide whether the session picker should also prefer the compact status label over the raw `LastSystemNotice` for known noisy backend/auth failures.

## Latest Update (2026-03-20 06:37 JST)

- Added a live managed-runtime attention boost in the TUI so projects with a currently running `/run` or `/start` session get extra attention weight even when there is no fresh Codex/OpenCode activity yet.
- Kept the stored `LastActivity` semantics intact instead of faking Codex/OpenCode activity from runtime launches: the runtime boost is applied as a TUI-layer overlay to effective attention score, sort order, and visible attention reasons.
- Rewired the attention-sorted project list and detail pane to use that live effective score, and appended a transparent `runtime_running` reason that shows the managed runtime duration (and port when known).
- Rebuilt the visible project ordering immediately after runtime start/stop actions so `/run`, `/start`, and `/stop` affect attention sort right away without waiting for the next scan.
- Added focused TUI regressions covering the new runtime attention score, attention-sort behavior, and detail-pane reason rendering.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/app_test.go internal/tui/runtime_attention.go` passed.
- `go test ./internal/tui -run 'Test(ProjectAttentionScoreAddsRunningRuntimeWeight|SortProjectsUsesRunningRuntimeAttentionBoost|RenderDetailContentShowsRuntimeAttentionReason|ProjectAttentionLabel|RuntimePaneShowsRuntimeOutputAndActions|QuitKeyStopsManagedRuntimes)' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-20T06:36:39+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 3`, `queued classifications: 3`).
- `make doctor` passed on the cached snapshot dated `2026-03-20T06:36:51+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui` hit the expected active-instance guard on the shared DB, so the UI smoke check used `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-runtime-attention-check INTERVAL=1h`, which reached the TUI sandbox and exited via `q`.

Next concrete tasks:

- Decide whether managed runtime state should eventually graduate from the TUI-only manager into a provider-neutral service/store layer so runtime-derived attention also shows up in `doctor`, screenshots, and any non-TUI consumers.
- Decide whether recently stopped runtimes should keep a short tapering attention bonus, or if “currently running only” is the right behavior after using this for a bit.
- If runtime-aware attention proves useful, consider adding an explicit runtime-derived status/reason badge in the list chrome so the score bump feels even more legible at a glance.

## Latest Update (2026-03-20 00:50 JST)

- Corrected the `/codex-new` fresh-open fix after a live `codex app-server` probe showed the previous guard was too aggressive: genuinely fresh threads can reject `thread/read includeTurns=true` with “not materialized yet; includeTurns is unavailable before first user message”.
- Updated the fresh-thread check so that specific pre-first-message `thread/read` failure is treated as evidence of a healthy new thread, while still rejecting forced-new opens that come back with retained history or an in-progress turn.
- Kept the bounded forced-new retry path in place, so Little Control Room still retries the real stale-reopen cases, but it no longer mistakes an unmaterialized fresh thread for a failed open and leave the old session visible.
- Added focused regression coverage for the newly observed live behavior by asserting that `ensureFreshThread` accepts the unmaterialized-thread error while still rejecting retained-history threads.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- Live `codex app-server` probe confirmed the previously missing behavior: after `thread/start`, a fresh unread thread can fail `thread/read includeTurns=true` with `thread ... is not materialized yet; includeTurns is unavailable before first user message`.
- `gofmt -w internal/codexapp/session.go internal/codexapp/session_test.go` passed.
- `go test ./internal/codexapp ./internal/tui -run 'Test(EnsureFreshThread(RejectsRetainedHistory|AcceptsEmptyThread|AcceptsUnmaterializedFreshThread)|LaunchCodexForSelectionForceNew(RetriesWhenPreviousThreadReopensFirst|RetriesWhenCodexRejectsFreshThread|WarnsWhenActiveSessionIsReopenedReadOnly))' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-20T00:50:18+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 3`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-20T00:50:26+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-codex-new-unmaterialized-fix-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.

Next concrete tasks:

- Re-run the actual user-facing `/codex-new` flow against the real embedded Codex pane and confirm the “previous session stayed on screen” report is gone now that healthy fresh threads are no longer rejected.
- Keep looking for a provider-native fresh-thread signal or metadata field so this logic can stop inferring freshness from `thread/read` behavior.
- If the stale-reopen bug still appears after this correction, capture the wrong reopened thread id and whether the pane showed an open failure status, because that would point back to either the remaining same-thread retry path or a TUI visibility bug.

## Latest Update (2026-03-19 23:36 JST)

- Hardened the fresh embedded Codex open path for `/codex-new` and embedded `/new`: forced-new launches now verify the thread they got back is actually fresh, not just a different-looking open result.
- Added a Codex app-server freshness guard that reads the just-started thread before accepting it. If the forced-new launch already contains retained history or an in-progress turn, Little Control Room now treats that as “old session reopened” and retries instead of accepting it as the new session.
- Expanded the TUI retry path from a single retry to a bounded multi-attempt loop, and taught it to retry both the older “same known thread ID came back” case and the new app-server “fresh open reused an existing thread” signal.
- Added focused regressions for both layers: Codex app-session tests covering the retained-history check, plus TUI coverage for the retry-on-provider-signal flow alongside the earlier same-thread reopen path.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/session.go internal/codexapp/session_test.go internal/tui/codex_pane.go internal/tui/app_test.go` passed.
- `go test ./internal/codexapp ./internal/tui -run 'Test(EnsureFreshThread(RejectsRetainedHistory|AcceptsEmptyThread)|LaunchCodexForSelectionForceNew(RetriesWhenPreviousThreadReopensFirst|RetriesWhenCodexRejectsFreshThread|WarnsWhenActiveSessionIsReopenedReadOnly))' -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-19T23:36:12+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T23:36:23+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-codex-new-fresh-open-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.

Next concrete tasks:

- Run a live repro against the real embedded Codex `/codex-new` flow the next time the stale-open glitch appears, to confirm the retained-history guard fully removes the user-visible fallback in practice.
- Decide whether the bounded retry count for forced-new Codex opens should stay at `3` or become a configurable/internal constant if more field data suggests a different sweet spot.
- If Codex eventually exposes an explicit “fresh thread created” signal, replace the retained-history inference with that provider-native guarantee.

## Latest Update (2026-03-19 22:57 JST)

- Added a transient project-name filter for the main TUI. You can now press `f` from the dashboard to open a live filter dialog, use `/filter <text>` to apply the same temporary narrowing from the command palette, and use `/filter clear` to remove it.
- Wired the temporary filter through the project-list rebuild path so it composes with the existing AI/all visibility toggle and saved exclude-name patterns. Matching is case-insensitive and checks both the tracked project name and the folder basename.
- Made the active filter visible in the list chrome instead of feeling like hidden state: the list header and footer now show the current filter, and the empty-state copy explains when no rows match the active filter.
- Added focused command/TUI coverage for the new parser, keybinding, dialog behavior, render state, and list filtering, and updated the README plus `docs/reference.md` so the new command and `f` shortcut are documented.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/commands/commands.go internal/commands/commands_test.go internal/tui/app.go internal/tui/project_filter.go internal/tui/project_filter_test.go internal/tui/app_test.go` passed.
- `go test ./internal/commands ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-19T22:57:06+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 2`, `queued classifications: 2`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T22:57:06+09:00` (`projects: 133`).
- `make tui` hit the expected active-instance guard on the shared DB, so the interactive UI check used `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-project-filter-check INTERVAL=1h`, which reached the TUI sandbox and exited via `q`.

Next concrete tasks:

- Decide whether the temporary filter should stay a strict substring match or grow into a looser acronym/fuzzy matcher later.
- Consider adding matched-substring highlighting inside the project column so the narrowed rows are even easier to visually scan.
- Decide whether we want an always-visible one-keystroke clear affordance for the active filter beyond reopening `f` or using `/filter clear`.

## Latest Update (2026-03-19 20:02 JST)

- Tightened the README cost/provider copy so it matches the current backend behavior: Codex/OpenCode usage is described as using the local login path, the footer estimate is now documented as API-key-only, and the rough `$1` to `$2` full-day estimate for a few active projects is back in place with the OpenAI dashboard called out as the billing source of truth.
- Cleaned up adjacent README setup/command drift that had become misleading after the recent backend work: Quick Start no longer claims an OpenAI API key is required at startup, `/setup` is documented as the first-run backend picker, and the removed `/finish` command is no longer listed.
- Synced `docs/reference.md` with the same command-surface reality by adding `/setup` and removing the stale `/finish` examples.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `make test` passed.
- `make scan` passed at `2026-03-19T20:01:48+09:00` (`activity projects: 88`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T20:01:48+09:00` (`projects: 133`).

Next concrete tasks:

- Keep the public docs in sync with the backend/setup work as the provider-selection UX settles, especially if we later add an OpenCode persistent-helper path or more `/setup` actions.
- Consider whether the README should also mention that local Codex/OpenCode backends show activity labels in the footer rather than dollar amounts, if we want to make that distinction even more explicit for first-time users.

## Latest Update (2026-03-19 19:20 JST)

- Fixed the helper-workspace leak that was polluting the project list with `lcroom-codex-helper-*` rows. Codex/OpenCode helper and local-runner workspaces now live under a dedicated Little Control Room internal workspace root instead of generic temp directories, and the scanner/service path filters now treat both that managed root and the older legacy `lcroom-*` temp prefixes as always-internal.
- Added best-effort startup hygiene for internal workspaces: stale directories under the managed internal root are cleaned up on service startup, and any already-leaked helper/index rows in the project store are marked `forgotten` and `out of scope` so they stop surfacing in the current project list.
- Added belt-and-suspenders filtering in `PathScope`, git repo discovery, and the scan merge path itself so even a detector that ignores scope does not get to reintroduce internal helper paths as visible projects.
- Added focused tests for internal-path detection/cleanup, scanner exclusion, and service-level hiding of leaked helper projects.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/appfs/workspaces.go internal/appfs/workspaces_test.go internal/codexapp/helper.go internal/llm/local_cli.go internal/llm/codex_helper.go internal/gitops/message.go internal/sessionclassify/client.go internal/scanner/scope.go internal/scanner/discovery.go internal/scanner/discovery_test.go internal/scanner/scope_test.go internal/service/service.go internal/service/service_test.go` passed.
- `go test ./internal/appfs ./internal/scanner ./internal/service ./internal/llm ./internal/gitops ./internal/sessionclassify -count=1` passed.
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-internal-workspace-filter-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.
- `make test` passed.
- `make scan` passed at `2026-03-19T19:20:08+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T19:20:08+09:00` (`projects: 133`).
- Live DB verification after the scan: `SELECT COUNT(*) ... WHERE forgotten = 0 AND in_scope = 1 AND name LIKE 'lcroom-%'` returned `0`, and the leaked legacy helper rows are now stored as `forgotten=1, in_scope=0`.

Next concrete tasks:

- Decide whether we want a lightweight maintenance command to permanently prune already-forgotten internal helper rows from the DB, or if keeping them hidden as historical artifacts is good enough.
- Add a small developer-facing smoke/debug command for the persistent helper and classifier so live backend checks do not require env-gated Go tests.
- Revisit OpenCode later if we want the same persistent-helper treatment there beyond the current shared internal-workspace root.

## Latest Update (2026-03-19 18:47 JST)

- Hardened the session-classification path after a follow-up live check: the classifier itself was healthy again after the helper schema fix, but it could still silently accept malformed JSON with missing required fields and mark the run `completed`, which is exactly how a “gray refresh happened but no summary showed up” state can occur.
- Added strict classifier result validation in the Codex/OpenCode/API client path: decoded results must now include a valid category, a non-empty summary, and an in-range confidence, otherwise the attempt is treated as retryable instead of being accepted as a bogus completed assessment.
- Added store-side recovery for stale completed classifications with blank summaries. If an older same-snapshot classification was marked `completed` without a summary, the next queue pass now re-enqueues it instead of treating it as permanently done, so past malformed completions can heal once the backend starts returning valid structured output again.
- Added focused regression coverage for both pieces plus an env-gated live classifier probe, and confirmed the live Codex classifier now returns a real category + summary for the current helper path.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/sessionclassify/client.go internal/sessionclassify/client_test.go internal/store/store.go internal/store/store_test.go internal/sessionclassify/client_live_test.go` passed.
- `go test ./internal/sessionclassify ./internal/store -count=1` passed.
- `LCROOM_RUN_LIVE_CODEX_HELPER_TEST=1 go test ./internal/sessionclassify -run TestCodexClassifierClientLive -count=1 -v` passed and returned a real non-empty classification summary.
- `make test` passed.
- `make scan` passed at `2026-03-19T18:46:53+09:00` (`activity projects: 99`, `tracked projects: 150`, `updated projects: 3`, `queued classifications: 3`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T18:46:53+09:00` (`projects: 144`).

Next concrete tasks:

- Add a small developer-facing smoke/debug command for the persistent helper and classifier so live backend checks do not require env-gated Go tests.
- If helper resource use becomes noticeable, consider sharing one warm Codex helper across commit help and classification instead of the current per-client helper reuse.
- Revisit OpenCode later if we want the same persistent-helper treatment there; for now it still uses cached `opencode run`.

## Latest Update (2026-03-19 18:40 JST)

- Fixed a live Codex commit-preview regression in the new persistent helper path: the warm `codex app-server` runner was reusing the old “schema enforced elsewhere” prompt shape, but `app-server` does not expose the same hard `--output-schema` contract as one-shot `codex exec`, so Codex could return valid-looking JSON with the wrong field names (for example `{"subject": ...}` instead of the requested `{"message": ...}`) and the commit dialog fell back.
- Updated the persistent Codex runner to embed the full JSON schema in the helper prompt instead of relying on implicit enforcement, which brings the helper path back in line with the existing OpenCode/local prompt strategy and fixes both commit-message generation and any other helper-backed structured tasks that depend on exact field names.
- Added regression coverage that checks the persistent Codex runner now includes the schema text in the prompt, plus env-gated live Codex helper probes for both the raw helper and the real commit-message client so future helper changes can be exercised against an actual logged-in local Codex install when needed.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/llm/codex_helper.go internal/llm/codex_helper_test.go internal/codexapp/helper_live_test.go internal/gitops/message_live_test.go` passed.
- `go test ./internal/llm ./internal/gitops -count=1` passed.
- `go test ./internal/codexapp ./internal/llm ./internal/gitops ./internal/sessionclassify -count=1` passed.
- `LCROOM_RUN_LIVE_CODEX_HELPER_TEST=1 go test ./internal/codexapp -run TestPromptHelperLive -count=1 -v` passed (`{"ok":true}`).
- `LCROOM_RUN_LIVE_CODEX_HELPER_TEST=1 go test ./internal/gitops -run TestCodexCommitMessageClientLive -count=1 -v` passed and returned a real non-empty commit message after the fix.
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-commit-helper-schema-fix INTERVAL=1h` reached the TUI sandbox and exited via `q`.
- `make test` passed.
- `make scan` passed at `2026-03-19T18:40:02+09:00` (`activity projects: 94`, `tracked projects: 145`, `updated projects: 4`, `queued classifications: 4`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T18:40:03+09:00` (`projects: 138`).

Next concrete tasks:

- Add a small developer-facing smoke/debug command for the persistent helper so live backend checks do not require running env-gated Go tests manually.
- If helper resource use becomes noticeable, consider sharing one warm Codex helper across commit help and classification instead of the current per-client helper reuse.
- Revisit OpenCode later if we want the same persistent-helper treatment there; for now it still uses cached `opencode run`.

## Latest Update (2026-03-19 18:29 JST)

- Swapped the Codex local backend from one-shot `codex exec` calls to a warm persistent helper built on `codex app-server`: Codex-backed commit-message generation and session classification now reuse a hidden app-server process, start a fresh thread for each request to avoid cross-request context bleed, and recycle helpers after idle time or repeated use.
- Kept OpenCode on the lighter cached `opencode run` path for now; the new persistent-helper path is Codex-only in this pass because Codex already had a mature in-repo transport we could safely reuse.
- Removed the ambiguous `/finish` command from the public command surface. `/commit` remains the single commit workflow entry point, with `Alt+Enter` still handling commit-and-push when the repo can push.
- Added lifecycle tests for the persistent runner (helper reuse, rotation after a request cap, discard-on-error) plus the earlier local-runner caching tests, so the new helper behavior is covered without depending on live Codex auth during unit tests.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/codexapp/helper.go internal/llm/codex_helper.go internal/llm/codex_helper_test.go internal/sessionclassify/client.go internal/gitops/message.go` passed.
- `go test ./internal/codexapp ./internal/llm ./internal/sessionclassify ./internal/gitops -count=1` passed.
- `go test ./internal/tui -count=1` passed.
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-persistent-helper-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.
- `make test` passed.
- `make scan` passed at `2026-03-19T18:28:38+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T18:28:46+09:00` (`projects: 133`).
- Note: this pass did not include a dedicated live Codex-auth smoke request through the new helper; verification is code-level and suite-level rather than an explicit real-account end-to-end helper probe.

Next concrete tasks:

- If Codex helper resource use becomes noticeable, consider sharing a single persistent helper between commit help and classification instead of today’s separate per-client helpers.
- Add an explicit live smoke test path or small debug command for the persistent helper so future transport changes can be exercised against a real logged-in Codex install without manual TUI work.
- Revisit OpenCode later if we want the same persistent-helper treatment there; for now it still relies on cached `opencode run` calls.

## Latest Update (2026-03-19 18:05 JST)

- Removed the ambiguous `/finish` slash command from the public TUI command surface. `/commit` remains the single commit workflow entry point, with `Alt+Enter` still handling commit-and-push when the repo can push.
- Added a low-risk local-backend speed win inside the Codex/OpenCode JSON-schema runners: identical local requests are now memoized per runner, so repeated commit-help or classification prompts with the same exact payload can return from cache instead of spawning another `codex exec` / `opencode run` process.
- Kept the earlier immediate-open commit modal UX intact, so slower local backends now at least show progress immediately while repeated identical requests can skip the extra helper round-trip.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/commands/commands.go internal/commands/commands_test.go internal/tui/app.go internal/tui/app_test.go internal/llm/local_cli.go internal/llm/local_cli_test.go` passed.
- `go test ./internal/commands ./internal/llm ./internal/tui -count=1` passed.
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-commit-cache-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.
- `make test` passed on rerun. The first attempt hit the existing flaky `TestRenderFooterPulsesWhenUsageIncreases`; the immediate rerun passed cleanly.
- `make scan` passed at `2026-03-19T18:04:56+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T18:05:04+09:00` (`projects: 133`).

Next concrete tasks:

- If local Codex still feels too slow after the new cache hits, the next bigger step is a real persistent helper/session path instead of one-shot `codex exec` processes.
- Consider whether repeated commit previews should also cache more of the non-AI repo analysis path, not just the local runner response, when the repo state hash is unchanged.
- Add a short README/help note for `/setup` and local-backend behavior once the backend-selection UX settles.

## Latest Update (2026-03-19 17:14 JST)

- Tightened the `/commit` and `/finish` UX for slower local AI backends: the commit-preview modal now opens immediately with a placeholder shell, keeps the supplied message visible when one is provided, shows `Generating ...` / `Inspecting repo changes...` while the async preview is still loading, and continues to block commit/apply actions until the preview finishes refreshing.
- Added focused TUI regressions covering the new immediate-open loading shell, the placeholder rendering, and the existing “Enter stays blocked while loading” behavior, so the modal no longer feels frozen while Codex is generating the preview.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-commit-loading-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.
- `make test` passed.
- `make scan` passed at `2026-03-19T17:14:14+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T17:14:24+09:00` (`projects: 133`).

Next concrete tasks:

- If Codex still feels too slow after the immediate-open modal, consider a second pass on backend latency itself, most likely by reusing a persistent local helper/session instead of spawning a fresh `codex exec` process for every summary or commit preview.
- Decide whether the loading modal should support `Esc` cancellation during generation, which would need a stale-response guard so a late async preview does not reopen a dialog the user already dismissed.
- Add a short README/help note for `/setup` and local-backend behavior once the backend-selection UX settles.

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
- Observed freshly started threads can reject `thread/read` with `includeTurns=true` before the first user message because the thread is not materialized yet, so forced-new freshness checks should treat that specific error as a healthy new-thread state rather than a retryable stale-session failure.
- Observed `thread/read` can still report `status.type = active` after the steerable turn is already gone, so embedded turn recovery should trust the presence or absence of an in-progress turn in `thread.turns[]` more than `status.type` alone when deciding whether a follow-up should steer or start a fresh turn.
- Observed Codex session rollouts can end a turn with `event_msg.payload.type == "turn_aborted"` (seen with `reason:"interrupted"`) without a later `task_complete`, so both artifact scanners and embedded-session lifecycle handling should treat `turn_aborted` as a terminal turn event rather than waiting for a separate completion marker.
- Observed interrupted turns can later read back through `thread/read` / `thread/resume` as idle with no active turn even when the JSONL tail ends in `turn_aborted`, so stale-busy recovery should prefer the live thread turn list over any missing `task_complete` marker.
- The installed schema also emits `thread/status/changed` plus streamed `plan`, `reasoning`, and `mcpToolCall` notifications, but it still does not expose a single authoritative "all visible output has settled" event, so embedded turn tracking should model `running`, `finishing`, and `reconciling` instead of a binary busy/idle flag.
- Embedded `codex app-server` stdout frames can exceed the prior 1 MiB scanner cap during tool-heavy turns (observed around MCP/browser screenshot activity), so the embedded transport must tolerate large JSON-RPC messages and treat stdout decode failures as fatal session breakage rather than a recoverable transcript-only warning.
- Embedded `codex app-server` sessions now launch in their own process group on Unix, and Little Control Room tears down that whole group on close, idle-timeout cleanup, and transport failure so long-lived child tool processes (for example `vite preview`) do not survive the embedded session.
- Observed ChatGPT-backed `403 Forbidden` failures on `backend-api/codex/responses` can reject both the websocket path and the later HTTP fallback even while `codex login status` still reports logged in, so Little Control Room should treat that pattern as an auth/account-side Codex failure rather than a websocket-only transport bug.

Current OpenCode transport assumption:

- The installed `opencode` CLI on this machine (observed `1.2.24`) exposes both `serve` and `acp`; `serve` publishes an HTTP/OpenAPI + SSE surface with session, message, status, event, permission, and question endpoints, and Little Control Room now uses that live surface for embedded OpenCode sessions.
- Observed `POST /session` on that `serve` surface creates a distinct new OpenCode session row immediately, before the first user prompt, so ambiguous embedded `/new` reports are more likely UI-confirmation or visibility issues than the server silently reusing the previous session.
- Observed `opencode.db` session parts are structured rather than text-only, including `text`, `reasoning`, `tool`, `patch`, `file`, `step-start`, and `step-finish`, so OpenCode transcript extraction and the embedded pane should preserve that structure instead of flattening it to plain text.
- Observed `prompt_async` behavior accepts a follow-up prompt while the session is still busy (returning `204` and later appending a second user/assistant turn), so the embedded pane can treat Enter as a steer/follow-up path much like embedded Codex.
- OpenCode `FilePartInput` accepts structured file parts and the current embedded implementation sends local image attachments as `data:` URLs; transport support is confirmed, but end-to-end image robustness should still be treated as provisional until more real image cases are exercised.

Current Claude Code transport assumption:

- The installed `claude` CLI on this machine (observed `2.1.87`) exposes a headless session surface via `--print` plus `--input-format stream-json` / `--output-format stream-json`, but current CLI help also requires `--verbose` whenever `--output-format=stream-json` is used with `--print`.
- Observed headless Claude runs emit structured `system:init`, `assistant`, `user` (tool results), `rate_limit_event`, and `result` JSON events with stable `session_id` values, which is sufficient for Little Control Room to drive per-turn embedded Claude requests and update transcript state without scraping terminal text.
- Observed headless Claude supports `--resume <session-id>` and persists those session ids into the normal Claude Code on-disk session artifacts, so Little Control Room can combine headless turn execution with the existing `~/.claude/projects/.../*.jsonl` transcript files and `~/.claude/sessions/*.json` active-PID detection.
- The locally installed CLI help confirms `--permission-mode` choices including `acceptEdits`, `dontAsk`, `plan`, and `bypassPermissions`; until Little Control Room wires Claude-specific approval callbacks, initial embedded support should treat permission handling as a provider-specific MVP compromise rather than claiming full Codex/OpenCode approval parity.
- The locally installed CLI help exposes accepted alias-style model names (for example `sonnet` and `opus`) and `--effort` values, but not a machine-readable model listing API. For now, the embedded Claude `/model` picker should use a small curated alias set (`sonnet`, `opus`, `haiku`) plus any current full model IDs already surfaced by the active session.

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

## Latest Update (2026-03-19 17:00 JST)

- Split local-backend activity from metered API spend in the footer: when `Codex` or `OpenCode` is the selected backend, the footer now shows non-dollar activity labels such as `Codex ready`, `Codex running`, or `Codex 1 call` instead of a `$...` estimate, so local commit-help/classification no longer implies extra direct LCR API cost.
- Fixed the local assessment model mismatch: the Codex/OpenCode classifier clients now default to `gpt-5.4-mini` instead of `gpt-5.4-nano`, because live repro confirmed `codex exec --model gpt-5.4-nano` fails under ChatGPT-backed Codex auth while `gpt-5.4-mini` succeeds. Added focused tests for the new local classifier default plus the local footer activity labels.
- Live verification note: a separate already-running TUI process on this machine was still using the previous classifier build during overlap testing, so existing in-flight classifications there can still show the old `gpt-5.4-nano` failure until that TUI/runtime is restarted. The newly built code path and direct `codex exec` repro are both good.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/app_test.go internal/sessionclassify/client.go internal/sessionclassify/client_test.go` passed.
- `go test ./internal/tui ./internal/sessionclassify -count=1` passed.
- `codex exec --skip-git-repo-check --ephemeral --json --model gpt-5.4-nano 'Return exactly {"ok":true}'` failed with `invalid_request_error` (`gpt-5.4-nano` unsupported for ChatGPT-backed Codex auth on this machine).
- `codex exec --skip-git-repo-check --ephemeral --json --model gpt-5.4-mini 'Return exactly {"ok":true}'` succeeded.
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-local-backend-ui-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.
- `make test` passed.
- `make scan` passed at `2026-03-19T16:59:02+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 2`, `queued classifications: 67`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T16:59:11+09:00` (`projects: 133`).

Next concrete tasks:

- Restart the currently running TUI/runtime on this machine so the live classifier worker picks up the new local `gpt-5.4-mini` default instead of continuing with the older in-memory `gpt-5.4-nano` client.
- Decide whether `/setup` should eventually launch `codex login` / `opencode auth login` directly, or keep today’s lighter “remind and refresh” flow.
- Add a short README/help note for the new `/setup` flow and local-backend footer behavior once the backend-selection UX settles.

## Latest Update (2026-03-19 16:50 JST)

- Refined the `/setup` chooser after another live UX pass: when the currently configured backend is unavailable but another backend is already ready, the setup cursor now defaults to that ready backend instead of sticking to the broken one. In the reported case, an unavailable OpenAI-key backend now opens `/setup` with `Codex` preselected when Codex is installed and logged in.
- Clarified the setup list itself by distinguishing `active` from `ready`: the current configured backend is labeled `active`, while already-available alternatives (for example logged-in Codex) get a brighter `ready` state so users can spot a working fallback without confusing it for the active choice.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/setup.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-setup-choice-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.
- `make test` passed.
- `make scan` passed at `2026-03-19T16:49:55+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T16:50:04+09:00` (`projects: 133`).

Next concrete tasks:

- Decide whether `/setup` should eventually launch `codex login` / `opencode auth login` directly, or keep today’s lighter “remind and refresh” flow.
- Consider making first-time API-key saves write `ai_backend = "openai_api"` explicitly even when the starting config was unconfigured, so the saved TOML becomes fully explicit after that first setup pass.
- Add a short README/help note for the new `/setup` flow once the backend-selection UX settles.

## Latest Update (2026-03-19 16:43 JST)

- Trimmed the persistent AI-backend warning chrome after another live pass: setup/backend refreshes no longer overwrite the main banner status with a long `Configured AI backend unavailable...` sentence, so the top line now stays short and lets the colored `AI unavailable (use /setup)` badge carry the warning by itself.
- Added a focused TUI regression that keeps the existing status text intact when an unavailable-backend snapshot arrives, preventing the banner from drifting back to the duplicated long-form wording.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-ai-warning-short-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.
- `make test` passed on rerun. The first attempt hit one transient failure in `TestRenderFooterPulsesWhenUsageIncreases`; the immediate rerun passed cleanly.
- `make scan` passed at `2026-03-19T16:42:42+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 0`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T16:42:50+09:00` (`projects: 133`).

Next concrete tasks:

- Decide whether `/setup` should eventually launch `codex login` / `opencode auth login` directly, or keep today’s lighter “remind and refresh” flow.
- Consider making first-time API-key saves write `ai_backend = "openai_api"` explicitly even when the starting config was unconfigured, so the saved TOML becomes fully explicit after that first setup pass.
- Add a short README/help note for the new `/setup` flow once the backend-selection UX settles.

## Latest Update (2026-03-19 16:39 JST)

- Tuned the new persistent AI-backend warning chrome after follow-up UX feedback: the top banner now shows a colored warning badge, the footer mirrors `AI setup` / `AI unavailable` with the same higher-contrast styling, and the copy stays generic (`AI unavailable (use /setup)`) instead of naming `OpenAI API key`.
- Added focused TUI regressions proving the unavailable-backend notice stays generic and renders as a styled badge, so the warning keeps standing out and does not drift back toward API-key-specific copy.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/app_test.go` passed.
- `go test ./internal/tui -count=1` passed.
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-ai-warning-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.
- `make test` passed.
- `make scan` passed at `2026-03-19T16:38:47+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T16:38:56+09:00` (`projects: 133`).

Next concrete tasks:

- Decide whether `/setup` should eventually launch `codex login` / `opencode auth login` directly, or keep today’s lighter “remind and refresh” flow.
- Consider making first-time API-key saves write `ai_backend = "openai_api"` explicitly even when the starting config was unconfigured, so the saved TOML becomes fully explicit after that first setup pass.
- Add a short README/help note for the new `/setup` flow once the backend-selection UX settles.

## Latest Update (2026-03-19 16:30 JST)

- Tightened the new setup/backends UX after live feedback: when the selected AI backend is unavailable, the warning now stays visible in the persistent TUI chrome instead of only flashing through transient status text, so removing an OpenAI key while `ai_backend = "openai_api"` now leaves a clear on-screen `AI unavailable` / `/setup` reminder while commit-help falls back.
- Added focused TUI regressions covering the new persistent unavailable-backend footer/header labels, keeping the no-backend-warning path from silently disappearing again.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/tui/app.go internal/tui/app_test.go` passed.
- `go test ./internal/tui ./internal/commands ./internal/config ./internal/llm -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-19T16:30:29+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 1`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T16:30:40+09:00` (`projects: 133`).

Next concrete tasks:

- Decide whether `/setup` should eventually launch `codex login` / `opencode auth login` directly, or keep today’s lighter “remind and refresh” flow.
- Consider making first-time API-key saves write `ai_backend = "openai_api"` explicitly even when the starting config was unconfigured, so the saved TOML becomes fully explicit after that first setup pass.
- Add a short README/help note for the new `/setup` flow once the backend-selection UX settles.

## Latest Update (2026-03-19 16:21 JST)

- Replaced the old hard OpenAI-key startup gate with a first-class AI backend setup flow: config now supports `ai_backend = "openai_api" | "codex" | "opencode" | "disabled"`, the TUI has a new `/setup` overlay with local backend checks, and first launch now auto-opens that setup overlay when no backend is configured instead of refusing to continue.
- Added local backend detection plus soft-gated readiness handling for OpenAI API keys, Codex login state, and OpenCode auth state, and rewired the service layer so session classification, commit-subject generation, and untracked-file review can use local `codex exec` or `opencode run` backends when those tools are installed and authenticated.
- Kept `/settings` focused on scope/API-key/threshold editing with `/setup` as the backend chooser, updated help/footer/CLI copy to point users at `/setup`, and added focused parser/tests around the new CLI-backed JSON-output path and setup/UI behavior.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `gofmt -w internal/cli/run.go internal/commands/commands.go internal/config/config.go internal/config/config_test.go internal/config/editable.go internal/config/ai_backend.go internal/aibackend/detect.go internal/gitops/message.go internal/service/service.go internal/sessionclassify/client.go internal/llm/local_cli.go internal/llm/local_cli_test.go internal/tui/app.go internal/tui/app_test.go internal/tui/settings.go internal/tui/setup.go` passed.
- `go test ./internal/config ./internal/commands ./internal/llm ./internal/gitops ./internal/sessionclassify ./internal/service ./internal/tui -count=1` passed.
- `make test` passed.
- `make scan` passed at `2026-03-19T16:20:21+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 2`, `queued classifications: 77`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T16:20:32+09:00` (`projects: 133`).
- `env COLUMNS=112 LINES=31 make tui-parallel PARALLEL_DATA_DIR=/tmp/lcroom-setup-ui-check INTERVAL=1h` reached the TUI sandbox and exited via `q`.
- `env COLUMNS=112 LINES=31 go run ./cmd/lcroom tui --config /tmp/lcroom-setup-unconfigured/config.toml --db /tmp/lcroom-setup-unconfigured/little-control-room.sqlite --codex-home "$HOME/.codex" --opencode-home "$HOME/.local/share/opencode" --interval 1h` reached the unconfigured first-run TUI and auto-opened the new Setup overlay; the process was then terminated after the overlay was verified.

Next concrete tasks:

- Decide whether `/setup` should eventually launch `codex login` / `opencode auth login` directly, or keep today’s lighter “remind and refresh” flow.
- Consider making first-time API-key saves write `ai_backend = "openai_api"` explicitly even when the starting config was unconfigured, so the saved TOML becomes fully explicit after that first setup pass.
- Add a short README/help note for the new `/setup` flow once the backend-selection UX settles.

## Latest Update (2026-03-19 15:40 JST)

- Simplified the README `Costs` section so it is shorter and more direct: it now clearly says Codex/OpenCode usage itself does not add extra LCR cost, that LCR's own OpenAI API traffic is mainly summaries/classification plus commit-help, and that the footer includes a live cost estimator.
- Adjusted that rough cost expectation to a safer range: with a few active projects, a full day of LCR usage is often around `$1` to `$2`, while still keeping the OpenAI dashboard as the billing source of truth.
- No Codex/OpenCode footprint assumptions changed, so `docs/codex_cli_footprint.md` stayed in sync without edits.

Verification snapshot:

- `make test` passed.
- `make scan` passed at `2026-03-19T15:40:43+09:00` (`activity projects: 87`, `tracked projects: 138`, `updated projects: 1`, `queued classifications: 77`).
- `make doctor` passed on the cached snapshot dated `2026-03-19T15:40:51+09:00` (`projects: 133`).

Next concrete tasks:

- Decide whether the same simplified cost wording should also replace the longer wording in any future docs/reference or onboarding surfaces so the estimate stays consistent across the project.
- If the footer estimator scope grows beyond summaries/classification and commit-help, refresh the README example so the `$1-$2/day with a few active projects` guidance stays honest.

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
