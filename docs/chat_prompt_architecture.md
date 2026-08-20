# Chat Agent Architecture

Chat keeps a stable conversational and safety contract while loading Little
Control Room state, help, and control schemas only when a turn needs them.

## Help Chat runtime

Help Chat runs `internal/lcagent.AgentRuntime` in process. The runtime owns one
provider-neutral model/tool loop and receives the Chat backend and model selected
by the existing `boss_*` settings. It resets provider continuation state at each
independent Chat run, retains it across tool turns within that run, and serializes
overlapping runs so switching or interrupting sessions cannot cross-link provider
response IDs.

The host supplies an exact lean tool profile. General coding, shell, write,
browser, and generic MCP tools are not present.

| Tool group | Tools | Source of truth |
| --- | --- | --- |
| Persisted LCR state | `list_lcr_queries`, `describe_lcr_query`, `run_lcr_query` | `internal/agentquery` |
| Confirmable LCR controls | `list_control_capabilities`, `describe_control_capability`, `propose_control_operation` | `internal/control` |
| App help | `lookup_lcr_help` | generated `internal/helpmeta` corpus |
| Host-only inspection | current TUI, live processes, Chat-session recall, linked context, skills, Repository Scout | existing Chat query adapters |
| Durable goals | `propose_goal` | `internal/bossrun` normalization and host confirmation |

The progressive list/describe/run or list/describe/propose sequence keeps full
query and control schemas out of the always-visible tool definitions. Tool names
and argument schemas are exact; the model does semantic selection without a
keyword or regex gate.

For a simple app-usage question, the agent calls `lookup_lcr_help`. When the
generated topic has a direct answer, that local result may terminate the turn
without paying for a second synthesis request. This is how launch-time and
in-app recording commands remain discoverable even when a model does not recall
them.

## Progress and streaming

The reusable runtime emits UI-neutral lifecycle events for:

- model request start, periodic progress, completion, retry, and failure;
- tool start, completion, and failure; and
- final text.

Help Chat maps those events onto the existing Bubble Tea activity stream. A
provider call emits a visible start immediately and a heartbeat every two
seconds, so a slow request does not leave the overlay blank. Tool arguments and
provider internals are not rendered. The final answer is emitted once after the
host appends any evidence receipts.

## State, privacy, and evidence

The compact app-state brief and current Chat tail remain explicit conversation
messages. Long sessions retain the existing file-backed checkpoint and utility-
model compaction path.

Persisted state goes through the shared query executor:

- privacy mode off uses trusted host disclosure, including private categories;
- privacy mode on hides every private-category project without an originating-
  project exception; and
- raw events, arbitrary SQL, Help Chat transcripts, and repository files are not
  implicit query capabilities.

Live TUI state, Chat recall, exact linked transcript excerpts, processes, and
repository-file evidence use separately named host tools. Repository Scout keeps
its workspace-only read policy, durable trace, mechanical evidence ranges, usage
accounting, and host-appended route receipt.

## Control and goal boundary

The agent cannot execute a mutation. `propose_control_operation` validates one
typed invocation from the shared registry and returns a terminal host outcome.
The existing Chat UI then:

1. revalidates loaded/new-project assumptions against the fresh state snapshot;
2. renders the existing capability-specific confirmation dialog; and
3. executes only after explicit operator confirmation.

Prompt-bearing proposals preserve the user's source, metric, timeframe, scope,
negations, and exclusions in a lossless task packet. Existing semantic policy
review still distinguishes category organization, backlog TODO capture, and
repository work requested now. Goal proposals follow the same terminal handoff
and are normalized by `internal/bossrun` before the host presents them.

## Compatibility path

Non-Help Chat turns retain the older utility-router and structured-planner path.
That path remains model-based and schema-bound; `general` is its broad fallback.
Keeping it separate lets Help Chat adopt the reusable LCAgent loop without
changing unrelated Chat behavior or its established tests in the same step.

When adding a Help Chat capability, update the transport-neutral query or control
registry first when one exists. Add a host-only tool only for data that cannot be
represented by the persisted query contract, and keep confirmation, privacy,
and evidence enforcement in host code rather than prompt text alone.
