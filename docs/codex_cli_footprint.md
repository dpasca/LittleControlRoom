# Codex CLI Footprint Discovery (Phase 0)

Date observed: 2026-03-05 (Asia/Tokyo)
Host: macOS user-home environment

This document summarizes *observed* Codex CLI on-disk artifacts from a real environment.

## 1. Storage locations

### User-home global state (primary)

Main directory:

- `~/.codex`

Observed key paths:

- `~/.codex/sessions/**/**/*.jsonl`
- `~/.codex/archived_sessions/*.jsonl`
- `~/.codex/history.jsonl`
- `~/.codex/log/codex-tui.log`
- `~/.codex/state_5.sqlite` (+ `-wal`, `-shm`)
- `~/.codex/sqlite/codex-dev.db`
- `~/.codex/shell_snapshots/*.sh`
- `~/.codex/worktrees/*/...`

### Project-local state

No project-local `.codex` directory was observed under scanned project roots in this machine.

Codex project association is still discoverable from global artifacts (mainly session logs containing `cwd`).

## 2. Session file formats observed

### Format A: modern JSONL (`session_meta`)

Observed in most files (1913/2053 sampled files):

- First line has `{"type":"session_meta", ...}`
- Stable fields in `payload`:
  - `id` (session/thread id)
  - `timestamp` (session start)
  - `cwd` (project working directory)
  - `cli_version`

Other line types include `response_item`, `event_msg`, `turn_context`.

Observed structured turn lifecycle markers under `event_msg.payload.type`:

- `task_started`
- `task_complete`
- `turn_aborted` (observed with `reason: "interrupted"`)

These are usable as a best-effort "latest turn completed" signal without parsing natural-language content. In particular, interrupted turns may end with `turn_aborted` and no later `task_complete`.

Observed recent conversational text usable for model-based "where was work left off?" classification:

- `response_item.payload.type == "message"` with assistant/user text parts
- `event_msg.payload.type == "user_message"` (`message`)
- `event_msg.payload.type == "agent_message"` (`message`)
- `event_msg.payload.type == "task_complete"` (`last_agent_message`)

### Format B: legacy JSONL

Observed in older files (140/2053 sampled files):

- First line has top-level fields like `id`, `timestamp`, `git` (no `type=session_meta`)
- `cwd` appears in early user `message` content under environment text:
  - `Current working directory: /path/...`

## 3. Files that update during active sessions

Observed to update while this active session was running:

- Active session JSONL file under `~/.codex/sessions/.../*.jsonl` (mtime increased between interactions)
- `~/.codex/state_5.sqlite-wal`
- `~/.codex/log/codex-tui.log`

Observed *not* to update for every tool call in this run:

- `~/.codex/history.jsonl` (updated on user prompt submission, not each tool command)

## 4. Parseable formats and stable identifiers

- JSONL
  - Session logs (`sessions`, `archived_sessions`)
  - History (`history.jsonl` with `ts`, `session_id`, `text`)
- SQLite
  - `state_5.sqlite` contains a `threads` table with:
    - `id` (thread/session id)
    - `cwd`
    - `updated_at`
    - `cli_version`
    - additional metadata columns
- Text logs
  - `log/codex-tui.log` (high-volume, useful as secondary signal)

Recommended stable identifiers for Little Control Room:

- `session_id` / thread id (from `session_meta.payload.id` or legacy first-line `id`)
- `cwd` (project path)
- file mtime for active session files

## 5. Practical detection strategy

Primary (filesystem-first):

1. Parse `~/.codex/sessions/**/*.jsonl` for `(session_id, cwd, started_at)`.
2. Use session file mtime as last activity signal.
3. Parse structured `event_msg.payload.type` lifecycle markers (`task_started`, `task_complete`, `turn_aborted`) to infer whether the latest turn completed.
4. For latest-session classification, read only a bounded tail of recent conversational events from the JSONL instead of reparsing full history.
5. Optionally scan recent output text for non-zero process exit markers.

Optional secondary accelerator:

- Read `~/.codex/state_5.sqlite` `threads` rows for quick latest `cwd` activity snapshots.

## 6. Notes

- Session data volume can be large; avoid full-file deep parsing every poll.
- A compatibility parser should support both modern and legacy session JSONL layouts.
