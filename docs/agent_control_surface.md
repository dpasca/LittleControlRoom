# Progressive Agent Control Surface

This is the write-side companion to the read-only
[Progressive Agent Query Surface](agent_query_surface.md). Queries return
bounded persisted state directly; controls queue typed operations for explicit
operator confirmation.

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
copy that instruction manually. Sending still requires the ordinary LCR
confirmation because the receiving engineer may edit files or invoke external
tools after the turn begins.

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

Authority controls discovery as well as proposal validation. Every currently
exposed capability still requires explicit TUI confirmation. The operation id
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
