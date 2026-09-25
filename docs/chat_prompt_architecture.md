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

Timeouts identify the last model/tool stage instead of asserting a backend
connection failure. Completed-call usage and retrieved source receipts survive
an interrupted run. Final result delivery uses the host cancellation context,
so exhausting the reply deadline does not discard that evidence. Token counts
reset for each submitted reply instead of showing the previous successful turn
beside a new failure.

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

### Saved Chat history

Inside the Help Chat overlay, `/sessions [session-id]` opens the existing saved
session picker or one exact conversation locally, without inference. Switching
waits until the current reply has finished or the user has stopped it.

`search_chat_sessions` returns bounded discovery results from
`help-chat-sessions/`, followed by legacy `boss-sessions/`. Short matching
messages remain whole, including project references above the matching line;
long previews explicitly report truncation. Search envelopes retain the source
directory, session ID, message index and timestamp.

`read_chat_session` reads the original exchange from those exact references.
It returns labelled turns with a bounded character/message budget and an exact
continuation for long messages. `include_events=true` explicitly includes nearby
log/flow receipts, allowing historical draft and artifact references to be
recovered without adding events to ordinary conversational context. Both
history tools refuse mixed transcripts in privacy mode.

Each read appends a host-authored source citation to the answer. The citation is
saved in the ordinary Markdown transcript, survives reloads, and is available to
follow-ups; the compaction prompt preserves its exact source references. The
model is instructed to open exchanges before drawing historical conclusions,
distinguish old assistant claims from verified current state, and never turn an
inventory search miss into a claim that work was Chat-only or deleted.

Worktree-removal receipts, like engineer lifecycle receipts, are saved as log
events and displayed in `/log`, outside ordinary Chat recall.

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
