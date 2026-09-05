# Progressive Agent Query Surface

Little Control Room gives ordinary embedded agents a bounded view of persisted
project and portfolio state without copying Help Chat's full coordinator context
into every model turn. The stable surface has three tools:

| Tool | Purpose |
| --- | --- |
| `list_lcr_queries` | List query domains, or compact summaries for one exact domain. It does not return capability schemas. |
| `describe_lcr_query` | Return the strict input schema, output envelope, scope, freshness, and sensitivity metadata for one query. |
| `run_lcr_query` | Validate and run one described query with bounded structured arguments. |

Codex, OpenCode, and Claude Code receive these tools through the `lcr_runtime`
MCP server. LCAgent and Help Chat expose native tools with the same names over
the same transport-neutral registry and executor. The registry, not any
adapter, is the source of truth.

## Agent workflow

For a cross-project question, an embedded agent:

1. Calls `list_lcr_queries` without a domain to see the small domain index.
2. Calls it again with one exact domain such as `project` or `work`.
3. Calls `describe_lcr_query` for one returned query name.
4. Calls `run_lcr_query` with arguments matching that query's input schema.
5. Follows the opaque `next_cursor` only when more records are useful.

This keeps the always-visible model cost at three generic tool definitions. A
domain listing adds only compact summaries, and a full schema is loaded only for
the query the agent intends to run.

## Query catalog

| Query | Required scope | Result |
| --- | --- | --- |
| `portfolio.overview` | `portfolio` | Portfolio counts and highest-attention visible projects. |
| `project.list` | `project` | Attention-ordered visible project summaries. Project scope returns only the originating project. |
| `project.search` | `portfolio` | Project metadata and persisted-summary search, never transcript search. |
| `project.detail` | `project` | One structured project snapshot with bounded TODO, session, and assessment state. |
| `project.todo_list` | `project` | Bounded TODO state for one visible project. |
| `project.session_list` | `project` | Persisted session metadata without transcript text or artifact paths. |
| `assessment.list` | `project` | Bounded persisted session assessments. Portfolio scope can span visible projects. |
| `work.agent_task_list` | `portfolio` | Delegated task summaries and resource references. |
| `work.agent_task_get` | `portfolio` | One delegated task by exact id. |
| `work.goal_run_list` | `portfolio` | Durable goal-run summaries. |
| `work.goal_run_get` | `portfolio` | One durable goal run with a bounded newest-first trace. |
| `demo_recording.latest` | `project` | The active demo recording, or latest finalized package, with conditional package-path disclosure. |
| `integrations.list` | `project` (user inventory requires `portfolio`) | Bounded native skill/plugin/MCP inventory with source, supported actions, revision, and activation warnings. |
| `integrations.catalog` | `portfolio` | Curated skill sources or available native Codex marketplace plugins. |

The internal CLI defaults query access to `project` scope. LCR's managed
embedded-session launchers explicitly grant `portfolio` scope so an agent can
coordinate with other non-private projects when the user's task calls for it.
Scope controls discovery and execution: a query outside the caller's scope is
not listed and is rejected if invoked directly.

`demo_recording.latest` is intentionally available at `project` scope so a
lower-authority caller can discover that a recording exists without learning
where the package lives. LCR-managed embedded sessions receive portfolio scope,
so they receive the authorized package path. A host may instead bind a grant to
one exact recording because it was explicitly attached or because the operator
confirmed disclosure. Grants are host inputs to the executor, never caller
claims in `run_lcr_query` arguments.

## Disclosure and privacy

Portfolio scope is not unrestricted database access.

- Projects in a private category are hidden, including their summaries,
  assessments, TODOs, sessions, delegated tasks, and referenced goal runs.
- The originating project remains visible to its own embedded agent even when
  that project is private.
- Help Chat is a trusted host surface rather than an originating embedded
  project. With privacy mode off, it receives host visibility; with privacy
  mode on, every private-category project is hidden with no origin exception.
- A private delegated task is visible only when it references the originating
  project. Goal runs fail closed when a referenced project or task cannot be
  proven visible.
- Help Chat transcripts, raw session event payloads, transcript excerpts,
  artifact paths, arbitrary repository files, application settings, and generic
  SQL access are not query capabilities.
- Demo recordings can contain public state from several unrelated projects.
  Without portfolio scope or an exact host-provided attachment/confirmation
  grant, `demo_recording.latest` omits `package_path` and returns only an opaque
  `lcr://demo-recordings/<id>` resource URI, status, format version, timestamps,
  duration, frame counts, and any association visible under the ordinary
  project/privacy rules.

Several queries intentionally return user-authored or model-authored text such
as TODOs, summaries, assessments, task descriptions, or goal traces. Their
catalog metadata marks them as `content`; callers should request them only when
that content is relevant. Metadata-only session listings are marked
`metadata`.

## Freshness and bounds

Every result declares its freshness and includes an `as_of` timestamp. Most
declare `persisted_snapshot` and describe persisted state read by the query—normally the
SQLite snapshot—and do not claim to mirror transient in-memory TUI state or a
provider process between persisted updates.

Integration queries instead declare `configuration_on_disk` or `catalog_fetch`.
They do not assert running-session availability. Project visibility is checked
before reading integration sources; credentials and native MCP arguments are
omitted. See [Agent integrations](agent_integrations.md) for scope and limits.

The demo-recording query reads LCR's private discovery reference and the
package manifest at call time. An incomplete package is reported as active or
finalizing only while its recorded owner process is still alive; otherwise LCR
falls back to the latest finalized package. The duration of a live recording is
computed through the query's `as_of` time.

Collection queries default to 20 records and cap a page at 50. Continuations
use opaque cursors. Detail queries apply their own smaller limits, long text is
clipped, and goal traces are explicitly bounded. The executor never returns raw
session-event payloads as a shortcut around those limits.

## Architecture

```text
generated runtime guidance / LCAgent system prompt
                      |
                      v
          three list/describe/run tools
        /              |                \
 runtime MCP     LCAgent native    Help Chat native
        \              |                /
                      v
       internal/agentquery registry + executor
                      |
                      v
       bounded reads from persisted LCR state
       + private demo-recording references/manifests
```

The query surface is read-only. Mutations remain in the separate progressive
[Agent Control Surface](agent_control_surface.md), where typed operations are
queued for explicit TUI confirmation.

## Adding a query

1. Add one named capability to `internal/agentquery` with its domain, minimum
   scope, sensitivity, freshness, strict input schema, and documented output
   envelope.
2. Implement the bounded executor using the transport-neutral reader interface.
3. Apply privacy filtering before pagination and record serialization.
4. Add registry, disclosure, pagination, MCP-adapter, and native-adapter tests as
   appropriate.

No new transport tool is needed. The query appears in compact domain discovery,
and its schema remains deferred until an agent describes it.
