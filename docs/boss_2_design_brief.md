# Boss 2.0 Design Brief

Boss 2.0 is the next architecture direction for Little Control Room's high-level assistant.

The current Boss Chat control path proved that a model can gather project state, propose typed actions, ask for confirmation, and execute through the TUI host. It also exposed a ceiling: a single structured action per turn makes Boss feel like a command palette with prose instead of an autonomous work coordinator.

This brief defines the larger target before implementation starts.

## North Star

Little Control Room should become a persistent control plane for AI work.

Boss is the chief operator for that control plane. Codex, OpenCode, Claude Code, and future providers are engineer harnesses that can perform bounded specialist work, but Boss owns the cross-project reality:

- what the user is trying to accomplish
- which projects, tasks, sessions, processes, worktrees, and TODOs matter
- what is stale, blocked, risky, or ready to close
- what work should be delegated
- what authority the user has granted
- whether the result was verified

Boss should accept objectives and carry them through as durable goal runs, not merely answer chat turns or emit one-off control invocations.

## Why The Current Shape Is Not Enough

The existing control surface is intentionally narrow:

```text
user message -> read-only query loop -> one control proposal -> confirmation -> one host action
```

That is safe and understandable, but it breaks down for real operator tasks:

- The user often asks for an outcome, not an action.
- Cross-project work requires judging many simultaneous trains of work.
- Cleanup, triage, and coordination often involve a set of resources.
- Approval should usually cover a meaningful risk boundary, not every tiny primitive.
- Boss needs to verify the state after acting before saying the goal is done.

Adding more special capabilities such as `archive_many`, `continue_many`, or `close_stale_agent_tasks_except_active` would only postpone the problem. The deeper fix is a goal runtime with composable primitives.

## Design Principles

- Boss owns the run. Specialist engineer sessions help, but Boss remains accountable for planning, permission scope, verification, and the final report.
- Goals are first-class. A user objective becomes a durable `goal_run` with state, plan, trace, authority, and done criteria.
- Plans are executable data. The model emits a compact plan IR that the app can validate, preview, execute, trace, pause, resume, and verify.
- Capabilities stay primitive. The control layer should expose general verbs over typed resources, not an ever-growing set of edge-case commands.
- Authority is scoped. Human review approves a clear scope of side effects and risk, then the runtime can execute until it reaches a new boundary.
- World state is externalized. Boss should reason over a maintained graph and attention index instead of rereading a giant prompt.
- Every decision is inspectable. The user and developer should be able to see why Boss thought something was stale, risky, done, or blocked.
- Start overqualified. Use a strong helm model first, then reduce cost with traces and evals after the system works.

## Core Concepts

### GoalRun

A durable unit of user intent.

Suggested fields:

- id
- title
- objective
- originating Boss Chat session and user message id
- status: `draft`, `observing`, `planning`, `waiting_for_approval`, `running`, `waiting`, `verifying`, `completed`, `failed`, `canceled`
- scope: resources, exclusions, and allowed action domains
- success criteria
- assumptions
- uncertainty notes
- current plan version
- authority grant, if any
- trace pointer
- created, updated, completed timestamps

Examples:

- "Clear stale delegated agents that have served their scope."
- "Figure out which active project needs my input next."
- "Coordinate a fresh verification before release."
- "Find and stop stale dev servers that are no longer attached to active work."

### World Model

A cached graph of operational reality.

Initial node kinds:

- project
- worktree
- engineer session
- agent task
- TODO
- process
- port
- runtime command
- file or artifact
- Boss goal run
- user decision

Initial edge kinds:

- owns
- attached_to
- spawned_by
- blocks
- depends_on
- touched
- mentions
- supersedes
- verified_by

The world model does not need to be perfect before Boss 2.0 starts. The first slice can build a focused view over existing project summaries and agent tasks, then expand.

### Attention Index

Boss needs portfolio-level attention management. The index ranks trains of work by operational relevance, not by raw recency.

Suggested attention signals:

- waiting for human
- stale but open
- active and quiet too long
- completed but not closed
- blocked by process, port, worktree, or provider state
- risky repo hygiene
- failed previous operation
- explicit user priority
- low-confidence classification

The model should see a compact set of ranked items plus targeted detail retrieval, not every artifact every turn.

### Plan IR

The plan IR should be expressive enough for dynamic orchestration but small enough to validate.

Initial step kinds:

- `observe`: gather structured state for resource selectors
- `classify`: assign labels with evidence and confidence
- `select`: choose target resources from classified candidates
- `propose`: prepare an authority request for the user
- `act`: execute one primitive capability against one or more resources
- `delegate`: create or continue an engineer task
- `await`: pause for worker output, user input, or time
- `verify`: check a done predicate against refreshed state
- `branch`: choose the next step from structured conditions
- `report`: summarize outcome, changes, failures, and remaining work

The executor, not the model, should enforce type checks, risk policy, idempotency, and resource existence.

### Capability Registry

The current `internal/control` vocabulary remains useful, but it becomes the primitive action layer beneath goal runs.

Each capability should declare:

- name
- resource kinds it can touch
- verb and side effect summary
- input and output schemas
- risk level
- approval policy
- idempotency key behavior
- retry safety
- rollback or compensation note
- preview renderer
- result schema

Capabilities should be boring and general:

- inspect projects, sessions, agent tasks, processes, worktrees, and TODOs
- update agent task status
- archive internal records
- create or continue an engineer task
- send a prompt to an engineer session
- start or stop managed runtimes
- run tests
- inspect or stop processes

The dynamic behavior should come from the plan runtime composing these primitives.

### Authority Grants

An authority grant is what the user approves.

Example:

```text
Archive 5 stale delegated agent records:
- agt_a
- agt_b
- agt_c
- agt_d
- agt_e

Do not close live engineer sessions.
Do not delete files or workspaces.
Report any task that is no longer archiveable.
```

The grant should be structured:

- approved resources
- allowed capabilities
- forbidden side effects
- max risk level
- expiration condition
- approval message id and timestamp

The executor can then run multiple primitive actions under one approval until it hits a new risk boundary.

## Runtime Loop

One Boss goal run should follow this loop:

1. Interpret the user objective into a draft goal.
2. Build or refresh the relevant world-model slice.
3. Ask the helm model to plan or replan using structured output.
4. Validate the plan against available capabilities and policy.
5. If side effects need approval, present a scoped preview and pause the same run.
6. Execute approved primitive steps, recording trace entries.
7. Refresh state after side effects.
8. Verify success criteria.
9. Replan on partial failure, changed state, or low-confidence evidence.
10. Report completion, residual risk, and any remaining next action.

Approval pauses are not new chat turns. They are resumable run states.

## Model Strategy

Model choice should be a role configuration, not a hard-coded assumption.

Initial roles:

- `boss_helm`: high-quality portfolio reasoning, planning, risk calls, and ambiguous user intent
- `boss_deep_judge`: harder arbitration, release safety, destructive or high-uncertainty plans
- `bulk_classifier`: cheap structured classification over many items
- `verifier`: fresh check of done predicates and risky claims
- `narrator`: concise user-facing report when the helm does not need to write it

Default posture:

- Start with a strong helm model, such as GPT-5.5 with low or medium reasoning effort.
- Use cheaper models for bulk item classification once traces show the task is routine.
- Escalate when confidence is low, the action is high-risk, or evidence conflicts.
- Keep providers swappable so roles can later point to GPT, Kimi, DeepSeek, local models, or another backend.

## Multi-Project Cognition

Boss has an extra problem that a single-repo agent does not: it must judge multiple trains of thought at once.

Do not solve this by dumping all context into one model call. Solve it with externalized cognition:

- per-train working memory
- a global attention index
- independent classification passes
- synthesis over ranked candidates
- explicit uncertainty and evidence age
- focused retrieval before action

The helm model should make judgment calls with instruments, not from an undifferentiated inbox.

## Walking Skeleton

The first implementation slice should prove the architecture with one concrete scenario:

```text
User: Clear stale agents that have served their scope.
```

Expected Boss 2.0 behavior:

1. Start a `goal_run`.
2. Observe open, waiting, completed, and recently archived agent tasks.
3. Classify each candidate as `archive`, `keep`, or `needs_review`, with evidence and confidence.
4. Propose one cleanup authority grant for the selected archive set.
5. On approval, archive each selected task using primitive update/archive actions.
6. Refresh agent task state.
7. Verify selected tasks no longer appear in active agent-task listings.
8. Report what was archived, what was kept, and any partial failures.

This is not a special `archive_many` feature. It is the smallest end-to-end proof of observe, classify, plan, approve, act, verify, report.

## Benchmark Scenarios

Boss 2.0 should be evaluated against scenarios before it grows more powers:

- Clear stale delegated agents safely.
- Summarize all active work and choose the next best human decision.
- Detect which project or agent task is waiting for the user.
- Continue a stale agent task, wait for its output, then close or reassign it.
- Verify whether a release/deploy is safe using fresh evidence instead of summaries.
- Notice and resolve a stale dev server or suspicious process across projects.
- Recover from a partial failure while executing a multi-step cleanup plan.
- Explain why it made a portfolio decision, with evidence and confidence.

Each scenario needs a replayable fixture or mocked state, an expected trace shape, and an expected final report contract.

## Migration Path

Phase 0: Design and fixtures

- Add this brief.
- Add fixture-driven benchmark scenarios.
- Define the initial GoalRun and Plan IR types.
- Keep the existing Boss Chat and control surface working.

Phase 1: Stale agent cleanup skeleton

- Add a goal-run executor for the stale-agent cleanup scenario.
- Use current agent-task storage and archive service calls.
- Add scoped approval for the whole cleanup plan.
- Trace every step in memory or transcript-friendly event records.

Phase 2: Durable run storage

- Persist goal runs, plan versions, authority grants, trace entries, and outcomes.
- Resume interrupted runs after app restart.
- Show run status in Boss Chat and the dashboard.

Phase 3: General portfolio orchestration

- Add the attention index.
- Add per-train working memory.
- Let Boss choose among cleanup, delegation, verification, and reporting modes.

Phase 4: Broader capability composition

- Expand primitive capabilities only as needed.
- Add process, runtime, worktree, TODO, and test-run actions under the same policy model.
- Add provider-neutral worker delegation and verification.

## Relationship To Existing Control Surface

`docs/boss_control_surface_plan.md` remains relevant as the primitive capability layer.

Boss 2.0 adds a higher layer:

```text
GoalRun runtime
  Plan IR
  Authority grants
  Trace and verification
  World model and attention index
    internal/control primitive capabilities
      service/session/store/TUI host APIs
```

Do not keep adding prompt rules that force the model to choose exactly one action for multi-resource objectives. That was useful for the first control MVP, but it is the behavior Boss 2.0 is meant to replace.

## Open Questions

- Should the first GoalRun storage be SQLite immediately, or an in-memory run plus transcript trace while the IR settles?
- Should plan generation be one helm call, or a classifier-plus-helm pipeline from the first slice?
- How should authority grants be rendered in compact terminal layouts?
- Which provider should be the default `boss_helm` backend when configured options differ in cost and quality?
- How much of this should eventually be exposed as MCP so external agents can inspect or drive LCR safely?
