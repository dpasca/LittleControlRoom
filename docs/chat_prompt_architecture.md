# Chat Prompt Architecture

Chat keeps its stable behavioral contract small and loads Little Control Room
state and control policy only when a turn needs them.

## Turn routing

The existing utility-model read-only router also returns a structured
`planner_domain`:

- `inspection`
- `project_work`
- `agent_task`
- `project_lifecycle`
- `settings`
- `git`
- `goal`
- `general`

This is semantic model routing. Do not replace it with keyword, regex, or
pattern gates. `general` is the compatibility fallback for genuinely broad
turns and for older or non-conforming structured responses.

The fast router receives the compacted same-session Chat summary and recent
conversation tail. It does not receive the current portfolio snapshot or TUI
view. Those are available through read-only queries.

## Scoped planner input

For a non-`general` domain, the main planner receives:

1. the stable Chat behavior and evidence contract;
2. the current conversation context;
3. read-only query results gathered for this turn;
4. an internal `control_reference` result for action-capable domains; and
5. a JSON schema containing only fields and action kinds relevant to the
   selected domain.

Current portfolio and TUI state are not ambient scoped-planner context. The
planner uses `current_tui`, `project_detail`, `todo_report`,
`agent_task_report`, `search_context`, `context_command`, `project_scout`, or
another read-only query when it needs current evidence or identifiers.

## Control reference

`control_reference` is assembled from `control.Capabilities()` so capability
name, description, risk, confirmation requirement, host effects, and input
fields retain one source of truth. Domain policy adds only routing distinctions
that the raw capability schema cannot express, such as:

- new repository versus work in a loaded project;
- new tracked work versus a same-task follow-up;
- project TODO versus delegated agent task;
- regular project archive versus scratch-task archive; and
- commit preview versus the later operator-confirmed commit or push.

The reference is internal planning evidence. It is shown as a tool event while
streaming, but it is excluded from user receipts and fallback answers.

## Safety boundaries

The following remain independent of optional context retrieval:

- every write/external control is a proposal requiring confirmation;
- `control.ValidateInvocation` normalizes and validates payloads;
- Chat validates model actions against the selected planner domain;
- the TUI validates loaded-project/new-project assumptions against the fresh
  state snapshot before presenting a proposal; and
- repository, deployment, migration, and external-current claims still require
  direct evidence.

When adding a capability, update the control registry first. Add domain routing
policy only when the capability schema and host validation cannot express the
choice. Add a new planner domain only when an existing domain would expose a
materially unrelated policy or schema surface.
