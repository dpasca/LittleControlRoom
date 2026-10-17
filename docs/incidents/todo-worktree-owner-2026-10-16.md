# Linked checkout accepted as a TODO owner

## Disposition and tracking

Confirmed ownership defect, tracked with existing **LittleControlRoom TODO #1618**.
That item covers `engineer.send_prompt` rejecting a repository-owned TODO when
the request names its linked checkout. This report adds the complementary write
failure: `todo.create_worktree_and_start_engineer` accepts a linked checkout and
persists it as a new TODO's owner. Both require a shared repository-owner boundary,
separate from the checkout and exact session receiving work. No duplicate TODO
was created during the 2026-10-16 triage. At triage, the existing TODO's text was
not edited; this report supplies the linked incident evidence and repair requirements in the LCR repository.

Implementation for review is now present in the dedicated LCR checkout on
`todo/engineer-send-prompt-rejects-a-repository-todo-s` (2026-10-17). The original
2026-10-16 triage evidence and its timeframes remain below. At triage, no exposed
control could repair ownership while retaining identity and work links. The
implementation adds that supported path; the running LCR still needs an updated
build before it can use it. **Live TODO #1792 has not been repaired.** No FCX
records, worktrees, sessions, files, or phone state were changed.

**#1618 implementation status: independently reviewed, changes uncommitted.** The
ordinary-add gap is corrected, its original failing reproduction now passes, and
the corrected implementation plus canonical `master` at `706ccb59` passed vet and
all 79 tested packages in an isolated snapshot. Updating the running build and
any subsequent supported live repair remain operator actions.

The launch that created this implementation checkout also unintentionally created
root-owned TODO **#1799** despite explicitly requesting existing **#1618**. The
2026-10-17 follow-up identifies operation `lcrop_1792196908932_9fa90dc8410a` and
message `lcrmsg_1792197067628_1398a5deda12`. #1618 remains the primary defect;
#1799's checkout, session, origin TODO and historical launch provenance remain
intact. This implementation neither removes that duplicate record nor relinks
its existing checkout. Future launch requests honor eligible existing IDs and
refuse conflicting IDs instead of clearing them.

## Evidence and timeframe

Source checkout: LCR `a0815ee159edda3f51faf61bcbef3c8402122dc0` (`v0.8.7`).
Incoming report: `lcrmsg_1792138888142_f2cbc1add16b`.
Original evidence file:
`/Users/davide/dev/repos/okmain--fcx-release-readiness-new-gui/_artifacts/fcx_release_readiness/lcr_todo_ownership_report.json`.

Paths used below:

| Role | Exact path |
| --- | --- |
| Repository owner | `/Users/davide/dev/repos/okmain` |
| Caller / release checkout | `/Users/davide/dev/repos/okmain--fcx-release-readiness-new-gui` |
| Tracer checkout | `/Users/davide/dev/repos/okmain--todo-improve-fcx-bullet-tracer-visibility-on-phone-sc` |

The supplied operation evidence records
`lcrop_1792131711589_61b9139155d3`, confirmed **2026-10-16 06:25:15 UTC** and
completed **06:25:52 UTC**. The caller supplied the release checkout as
`project_path`. LCR created TODO #1792, “Improve FCX bullet/tracer visibility on
phone screens”, under that checkout and successfully launched the tracer worker.

Read-only LCR `project.detail` and `project.todo_list` queries at
**2026-10-16 08:22:47 UTC** confirmed:

| Record | Persisted owner | Work checkout | Exact Codex session |
| --- | --- | --- | --- |
| Release TODO #1791 | Repository owner | Release checkout | `01a14322-9249-76e1-b24a-3a8b89bc61f3` |
| Tracer TODO #1792 | **Release checkout, incorrectly** | Tracer checkout | `01a14363-6743-77f0-aeec-f3470b0d2efd` |

Both TODOs remained open with idle work state. The complete root open-TODO list
contained #1791 and excluded #1792; the release checkout's list contained #1792.
Both checkout summaries identified `/Users/davide/dev/repos/okmain` as their
`worktree.root_path`. Their origin TODO IDs were respectively 1791 and 1792.
The tracer branch was `todo/improve-fcx-bullet-tracer-visibility-on-phone-sc`.
This establishes persisted ownership, not merely a misplaced dashboard row.

The original handoff described a completed desktop-only tracer experiment,
intact uncommitted edits, and pending phone review. Later query assessments
describe subsequent Android testing and a tracer merge, while the tracer Git
snapshot still reports a dirty checkout. Those summaries do not establish the
current FCX diff or release readiness. This investigation did not independently
verify those later claims. In particular, “Android tests had not started” refers
to the original confirmation pause, not all subsequent caller activity.

## Cause and relationship to #1618

This section describes the inspected triage build, not the changed source now
under review. The implementation section below names the replacement boundaries.

1. [`NormalizeTodoCreateWorktreeAndStartEngineerInput`](../../internal/control/capabilities.go)
   trims and cleans the path but does not resolve repository ownership.
   [`Executor.Propose`](../../internal/agentcontrol/executor.go) performs typed
   validation; its repository preflight is specific to submodule alignment, not
   TODO ownership. The host's proposal-loading path likewise has no TODO-owner
   preflight.
2. [`executeTodoCreateWorktreeAndStartEngineerControlWithOutcome`](../../internal/tui/control_bridge.go)
   uses `resolveControlProjectRef`, which returns the exact loaded checkout.
   It copies that checkout's path into the launch input. `Service.AddTodoForLaunch`
   passes it through to [`Store.AddTodoForLaunch`](../../internal/store/todo_launches.go),
   which persists it as `project_todos.project_path` together with the launch
   request journal. There is no repository-owner normalization in this path.
3. [`Service.CreateTodoWorktree`](../../internal/service/worktree.go) first compares
   the TODO owner with the supplied project path. Those two incorrect checkout
   paths match. Only afterward does it resolve `worktreeRootPath` using Git
   metadata. Consequently the child checkout is created under the correct root
   while the TODO remains owned by the release checkout.
4. For #1618, `resolveControlTrackedTodo` in the same TUI bridge compares the
   supplied checkout with `TodoItem.ProjectPath` literally. This rejects a valid
   root-owned TODO before `resolveControlTodoWorkProject` can follow its recorded
   work checkout. The lookup runs after confirmation.

The repository-scoped capture path already has the intended behavior:
[`todocapture.Service.ResolveProject`](../../internal/todocapture/service.go)
uses Git and trusted stored mappings, resolves a unique loaded root, and fails
closed for unavailable or ambiguous ownership. Its linked/nested-checkout and
symlink tests do not cover the launch control above. Extract or reuse that
resolution policy in a UI-neutral owner resolver; do not normalize every
`engineer.send_prompt.project_path` to the root, because exact-session delivery
must still address the intended checkout. Keep permission targets and previews
consistent with the resolved owner and destination, then revalidate at execution.

## Confirmation and caller continuation

The first three paragraphs describe the confirmation behavior and caller pause
in the original incident. The implementation adds the explicit review below.

The confirmation renderer already displays a project, TODO text, “dedicated
worktree”, and engineer provider. It does not display a separately resolved
repository owner or concrete planned worker destination. The permission wrapper
mentions caller continuation only when `ResumeOnSuccess` is true; the omitted
case has no equivalent explanation that confirmation will not itself resume
the caller.

The incident's proposal returned `requires_new_user_turn=true`, and the caller
omitted `resume_on_success`. The runtime skill required that turn to end. This
explains the pause before Android testing; it is not evidence of lost work, a
crashed test, or the worker replacing the parent session. Launch completion and
worker-result reporting are distinct from resuming the parent's unfinished work.

For future requests with unfinished caller work, explicitly request
`resume_on_success=true` and end the turn as instructed. The current host can
then continue the same eligible caller after operation success. New input, stop,
replacement, failure, or host restart can suppress that continuation. A final
notification should leave it omitted.

The fix should make confirmation show all three effects (create a root-owned
TODO, create a separate worktree, start a new engineer), the owner and planned
destination, and the caller's continuation state in both modes. If a destination
cannot yet be fixed, say so rather than presenting a guessed path. Keep those
facts reviewable on short screens without requiring raw JSON.

## Repair assessment for #1792 at triage

The live TODO control catalog exposes only `todo.add`, `todo.complete`, and
`todo.create_worktree_and_start_engineer`. There is no owner-move or TODO-edit
control. The store's ordinary TODO update methods edit text/attachments, not
ownership. `MoveProjectPath` and `ConsolidateProjectPath` move or combine whole
project identities, including session/artifact records; they are unsuitable for
moving one TODO between two distinct, valid checkouts. Copying/recreating a TODO
would lose the required ID continuity and risk duplicate work. These alternatives
were rejected, and no repair operation was submitted.

A supported repair requires a dedicated, guarded host control/service operation
or an explicitly designed migration, implemented and tested before use. For this
case it must:

- Verify TODO 1792 still has the expected release-checkout owner, and independently
  verify that checkout and the tracer checkout belong to the loaded `okmain` root.
  Refuse changed, ambiguous, or cross-repository mappings before confirmation and
  again inside execution.
- Atomically change only #1792's owner to `/Users/davide/dev/repos/okmain`, with
  an explicit destination ordering policy and an audit record. Preserve its ID,
  text, created time, completion state, attachments, work state, work checkout,
  provider, and session link. Append it without modifying #1791's record.
- Retain the tracer checkout's origin TODO ID 1792 and existing worktree plan,
  branch, directory, files, and session. Preserve #1791's release checkout/session.
  This repair must not launch, restart, remove, recreate, merge, or clean either
  checkout.
- Preserve historical request/operation provenance and make launch replay aware
  of the repaired ownership. The original request ID must continue to refer to
  the same TODO/worktree/session; it must not allocate another task or launch.
- Refresh both owner views/counts asynchronously. Verify root TODOs contain
  #1791 and #1792, the release checkout has no directly owned #1792, and both
  work links and exact sessions are unchanged. A repeated repair is an auditable
  no-op, while stale expected-owner input fails safely.

Until such a repair exists, leave #1792 in place. Exact-session communication can
target its existing tracer checkout/session without a `todo_id` redirect; that
preserves work but does **not** repair ownership. The LCR investigation has no
phone actions; Android testing remains with the FCX caller.

## Regression requirements

- Invoke the launch control from a known linked checkout: exactly one root-owned
  TODO, zero checkout-owned TODOs, one child worktree/session, and unchanged
  caller TODO/session. Exercise service entry points as well as the host bridge.
- Repeat/restart the same launch request and partially failed preparation: retain
  the original TODO, saved destination, and engineer claim without duplicates.
- Send a root-owned TODO to its recorded linked checkout (#1618): resolve its
  owner before confirmation, then deliver to the exact requested session. Reject
  unrelated repositories and incompatible session/worktree targets before asking.
- Cover missing roots, stale mappings, and ambiguous symlink aliases with no
  guessed ownership. Preserve authorization scope after normalization.
- Check confirmation with long names/prompts on short screens, showing owner,
  destination, all three effects, and continuation enabled/omitted states.
- Verify continuation after successful launch, no implicit continuation when
  omitted, and suppression on stop, new input, failure, replacement, or restart.
- Test the owner repair's atomic rollback, stale-state refusal, exact retries,
  retained launch journal, root #1791 invariants, and unchanged worker files and
  sessions. At triage, tests covered capture-root resolution, root-started launch,
  launch recovery, and continuation; they did not establish this repair behavior.

## Triage validation (2026-10-16)

Evidence was collected with read-only LCR queries and source inspection. This
report adds documentation only; no runtime fix or owner repair is included.
`make test` (including vet), `make scan`, and `make doctor` passed. Scan and doctor
used an isolated config/database at `/tmp/lcr-todo-1618-triage.z6HUN2`, restricted
to the LCR project. Report source links and whitespace were also checked. These
are baseline checks, not proof that the reported defect is fixed. No new
regression tests were added in this triage, and no UI behavior was changed.

## Coordination receipt

The findings were delivered through `engineer.send_prompt` to the exact caller
Codex session `01a14322-9249-76e1-b24a-3a8b89bc61f3` in the release checkout.
Operation `lcrop_1792139421024_f98a1eb0ccac` completed at
**2026-10-16 08:30:25 UTC**, with delivery state `delivered` and message ID
`lcrmsg_1792139421194_2fd4518a8e40`. The approved project collaboration covered
this notification. This receipt confirms delivery of the triage, not completion
of subsequent FCX testing. The message requested no acknowledgment-only reply.

## Implementation for review (2026-10-17)

- `internal/projectowner` extracts the existing capture resolver. Launch, linked
  TODO routing and repair use its stricter live repository check: a unique
  loaded primary checkout, live Git membership, exact checkout roots, available
  paths, and no contradictory stored mapping. Capture retains its existing
  non-Git/offline read behavior; no detector-source assumptions changed.
- `internal/todocontrol` is the shared preflight boundary for embedded proposals,
  Help Chat, service launch and host execution. Owner and destination expectations
  are bound before confirmation and checked again during execution and before
  queueing a TODO handoff. The handoff keeps its requested checkout, provider and
  exact session. A root address follows the TODO's recorded worker; a sibling or
  cross-repository target refuses. Existing exact-session mailbox checks remain.
  Live session replacement is checked in background proposal commands for both
  Help Chat and the external host bridge.
- `Service.AddTodoForLaunch` persists a root-owned TODO independently from its
  historical request path. `CreateTodoWorktree` resolves the same owner before
  comparing ownership. New launches append to ordering, preserving existing TODO
  records. A supplied eligible open `todo_id` is atomically bound to the launch
  journal, retaining its text and metadata; existing assignments and conflicting
  request identities refuse. Exact retries retain the original TODO, destination,
  provider choice, session claim and launch journal, including after owner repair.
- Confirmation shows the repository TODO owner, worker destination (explicitly
  not yet fixed for a fresh allocation), create-versus-existing TODO semantics,
  worktree and engineer effects, and caller continuation enabled or omitted/false.
  Paths wrap; review content scrolls on short screens with fixed actions. Existing
  approval busy handling suppresses repeat activation. Database/Git work and view
  refreshes remain outside Bubble Tea's update/render path.

### Supported repair path and running-build availability

The new **host-scoped** capability is `todo.repair_owner`. An operator can request
it through Help Chat in an updated build. For the original incident its arguments
are:

```json
{
  "todo_id": 1792,
  "expected_owner_path": "/Users/davide/dev/repos/okmain--fcx-release-readiness-new-gui",
  "repository_path": "/Users/davide/dev/repos/okmain"
}
```

This is a documented example, **not an executed live repair**. The host supplies a
request ID when omitted and binds `expected_revision` to the reviewed TODO,
attachments, work links, launch plan and historical journal. Execution verifies
that snapshot under the SQLite write transaction. Only this TODO's owner and
position change; it appends to the destination's completion-state ordering.
Timestamps, ID, text, completion, attachments, work state, checkout, provider,
exact session, worktree origin and historical launch input remain intact. The
audit and ownership change commit together. An exact retry records an audited
no-op; a different request with the old expected owner or changed revision refuses.
Both owner views refresh asynchronously. Repair never starts or modifies a worker.

A read-only check of the running runtime on 2026-10-17 returned **unknown control
capability** for `todo.repair_owner`. The running process was not rebuilt or
restarted. After updating LCR, any live repair must use this supported confirmed
control. No SQL or source-level repair script was run against production state.

### Initial implementation validation (before ordinary-add review)

The focused ownership, existing-TODO launch, retry, repair, host bridge and
short-screen confirmation regressions passed. The original triage validation
above remains baseline evidence only.

- Default `make test` completed vet and passed 78 packages, then reached Go's
  10-minute service-package timeout (603.120s). The active cases were the existing
  `TestRemovalClassifiesGitFilesByPointerContent/empty_cache_marker` and
  `/unrelated_content` removal fixtures. There were no ownership assertion failures
  in that run. The same full suite passed with a 30-minute package cap:
  vet and all 79 tested packages passed, with no package or test-pattern exclusions. Service completed
  in 413.735s, TUI in 52.937s, agent control in 24.883s, and shared TODO preflight
  in 13.988s. The final unmodified **`make test` also passed**, exit 0: vet and all
  79 tested packages passed; service completed in 226.215s and TUI in 26.899s.
- The larger help capability corpus exposed a test-only stdout pipe deadlock.
  `internal/cli/run_test.go` now drains the pipe concurrently. Its help-corpus test
  and the full CLI package passed after that change.
- The existing worktree-lock cancellation test now cancels at the reported lock
  wait instead of racing live Git inspection against a 20 ms timer. The existing
  `TestCreateTodoWorktree*` compatibility cases passed in 12.367s. Repository
  preflight preserves context cancellation errors for callers.
- `make scan` and `make doctor` passed with config, SQLite DB and Codex/OpenCode/
  Claude artifact homes isolated beneath this checkout's `.todo-owner-validation`
  directory. The configured project scope was this assigned LCR checkout;
  classification and the mobile server were disabled. A synthetic session record
  supplied deterministic local scan evidence. No production DB was opened for
  writes by these checks.
- A real PTY-backed `make tui` loaded the dashboard and opened `/perf` at 80×24.
  The initial run hit the existing guard's false-positive detection of the Make
  shell wrapper as another runtime. The isolated retry used the documented
  `--allow-multiple-instances` debug escape hatch. `/perf` reported no captured
  stalls; its one latency entry was a 1.5-second embedded-pane open during
  navigation. No engineer prompt was sent. The isolated TUI/provider processes
  exited afterward. Control-specific long-text/path, scrolling, fixed-action and
  busy-state behavior is covered by the automated 64×18 dialog regression.
- Final source whitespace and diff inspection passed. Canonical LCR remains clean
  on `master`; implementation changes are uncommitted in the assigned branch.

Validation covers this assigned branch's base `a0815ee1` plus the implementation.
During the task, canonical `master` independently advanced to `706ccb59`
("Allow engineer messages to follow the current session", 2026-10-17 10:07:04
+09:00). Its schema, confirmation, permission and engineer-message changes overlap
some edited files. The canonical commit was retained untouched; no merge,
rebase, commit or combined-tree validation was performed. Review/integration
must account for that newer messaging behavior.

Extended full-suite and final default-command results: **passed**, exit 0.
Generated validation logs and the isolated sandbox were retained outside the
review checkout at
`/var/folders/fy/9y_kzt353j13k3kyv5t72tcm0000gn/T/lcr-todo1618-review-validation-hmths3pi`.
No implementation or report content was discarded during this cleanup.

The implementation handoff was delivered through the approved project
collaboration to exact root Codex session
`01a13e4a-bcd9-71a3-8b41-c7475129d906` at **2026-10-17 01:54:49 UTC**:
operation `lcrop_1792202089426_5e4284b77674`, message
`lcrmsg_1792202089695_cd29ba25b8dc`, delivery state `delivered`. Automatic caller
continuation was not requested. The handoff explicitly noted the newer canonical
commit and the absence of combined-tree validation.

### Review finding and ordinary-add correction (2026-10-17)

Review message `lcrmsg_1792202269939_99ecc14a707b` found a missed write boundary:
ordinary `Service.AddTodo` / `AddTodoWithAttachments` and `todo.add` still used the
exact loaded checkout. The independent reproduction called
`svc.AddTodo(ctx, f.Caller, "linked checkout task")` and received/persisted
`ProjectPath=f.Caller`, rather than `f.Root`. Its source is
`/var/folders/fy/9y_kzt353j13k3kyv5t72tcm0000gn/T/lcr-todo1618-combined-review-ki22w1kh/.review-repro/todo_scope_test.go`;
`go test ./.review-repro -run TestPlainTodoAddUsesRepositoryOwner -count=1`
failed, exit 1, in 4.161s. This showed the earlier "complete" disposition was too
broad: launch and capture coverage did not establish the invariant for plain add.

The correction extends the shared owner policy to plain adds and attachment adds.
Loaded non-Git projects keep their own backlog. Known linked/Git projects require
live, unique repository membership; missing root/checkout/Git metadata, an unloaded
root, contradictory cross-repository mappings and ambiguous root aliases refuse
before any write. Suggestions, events and asynchronous service refreshes use the
resolved owner. No attachment path, bytes, ordering or creation time is changed by
owner resolution.

`todo.add` now binds `todo_owner_path` before confirmation and checks it again in
the service at execution. The requested checkout stays in the invocation and
permission target. Confirmation shows requested checkout, repository TODO owner,
and that adding does not start a worktree or engineer. Results and refreshes use
the actual owner. The ordinary editor separately retains its caller identity to
clear busy/pending-save state and refreshes the owner and caller views using
existing asynchronous coalescing. After saving, the TODO dialog shows the owner
backlog even when the root is outside the visible project filter; the selected
checkout remains unchanged. Continuation confirmation now uses product
language: disabled callers wait for another message; enabled continuation remains
explicit. The API parameter expression was removed from the dialog.

The new plain-add, attachment-preservation, non-Git, refusal-without-write,
changed-owner-after-review, proposal-permission, editor-refresh and short-screen
regressions passed (service 4.425s, agent control 1.285s, TUI 1.850s, Chat 2.805s,
server 2.245s). The existing mobile API scope test now uses a real isolated Git
fixture instead of nonexistent directories labeled as Git checkouts. This is test
data only; no connected phone was used. The existing continuation/permission
fixture now registers its non-Git target instead of proposing to an unloaded
`/target`. Full agent-control and TUI packages passed after the correction in
17.150s and 75.794s respectively. The first aggregate correction run passed the
service package in 457.868s and the TUI package in 51.761s, but exited 2 because
it still contained the old unloaded continuation-test fixture. That fixture is
corrected as described above. The final standard **`make test` passed**, exit 0:
vet and all 79 tested packages passed, with no package or test-pattern exclusions.
Agent control completed in 9.688s, TUI in 49.164s; service reused its successful
457.868s result. This final run includes the filtered-owner editor fix, the
registered continuation fixture, the new plain-add/attachment regressions, and
the product-language continuation text. Raw aggregate evidence is retained at
`/tmp/lcr-todo1618-plain-add-reviewed-make-test.log`.

A fresh isolated config, DB and three provider artifact homes passed `make scan`
and `make doctor`. One synthetic session record scoped scanning to this assigned
checkout; classifications and the mobile server were disabled. A real PTY-backed
`make tui` at 80×24 saved two synthetic TODOs from this linked checkout to
`/Users/davide/dev/repos/LittleControlRoom` **in that isolated DB only**. The final
run showed both items in the owner backlog, with the linked checkout still
selected. `/perf` showed no in-flight or recent waits, a 2-second armed watchdog,
and no captured stalls. Both TUI runs exited 0; the documented
`--allow-multiple-instances` escape hatch covered the Make wrapper guard's
false-positive detection. No engineer session was launched. Evidence and the
isolated sandbox are retained at
`/var/folders/fy/9y_kzt353j13k3kyv5t72tcm0000gn/T/lcr-todo1618-plain-add-validation-6wm0hqu9`.

Independent compatibility evidence from
`lcrmsg_1792202562428_e58307cad64b` is scoped to the **original implementation plus
master `706ccb599d7c065149de9c95ec2a3dfb6c1cc714`, before this correction**.
The review's external snapshot retained both sides using three-way scratch-file
composition, with no textual conflicts across six overlapping files:
`internal/boss/control_dialog.go`, `internal/control/capabilities.go`,
`internal/control/permissions.go`, `internal/control/types.go`,
`internal/store/schema.go`, and `internal/tui/engineer_messages.go`.
No branch/worktree merge or rebase was performed and neither side was discarded.
Its `make test` passed vet and all 79 tested packages, exit 0 (service 266.166s,
TUI 36.462s, Codex app 58.601s, control 4.060s). Evidence is retained in
`review-findings.json` and `review-make-test.log` in that snapshot. The hidden
ordinary-add reproduction was excluded from `./...` and failed separately; that
compatibility pass does not validate this later correction.

At the worker's correction handoff, the correction remained uncommitted on the
original dedicated branch/base and had not received combined-master validation.
The independent final review below closes that validation gap.
Whitespace and final diff inspection passed; all 43 changed Go files are
`gofmt`-clean. Canonical LCR remains clean on `master` at `706ccb59`. No live FCX
repair, production SQL, phone action, commit, merge, rebase, push or worktree
removal was performed. The earlier #1799 record and its worktree/session
provenance remain intact.

The correction handoff was delivered to the same exact root Codex session
`01a13e4a-bcd9-71a3-8b41-c7475129d906` at **2026-10-17 02:16:09 UTC**:
operation `lcrop_1792203368802_7b22bea66fa1`, message
`lcrmsg_1792203369171_f146c4069125`, delivery state `delivered`.

### Independent final review (2026-10-17)

The requesting root session inspected the correction's source and tests, including
ordinary adds, attachment retention, proposal/execution ownership checks, permission
constraints, editor state and refreshes. No further source-code findings remained.
The exact `TestPlainTodoAddUsesRepositoryOwner` reproduction that failed the first
review now passes, exit 0, in 1.175s.

The complete corrected worktree changes were composed with canonical `master`
`706ccb599d7c065149de9c95ec2a3dfb6c1cc714` in an external snapshot, using the
original branch base `a0815ee159edda3f51faf61bcbef3c8402122dc0` for three-way
file composition. Both the ownership correction and the newer current-session
messaging behavior were retained. The same six overlapping files listed above
combined without textual conflicts; no content from either side was discarded.
No Git branch or checkout was merged, rebased or committed.

On that corrected combined snapshot, standard **`make test` passed**, exit 0:
vet and all **79 tested packages**, with no package or test-pattern exclusions.
Service completed in 312.116s, TUI in 43.792s, agent control in 9.923s, Chat in
2.206s and control in 5.296s. The hidden independent reproduction passed
separately; it was not counted among the 79 packages. This result supersedes the
earlier combined run's explicit exclusion of the ordinary-add correction.

Evidence is retained at
`/var/folders/fy/9y_kzt353j13k3kyv5t72tcm0000gn/T/lcr-todo1618-corrected-combined-txc9sl83`:
`review-manifest.json`, `feature-files-sha256.json`, `review-make-test.log`,
`review-final.json`, and `.review-repro/todo_scope_test.go`. All 44 task files
matched their recorded hashes at completion of testing, before this documentation
update. Review completed at **2026-10-17 02:24:14 UTC**. Canonical `master` remained
clean at `706ccb59`; all implementation and report changes remain uncommitted in
the dedicated worktree. The running LCR and live TODO #1792 remain unchanged;
#1799's existing checkout/session and provenance are retained.

### Report relocation

The original untracked report was copied into this assigned checkout as the first
filesystem action, compared byte-for-byte (12,386 bytes), and only then unlinked
at the exact authorized canonical source path. Its original SHA256 was
`a96ace422dc42e304a351df694e3023cc6f0d9ea989ced3cf2ea64ca6a401c70`.
There was no destination conflict. The canonical checkout was verified clean on
`master` immediately afterward. Subsequent report changes are the disposition,
implementation, launch-mismatch and validation updates described here; the original
incident evidence and timeframes are preserved.
