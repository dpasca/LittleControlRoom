# Little Control Room Status

## Snapshot

Little Control Room is a control center for AI tasks with multi-project visibility and prioritization.

This file is a stable project snapshot, not a per-session handoff log.
Do not append branch-by-branch or worktree-by-worktree updates here.
Use git history, PR descriptions, TODOs, and dedicated docs for implementation chronology.
Older notes from the previous rolling-log workflow live in [docs/status_archive.md](docs/status_archive.md).

## Core Intent

1. Keep monitoring logic UI-agnostic.
2. Treat Codex disk artifacts as source-of-truth inputs.
3. Keep scoring reasons transparent and inspectable.

## Current State

- Artifact-first scanning persists project and session state in SQLite and surfaces transparent attention reasons.
- The TUI supports multi-project list/detail workflows, slash commands, project notes and TODOs, diff and commit helpers, and repo-health visibility.
- Embedded Codex, OpenCode, Claude Code, and LCAgent panes support live sessions, resume and new flows, model selection, approval and input handling, mixed-provider project workflows, and graceful-restart journaling that can continue LCR-owned interrupted turns after confirmation.
- Help Chat is the single high-level conversation surface. It opens as a centered overlay over the main TUI and provides project-state queries, project-inventory reflection, bounded context lookup, confirmable control proposals, generic agent-task delegation, durable goal runs, and file-backed chat sessions without duplicating the dashboard, activity log, or flow UI. New project work can be confirmed as one tracked TODO → dedicated worktree → engineer launch, with durable start/result receipts and a TODO-only alternative; an idle root-checkout turn is not treated as finished work. AI work is described by task/project state rather than human-style agent names.
- Linked worktrees are first-class: grouped under repo roots, merge-aware, and surfaced with explicit conflict and status feedback.
- Managed runtime commands can launch, stop, inspect, and follow project-local processes from the TUI.
- The TUI hosts a monitor-first mobile web surface in-process, with persisted auto-start, listen-address, and default-off live-session message settings, a loopback default, a one-run CLI override, and a dedicated access panel that reports detected LAN URLs, pairing, and phone-control state without duplicating setup controls. Non-loopback clients pair through a short startup code and receive a persistent signed device cookie; the surface shares service, store, privacy, and UI-neutral semantic models, overlays nonblocking live engineer snapshots, falls back to recorded artifact transcripts, and can send, steer, or queue text only into the expected current live session when explicitly enabled.
- Screenshot and export tooling exists for deterministic UI captures.

## Current Priorities

- Keep polishing embedded Codex and OpenCode parity and move toward a provider-neutral session abstraction.
- Evolve the paired mobile surface from read-only monitoring toward explicitly confirmed session controls without creating a second domain model, and add transport encryption beyond trusted-LAN HTTP.
- Evolve Help Chat from single-action control proposals toward a durable goal-run runtime with scoped authority, plan execution, verification, traces, and portfolio-level attention.
- Improve worktree ergonomics without hiding repo-centric status or merge-safety cues.
- Strengthen managed-runtime UX and cross-platform launch and debug behavior.
- Keep scoring reasons and backend cost and activity signals easy to inspect.

## Durable Assumptions

- Codex artifacts live primarily under `~/.codex`, not per-project `.codex` directories.
- Project mapping comes from session metadata `cwd` values.
- OpenCode artifacts live primarily under `~/.local/share/opencode` and are mapped via session and project metadata.
- Experimental LCAgent uses canonical thread state under the app data directory (`lcagent/threads/<thread-id>/state.json`) as the model-resume source of truth; JSONL session files are per-run traces for replay, metrics, and audit.
- LCR-managed embedded agents preserve broad cross-directory access while applying provider-aware destructive-command guardrails; the current direct-`rm` policy, threat model, and known bypasses are documented in [docs/destructive_command_safety.md](docs/destructive_command_safety.md).
- Reusable agent context/checkpoint helpers live in `internal/agentcontext`; LCAgent uses them for durable thread state, and Help Chat uses them for per-session context checkpoints with compacted summaries plus recent chat tails.
- Embedded graceful-restart intent lives under the app data directory in `embedded-sessions/restart-intents.json`; the startup recovery dialog is journal-only, provider artifacts remain the conversation source of truth, and reopening a session is not treated as resuming in-flight model computation.
- Help Chat inference is configured separately from background project-analysis inference, with a high-grade model for main reasoning and a lower-cost utility model for routine routing. Existing `boss_*` config keys remain the compatibility boundary, while summaries/classification continue to use Codex, OpenCode, Claude Code, MLX, Ollama, or another selected backend.
- Help Chat transcripts are local Markdown text files under the app data directory (`help-chat-sessions/`), not SQLite rows. Recall searches current Help Chat sessions first and legacy `boss-sessions/` transcripts second.
- If detector assumptions change, update [docs/codex_cli_footprint.md](docs/codex_cli_footprint.md) in the same change.

## Status File Policy

- Keep this file short and branch-agnostic.
- Update it only for durable project-wide changes such as goals, architecture assumptions, or long-lived priorities.
- Do not record timestamps, verification snapshots, or session-by-session changelogs here.
- Prefer git history and dedicated docs for change chronology.
