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
- Embedded Codex and OpenCode panes support live sessions, resume and new flows, model selection, approval and input handling, and mixed-provider project workflows.
- Boss mode provides a chat-first high-level layer over the classic TUI, with read-only project-state queries, bounded context-command lookup, confirmable control proposals, generic agent-task delegation, file-backed chat sessions, and separate boss-chat helm/utility inference configuration. In Boss Chat, Codex/OpenCode/Claude Code work sessions are called engineer sessions to distinguish them from Boss Chat transcripts.
- Linked worktrees are first-class: grouped under repo roots, merge-aware, and surfaced with explicit conflict and status feedback.
- Managed runtime commands can launch, stop, inspect, and follow project-local processes from the TUI.
- Screenshot and export tooling exists for deterministic UI captures.

## Current Priorities

- Keep polishing embedded Codex and OpenCode parity and move toward a provider-neutral session abstraction.
- Evolve Boss from single-action control proposals toward a durable goal-run runtime with scoped authority, plan execution, verification, traces, and portfolio-level attention.
- Improve worktree ergonomics without hiding repo-centric status or merge-safety cues.
- Strengthen managed-runtime UX and cross-platform launch and debug behavior.
- Keep scoring reasons and backend cost and activity signals easy to inspect.

## Durable Assumptions

- Codex artifacts live primarily under `~/.codex`, not per-project `.codex` directories.
- Project mapping comes from session metadata `cwd` values.
- OpenCode artifacts live primarily under `~/.local/share/opencode` and are mapped via session and project metadata.
- Boss chat inference is configured separately from background project-analysis inference, with a high-grade helm model for main Boss reasoning and a lower-cost utility model for routine routing, while summaries/classification continue to use Codex, OpenCode, Claude Code, MLX, Ollama, or another selected backend.
- Boss chat transcripts are local Markdown text files under the app data directory (`boss-sessions/`), not SQLite rows; assistant recall packages search matches as XML-like snippets at query time.
- If detector assumptions change, update [docs/codex_cli_footprint.md](docs/codex_cli_footprint.md) in the same change.

## Status File Policy

- Keep this file short and branch-agnostic.
- Update it only for durable project-wide changes such as goals, architecture assumptions, or long-lived priorities.
- Do not record timestamps, verification snapshots, or session-by-session changelogs here.
- Prefer git history and dedicated docs for change chronology.
