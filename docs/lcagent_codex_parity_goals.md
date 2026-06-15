# LCAgent Codex-Parity Goals

This document tracks the remaining work for making LCAgent feel like a great
coding-first agent, assuming it can use the same model quality as Codex.

The target is not full general-assistant breadth yet. Global memory, rich
personal-assistant connectors, and broad non-coding workflows can wait. The
near-term bar is: LCAgent should be boringly reliable on real coding work,
recover gracefully when tools fail, expose enough trace evidence to debug
mistakes, and feel natural inside Little Control Room.

## Current Baseline

LCAgent now has the important coding-agent skeleton:

- live provider loops plus scripted deterministic evals
- workspace-scoped read, search, command, patch, and exact text-edit tools
- verification semantics and repair feedback
- route presets for common coding lanes
- a `lcagent scout` cheap read-only delegation lane
- embedded `/review` and `/compact`
- JSONL trace artifacts, metrics, trace-quality summaries, resume-picker trace
  hints, project-detail trace rollups, and live evals

The remaining goals below are mostly hardening, calibration, and UX polish
rather than a ground-up redesign.

## Goal Tracker

| Priority | Goal | Status | Why It Matters |
| --- | --- | --- | --- |
| P0 | Real session continuity | Started | Long coding tasks need durable context without pretending summaries are full memory. |
| P0 | Provider hardening | Started | Same-model quality only matters if the adapter survives rate limits, timeouts, and provider quirks. |
| P0 | More brutal live evals | Started | We need evidence from tasks that resemble actual coding work, not only smoke fixtures. |
| P0 | Edit/apply sophistication | Started | A coding agent is only as good as its ability to make and repair changes reliably. |
| P0 | Autonomy calibration | Started | Low-autonomy needs to allow normal verification while still blocking risky commands. |
| P1 | Interactive UX parity | Started | The embedded experience should make approvals, blocking states, review, and trace quality easy to act on. |
| P1 | Trace-quality dashboarding | Started | The trace signals exist; LCR should make them scannable during daily use. |
| P2 | Efficient scout/delegation lanes | Started | Cheap bounded exploration can save time/cost once core reliability is strong. |

## P0 Goals

### 1. Real Session Continuity

Current state:

- New artifacts persist a private exact model-context snapshot, and
  `--continue-from` prefers replaying that message state before the new prompt.
- Older artifacts without a snapshot still seed a continuing run through
  summarized context as an explicitly labeled fallback.
- `/compact` can write a durable handoff summary.
- Continuation metadata appears in traces.
- Continuation and resume-context events report whether the run used exact
  replay or summary fallback, including exact replay message counts when
  available.
- Max-turn final handoff now preserves harness-known files touched and recorded
  verification details in structured final events.
- `--continue-from` is the explicit continuation entry point, with `--resume`
  kept as a compatibility alias.
- New continuation artifacts carry parent/root session IDs, chain depth,
  handoff source, and pending verification/file state into trace parsing,
  embedded status text, and compact summaries.
- The deterministic eval lane includes a max-turn handoff continuation case,
  and the provider-loop tests cover an actual max-turn handoff followed by
  `--continue-from` with pending file/verification state preserved.

Missing:

- User-visible branch/rewind/restart behavior.
- Provider-native persistent thread handling beyond local exact replay
  snapshots.
- Better browsing of compact summaries from replay/session pickers.
- User-visible branch/rewind/restart affordances for continuation chains.

Done when:

- A user can resume a long LCAgent task and understand exactly what context is
  preserved, what must be re-read, and where the current handoff came from.
- LCR surfaces the continuation chain without requiring JSONL inspection.
- Compaction is user-controlled enough that long sessions do not feel fragile.

### 2. Provider Hardening

Current state:

- OpenRouter, OpenAI, DeepSeek, and Moonshot/Kimi adapters exist.
- Route presets make common lanes easier to select.
- Request timeout knobs exist.
- Provider failures are classified into traceable failure kinds such as
  `rate_limited`, `timeout`, `auth`, `quota`, `malformed_response`,
  `transient_http`, and `provider_schema`.
- Transient provider failures can retry with bounded backoff, and retries are
  recorded as `provider_retry` / `provider_retry_succeeded` events.
- `trace_quality` includes provider failure and retry counts so unstable routes
  are easier to separate from model/task failures.
- Embedded LCAgent provider failures now include short actionable hints for
  quota, auth, rate-limit, timeout, transient HTTP, and malformed-response
  failures.

Missing:

- Broader retry/backoff calibration across real provider failures.
- Richer blocked-state UI beyond one-line embedded transcript hints.
- Better timeout recovery and partial-output handling.
- Provider-specific prompt-cache behavior and accounting.
- OpenRouter provider pinning and fallback behavior that is explicit in traces.
- Adapter tests for malformed responses, transient failures, and provider
  schema drift.

Done when:

- Provider failures produce actionable status text and trace events.
- A transient failure rarely ruins a whole coding turn.
- Route comparisons can separate model quality from adapter/provider failures.

### 3. More Brutal Live Evals

Current state:

- `lcagent live-eval` covers a small README edit, Go bug fix, feature slice,
  dependency-light JavaScript, Python unittest, and Rust cargo bug fixes,
  read-only repo orientation, current-diff review, and a multi-file refactor.
- Per-case reports include correctness, observed/expected verification status,
  trace quality, cost, read volume, and artifacts.
- The 2026-05-16 expanded-case run is archived in
  [lcagent_live_eval_runs.md](lcagent_live_eval_runs.md). It showed the review
  case passing and the refactor case reaching correct code but missing the final
  rerun/report because of the turn budget.

Missing:

- Dependency and environment failure cases.
- Long-running sessions that require compaction/resume.
- Repeated runs for route stability, not just single-pass success.

Done when:

- The live suite can catch regressions in edit quality, verification behavior,
  tool churn, and provider-specific failure modes.
- We can compare route presets with enough evidence to choose defaults without
  relying on vibes.

### 4. Edit/Apply Sophistication

Current state:

- `apply_patch` returns structured failure metadata.
- Patch feedback suggests targeted re-reads.
- `create_file` supports initial file writes without patch grammar.
- `replace_file` supports guarded whole-file rewrites using SHA-256 values
  copied from `read_file`.
- `replace_lines` supports known line-range replacement/deletion.
- `replace_text` gives a simple exact-replacement fallback.
- Repeated identical patch feedback is suppressed and escalated into
  model-facing repair guidance that asks the model to re-read current lines,
  retry a smaller hunk, or choose the smallest direct edit tool.

Missing:

- More reliable multi-hunk edits against stale context.
- Optional post-edit formatter/check hooks.
- Guardrails for generated files and large mechanical edits.
- Provider-adaptive edit dialects if strict patch syntax remains brittle.

Done when:

- Common stale-context failures recover in one or two turns.
- Small exact edits do not get stuck in patch syntax loops.
- Larger edits preserve surrounding code and are easy to audit.

### 5. Autonomy Calibration

Current state:

- Low-autonomy allows common verification commands across common stacks.
- Denials produce guidance for safer nearby forms.
- Controlled `pnpm exec` wrappers are allowed for local JS/TS verifier CLIs
  such as `tsc --noEmit`, `eslint`, `prettier --check`, and `biome check`,
  while broader package execution remains denied.

Missing:

- Fewer false denials for normal project verification.
- Better command-specific blocked states in the UI.
- Confidence that mutating commands remain appropriately gated.

Done when:

- Normal test/build/typecheck flows work without annoying detours.
- Risky commands still produce clear, actionable denials.
- Policy changes are backed by trace examples and regression tests.

## P1 Goals

### 6. Interactive UX Parity

Current state:

- Embedded LCAgent supports prompt turns, replay, curated model choices,
  `/review`, `/compact`, and trace-quality status after completed runs.
- LCAgent saved-session resume choices show continuation/pending-verification
  hints and compact trace-quality badges.

Missing:

- Approvals.
- Attachments.
- Structured tool input and elicitation.
- Richer diff/review surfaces.
- Better status treatment for blocked, retrying, rate-limited, or waiting
  states.

Done when:

- LCAgent feels like a first-class embedded engineer session, not a thinner
  experimental pane.
- Users can understand and resolve blocked states without reading raw traces.

### 7. Trace-Quality Dashboarding

Current state:

- `lcagent metrics` and live evals emit `trace_quality`.
- Boss/LCR trace harvests include compact quality summaries.
- The LCAgent resume picker surfaces compact trace-quality badges and selected
  session detail hints for quality, continuation depth, pending verification,
  and repair/feedback counts.
- Project detail intentionally keeps LCAgent trace-quality rollups out of the
  user-facing summary. Developer inspection uses embedded session badges,
  Boss/LCR goal-run reports, and the hidden `/dev-lcreview` TODO flow.

Missing:

- Per-project recent LCAgent quality trends.
- Project-list or dashboard-level trace-quality rollups.
- Quick drill-down from score/finding to the relevant trace event.

Done when:

- A user can scan recent LCAgent work and immediately see whether failures came
  from verification, patching, provider instability, read churn, or cost.

## P2 Goals

### 8. Efficient Scout/Delegation Lanes

Current state:

- Route presets include `cheap-scout`.
- `lcagent scout` wraps `exec` with the cheap-scout preset, low max turns,
  autonomy off, a read-only scout prompt contract, and a `delegation_mode`
  trace event with the expected handoff sections.
- LCR has broader agent-task concepts outside LCAgent.

Missing:

- A polished embedded/TUI way to send bounded exploration or verification
  subtasks to a cheaper/lower-latency route.
- Guardrails so delegation does not create confusing overlapping edits.
- Small write-job delegation, if we decide it is worth adding later.

Done when:

- LCAgent can cheaply ask for bounded side information without slowing the main
  coding path or muddying ownership of edits.

## Operating Rule

Prefer evidence over ambition. For each goal, land the smallest slice that
improves real coding reliability, then run deterministic evals, live evals, and
trace review before widening the scope.
