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

Observed completion statuses worth treating as terminal:

- `completed`
- `failed`
- `error`
- `errored`
- `cancelled` / `canceled`
- `interrupted`
- `stopped`

`stopped` is how a later Claude process reports a background command for which
the previous process left no completion record. It is terminal for turn
detection, but it is not evidence that the command succeeded.

## 5. Important detector implication

The parent Claude session JSONL may look done enough to misclassify a session even when work is still running elsewhere.

In particular:

- a turn may end with `system` `subtype == "turn_duration"` after launching a background task
- the most recent top-level entry may stop changing while nested `subagents/*.jsonl` keeps updating
- temp `tasks/*.output` files may continue changing while the parent log is idle

Because of that, latest-turn detection should not rely only on the final top-level JSONL entry type.

The same distinction applies to an embedded stream-JSON process. A `result`
record closes one model-response boundary; it does not prove that
provider-declared background work finished. However, keeping stream input open
is not sufficient to retain ownership: Claude Code can emit its final result,
exit the headless `claude -p` process, and clean up a child background task
before it records a terminal notification.

Conversely, the headless process can remain alive after the durable session
JSONL records an explicit terminal assistant stop, without emitting the matching
terminal boundary on stdout. LCR periodically reloads the transcript and may
release its final submitted turn only when that verified terminal record is not
older than the locally captured submission. A terminal record from an earlier
turn must not close a newer prompt that has not reached the JSONL yet.

For that reason, every LCR-owned embedded Claude process is launched with
`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`. Bash commands and tests then remain
foreground work owned by the Claude turn, so LCR does not mistake an
intermediate model response for durable process ownership. Structured async
tracking remains a defensive detector for restored transcripts, externally
owned sessions, and any task evidence a future Claude version still emits. If
the owning Claude process exits with such a task unresolved, LCR shows the task
as lost instead of treating the session as ready.

## 6. Embedded stream input and permission callbacks

Claude Code's stream-JSON input mode accepts another user message while a
response is active. The message remains queued and runs as its own subsequent
turn. Little Control Room therefore writes a busy-session follow-up directly to
the existing stream; it must not synthesize an interrupt first. Only an explicit
`ctrl+c` or `/pause` action stops the active turn. Claude may persist canceled
tool results using denial-shaped provider text after an interrupt, so LCR records
the explicit interruption source and does not present those canceled tools as
individually denied by the user.

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
3. Treat pending `backgroundTaskId` and async `agentId` launches as in-progress until a terminal task notification is observed.
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

- Prefer structured Claude fields over regex or keyword heuristics.
- Treat subagent and background-task artifacts as source-of-truth activity signals for Claude when they are present.
- If Claude CLI artifact layouts change, update this note in the same change as the detector logic.
