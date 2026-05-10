# LCAgent Experimental Handoff

Date updated: 2026-05-10

This note describes the current LCAgent state after the MVP branch was made
merge-ready. The older `docs/lcagent_mvp_implementation_handoff.md` is still a
useful historical implementation brief, but it no longer describes what is
missing today.

## Current State

LCAgent is an experimental, LCR-native coding-agent harness. It is usable from
the TUI through `/lcagent` and `/lcagent-new`, and it emits local JSONL session
artifacts that LCR can scan and replay.

Implemented pieces:

- `cmd/lcagent exec` with scripted and live provider loops.
- Structured JSONL events for session metadata, tool calls/results, plans,
  final responses, touched files, provider usage, tool profiles, and context
  profiles.
- Workspace-contained tools: `read_file`, `file_outline`, `module_outline`,
  `list_files`, literal `search`, `load_skill`, `run_command`, `apply_patch`,
  `update_plan`, and `final_response`.
- Provider adapters for OpenRouter, OpenAI, DeepSeek, and Moonshot/Kimi routes.
- Experimental tool profiles: `balanced` and `generous`.
- Experimental context profiles: `balanced` and `large`.
- LCR settings for executable path, env file, provider, autonomy, tool profile,
  context profile, and request timeout.
- LCR detector/replay support for LCAgent artifacts.
- Benchmarking artifacts under `docs/research/` and workflow notes in
  `docs/lcagent_benchmarking.md`.

Default posture:

- Treat LCAgent as opt-in and experimental.
- Keep `lcagent_auto = "low"`, `lcagent_tool_profile = "balanced"`, and
  `lcagent_context_profile = "balanced"` as the conservative defaults.
- Use `generous` and `large` for benchmark lanes or providers with enough
  context/cache economics to spend the extra evidence well.

## Known Gaps

LCR session parity:

- LCAgent does not support approvals, attachments, structured tool input,
  elicitation, compact, review, or goal state in the embedded pane.
- Replayed LCAgent history is display-only. Sending a new prompt starts a fresh
  one-shot run rather than continuing the previous model context.
- The model picker is intentionally minimal and does not discover provider
  models or validate provider credentials.

Setup and product UX:

- `/settings` exposes the LCAgent knobs, but there is no dedicated guided
  `/setup` card yet.
- The env-file path is checked for existence, but LCR does not validate the
  selected provider key or perform a lightweight provider smoke call.
- Provider/model/profile choices are text fields, not curated pickers.
- Cost/cached-token feedback is visible through session artifacts and metrics,
  but not yet surfaced as a friendly setup or run summary.

Harness and policy hardening:

- Policy denials currently surface through tool failures; there is no dedicated
  `permission_denied` lifecycle event or first-class denial audit surface yet.
- Patch application works, but the run trace would benefit from a post-apply
  diff summary and stronger verification guidance.
- The command policy needs more real-task calibration, especially around what
  low autonomy should permit for normal test workflows.
- Provider-specific quirks still need hardening: retries, rate-limit messages,
  timeout defaults, prompt-cache behavior, and OpenRouter provider pinning.

Eval maturity:

- The current benchmark is valuable but still small and partly qualitative.
- Scores are human-assigned from fixed-task outputs; there is no automated
  regression suite for answer quality yet.
- We should keep using fixed target commits for comparable runs and avoid
  benchmarking against the moving LCR worktree.

## Recommended Next Steps

1. Merge the branch as an experimental feature after push/PR review.
   The release note should say LCAgent is opt-in, one-shot, and configured from
   `/settings`.

2. Add a dedicated LCAgent setup card.
   It should cover provider, env file, model, autonomy, tool/context profiles,
   and a redacted credential/provider smoke check.

3. Improve embedded-session parity.
   Start with graceful UI behavior for unsupported `/compact`, `/review`,
   approvals, and attachments, then decide whether true resumable context is in
   scope.

4. Harden policy traces.
   Add explicit denial events, post-patch diff summaries, and clearer
   verification outcomes so LCR can explain what the agent did and what it
   refused.

5. Turn the benchmark into a repeatable eval lane.
   Keep the fixed-commit workflow, add a small set of coding-agent tasks, record
   tool/context profile columns, and preserve short qualitative excerpts for the
   human score.

6. Continue model-route exploration selectively.
   DeepSeek V4 Pro and Kimi K2.6 remain the most interesting cost/performance
   routes to improve. GPT-5.5 low remains the high-quality reference lane. Large
   context should stay opt-in unless a route demonstrates better quality per
   dollar.

## Merge Checklist

- LCAgent remains labeled experimental in docs and command summaries.
- Defaults stay conservative: low autonomy, balanced tools, balanced context.
- `make test`, `make scan`, and `make doctor` pass.
- Benchmark artifacts remain under `docs/research/`; raw provider logs and API
  keys are not committed.
