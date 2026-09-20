# Visible engineer delegation: continuation handoff

Prepared 2026-09-21. Read this, `STATUS.md`, repository `AGENTS.md`, then
`docs/engineer_delegation_plan.md`. This is branch-specific context, not a project
status update.

## User intent and working preferences

Build a practical, cheaper delegation loop: a capable caller engineer delegates
bounded coding work to a cheaper worker (potentially a different agent/provider),
then independently verifies, fixes or requests corrections, and accepts the work.
The user should not need to relay messages, read long worker explanations, or
answer repeated commit/continuation questions.

**Every worker must remain visible in the normal TUI task list. No hidden
subagents.** Preserve provider identity on leaving/reopening a task, including
LCAgent; reopening must not silently start a clean Codex session. Keep reports
short. Default to returning edits without committing unless the user requested a
commit. Model cost matters; do not use expensive inference for routine work.

The user's latest request was to create this handoff so another agent can continue
because the current provider's usage is running out. No agent was launched or
messaged by this handoff request. No paid provider smoke test has been run.

## Exact checkout and Git state

- Assigned workspace: `/Users/davide/dev/repos/LittleControlRoom--ai-agent-task-delegation`
- Branch: `spike/ai-agent-task-delegation`
- Canonical checkout: `/Users/davide/dev/repos/LittleControlRoom`, expected on `master`.
  Do not work there or rename its branch.
- HEAD at handoff: `adbe2033 Implement agent task handoff and result tracking`
  (59 files, 2,839 insertions, 65 deletions; includes ownership and structured results).
- Earlier commits:
  - `adc06e83 Persist and apply per-task engineer model selection`
  - `9270efa6 Preserve stable control identities for delegated tasks`
  - `03172774 docs: plan visible engineer delegation and caller review`
- Worktree was clean immediately before this document was created. The previous
  conversational report said implementation was uncommitted; that is now stale.
  Recheck Git state rather than replaying that assumption. This handoff document
  itself is left uncommitted.
- No deployment/restart of the user's main LCR runtime was performed in the last
  implementation turn. Committed source is not proof that the running app has it.

## What is implemented

### Stable identities and selectable visible workers

Task origins retain exact project/worktree, provider, control session key and
provider session ID. Late host binding resolves the return address; ambiguous
legacy identities do not silently route to another session. Model/provider/effort
selection persists across task continuation, with requested versus observed model
shown separately. Codex, Claude Code, OpenCode and LCAgent are supported.

### Exclusive managed repository ownership

`agent_task.create` supports opt-in `repository_write: true`. It requires an exact
affiliated checkout, a clean baseline and idle managed engineers/processes. It
records a durable lease and baseline HEAD/branch. Another managed writer cannot
enter while ownership is held. This conservatively blocks caller turns too;
transcript inspection is still available.

Native provider submissions, new-session launch/registration, Codex goal/review
entry points and managed process starts use admission guards. Ownership is
released only after idle checks, with bounded Git status/content fingerprint
recorded. Restart never silently steals a lease. Explicit close with `waiting`
can release a stopped worker without accepting its result. No automatic stash,
reset, commit or disposal of edits occurs.

This coordinates managed participants; it cannot sandbox external editors or
attribute every edit to a particular actor. Fingerprints are evidence, not proof
that the worker authored all changes.

### Structured results and caller review (opt-in)

`agent_task.create` supports `structured_results: true`. Legacy tasks keep their
existing free-text behavior. The confirmed opt-in authorizes only these bounded
metadata calls without another operator confirmation:

- `agent_task.submit_result`: exact worker submits current `run_id`, outcome
  (`ready_for_review`, `blocked`, `failed`), concise summary, optional criterion
  evidence, checks, base revision, files, risks and questions. One immutable result
  per run; identical retries are idempotent, conflicting replacements rejected.
- `agent_task.review_result`: exact originating caller, or an explicitly confirmed
  host operator, records `accept` or `changes_requested` for the current exact
  revision. Worker self-acceptance and unrelated callers are rejected. Acceptance
  requires a ready result and completed idle handoff, with ownership released.

Identity comes from trusted host context, never agent-supplied arguments. LCAgent
has a host-bound logical thread identity compatibility path. These two calls run
through the shared control executor immediately, return no queued operation ID,
and must not be polled through `get_control_operation`. They do not start a turn,
commit, push or delete anything. Worker/caller checks remain claims, distinct from
host repository evidence.

Run IDs increase when a new worker turn begins; active steering remains in the
current run. New runs invalidate pending older callbacks and reset current result
metadata while retaining immutable history. Main phases are `queued`, `working`,
`submitted`, `awaiting_review`, `blocked`, `failed`, `unclassified`,
`changes_requested`, `completed`, and `canceled`.

After submission the worker ends its turn. Host settlement waits for verified
idle, releases ownership, stores handoff evidence and queues a revision-tagged
callback to the exact original caller. Missing/closed callers are not reopened;
busy callers are not steered. Native submission rechecks cancellation/revision
under admission immediately before starting. Explicit worker/caller stop cancels
pending callbacks; late claims remain inspectable without automatic review.
Free-text-only endings are `unclassified`, not success. Generic task close cannot
bypass structured acceptance. Acceptance keeps the task visible rather than
trashing it.

Task rows/details show provider, run phase, evidence counts, review decision and
callback state. Routine structured handoffs avoid the old lengthy Chat notice
asking the operator what to do. Normal pending caller review is classified as
in-progress rather than waiting for the user.

`work.agent_task_get` returns current workflow; optional positive
`result_revision` returns historical `worker_claims`, `caller_review`, and
`host_repository_evidence`, after privacy checks. Lists expose compact workflow
metadata instead of all evidence.

## Next implementation slice

Read step 4 of `docs/engineer_delegation_plan.md`. The most important gap is a
**bounded correction loop**, including safe continuation over uncommitted edits.
`changes_requested` currently only records review; `agent_task.continue` still
requires confirmation. Every repository acquisition currently requires a clean
checkout, so ordinary corrections to a worker's dirty result are not yet supported.

Recommended order (design guidance, not an already implemented contract):

1. Add an explicit captured dirty-baseline ownership boundary for correction runs.
   Tie continuation to the exact task, reviewed result revision and repository
   handoff fingerprint. Check HEAD/branch/index/worktree/untracked state before
   reacquiring ownership. If intervening edits or unknown evidence prevent proving
   the intended boundary, fail visibly and preserve everything. Do not “solve” this
   by stashing, resetting or silently adopting arbitrary dirty files. Decide how
   caller fixes become an explicitly captured new baseline.
2. Add durable scoped supervision authorization: exact caller/worker, repository,
   model selection, allowed correction operations, correction-count limits,
   supported time/turn/token limits, stop/revocation behavior. Do not silently
   broaden authority or treat a callback prompt as authorization. Initial creation
   and corrections need a clear bounded grant before removing confirmations.
   Exact-caller acceptance already exists for opted-in tasks; preserve that path.
3. Connect caller review → bounded correction → next revision → independent
   acceptance. Keep workers visible, model choices stable and notices concise.
   Missing/stopped/replaced callers must never be revived by late results.
4. Add worker versus caller-review/correction usage accounting and totals. Use
   reported usage where available and label estimates/unknowns. Do not advertise
   a hard dollar cap if the provider cannot enforce it. Evaluate cost per accepted
   task before adding multiple writable workers or recursive delegation.

Acceptance scenario: one visible cheaper worker makes a plausible but incomplete
change; caller rejects it with concrete evidence; an authorized correction runs
on preserved edits; caller verifies the new revision and accepts. Old callbacks,
stop/restart and duplicate messages must not launch extra work or accept stale
results. Test superficially passing worker checks that a caller correctly rejects.
Use deterministic adapters first; any real-provider smoke should be deliberately
bounded and identify its model/usage, without expensive silent fallback.

## Code map and invariants worth preserving

- `internal/model/agent_task_result.go`: result/check/review/workflow/actor types.
  `internal/model/model.go`: task repository and model-selection state.
- `internal/control/agent_task_results.go`: strict result/review schemas and
  bounded validation. Registry/create flags in `capabilities.go`, `types.go`;
  metadata confirmation exception in `discovery.go`.
- `internal/store/agent_task_results.go`: optimistic workflow compare-and-swap,
  run lifecycle, actor checks, immutable history, review and cancellation.
  Acceptance rechecks held ownership inside the write transaction.
- `internal/store/schema.go`: `workflow_json`, `agent_task_results`,
  `agent_task_result_handoffs`, and `engineer_messages.agent_task_revision`.
  `agent_task_repository.go`: durable leases; `engineer_messages.go`: callbacks.
- `internal/service/agent_task_repository.go`: `repositoryMu`, turn/process
  admission, idle checks, preflight, bounded fingerprint, release. Inspect this
  before designing dirty continuation; fingerprint alone is not an attribution
  or permission grant.
- `internal/service/agent_task_results.go`: idle settlement and explicit-stop
  suppression. `agent_task.go`: creation, callback prompt/queue, legacy guards.
- `internal/codexapp/turn_admission.go`: admission before session mutex; new
  launch holds it until registration. Initial-constructor skip is cleared when
  the factory returns so a later turn cannot bypass admission.
- `internal/codexapp/types.go`: `Submission.RequireIdle` / `BeforeStart`,
  `LaunchRequest.RequireLiveIdle` / `SubmissionCheck`; manager reuse checks.
  Four native submission implementations enforce the final gate.
- `internal/agentcontrol/executor.go`: immediate exact-session metadata calls.
  `internal/agentquery/{executor,records,catalog}.go`: bounded current/history reads.
- `internal/tui/{control_bridge,agent_task_results,agent_task_project,
  engineer_messages,codex_pane,embedded_session,app}.go`: creation/prompt, review,
  visible state, callback delivery, stop/close and wiring. Slow work must stay
  in `tea.Cmd` or background services, never Update/render.
- `internal/boss/`: create flag preservation and explicit operator review preview.
- `internal/codexapp/codex_home_overlay.go` and `internal/runtimemcp/server.go`:
  generated runtime/provider instructions; keep aligned with contract changes.
- `docs/agent_control_surface.md`, `docs/agent_query_surface.md` and delegation
  plan document the shipped source contract.

Use the existing `agent_task_results_test.go` files in control/store/service/
agentcontrol/agentquery and the admission/TUI message tests. TUI helper
`collectCmdMsgs` can swallow panics; assert actual receipts/calls, not merely that
iterating messages did not fail. Do not add keyword/regex routing for AI behavior.

Before diagnosing submodule dirtiness or changing Git worktree config, load LCR's
built-in `submodule-worktrees` knowledge topic. Nested linked worktrees can share
configuration with the canonical checkout; do not write shared config casually.
For LCR session control/delegation, load the currently installed runtime skill;
skill paths vary per embedded session. Do not spawn hidden collaboration agents.

## Validation already performed and honest limits

Last implementation turn:

- `make test` ran vet and the full Go suite. One failure:
  `TestOpenCodeConfigOverlayDebugSkillShowsShadowPlaywrightSkill` in codexapp.
  The installed `opencode debug skill` output omitted the expected overlay
  Playwright skill. It also failed on an isolated retry and had been seen in prior
  ownership work. Do not claim the complete suite is green or hide the exclusion.
- The service full suite passed (~233 seconds); TUI full suite passed.
- All 16 caller/worker provider combinations passed deterministic control tests.
  Tests also cover identities/spoofing, stale/conflicting results, restart history,
  blocked results, unclassified endings, canceled late results, ownership release,
  stopped callers and cancellation at native submission for all four providers.
- After final guard changes, focused delegation/ownership tests passed. Full
  affected packages (agentcontrol, agentquery, boss, control, store, tui,
  runtimemcp, codexapp) passed with only the above external CLI smoke test excluded.
  Final control and TUI suites passed after the last UI wording changes.
- `go vet ./...` and `git diff --check` passed.
- Isolated `make scan` and `make doctor` passed. PTY-backed `make tui` showed
  working/review/blocked/completed/unclassified/canceled fixtures and preserved
  LCAgent labels; `/perf` reported no captured stalls in that run.
- No live paid provider turn, production database mutation, main runtime restart,
  push, or merge was performed in that implementation turn. End-to-end live
  provider behavior and actual savings remain unmeasured.

Previous logs may still exist under `/tmp/lcr-results-*.log` (full-test, packages,
final-focused, final-ui, vet, scan, doctor, opencode-retry). Treat these as optional
local evidence, not committed artifacts. Do not dump the OpenCode failure log:
its CLI output contains huge skill bodies. Filter to failure names/status lines.

For new changes, run required `make test`, isolated scan/doctor, and PTY TUI checks
when touching UI. Keep temporary config/DB/artifact homes in an isolated directory
inside the assigned workspace; never point experiments at the production DB.
The previous fixture directory was removed after the TUI exited normally.
Use a real PTY command with `stty cols 160 rows 48` before `make tui`; do not launch
it through a Python heredoc (stdin becomes a pipe). Startup settings can be closed
with separate Esc presses. `/` then `perf` + Enter opens performance. Quit uses
`q`, then select Quit with Tab if necessary, then Enter. Never press Enter on a
fixture task: that would launch a provider rather than merely inspect its details.
