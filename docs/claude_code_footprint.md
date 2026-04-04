# Claude Code Footprint Discovery

Date observed: 2026-03-31 (Asia/Tokyo)
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
- `origin.kind`

The encoded project directory name under `~/.claude/projects` is derived from the project path, but project association should still come from session metadata such as `cwd`.

PID session metadata under `~/.claude/sessions/*.json` is useful for finding a still-open Claude CLI instance, but on its own it does not prove the latest turn is still running. An external terminal can stay open at the prompt after Claude has already finished the turn.

## 3. Structured async and subagent signals

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

## 4. Important detector implication

The parent Claude session JSONL may look done enough to misclassify a session even when work is still running elsewhere.

In particular:

- a turn may end with `system` `subtype == "turn_duration"` after launching a background task
- the most recent top-level entry may stop changing while nested `subagents/*.jsonl` keeps updating
- temp `tasks/*.output` files may continue changing while the parent log is idle

Because of that, latest-turn detection should not rely only on the final top-level JSONL entry type.

## 5. Practical detection strategy

Recommended filesystem-first approach:

1. Parse `~/.claude/projects/<encoded-project>/*.jsonl` for `sessionId`, `cwd`, and start time.
2. Track latest-turn state from structured entry types instead of natural-language transcript text.
3. Treat pending `backgroundTaskId` and async `agentId` launches as in-progress until a terminal task notification is observed.
4. Fold auxiliary activity into `LastEventAt` using:
   - `~/.claude/projects/<encoded-project>/<session-id>/subagents/*.jsonl`
   - temp `claude-*` task outputs under `/tmp`, `/private/tmp`, and `os.TempDir()`
5. Treat a trailing ordinary `user` prompt as the start of a new unfinished turn until Claude answers it.
6. Use live PID metadata only as a fallback when structured transcript state is missing or already incomplete; do not override an explicitly completed turn just because the CLI process is still alive.
7. Invalidate parser caches when either the parent session JSONL mtime or auxiliary artifact mtimes change, preserving sub-second precision so same-second Claude writes do not get stuck behind stale cached parses.

## 6. Notes

- Prefer structured Claude fields over regex or keyword heuristics.
- Treat subagent and background-task artifacts as source-of-truth activity signals for Claude when they are present.
- If Claude CLI artifact layouts change, update this note in the same change as the detector logic.
