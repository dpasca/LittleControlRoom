# Claude Code Footprint Discovery

Date observed: 2026-03-31; context/compaction and structured async-task fields re-verified 2026-07-29; embedded stream input and permission callbacks re-verified 2026-08-13; Auto permission mode re-verified 2026-08-25 (Asia/Tokyo)
Host: macOS user-home environment

This document summarizes observed Claude Code on-disk artifacts and the detector assumptions Little Control Room currently relies on.

## 1. Storage locations

### User-home global state (primary)

Main directory:

- `~/.claude`

Observed key paths:

- `~/.claude/projects/<encoded-project>/<session-id>.jsonl`
- `~/.claude/projects/<encoded-project>/<session-id>/subagents/*.jsonl`
- `~/.claude/sessions/*.json`

### Temporary task output state

Observed background task output paths:

- `/tmp/claude-*/<encoded-project>/<session-id>/tasks/*.output`
- `/private/tmp/claude-*/<encoded-project>/<session-id>/tasks/*.output`

On this machine, Claude task output may appear under temp roots even when the parent session JSONL under `~/.claude/projects/...` is quiet.

## 2. Session file structure observed

Top-level session logs are JSONL with entries such as:

- `user`
- `assistant`
- `progress`
- `system`
- `queue-operation`

Useful stable fields include:

- `sessionId`
- `cwd`
- `timestamp`
- `subtype`
- `toolUseResult`
- `tool_use_result` in stream-JSON output
- `origin.kind`
- `uuid` / `parentUuid`
- `isMeta`
- `isCompactSummary`
- `promptSource`

Assistant records also expose `message.usage` counters:

- `input_tokens`
- `cache_creation_input_tokens`
- `cache_read_input_tokens`
- `output_tokens`

Usage is per API message, not a cumulative session counter. Claude can persist
multiple assistant records for different content blocks from the same API
response; those records share `message.id` and repeat the same usage object.
Session totals must therefore deduplicate by `message.id` before adding the
counters. The newest unique message remains the current-context/last-call
sample, while the deduplicated sum is the session total.

For context occupancy, Claude's current input is the sum of the first three
input/cache counters. The streamed `result.modelUsage` object supplies the
model's `contextWindow`; that runtime result is not assumed to be present in
the saved session JSONL.

The encoded project directory name under `~/.claude/projects` is derived from the project path, but project association should still come from session metadata such as `cwd`.

PID session metadata under `~/.claude/sessions/*.json` is useful for finding a still-open Claude CLI instance, but on its own it does not prove the latest turn is still running. An external terminal can stay open at the prompt after Claude has already finished the turn.

Current Claude Code PID metadata also exposes structured turn activity:

- `status == "busy"` while Claude is processing a turn
- `status == "shell"` while an external shell action remains active
- `status == "idle"` when the CLI is open at the prompt
- `statusUpdatedAt` for the current status interval

Little Control Room keeps a session read-only while another live Claude process owns it, but only `busy` and `shell` count as running work. The activity timer uses `statusUpdatedAt`, not the process-wide `startedAt`.

## 3. Generated user-role records

Claude Code can persist provider-generated records with `message.role == "user"`.
Local slash commands are one observed example:

- an `isMeta == true` user event starts the generated group
- command and local-output events follow as non-meta user events
- the group is linked in order through `uuid` / `parentUuid`
- actual submitted prompts identify their source with `origin.kind == "human"`
  or, when no non-human origin is present, a non-empty `promptSource` such as
  `typed` or `sdk`

Compaction is another example. Claude emits a structured `system` event with
`subtype == "compact_boundary"` and compaction metadata including `pre_tokens`
and `trigger`, then persists the generated summary as a user-role record with
`isCompactSummary == true`. The summary is model context, not a new human turn.
Context usage from before the boundary is stale and should remain unknown until
the next assistant usage record. Compaction does not reset historical session
totals: keep the deduplicated accumulator, clear only the current-context
sample, and combine the next post-compaction message with the earlier total.

Claude Code's stream-JSON/Agent SDK can also emit `rate_limit_event` records
with `rate_limit_info` for a changed five-hour, weekly, or model-specific limit.
Those events report utilization as a fraction and a Unix reset timestamp. They
are useful live updates but may describe only the limit whose state changed;
the authenticated claude.ai usage response remains the complete account
snapshot for the ordinary five-hour and seven-day windows. Treat account usage
as optional: it is absent for API-key/cloud billing and failures must not block
session rendering or input.

Transcript readers should follow those structured fields and event ancestry.
They should not identify local commands by matching the XML-shaped text stored
inside `message.content`; user-submitted XML-looking text is still conversation.

## 4. Structured async and subagent signals

Observed machine-readable signals for unfinished delegated work:

- Background shell tasks:
  - `toolUseResult.backgroundTaskId`
- Async agent launches:
  - `toolUseResult.isAsync == true`
  - `toolUseResult.status == "async_launched"`
  - `toolUseResult.agentId`
- Task completion notifications:
  - `user` entries with `origin.kind == "task-notification"`
  - `queue-operation` entries carrying `<task-notification>...</task-notification>` content
- Task stop receipts (`TaskStop` tool results):
  - `toolUseResult.task_id` with `toolUseResult.task_type` (stream JSON: `tool_use_result`)

A task ended with `TaskStop` gets stream `task_updated` (`patch.status ==
"killed"`) and `task_notification` (`status == "stopped"`) frames, but the
session JSONL records neither; the receipt is its only transcript evidence and
counts as `stopped`. Subagent transcripts omit `toolUseResult` for both the
launch and the stop. Verified on 2026-10-02 with Claude Code 2.1.284.

Observed completion statuses worth treating as terminal:

- `completed`
- `failed`
- `error`
- `errored`
- `cancelled` / `canceled`
- `interrupted`
- `stopped`
- `killed` (stream `task_updated.patch.status`)

`stopped` is how a later Claude process reports a background command for which
the previous process left no completion record. It is terminal for turn
detection, but it is not evidence that the command succeeded.

`Monitor` tasks have no complete transcript lifecycle. The launch result has
`toolUseResult.taskId`, `timeoutMs`, and `persistent`; each event is a
`<task-notification>` with an `<event>` and no `<status>`. A monitor whose
source exits reports `completed`, but one that expires is killed with only an
`<event>` notice. Transcript turn detection therefore ignores Monitor
launches, and a reload can read the parent turn as completed while monitors
run. For an LCR-owned stream, the stream's task events remain authoritative:
live views count its running tasks, or a parent turn the stream reports as
running, as active work despite that completed transcript state. Verified on
2026-10-01 with Claude Code 2.1.284.

## 5. Important detector implication

The parent Claude session JSONL may look done enough to misclassify a session even when work is still running elsewhere.

In particular:

- a turn may end with `system` `subtype == "turn_duration"` after launching a background task
- the most recent top-level entry may stop changing while nested `subagents/*.jsonl` keeps updating
- temp `tasks/*.output` files may continue changing while the parent log is idle

Because of that, latest-turn detection should not rely only on the final top-level JSONL entry type.

The same distinction applies to an embedded stream-JSON process. A `result`
record closes one model-response boundary; it does not prove that
provider-declared background work finished. Older/uninitialized headless runs
could exit after that result and clean up a background task before recording
its terminal notification. LCR must own the protocol lifecycle, not just keep
an arbitrary pipe open.

Conversely, the headless process can remain alive after the durable session
JSONL records an explicit terminal assistant stop, without emitting the matching
terminal boundary on stdout. In foreground compatibility mode, LCR periodically reloads the transcript and may
release its final submitted turn only when that verified terminal record is not
older than the locally captured submission. A terminal record from an earlier
turn must not close a newer prompt that has not reached the JSONL yet.

LCR uses initialized managed streams with Claude Code **2.1.284 or later**,
the minimum verified version for this integration. It enables
`CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1`, clears the background-task disable
setting, and sets `CLAUDE_CODE_FORK_SUBAGENT=1` so the main conversation's
subagents run in the background. The parent can accept follow-ups while its
workers remain owned by the same process. Native permission callbacks and
LCR's destructive-command hook remain installed.

Ownership is released only on a fresh `session_state_changed` `idle` event
when every submitted message has a terminal receipt and no tracked task or
permission request remains. `task_started`, `task_progress`,
`task_updated.patch.status`, and `task_notification` supply task evidence;
`background_tasks_changed` supplies positive ownership evidence but its
omissions do not finish foreground tasks. Explicit `ambient` housekeeping is
excluded. Late launch evidence cannot revive a terminal task; an explicit
new `task_started` can resume it. A task completion alone never closes stdin:
its notification may still wake another parent turn. Parent results, child
messages, and old disk snapshots cannot release that stream.

Older or unidentified executables retain
`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` and show an upgrade notice. Their
foreground turn recovery remains unchanged. If any owning process exits with
unresolved tasks, LCR reports lost ownership rather than claiming success.

Verified on 2026-09-30 with Claude Code 2.1.284: an initialized process accepted
and answered another input while a 15-second background shell task ran, then
processed its completion notification before returning to idle. The parent
reported idle between user turns while that shell task was still running;
both parent state and task ownership therefore matter.

## 6. Embedded stream input and permission callbacks

Claude Code's stream-JSON input mode accepts another user message while a
response is active. The message remains queued and runs as its own subsequent
turn. Little Control Room therefore writes a busy-session follow-up directly to
the existing stream; it must not synthesize an interrupt first. Only an explicit
`ctrl+c` or `/pause` action stops the active turn. Claude may persist canceled
tool results using denial-shaped provider text after an interrupt, so LCR records
the explicit interruption source and does not present those canceled tools as
individually denied by the user.

Each submitted message now has a UUID. `command_lifecycle.command_uuid`
correlates `queued`, `started`, `completed`, `cancelled`, `discarded`, and
`refused` receipts to that exact input; `--replay-user-messages` supplies an
acknowledgment fallback. Writing stdin only means **sent**, never processed.
Automatic task-notification results do not consume user messages. Pending
receipts become unconfirmed on unexpected process exit, or interrupted on an
explicit stop; LCR does not automatically resend them. Receipt history is a
bounded live-session projection, not a replacement for durable provider logs
or the engineer-message mailbox.

The TUI and mobile session surface show message delivery separately from
worker activity. Input waiting two minutes gets a warning. A running parent
with no parent event for five minutes gets a separate inactivity notice;
worker traffic cannot reset that clock. Neither condition claims a deadlock
or automatically stops work. Available parents waiting on background work
are not warned for silence.

Protocol references: [streaming input](https://code.claude.com/docs/en/agent-sdk/streaming-vs-single-mode),
[subagent foreground/background rules](https://code.claude.com/docs/en/sub-agents#run-subagents-in-foreground-or-background),
and the [official SDK changelog](https://github.com/anthropics/claude-agent-sdk-typescript/blob/main/CHANGELOG.md).

LCR creates a private, per-session Unix socket for permission modes that may
need interaction and registers `mcp__lcr_runtime__request_tool_approval` through
Claude's `--permission-prompt-tool` callback. The isolated runtime MCP process
forwards the exact `tool_name`, tool input, and `tool_use_id` to the owning
session. An approval returns the original input unchanged; a decline returns a
non-interrupting denial; canceling the dialog returns an interrupting denial.
`AskUserQuestion` uses the same bridge and returns the original question payload
with a structured `answers` object.

Current callback requests do not carry a durable approval scope, so Claude
approvals are deliberately one-shot and the TUI does not offer an invented
"accept for session" action.

Claude Code 2.1.241 was installed for the 2026-08-25 verification. Its
`--permission-mode` choices are `acceptEdits`, `auto`, `bypassPermissions`,
`manual`, `dontAsk`, and `plan`. Anthropic introduced Auto in 2.1.83. LCR now
uses a provider-specific `claude_permission_mode` setting and explicitly passes
the selected value to `claude -p`; this matters because print mode does not
simply inherit the interactive terminal's built-in default. The LCR default is
`auto`, independently of the Codex/OpenCode `codex_launch_preset`.
Existing users do not need to edit their config: a missing
`claude_permission_mode` resolves to `auto`, even when the existing
`codex_launch_preset` is `yolo`. Codex and OpenCode keep that preset, and the
next normal settings save materializes the new Claude key. The upgraded app
applies Auto to each newly opened or recovered Claude helper; only a helper
already running while the setting changes retains its original launch mode
until restart.

Auto is the best everyday fit for LCR's embedded Claude lane:

- Routine reads and ordinary working-directory edits proceed without a second
  classifier call. Riskier shell and network actions receive a background
  classifier review, avoiding routine dialog churn without removing Claude's
  risk-aware decision layer.
- The permission-prompt callback remains installed, so explicit `ask` rules,
  user questions, and classifier fallback prompts can still reach the owning
  LCR pane.
- LCR's `PreToolUse` recursive-`rm` hook runs before Claude's permission
  decision and remains authoritative in both `auto` and
  `bypassPermissions`.
- `bypassPermissions` remains available as an explicit escape hatch and does
  not create an approval bridge. `dontAsk` likewise remains intentionally
  non-interactive. `acceptEdits`, `manual`, and `plan` retain the bridge.
- If the bridge cannot initialize, Manual fails closed to `dontAsk`; the other
  modes keep their native behavior and LCR reports that interactive approval is
  unavailable.

The tradeoff is an extra classifier round trip for relevant shell or network
actions. Reads and ordinary working-directory edits skip that review. Auto is
also not a sandbox: normal Git pushes and declared dependency changes may still
proceed, while its built-in policy concentrates on destructive, irreversible,
production, shared-infrastructure, and data-exfiltration risk. Repository
instructions, LCR's hook, backups, and ordinary operator review remain part of
the safety model.

Auto support is model- and route-dependent. At verification time, Anthropic's
documentation listed current Sonnet, Opus, and Fable model minimums and excluded
Haiku/older model generations; some signed-in or gateway routes had stricter
minimums. The live documentation is the source of truth. When Auto is not
available, Claude starts in Manual mode rather than failing the session. LCR
parses the stream `init.permissionMode`, shows the effective mode in the pane
badge, and emits a clear Auto-to-Manual fallback notice.

Primary references:

- [Permission modes](https://code.claude.com/docs/en/permission-modes)
- [Configure Auto mode](https://code.claude.com/docs/en/auto-mode-config)
- [Claude Code changelog](https://code.claude.com/docs/en/changelog)
- [Permissions and hook evaluation order](https://code.claude.com/docs/en/permissions)

## 7. Practical detection strategy

Recommended filesystem-first approach:

1. Parse `~/.claude/projects/<encoded-project>/*.jsonl` for `sessionId`, `cwd`, and start time.
2. Track latest-turn state from structured entry types instead of natural-language transcript text.
3. Treat pending `backgroundTaskId` and async `agentId` launches as in-progress until a terminal task notification or `TaskStop` receipt is observed.
4. Fold auxiliary activity into `LastEventAt` using:
   - `~/.claude/projects/<encoded-project>/<session-id>/subagents/*.jsonl`
   - temp `claude-*` task outputs under `/tmp`, `/private/tmp`, and `os.TempDir()`
5. Treat a trailing ordinary `user` prompt as the start of a new unfinished turn until Claude answers it.
6. Ignore provider-generated user-role chains when deriving conversational turns
   or visible transcript entries, including records marked
   `isCompactSummary == true`.
7. Use live PID metadata only as a fallback when structured transcript state is missing or already incomplete; do not override an explicitly completed turn just because the CLI process is still alive. For an externally owned session, separate process ownership from turn activity using the PID file's structured `status`.
8. Invalidate parser caches when either the parent session JSONL mtime or auxiliary artifact mtimes change, preserving sub-second precision so same-second Claude writes do not get stuck behind stale cached parses.
9. Disable Claude Code's native background-task functionality for an LCR-owned
   stream. A foreground tool call keeps the provider process alive until the
   command exits; leaving stream input open does not keep a native background
   task alive after `claude -p` exits.
10. Continue parsing structured task events defensively. Surface unresolved
    ownership explicitly, and never infer completion from the model's prose.

## 8. Notes

### Foreground child progress in the parent pane (verified 2026-09-30)

Claude Code 2.1.284 writes a sibling `agent-<id>.meta.json` with `description`,
`agentType`, `toolUseId`, and `requestShape` for a child. With LCR's background-task mode
disabled, an `Agent` invocation requesting background execution can still have
`requestShape: "foreground"`. Its tools keep updating the child JSONL while the
parent is waiting; absence of an async launch result does not mean no child exists.

The parent pane, dashboard row and detail pane, and live mobile session summary
expose this activity separately from provider-owned background tasks. Snapshot
reads queue a coalesced five-second refresh outside the session lock; while any
child is unfinished, each completed refresh re-arms the next one so a quiet
parent (including a hidden session) does not freeze child progress. The reader examines at most 16 recent children,
caches unchanged logs, reads at most the final 1 MiB of each changed log and 64 KiB
of its metadata, and ignores partial records. It validates `isSidechain`, `agentId`,
and `sessionId`; only structured lifecycle timestamps within the current parent
turn contribute. Descriptions and latest tool actions come from explicit fields,
not from interpreting prompts or command text. Oversized records outside the
bounded tail may make recent activity unavailable until another complete event.

The compact parent panel lists up to four unfinished children with agent type,
last action and event age even without the sidebar, and collapses completed
children into one line; once every child has completed, the panel is that single
line and the footer keeps the parent's working timer. After 20 minutes without a
child event it says "no recent activity"; queue writes, attachments and filesystem
mtime do not renew that activity. A terminal record means the child completed,
not that the parent reviewed or delivered its report. Child snapshots never
complete, interrupt or take ownership of the parent turn. Read failures are
displayed explicitly, and stale refresh results cannot cross a parent-turn change.

### Worktree subagents (verified 2026-09-27)

Claude Code 2.1.282 stores worktree-isolated agents below the **parent's**
project directory at `<parent-session-id>/subagents/agent-<agent-id>.jsonl`.
Their records carry `isSidechain: true`, `agentId`, the parent's `sessionId`,
and the child's actual worktree `cwd`. The worktree need not have a top-level
conversation in its own encoded project directory. Directory names such as
`.claude/worktrees/agent-*` alone do not establish Claude ownership.

LCR scans these exact nested transcript locations and associates each child
with its recorded `cwd`. Its internal raw session identity is
`<parent-session-id>/agent-<agent-id>` so siblings cannot overwrite one another
or move the parent conversation into a worktree. Same-checkout helpers continue
to contribute auxiliary activity to the parent without replacing its resumable
conversation. A scope containing only the child can still discover its log.

Child timestamps, structured turn state, and transcript content supply the CC
badge, activity timer, and ordinary session assessments. Dashboard changes arrive
on scans; opening the child uses the existing background transcript refresh.
This is recorded activity, not a guarantee that an external process is alive.
The parent's PID or busy status does not prove an individual child is running.
The read-only viewer infers activity only while an unfinished child's latest
structured turn event is at most 20 minutes old. After that, or if its event
timestamp is missing, it reports stalled/unfinished with no running timer.
This is a freshness limit on inferred activity, not evidence of completion,
interruption, or a resumable pause. Fresh turn events restore inferred activity;
file modification time alone does not. The viewer stays read-only throughout.

Enter opens a read-only child transcript with its parent and agent identifiers,
model, tool activity, and available usage. The composite identity is never
passed to `claude --resume`. Input and compaction remain unavailable even after
completion; manage the agent through the parent conversation. Closing the local
viewer does not complete or interrupt external work. An explicit new-session
command can still create a separate conversation in that checkout.

- Prefer structured Claude fields over regex or keyword heuristics.
- Treat subagent and background-task artifacts as source-of-truth activity signals for Claude when they are present.
- If Claude CLI artifact layouts change, update this note in the same change as the detector logic.
