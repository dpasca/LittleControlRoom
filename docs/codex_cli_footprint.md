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

Embedded cold-resume also treats the latest matching `task_complete` or
`turn_aborted` marker as the durable lifecycle state for that turn. This keeps
a resumed app-server response or notification replay from temporarily
reclassifying the same settled turn as active; a different turn id still starts
a new lifecycle normally.

Observed recent conversational text usable for model-based "where was work left off?" classification:

- `response_item.payload.type == "message"` with assistant text parts
- `event_msg.payload.type == "user_message"` (`message`)
- `event_msg.payload.type == "agent_message"` (`message`)
- `event_msg.payload.type == "task_complete"` (`last_agent_message`)

`response_item` messages with `role == "user"` are model-context inputs, not
user-visible transcript events. They can contain injected `AGENTS.md`, skill,
permission, or environment context alongside the real prompt. User-facing
transcripts and classification input therefore take user turns from structured
`event_msg.payload.type == "user_message"` records instead.

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
- `cwd` (raw detected working directory; preserve this as provenance)
- Git top-level path for project ownership when `cwd` is inside a Git worktree
- file mtime for active session files

## 5. Practical detection strategy

Primary (filesystem-first):

1. Parse `~/.codex/sessions/**/*.jsonl` for `(session_id, cwd, started_at)`.
2. Canonicalize Git-backed `cwd` values to the containing worktree top-level for project ownership, while keeping the raw `cwd` as the detected path.
3. Use session file mtime as last activity signal.
4. Parse structured `event_msg.payload.type` lifecycle markers (`task_started`, `task_complete`, `turn_aborted`) to infer whether the latest turn completed.
5. For latest-session classification, read only a bounded tail of recent conversational events from the JSONL instead of reparsing full history.
6. Optionally scan recent output text for non-zero process exit markers.

Optional secondary accelerator:

- Read `~/.codex/state_5.sqlite` `threads` rows for quick latest `cwd` activity snapshots.

## 6. Notes

- Session data volume can be large; avoid full-file deep parsing every poll.
- A compatibility parser should support both modern and legacy session JSONL layouts.
- Codex runs launched from repository subdirectories should not create separate LCR projects when Git identifies the same worktree top-level.

## 7. Runtime companion compatibility

On 2026-07-10, Codex CLI `0.144.0` was observed exposing the stable `code_mode_host` feature as enabled while the Homebrew cask installed only the main `codex` binary. The upstream release publishes `codex-code-mode-host` separately, and the mismatch causes tool calls to fail with `failed to spawn code-mode host` even though `codex app-server` itself starts normally.

For embedded Codex sessions, LCR performs a bounded startup preflight outside the TUI update/render path:

1. Check whether `codex-code-mode-host` is executable on `PATH` or beside the resolved Codex binary.
2. If it is absent, read `codex features list` and confirm that `code_mode_host` is both available and enabled.
3. Add `--disable code_mode_host` only to that LCR-managed app-server process and show a compatibility notice.

This fallback does not edit `~/.codex/config.toml`. Healthy installs keep the feature enabled, older CLIs without the feature are left unchanged, and raw host-spawn failures receive an actionable diagnosis instead of only opaque stderr.

The compatibility result is reused while both the resolved Codex executable and `config.toml` fingerprints remain unchanged. LCR also repairs stale `state_5.sqlite` rollout paths once after a successful pass per Codex home and per LCR runtime, rather than rescanning the complete thread table for every embedded session. Failed cleanup attempts are not cached, so transient SQLite locks can recover on a later launch.

## 8. LCR-managed workspace context

LCR-managed embedded Codex app-server sessions receive an application-context
entry on every turn. The entry records the assigned workspace, canonical
repository root, and trusted expected root branch, and asks Codex to request
permission before crossing checkout boundaries. This context is advisory and is
not inferred from natural-language transcript text.

When a session assigned to a linked worktree emits a structured command item
whose `cwd` is inside the canonical root but outside the assigned worktree, LCR
adds a transcript warning and persists a repository incident event. The detector
uses the app-server command item's structured `cwd`; it does not claim coverage
for standalone Codex processes, direct filesystem tools, or commands whose
working directory is not reported. See
[`repository_root_integrity.md`](repository_root_integrity.md) for the warning and
repair workflow.
