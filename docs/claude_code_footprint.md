# Claude Code Footprint Discovery

Dates observed: 2026-03-31 and 2026-07-29 (Asia/Tokyo)
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
- `promptSource`

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

For that reason, every LCR-owned embedded Claude process is launched with
`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`. Bash commands and tests then remain
foreground work owned by the Claude turn, so LCR does not mistake an
intermediate model response for durable process ownership. Structured async
tracking remains a defensive detector for restored transcripts, externally
owned sessions, and any task evidence a future Claude version still emits. If
the owning Claude process exits with such a task unresolved, LCR shows the task
as lost instead of treating the session as ready.

## 6. Practical detection strategy

Recommended filesystem-first approach:

1. Parse `~/.claude/projects/<encoded-project>/*.jsonl` for `sessionId`, `cwd`, and start time.
2. Track latest-turn state from structured entry types instead of natural-language transcript text.
3. Treat pending `backgroundTaskId` and async `agentId` launches as in-progress until a terminal task notification is observed.
4. Fold auxiliary activity into `LastEventAt` using:
   - `~/.claude/projects/<encoded-project>/<session-id>/subagents/*.jsonl`
   - temp `claude-*` task outputs under `/tmp`, `/private/tmp`, and `os.TempDir()`
5. Treat a trailing ordinary `user` prompt as the start of a new unfinished turn until Claude answers it.
6. Ignore provider-generated user-role chains when deriving conversational turns
   or visible transcript entries.
7. Use live PID metadata only as a fallback when structured transcript state is missing or already incomplete; do not override an explicitly completed turn just because the CLI process is still alive. For an externally owned session, separate process ownership from turn activity using the PID file's structured `status`.
8. Invalidate parser caches when either the parent session JSONL mtime or auxiliary artifact mtimes change, preserving sub-second precision so same-second Claude writes do not get stuck behind stale cached parses.
9. Disable Claude Code's native background-task functionality for an LCR-owned
   stream. A foreground tool call keeps the provider process alive until the
   command exits; leaving stream input open does not keep a native background
   task alive after `claude -p` exits.
10. Continue parsing structured task events defensively. Surface unresolved
    ownership explicitly, and never infer completion from the model's prose.

## 7. Notes

- Prefer structured Claude fields over regex or keyword heuristics.
- Treat subagent and background-task artifacts as source-of-truth activity signals for Claude when they are present.
- If Claude CLI artifact layouts change, update this note in the same change as the detector logic.
