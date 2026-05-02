# Boss Control Surface Plan

This document defines the first planning pass for a generalized control surface that Boss Chat can use to act on Little Control Room without becoming tightly coupled to every TUI feature.

The goal is to let Boss Chat coordinate project work, especially embedded engineer sessions, through typed capabilities instead of hard-coded UI behavior.

## Goals

- Give Boss Chat a stable, discoverable way to request actions.
- Keep protocol terms independent from Go package names and TUI implementation details.
- Treat Codex, OpenCode, and Claude Code as provider choices under one engineer-session domain.
- Support confirmation, auditability, async progress, and retries for mutating actions.
- Let future local APIs or MCP transports wrap the same internal control layer.

## Non-goals

- Exposing arbitrary shell execution as a generic Boss Chat capability.
- Replacing the TUI command system in the first pass.
- Making Boss Chat know how each TUI dialog works internally.
- Implementing every existing slash command as a capability before proving the first vertical slice.
- Depending on keyword or regex intent heuristics to choose actions.

## Design Direction

Little Control Room should have an internal control layer that sits below Boss Chat and above service/session APIs.

Boss Chat should be one client of this layer. The classic TUI, future HTTP or Unix socket APIs, and a possible MCP server can also become clients or adapters later.

Suggested package direction:

```text
internal/control
  capability registry
  invocation validation
  operation lifecycle
  confirmation metadata
  provider/resource vocabulary
```

The internal Go interface should be the source of truth. MCP, if added, should be a transport adapter over the same control layer rather than the core abstraction.

## Vocabulary

### Capability

A named action that Little Control Room can perform.

Examples:

- `engineer.send_prompt`
- `project.add_todo`
- `todo.create_worktree`
- `runtime.start`
- `project.snooze`

Each capability should declare:

- name
- description
- input schema
- output schema
- risk level
- confirmation policy
- provider support when relevant
- whether it is headless or requires a UI host
- whether it returns synchronously or starts an async operation

### Invocation

A request to run a capability with typed arguments.

Invocations should accept an optional `request_id` so mutating calls can be idempotent across model/tool retries.

### Operation

A tracked execution of an invocation.

Operations should include:

- operation id
- capability name
- status
- target resource references
- normalized arguments
- requested by
- confirmation state
- started/completed timestamps
- result or error

Suggested statuses:

- `proposed`
- `waiting_for_confirmation`
- `running`
- `completed`
- `failed`
- `canceled`

### ResourceRef

A stable reference to something Little Control Room knows about.

Examples:

```json
{"kind":"project","path":"/path/to/project"}
{"kind":"engineer_session","project_path":"/path/to/project","provider":"opencode","session_id":"ses_123"}
{"kind":"todo","project_path":"/path/to/project","id":42}
{"kind":"agent_task","id":"agt_20260502T091500_ab12cd34ef"}
{"kind":"process","pid":49995,"label":"ts-node-dev"}
```

## Agent Task Threads

Project paths should not be mandatory for every delegated action.
Boss Chat needs a lightweight venue for temporary work that may take several turns but should not become a dashboard project.

Use `agent_task` as the durable envelope Boss owns:

- id
- title
- kind: `ephemeral`, `project`, `scratch_task`, or `system_ops`
- status: `active`, `waiting`, `completed`, or `archived`
- summary / rolling brief
- current provider and engineer session id, if any
- workspace path, when the task needs a small managed directory
- related resources such as projects, PIDs, ports, files, TODOs, or engineer sessions
- created, touched, completed, archived, and expiry timestamps

Engineer sessions attach to an agent task; they are not the task itself.
Boss can continue the same task with the same provider session, start a fresh provider session under the same task, or close/archive the task once the work is done.

Venue guidance:

- `project`: normal repo/project work.
- `scratch_task`: durable folder-backed task already visible in the dashboard.
- `ephemeral`: temporary multi-turn task, hidden from the project list by default.
- `system_ops`: host/LCR operation or investigation with no natural repo.

Ephemeral and system-ops task workspaces live under Little Control Room app data internal workspaces and are treated as managed internal paths so scanning does not promote them to normal projects.
Completed ephemeral tasks should remain recallable through a short summary, then be auto-archived or purged by later lifecycle code.

## Provider Model

The protocol should not expose internal package names such as `codexapp`.

Use the domain name `engineer` for embedded AI coding sessions.

Provider values:

- `auto`
- `codex`
- `opencode`
- `claude_code`

`auto` means Little Control Room chooses the provider using the same project/user preference logic the TUI would use.

The control layer should report provider availability and feature support separately from action support. A disabled provider can still exist in the schema.

Example capability metadata:

```json
{
  "name": "engineer.send_prompt",
  "providers": [
    {
      "id": "codex",
      "available": true,
      "features": ["send_prompt", "resume", "force_new", "approval_response", "review", "compact"]
    },
    {
      "id": "opencode",
      "available": true,
      "features": ["send_prompt", "resume", "force_new", "approval_response"]
    },
    {
      "id": "claude_code",
      "available": false,
      "reason": "disabled",
      "features": ["send_prompt", "resume", "force_new"]
    }
  ]
}
```

## Headless vs Host Actions

Some actions can run mostly headlessly through service/session APIs:

- add a TODO
- mark a TODO done
- create a TODO worktree
- send a prompt to an engineer session
- start or stop a managed runtime
- snooze or mark a project read

Some actions are UI-hosted:

- reveal an embedded session
- open a model picker
- open a diff view
- focus a pane
- show a confirmation dialog

Capabilities should declare whether they require a host:

```json
{
  "requires_host": false,
  "host_effects": ["may_reveal_engineer_session"]
}
```

Boss Chat can request a host effect, but the TUI host decides whether and how to perform it.

## Confirmation And Risk

Every mutating action should flow through a confirmation-aware operation lifecycle by default.

Risk levels:

- `read`: no mutation
- `write`: changes Little Control Room state or sends work to an engineer
- `external`: may execute project/runtime/provider-side effects
- `destructive`: deletes, discards, forcefully stops, or removes state/files

`engineer.send_prompt` should be treated as at least `external`, because the engineer may edit files or run tools under its own approval model.

Boss Chat should produce a user-facing preview before execution:

```text
Send this to OpenCode in Little Control Room?

Please fix the failing tests and report what changed.
```

## Initial Vertical Slice

The first implemented capability should be:

```text
engineer.send_prompt
```

Suggested input schema:

```json
{
  "request_id": "optional-stable-id",
  "project_path": "/path/to/project",
  "project_name": "",
  "provider": "auto",
  "session_mode": "resume_or_new",
  "prompt": "Please fix the failing tests and report back.",
  "reveal": false
}
```

Allowed `session_mode` values:

- `resume_or_new`
- `new`

Suggested result schema:

```json
{
  "provider": "opencode",
  "project_path": "/path/to/project",
  "session_id": "ses_123",
  "reused": true,
  "prompt_sent": true,
  "revealed": false,
  "status": "Prompt sent to OpenCode"
}
```

Execution outline:

1. Boss resolves the target project using existing read-only tools.
2. Boss proposes `engineer.send_prompt` with typed arguments.
3. The control layer validates and normalizes the invocation.
4. The TUI host presents confirmation.
5. On approval, the host opens or reuses the embedded engineer session and submits the prompt.
6. The operation result is appended back into Boss Chat.

OpenCode is the best first provider to test because it is currently available for local verification. Codex should use the same protocol path. Claude Code should remain a first-class provider value even while disabled.

## Later Capabilities

After `engineer.send_prompt`, add capabilities only when the control lifecycle has proven itself.

Likely next actions:

- `engineer.delegate_task`
- `agent_task.create`
- `agent_task.continue`
- `agent_task.close`
- `project.add_todo`
- `todo.mark_done`
- `todo.create_worktree`
- `todo.create_worktree_and_start_engineer`
- `process.terminate`
- `runtime.start`
- `runtime.stop`
- `project.snooze`
- `git.prepare_commit`

Keep each action typed and narrow. Do not add a generic "do anything in the TUI" capability.

## Boss Chat Integration

Boss Chat should keep its current read-only query loop for state gathering.

For actions, add a second structured path:

```text
read context -> propose control invocation -> confirm -> execute -> answer with operation result
```

The model must choose actions through structured output. Local code should validate schemas, permissions, provider availability, project existence, and confirmation state.

## Audit Trail

Each executed operation should be recorded in a durable place, eventually including:

- Boss Chat session id
- user message that requested the action
- normalized invocation
- confirmation decision
- linked project/session/TODO resources
- operation status and result

For the MVP, it is acceptable to append a concise operation result to the Boss Chat transcript and rely on existing project events for project-level mutations.

## Open Questions

- Should operation history live in SQLite from the first implementation, or start transcript-only and move to SQLite when more capabilities exist?
- Should the TUI host own all confirmations, or should `internal/control` expose reusable confirmation rendering data only?
- Should `engineer.send_prompt` default to reveal the session or keep it hidden unless the user asks?
- How should provider `auto` behave when a project has multiple recent engineer providers?
- How much of the existing slash command parser should be reused for future control capabilities?

## MVP Boundary

Do now:

- Define the internal control vocabulary.
- Implement `engineer.send_prompt` with `provider: auto|codex|opencode|claude_code`.
- Route execution through existing embedded-session APIs.
- Require confirmation before sending.
- Verify with OpenCode.

Do later:

- External MCP transport.
- Broad TUI command coverage.
- Persistent operation database.
- Destructive actions.
- Generic automation or shell execution.
