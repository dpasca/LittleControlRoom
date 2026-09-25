# Progressive Agent Control Surface

This is the write-side companion to the read-only
[Progressive Agent Query Surface](agent_query_surface.md). Queries return
bounded persisted state directly; controls queue typed operations for operator
confirmation or delivery under an existing project collaboration approval.

The proposed [visible engineer delegation plan](engineer_delegation_plan.md)
builds on this surface with explicit worker models, durable caller identity,
structured results and caller review. It is a future implementation plan, not
an expansion of the current authorization contract.

Little Control Room gives embedded Codex, OpenCode, Claude Code, and LCAgent
sessions access to its typed control registry without publishing one tool per
action. Codex, OpenCode, and Claude Code use MCP; LCAgent exposes the same
contracts as native tools. The stable surface has four control tools:

| Tool | Purpose |
| --- | --- |
| `list_control_capabilities` | Return domains and compact capability summaries, optionally filtered by one exact domain. |
| `describe_control_capability` | Return the exact schema, risk, authority scope, host effects, and confirmation policy for one capability. |
| `propose_control_operation` | Validate and durably queue one typed operation. It never executes the action directly. |
| `get_control_operation` | Read the status or terminal result of an operation created by the same embedded session. |

This is the portable progressive-disclosure layer. Direct, frequently used
runtime and repository-TODO tools remain available alongside it.

Help Chat uses the same registry through a native in-process profile. It exposes
`list_control_capabilities`, `describe_control_capability`, and
`propose_control_operation`; it does not need `get_control_operation` because a
valid proposal is a terminal LCAgent outcome handed directly to Chat's existing
host confirmation dialog. The native adapter validates the same typed
invocation and never executes it.

## Engineer model selection

The engineer and task controls (`engineer.send_prompt`,
`todo.create_worktree_and_start_engineer`,
`project.create_and_start_engineer`, `agent_task.create`, and
`agent_task.continue`) accept optional `model`,
`reasoning_effort`, and LCAgent `model_provider` fields. Discover exact IDs
through the read-only `engineer.models` query for an explicit provider.
The query returns supported effort IDs, model defaults, source, and observation
time. Catalogs are saved when the host opens a provider session or loads its
model picker. They distinguish provider listings from Claude's built-in aliases
and LCAgent's combined curated/provider routes. A listing is evidence, not a
guarantee of current account access; an empty catalog means discovery has not
completed. Pagination uses `limit` and `offset`.

Explicit choices are validated asynchronously before repository/TODO mutations.
They override saved model preferences for this operation only. Omitted choices
preserve existing defaults. A model-changing follow-up waits for the active turn
to finish instead of steering a turn running on the previous model. The durable
mailbox preserves the choice across restarts.

Use `select_model: true` with an explicit provider to ask the operator to choose
a model and effort. After control confirmation, the host opens the picker
**before** creating the TODO/worktree or sending the prompt. Cancellation or
loading failure terminates the operation. `select_model` and an explicit
`model` are mutually exclusive.

`reveal: true` only shows the engineer session. It does **not** request a model
picker. Confirmation displays either the explicit model/effort, current defaults,
or the pending picker choice.

For delegated tasks, an explicit choice is persisted on the task before launch.
Continuation with omitted model fields inherits that choice and revalidates it;
reopening the task also prefers it over global defaults. Switching the engineer
provider for a task with a saved model requires a new explicit model choice.
An unavailable model fails explicitly. Task model selection does not update global
preferences, and choosing a model inside a task's pane updates that task's choice.
An active worker must finish before a continuation with a model choice can start.

Task details and queries distinguish `model_selection` (requested provider, model,
model vendor and effort) from `observed_model` (last provider-reported choice).
Pending changes are not reported as effective; older sessions cannot overwrite
the current worker's report. Legacy tasks without explicit choices still use the
existing defaults. The task picker requires an explicit model; cancellation starts
no work. These fields describe the current task, not a per-run accounting ledger.

### Repository write ownership for delegated tasks

`agent_task.create` accepts optional `repository_write: true`. The checkout comes
from the task's exact worktree/project affiliation, not an arbitrary worker path.
The host persists the visible task before preflight. An attached Git branch,
clean tracked/untracked state, writable checkout, no unfinished Git operation,
and idle managed engineers/processes are required. Failure leaves a visible task
with a repository blocker; it never stashes, commits, or removes existing edits.

The durable exclusive lease coordinates managed engineer turns and process
starts. Other managed turns in that checkout are blocked, including caller turns;
transcript inspection and stop controls remain usable. Both normal and background
engineer lanes participate. Symlink aliases and overlapping directories cannot
bypass the lease. This is host coordination, not an OS sandbox: other editors and
unmanaged processes are outside its enforcement boundary.

At handoff the worker and managed processes must be idle/stopped. The host records
HEAD/branch changes, bounded changed-file status and an evidence fingerprint,
then releases ownership before queuing caller review. Changes are evidence to
review, not proof of which actor authored them. A missing checkout or oversized
evidence is retained as an explicit handoff issue and does not strand a stopped
owner. There is no silent timeout or takeover after restart. After stopping work,
explicit `agent_task.close` with `status: waiting` releases ownership without
accepting the result; active processes still block release.

The lease is opt-in for compatibility. Legacy tasks keep their existing behavior
but cannot start a conflicting turn against a leased checkout. A first acquisition
requires a clean baseline. A correction run may reacquire a dirty checkout only at
an authorized boundary, described below. Workers are instructed to return edits
without committing and to leave no write-capable processes running.

### Correction baselines

A structured task whose current revision carries a `changes_requested` review may
reacquire write ownership over the dirty checkout the caller reviewed. Admission
compares the observed evidence fingerprint against an authorized starting state:
the fingerprint recorded at that revision's handoff, or a boundary the host
captured explicitly after the caller made its own fixes. Any other state fails
visibly with the expected and observed fingerprints and leaves every edit in
place; nothing is stashed, reset or silently adopted. A capture is tied to the
exact reviewed run, so it cannot be replayed once that run has advanced. An
accepted result authorizes no further correction, and a clean acquisition clears
any captured boundary. Capture is a read: it refuses to run while a worker holds
ownership or another managed writer is active in that checkout.

## Agent workflow

For a request such as creating a project, the embedded agent:

1. Calls `list_control_capabilities` with `domain: "project"`.
2. Selects `project.create_and_start_engineer`.
3. Calls `describe_control_capability` to load only that capability's exact
   input schema.
4. Calls `propose_control_operation` with schema-matching arguments and a stable
   caller `request_id`.
5. Stops the turn and tells the user that LCR is waiting for confirmation.
6. On a later user turn, calls `get_control_operation` with the returned
   operation id.

The generated `runtime` skill teaches this sequence. It contains the workflow,
not the capability schemas; the registry remains the schema source of truth.

## Linked worktree removal

Agents can remove a tracked linked worktree with `worktree.remove` through the
same four control tools. Discover `domain: "worktree"`, describe
`worktree.remove`, then propose `{"worktree_path":"/absolute/path/to/worktree"}`
with a stable request ID. Resolve the exact path with project queries first;
the operation never infers a target from a branch name or the TUI selection.
Each proposal targets one worktree and requires operator confirmation, including
when project collaboration is enabled. A proposal alone deletes nothing.

After confirmation, the host runs the existing removal service asynchronously.
This permanently deletes the directory and all its contents, including dirty,
untracked, and ignored files, without creating a recovery archive. The service
protects primary checkouts, shared Git stores, symlink targets, and mounted
filesystems. It removes the target's Git registrations (including owned nested
submodule registrations), clears TODO work/session links and LCR cached state,
and immediately removes the row and refreshes the project list when complete.
Branches, global conversation history, and TODO done/open state are retained.
Idle embedded sessions close; active engineers and runtimes are not stopped and
can recreate files. Stop ongoing work before requesting cleanup when appropriate.

`get_control_operation` returns the terminal status and a structured `worktree`
receipt with `worktree_path`, `root_path`, `worktree_removed`, and
`idle_session_closed`. Failure reports the underlying error; it never claims
removal completed. A tracked checkout that is already missing can still use
this operation to clean up Git and LCR state.

Deleting a directory outside LCR has no immediate TUI notification. The next
scan reconciles missing checkouts and clears their TODO work links. Leftover or
recreated directories can remain visible as orphaned worktrees. Prefer the
dedicated operation for prompt display updates and coordinated cleanup.

## Session-to-session handoffs

An embedded engineer can hand work to another embedded engineer through the
same confirmed control path. The sender first uses `project.search` and
`project.session_list` (or `project.detail`) to identify the receiving project,
provider, and current session, then proposes `engineer.send_prompt` with
`session_mode: resume_or_new`.

When the sender knows the receiving session, it supplies `target_session_id`
and the matching explicit `provider`. Exact targeting works for Codex,
OpenCode, Claude Code, and LCAgent. LCAgent accepts both its stable logical
thread id and the run ids belonging to that thread.

After confirmation, the host writes the message to SQLite before attempting a
provider call. It resumes an exact idle target and starts a turn. An eligible
active Codex turn can be steered; active OpenCode, Claude Code, and LCAgent
targets remain queued until idle. If the inspected target has been replaced or
can no longer be resumed, LCR fails the message instead of delivering it to the
replacement. The originating control operation remains `running` while a
message is queued and becomes terminal only after delivery or terminal failure.

This gives handoff documents a delivery path: the document can hold the full
context, while the control message tells the receiving engineer what to read
and what outcome to pursue. The sender does not need to ask the operator to
copy that instruction manually. By default, sending requires LCR confirmation.

### Delegated-task return addresses

Task creation captures the caller's host control key separately from its provider
conversation ID. LCR persists a binding scoped to the exact worktree, provider,
and control key when the embedded session announces its identity. Native LCAgent
binds its stable thread before invoking controls. A binding cannot be overwritten
by a newer session in the same worktree.

If the identity has not arrived, the result stays on the visible task with an
explicit delivery status. A late binding queues the result without requiring a
list refresh; persisted bindings and queued callbacks survive restart. Delivery
still uses the existing exact-target mailbox and confirmation policy. Legacy
records are only repaired from exact recorded identity; ambiguous queued attempts
are retained as failures and require explicit redelivery to a verified caller.

### Dispatched-engineer reports

When an embedded session's `todo.create_worktree_and_start_engineer` (or
`project.create_and_start_engineer`) launches an engineer, LCR records an
`engineer_dispatches` row: the worker's worktree, provider, and session, plus the
caller's worktree, provider, and control key. This lets one session hand out
work across several worktrees and hear back without polling.

Reports are request-scoped. The launch turn is armed. Each later
`engineer.send_prompt` from the same caller control key that is delivered to that
worker re-arms it. When an armed worker turn settles, LCR stores a report with the
worker's bounded final message (or the last error when there is none), the TODO,
the branch, and the exact follow-up target, then queues it through the mailbox to
the caller's exact session. Turns the operator starts in the worker, messages
from other sessions, and LCR's own callbacks do not arm reports. Launches from
the TUI or Chat create no row; those surfaces already show completion.

Caller identity uses the same bindings as delegated tasks. If the caller has not
announced its provider session yet, the report waits with a visible
`reply_error` and is queued when the binding arrives. A report does not authorize
merges, pushes, or worktree removal.

### Approve collaboration once per project pair

At an eligible engineer handoff, open review with **Ctrl+G**. **Enter** sends
only that message. **A** approves collaboration between the displayed projects
and sends the message. This permission is bidirectional, persists in SQLite,
and applies to future sessions in those two exact project paths. It does not
implicitly include sibling worktrees, renamed folders, or other projects.

Open **`/collab`** from the dashboard or either embedded project to see its
trusted peers; select a peer and press **R** to revoke. Revocation affects
unapproved/future messages, including requests claimed but not yet authorized;
it does not cancel messages already authorized for delivery or stop running work.

Automatic delivery requires `engineer.send_prompt`, `resume_or_new`, an explicit
provider and exact `target_session_id`, and no model selection or TODO-based
worktree redirect. Fresh launches, model changes, TODO redirects, other project
pairs, Help Chat proposals, and other control capabilities still ask normally.
Agents cannot create or revoke grants through the control catalog. Grant creation
and approval of the first message are atomic; operations retain
`confirmation_by: project_collaboration` as audit evidence.

Trusted messages pass unrelated pending confirmations through the existing
durable mailbox, retaining exact-session validation and delivery receipts.
The proposal/status result reports `automatic_delivery: true` and
`requires_new_user_turn: false` while an approved message awaits execution.
Agents should continue independent authorized work instead of asking again;
queued is not delivered, so use `get_control_operation` for a delivery receipt.
Avoid acknowledgment-only reply loops. Collaboration permits coordination and
continuation of work the user has already authorized, not an expansion of task
scope or an override of explicit stops and other action approvals.

Restart LCR on the updated build to use this policy; reconnect existing external
provider sessions to refresh their MCP descriptions and generated runtime skill.

## Architecture

The `integrations` domain exposes `integrations.manage` through the same
confirmation path, with explicit provider/scope and an inspected configuration
revision. It covers skill installation/visibility/removal, supported native
plugins, MCP configuration, and separate connection checks. Help Chat and all
embedded providers can propose these actions. See
[Agent integrations](agent_integrations.md) for compatibility and activation.

```text
generated runtime skill                 embedded LCAgent
          |                                    |
          v                                    v
four MCP discovery/operation tools    four native control tools
          |                                    |
          +------------------+-----------------+
                             v
internal/control registry and strict invocation validation
                             |
                             v
                 SQLite control_operations queue
                             |
                             v
                       TUI confirmation
                             |
              +--------------+---------------+
              |                              |
              v                              v
     ordinary typed executor       SQLite engineer_messages mailbox
                                             |
                                             v
                           steer / exact resume / wait for idle
```

The isolated MCP process writes a `proposed` operation to SQLite. A background
TUI relay claims one proposal at a time and changes it to
`waiting_for_confirmation`. Arrival raises a persistent agent-request notice
without taking keyboard focus from the operator's current surface. The
operator presses `Ctrl+G` to deliberately open a TUI-owned modal showing the
capability-specific target and effects, including over an embedded Codex,
OpenCode, or Claude Code pane. The modal does not open or depend on Help Chat.
Only `Enter` after that explicit review transition moves the operation to
`running`; cancellation and the final execution result are written back for
the originating session to inspect. For `engineer.send_prompt`, a successful
confirmation receipt includes a mailbox message id and queued delivery state;
it is not a claim that the recipient has already received the prompt.

Follow-on host dialogs stay on that same surface. For example, confirming
`git.prepare_commit` opens the normal commit preview over the embedded session
and gives the preview input priority without hiding or closing the session.
Help Chat proposals reuse the same stateless structured-dialog renderer, but
Help Chat owns only proposals created by its own conversation. Its in-process
path does not enqueue an MCP operation: the typed invocation is returned to the
host model directly, then the same snapshot validation and explicit confirmation
rules apply.

A canceled or failed operation ends the originating agent's current write-side
workflow. The runtime result tells the agent to stop rather than retry the
operation or continue later mutations or external actions through shell or
another tool. A fresh proposal can be created on a later user turn.

The confirmed `engineer.send_prompt` capability can target Codex, OpenCode,
Claude Code, or LCAgent and can pin a known session id for every provider. If no
target id is supplied, LCR binds the currently selected known recipient when
possible. A Claude launch still passes through the normal `ANTHROPIC_API_KEY`
billing warning; enabling Claude in the control executor does not bypass that
operator acknowledgement.

Waiting confirmations are returned to `proposed` when a new TUI host starts, so
a restart does not strand the request. Mailbox deliveries interrupted after a
claim are returned to `queued` and retried against the same target. The
standalone web server does not claim operations because it has no equivalent
operator-confirmation or mailbox-dispatch surface.

Provider submission and the SQLite delivery receipt cannot be committed in one
transaction, so the crash-boundary guarantee is at-least-once: a host failure
after provider acceptance but before the receipt commit can repeat the prompt.
Every delivered prompt carries its stable LCR message id so the receiving agent
can recognize that narrow restart retry case as the same handoff.

## Authority and safety

Capabilities declare one of three cumulative authority scopes:

- `project`: actions bounded to a project, such as TODO and Git flows.
- `portfolio`: cross-project actions, such as project creation and delegated
  task lifecycle.
- `host`: application-wide settings.

LCR-managed embedded sessions receive `portfolio` authority. Host settings are
therefore absent from their catalog. The internal runtime-MCP CLI defaults to
`project` unless its launcher explicitly grants more.

Authority controls discovery as well as proposal validation. Capabilities require
explicit TUI confirmation, with the project collaboration and opted-in structured-task metadata exceptions. The operation id
is generated by LCR and injected as the invocation `request_id`; the caller's
optional `request_id` is an idempotency key scoped to its embedded session.
Reusing that key with different arguments is rejected.

This surface does not mean arbitrary access to LCR internals. It exposes every
action deliberately registered as a typed capability. It does not expose a
generic shell command, raw database access, TUI key injection, or an
unconfirmed mutation escape hatch.

## Adding capabilities

Add a new action to the internal control registry:

1. Define its name, typed input normalization, strict validation, output schema,
   risk, confirmation policy, host effects, domain, and authority scope.
2. Implement or reuse its TUI-hosted executor.
3. Add registry, MCP discovery/proposal, and confirmation-lifecycle tests.

No new MCP tool or extra generated-skill schema is needed. The new action
appears in compact listings and its full schema is loaded only when described.

## Performance model

The always-visible cost is four small tool definitions plus the short skill
metadata. Capability summaries are returned only after a list call, and one
full schema is returned only after a describe call. This keeps the design
portable across MCP clients.

If a model host supports native deferred tool loading or tool search, the same
MCP namespace can additionally be marked deferred by that host. That is an
optimization of the first discovery step, not a correctness dependency and not
a replacement for the registry, authority checks, confirmation, or durable
operation lifecycle.

## Structured task results and caller review

Set `structured_results: true` on `agent_task.create` to opt into this contract.
The confirmed creation authorizes bounded metadata submission by the exact worker
and review by the exact originating caller. It does not authorize correction
turns, commits, pushes or deletion. Legacy tasks retain their free-text handoff.

- `agent_task.submit_result`: exact `task_id`, current `run_id`, outcome
  (`ready_for_review`, `blocked`, `failed`), short summary, optional criteria,
  checks, base revision, changed files, risks and questions. Checks name command,
  working directory, outcome and exit code. Payloads have schema-enforced bounds.
- `agent_task.review_result`: exact current `revision`, `accept` or
  `changes_requested`, summary and independent caller checks. Acceptance requires
  a ready result and idle handoff with repository ownership released. An explicit
  host operator review is also supported. Workers cannot approve themselves.

These two metadata capabilities execute immediately through the shared control
executor. Their receipt has no queued operation ID; do not poll
`get_control_operation`. Identical submissions/reviews are idempotent by run or
revision; conflicting replacements are rejected. Actor identity comes from the
host connection, never tool arguments. Historical claims/reviews survive restart.

The worker ends its turn after submitting. The host verifies idle, captures and
releases repository ownership, then queues one revision-tagged callback. It waits
for the original caller to be live and idle, rechecks the revision immediately
before submission, and never starts a missing caller or steers an active turn.
Explicit worker/caller stop cancels pending callbacks; late worker results remain
inspectable without reviving automatic review. Free-text-only endings become
`unclassified`. Task phases and concise evidence counts remain visible in the TUI.

Review rejection does not itself launch work. `agent_task.continue` creates a new
run that resumes the same worker on the exact reviewed edits rather than a clean
checkout. `agent_task.close` cannot bypass structured acceptance.

### Correction grants

`agent_task.create` accepts optional `max_corrections` (0–3, requires
`structured_results`). The operator agrees to it in the same confirmation that
creates the task, and the dialog states it. It covers nothing but a correction:
the original caller, from its host-bound session, reopening this task with
`session_mode: resume_or_new`, an inherited provider and its saved model, while a
`changes_requested` review of the current revision is outstanding. A fresh
session, a named provider or model, another caller, a worker reopening itself, an
accepted or superseded revision, an archived or completed task, and a grant that
is spent or revoked all return to ordinary confirmation rather than failing.

A round is consumed atomically when the grant authorizes execution, so a
duplicate or replayed proposal cannot spend two. The grant survives new runs
without refilling. Any explicit stop revokes the remainder, and the operator can
revoke it from the task actions dialog without stopping the worker or touching
its edits. Task detail states how much of the grant is used and whether it is
live, spent or revoked. Grants never cover commits, pushes, deletion, model
changes or a second concurrent worker.
