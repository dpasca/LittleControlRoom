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
- Chat is the single high-level conversation surface. It opens as a centered overlay over the main TUI and provides project-state queries, project-inventory reflection, bounded context lookup, confirmable control proposals, generic agent-task delegation, durable goal runs, and file-backed chat sessions without duplicating the dashboard, activity log, or flow UI. A utility model semantically selects a planner domain so current app state, control policy, and structured action fields are loaded only for turns that need them; the host still validates every proposed capability and target. Project-list organization is separate from repository work: Chat can register an existing folder into a named category without creating project work. Existing-project work can be confirmed as one tracked TODO → dedicated worktree → engineer launch, with a TODO-only alternative. Engineer launch, progress, completion, and failure receipts persist as non-conversational session events in the separate `/log` window, outside Chat recall and model context. Unloaded-repository work has a distinct confirmed setup path that creates and registers a new Git repository when the target is unused or registers an existing Git repository when the user says it already exists, rejecting non-Git path collisions before the same tracked launch and reporting staged partial failures; an idle root-checkout turn is not treated as finished work. AI work is described by task/project state rather than human-style agent names.
- Linked worktrees are first-class: grouped under repo roots, merge-aware, and surfaced with explicit conflict and status feedback. LCR-created linked worktrees retain their initial branch identity and show a warn-only project-summary notice if the checkout is later switched to another branch. Repository families also retain an evidence-backed expected root branch, raise persistent warn-only root-mismatch attention, and offer investigation-first engineer handoff or conservative linked-worktree repair.
- `/resolve` runs conflict repair in a separate managed background engineer lane, preserving any interactive session for the same checkout while projecting live progress and terminal outcomes, including required-attention states, onto the project row/detail, refreshing Git status after completion, and restoring both lanes independently after a graceful restart.
- Managed runtime commands can launch, stop, inspect, and follow project-local processes from the TUI.
- LCR-managed embedded Codex, OpenCode, Claude Code, and LCAgent sessions can optionally capture repository-scoped project TODOs through a shared list-before-add contract with configurable intent policy, trusted worktree-to-root resolution, duplicate-safe reviewed writes, and visible receipts.
- The TUI hosts a monitor-first mobile web surface in-process, with persisted auto-start, listen-address, and default-off live-session message settings, a loopback default, a one-run CLI override, and a dedicated access panel that reports detected LAN URLs, pairing, and phone-control state without duplicating setup controls. Its portrait dashboard mirrors the main TUI's project-first scan pattern with compact assessment, agent, and flag signals. Non-loopback clients pair through a short startup code and receive a persistent signed device cookie; the surface shares service, store, privacy, and UI-neutral semantic models, overlays nonblocking live engineer snapshots, streams transcript revisions to an incrementally updated view, falls back to recorded artifact transcripts, and can send, steer, or queue text only into the expected current live session when explicitly enabled.
- Screenshot and export tooling exists for deterministic UI captures. Demo sessions can also record rendered Bubble Tea views into compact, independently seekable gzip chunks, edit non-destructive clips in a TUI timeline, and export selected ranges as asciicast v3 without retaining key values or entered text. Capture-time masking replaces a selected private category or a visible embedded session for a private project with a fixed privacy screen before the frame reaches the recorder.
- Official GitHub release builds use a UI-neutral, once-daily stable-release checker. The TUI surfaces an update badge and requires explicit approval before verified staging, rollback-protected replacement of both shipped binaries, graceful engineer-turn journaling, and an in-process restart; source and package-manager-owned builds do not self-update. The build path is continuously checked on Linux and macOS, and local snapshots share the same pinned, non-mutating preflight and archive verification used by CI.

## Current Priorities

- Keep polishing embedded Codex and OpenCode parity and move toward a provider-neutral session abstraction.
- Evolve the paired mobile surface from read-only monitoring toward explicitly confirmed session controls without creating a second domain model, and add transport encryption beyond trusted-LAN HTTP.
- Evolve Chat from single-action control proposals toward a durable goal-run runtime with scoped authority, plan execution, verification, traces, and portfolio-level attention.
- Improve worktree ergonomics without hiding repo-centric status or merge-safety cues.
- Strengthen managed-runtime UX and cross-platform launch and debug behavior.
- Keep scoring reasons and backend cost and activity signals easy to inspect.

## Durable Assumptions

- Codex artifacts live primarily under `~/.codex`, not per-project `.codex` directories.
- Project mapping comes from session metadata `cwd` values.
- OpenCode artifacts live primarily under `~/.local/share/opencode` and are mapped via session and project metadata.
- Live embedded-session snapshots and scan working sets are rebuildable in-memory projections; SQLite persists meaningful session transitions and changed project state, not streaming activity heartbeats.
- Demo replay uses rendered-view capture rather than Bubble Tea message replay: asynchronous commands and live subsystem state are not assumed to be deterministic, while complete view frames can be safely delta-compressed, sought, and exported.
- Full project scans are service-coordinated and non-overlapping, while targeted project refreshes remain independent; scheduler interval changes apply live and scheduled scans use bounded cooperative deadlines.
- Experimental LCAgent uses canonical thread state under the app data directory (`lcagent/threads/<thread-id>/state.json`) as the model-resume source of truth; JSONL session files are per-run traces for replay, metrics, and audit.
- LCR-managed embedded agents preserve broad cross-directory access while applying provider-aware destructive-command guardrails; the current direct-`rm` policy, threat model, and known bypasses are documented in [docs/destructive_command_safety.md](docs/destructive_command_safety.md).
- Repository-root integrity is advisory by default. Expected branches come from durable LCR/user evidence rather than assumed branch names; exact-state acknowledgement is reversible, while automated repair remains unavailable unless the clean-checkout, branch, lock, worktree, and live-runtime safety checks all pass.
- Embedded engineer TODO capture is repository-scoped rather than worktree-scoped. Provider tools never accept a project override; they must review the current open-TODO revision before adding, while SQLite serializes concurrent exact-retry deduplication and carries the live capture policy across isolated MCP processes.
- Reusable agent context/checkpoint helpers live in `internal/agentcontext`; LCAgent uses them for durable thread state, and Chat uses them for per-session context checkpoints with compacted summaries plus recent chat tails.
- Embedded graceful-restart intent lives under the app data directory in `embedded-sessions/restart-intents.json`; the startup recovery dialog is journal-only, provider artifacts remain the conversation source of truth, and reopening a session is not treated as resuming in-flight model computation.
- Chat inference is configured separately from background project-analysis inference, with a high-grade model for main reasoning and a lower-cost utility model for routine routing. Existing `boss_*` config keys remain the compatibility boundary, while summaries/classification continue to use Codex, OpenCode, Claude Code, MLX, Ollama, or another selected backend.
- Chat planner routing is model-based and structured, not keyword- or regex-gated. Scoped planner turns omit ambient portfolio/TUI state, retrieve current facts through read-only tools, load capability policy from the control registry through an internal control reference, and use a domain-specific action schema; `general` remains the broad compatibility fallback.
- Chat repository-content questions use the shared in-process LCAgent Repository Scout with workspace-only reads, project instructions, durable traces, and mechanical file-range evidence. An explicit LCAgent route is an optional first choice; otherwise Scout inherits Chat utility/main inference and then compatible project-analysis inference, reporting every fallback or unavailable inspection instead of inferring absence.
- Chat transcripts are local Markdown text files under the app data directory (`help-chat-sessions/`), not SQLite rows. Recall searches current Chat sessions first and legacy `boss-sessions/` transcripts second.
- If detector assumptions change, update [docs/codex_cli_footprint.md](docs/codex_cli_footprint.md) in the same change.

## Status File Policy

- Keep this file short and branch-agnostic.
- Update it only for durable project-wide changes such as goals, architecture assumptions, or long-lived priorities.
- Do not record timestamps, verification snapshots, or session-by-session changelogs here.
- Prefer git history and dedicated docs for change chronology.
