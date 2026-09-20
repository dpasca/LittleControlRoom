# Visible engineer delegation

Status: proposed implementation plan. This document does not describe shipped
behavior or authorize changes to running sessions. It extends the existing
[control surface](agent_control_surface.md) and [query surface](agent_query_surface.md).

## Product contract

An engineer remains responsible for the user's request while delegating bounded
implementation work to another provider/model. The caller supplies the objective,
constraints and acceptance criteria, then reviews the actual changes and fixes
defects before reporting completion. Less expensive workers should reduce total
cost, including the caller's review and correction work.

Every worker is a persisted, visible LCR task before execution starts. It nests
under its originating project/worktree and can be opened, inspected or stopped.
Neither background execution nor `reveal: false` hides its row. Completed tasks
remain visible until the operator explicitly trashes them. Provider-native hidden
subagents are not an alternative execution route for this feature.

The first release supports one active worker per caller, one delegation level,
and at most two automatic correction rounds. These are proposed product defaults,
not current limits. Any supported caller can select any supported worker provider
(Codex, OpenCode, Claude Code or LCAgent) independently of its own provider.

## Evidence and existing foundations

The F-14 documentation task exposed several separate issues:

- The worker produced a commit and test evidence, but repository writes initially
  required operator-run patches because its staging directory was its only writable
  root. Current launch enrichment already grants an affiliated LCAgent the exact
  originating repository root; preserve and exercise that fix on launch and resume.
- A fresh caller's MCP key was persisted as its resumable provider session ID.
  The result callback tried to open a nonexistent Claude transcript. Manual exact
  delivery restored review; Claude then found and fixed a real semantic test gap.
- Task creation selected a provider but offered no operation-local model/effort.
- The worker was instructed to commit and write a large user-facing handoff.
  Waiting for a caller, waiting for a user, and finishing a turn were conflated.

Useful implementation anchors:

| Concern | Existing code to extend |
| --- | --- |
| Task inputs and schemas | `internal/control/capabilities.go` |
| Model validation/catalog | `internal/control/engineer_models.go` |
| Caller provenance and launch | `internal/tui/control_bridge.go` |
| Fresh MCP identity generation | `internal/codexapp/browser_launch.go` |
| Durable tasks/results | `internal/model/model.go`, `internal/store/agent_tasks.go`, `internal/service/agent_task.go` |
| Durable callback transport | `internal/store/engineer_messages.go`, `internal/tui/engineer_messages.go` |
| Turn-end handling | `internal/tui/boss_mode.go` |
| Affiliated writable roots | `internal/tui/codex_pane.go` |
| Visible rows and attention | `internal/tui/agent_task_project.go` |
| Existing messaging grants | `internal/control/collaboration.go` |

Reuse these boundaries. Scheduling, authorization, result validation and lifecycle
transitions belong in UI-independent service/control/store code; the TUI supplies
host execution and renders cached state.

## Workflow

1. Caller creates a scoped task with acceptance criteria and an explicit worker
   provider/model. LCR binds the caller from trusted host context.
2. LCR validates model availability, repository access and caller return address,
   persists the visible task and approved scope, then launches the worker.
3. Worker edits and checks the code, then submits a structured result to LCR.
   It does not ask the user to commit or produce a long handoff by default.
4. LCR persists the result and queues review to the exact caller. A busy caller
   receives review at a safe turn boundary; a stopped caller is not revived.
5. Caller inspects the diff and evidence, runs independent checks as needed, and
   either accepts, fixes locally, or sends a focused correction to the same task.
6. Caller records acceptance of the current result revision, releases the task's
   execution resources, and reports briefly to the user. Commit handling stays
   with the caller under the repository's existing policy.

## Identity and reliable delivery

Persist distinct identities: host logical engineer ID, MCP operation/idempotency
key, provider, provider resume/thread ID, and launch generation. Bind fresh
provider IDs when the provider announces them; keep that mapping across restart.
An MCP key must never be interpreted as an artifact filename or resume ID.

Capture provenance at task creation, not by looking up whichever session is
currently selected when the worker finishes. If provider identity is not yet
known, retain the exact host binding and queue until resolved. Show an explicit
delivery problem if it cannot be resolved. Never retarget to the latest session
or silently start a replacement. Operator reassignment is an explicit action.

Use a monotonic result revision and durable mailbox idempotency key derived from
task ID plus revision. Keep delivery receipts distinct from review acceptance.
On restart, recover pending delivery without duplicating acceptance or correction
side effects. Provider delivery may be ambiguous after a crash: retain the message
ID and reconcile available evidence, rather than promising exactly-once turns.

Old records with valid known IDs continue to work. Repair an old ambiguous caller
only with an exact recorded mapping; otherwise expose reassignment. Do not infer
identity from the newest transcript. Preserve historical failed attempts while
showing a later accepted result as completed, not still awaiting confirmation.

## Task and result contracts

Extend task creation/continuation using the existing `EngineerModelSelection`
contract. Persist requested and effective model, model provider and effort for
each run. Validate before launching; continuation retains its choice unless an
explicit authorized change is requested. Catalog absence or account rejection is
an explicit failure, with no silent expensive-model fallback.

Caller ownership is independent of optional `parent_task_id`: an ordinary
engineer can delegate without already being a task. Use `parent_task_id` only
for an actual task hierarchy. Do not make callers invent a parent task.

The durable brief includes objective, allowed repository/files or areas,
exclusions, acceptance criteria, required checks, workspace mode, limits and
commit policy. Pass relevant file references and context, not the caller's entire
conversation. Let the caller model decide decomposition; no keyword/regex routing.

Add a shared typed result-submission capability to the registry, available through
MCP and LCAgent adapters. Bind submissions to the actual worker/task identity.
Suggested result fields:

- Task/run ID, result revision and outcome (`ready_for_review`, `blocked`, `failed`).
- Short substantive summary and criterion-by-criterion completion/limitations.
- Base revision and changed-file/diff artifact references.
- Checks with command, cwd, outcome, exit code and bounded evidence references.
- Remaining risks, blockers and questions intended for the caller.

Treat worker claims as claims; distinguish host-captured execution evidence.
Detailed evidence stays inspectable through bounded task queries and the transcript.
Do not require the caller to discover global raw transcript paths to review a task.
Result consumption must reject stale revisions and unauthorized callers.

Separate lifecycle phase from provider run status and from callback delivery:

| Phase | Next owner |
| --- | --- |
| queued / working | Host / worker |
| blocked | Caller, or operator when human action is necessary |
| awaiting review / reviewing | Caller |
| changes requested | Same worker, new result revision |
| completed | Accepted by caller; remains visible |
| failed / canceled | Explicit failure or stop; remains visible |

Preserve compatibility with existing task statuses via a phase field and explicit
projection until consumers migrate. Legacy free-text turn endings may be shown
as unclassified output; they must not automatically become accepted or ready
results. Avoid natural-language heuristics for lifecycle transitions.

## Authorization and workspace ownership

Introduce a delegation-specific grant through the existing confirmation machinery.
The initial task approval covers that task's worker execution, exact-caller return,
bounded correction rounds, review acceptance and non-destructive completion.
A host setting can allow future delegations within operator-selected project,
provider/model and resource limits. Grants are visible and revocable; they are
not inferred from worker prose or the existing project-pair messaging grant.

The worker cannot expand its scope, approve itself, or mark its work accepted.
Changing repository, exceeding limits or using an unapproved model requires the
existing explicit approval path. Commit/push/merge/trash remain separate actions.
Revocation prevents new automatic actions; stop cancels running work and pending
automatic review/correction. Resuming after an explicit stop requires an explicit
resume. Late results are retained for inspection but do not restart the loop.

For the first release, use the originating worktree with a host-managed exclusive
write lease for LCR-owned sessions. The caller delegates write ownership and can
do independent read-only work while the worker runs. Caller review/fixes begin
only after worker writes and write-capable processes have stopped. Show the owner;
never wait silently on a lease from a stopped or missing session.

Preflight clean/baseline state and existing writers before launch. A lease cannot
prevent external editors: record the starting state and detect intervening changes
at handoff; report conflicts and preserve edits. Default to a clean baseline;
support dirty baselines only with an explicit captured ownership boundary. Do not
stash or overwrite user changes automatically.

Use the existing scoped LCAgent root grant, keeping staging artifacts separate
from source edits. Other providers must receive the same intended repository and
capability context. Permissions must be checked before expensive investigation.
Parallel writable workers and isolated Git worktree integration are a later slice;
read the built-in submodule-worktrees guidance before implementing their setup.

## Visibility and cost

The existing project list is the primary surface. Worker rows show phase and agent/
model; the detail view shows caller, workspace ownership, limits, result, checks,
review outcome and delivery errors. Failed launches also leave a visible record.
Normal caller review is not classified as waiting for the user. Preserve inspect,
open-transcript and stop actions; apply existing shortcut and busy-state rules.

Default to background launch without stealing focus. A short caller notice names
the visible task. Worker chatter stays in its pane. The caller's final report is
normally 3–5 bullets; evidence is expandable. Persist transitions and result
revisions, not streaming heartbeat churn, and coalesce asynchronous refreshes.

Track worker usage and caller review/correction usage separately and in total.
Use provider-reported tokens/cost when available; label estimates and unknowns.
Do not claim an exact dollar cap where the provider cannot enforce one. Enforce
supported token/turn/time limits and correction-count limits at the host boundary.
Evaluate cost per accepted task, human interventions, review defects, time to
acceptance and rework. A cheap worker with expensive retries may cost more overall.

## Delivery sequence and acceptance gates

1. **Reliable return address.** Add durable host/provider identity binding and use
   it in task provenance/callbacks. Test fresh and resumed callers for all four
   providers, late identity binding, replaced sessions and restart recovery.
   Keep task visibility and existing confirmation behavior intact in this slice.
2. **Selectable visible workers.** Add task model/effort selection and run metadata,
   repository preflight and write ownership. Test fresh/resumed LCAgent root grants,
   rejected models, interrupted launch, external changes and row visibility.
3. **Explicit results and review.** Add result revisions, typed submissions,
   lifecycle phases, bounded evidence queries and exact-caller review delivery.
   Test blocked/failed/interrupted turns, stale results, busy callers, duplicate
   delivery, unavailable callers and late results after stop.
4. **Bounded automatic supervision.** Add scoped grants for creation policy,
   corrections and acceptance; caller/worker report contracts; concise UI notices.
   Test revocation, scope widening, unauthorized acceptance, correction limits,
   task completion without trash and provider permission failures.
5. **Cost evaluation, then concurrency.** Compare accepted tasks with and without
   delegation before expanding to multiple writers or recursive delegation.

The first end-to-end milestone is one visible worker, on an explicit model,
receiving bounded work and returning a typed result to its original caller for
review and completion, without operator message relaying or patch application.
The caller must be able to reject a superficially passing test, request a correction
and accept the next revision. Only then call the loop reliable.

Use deterministic provider adapters for lifecycle and restart regressions. Run a
bounded real-provider smoke matrix after those pass; record effective model and
usage rather than silently choosing a costly fallback. Verify cross-provider
caller/worker combinations, not just same-provider delegations.

Run `make test`, `make scan`, and `make doctor` for implementation changes. Use
isolated scan/doctor data. For UI changes, use a PTY-backed TUI, exercise `/perf`,
inspect active/review/blocked/completed rows and stop behavior, and check spinner
gaps while results arrive. No disk, database, Git or contended session reads belong
in the Update/render path.

Update the control/query docs and generated runtime guidance with each shipped
contract change. Update STATUS.md only when the behavior actually ships; update
the artifact footprint document if detector assumptions change.
