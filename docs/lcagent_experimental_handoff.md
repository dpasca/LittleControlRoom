# LCAgent Experimental Handoff

Date updated: 2026-05-14

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
- Verification traces distinguish actual `run_command` checks marked with
  `purpose=verify` from verification text merely reported in `final_response`.
- Verification feedback events nudge the live model loop after failed, denied,
  timed-out, or prematurely reported verification, so the next turn can repair,
  choose a safer command, or explain why verification is blocked.
- Low-autonomy command policy permits argv-only verification forms across common
  stacks: Go test/list/vet and whole-repo build, Make/Just verification
  targets, package-manager test/check/build scripts, Cargo checks, Python test
  and typecheck tools, JS/TS checks, and read-only formatter modes.
- Workspace-contained tools: `read_file`, `file_outline`, `module_outline`,
  `list_files`, literal `search`, optional `web_search`, `load_skill`,
  `run_command`, `apply_patch`, `update_plan`, and `final_response`.
- Provider adapters for OpenRouter, OpenAI, DeepSeek, and Moonshot/Kimi routes.
- Experimental tool profiles: `balanced` and `generous`.
- Experimental context profiles: `balanced` and `large`.
- LCR settings for executable path, env file, provider, autonomy, tool profile,
  context profile, request timeout, and optional web search backend credentials.
- LCR detector/replay support for LCAgent artifacts.
- Benchmarking artifacts under `docs/research/` and workflow notes in
  `docs/lcagent_benchmarking.md`.

Default posture:

- Treat LCAgent as opt-in and experimental.
- Keep `lcagent_auto = "low"`, `lcagent_tool_profile = "balanced"`, and
  `lcagent_context_profile = "balanced"` as the conservative defaults.
- Use `generous` and `large` for benchmark lanes or providers with enough
  context/cache economics to spend the extra evidence well.
- Keep the roadmap coding-first. Global memory, broad personal-assistant
  behavior, and rich non-coding connectors can wait until the coding loop is
  reliable enough to use as a daily worker.

## Known Gaps

LCR session parity:

- LCAgent does not support approvals, attachments, structured tool input,
  elicitation, compact, review, or goal state in the embedded pane.
- Replayed LCAgent history can seed a continuing run through summarized context,
  but it is not a true persistent model thread with full transcript state,
  branching, or user-controlled compaction.
- The model picker is intentionally minimal and does not discover provider
  models or validate provider credentials.
- `web_search` is off by default. Exa-backed search needs an Exa API key;
  Google-backed search needs a Programmable Search API key and search engine
  ID; SearXNG-backed search needs a base URL.

Setup and product UX:

- `/settings` exposes the LCAgent knobs, but there is no dedicated guided
  `/setup` card yet.
- The env-file path is checked for existence, but LCR does not validate the
  selected provider key or perform a lightweight provider smoke call.
- Provider/model/profile choices are text fields, not curated pickers.
- Cost/cached-token feedback is visible through session artifacts and metrics,
  but not yet surfaced as a friendly setup or run summary.

Harness and policy hardening:

- Permission denials and patch diff summaries are recorded, but they need a
  better first-class UI/audit surface in run summaries and Boss reports.
- Verification traces now feed first-pass repair guidance back into the live
  model loop. The next hardening step is to tune this against real traces and
  surface the resulting repair/blocked state more richly in Boss summaries.
- The command policy has an initial common-stack low-autonomy allowlist, but it
  still needs trace-driven calibration against real coding tasks and provider
  mistakes.
- Provider-specific quirks still need hardening: retries, rate-limit messages,
  timeout defaults, prompt-cache behavior, and OpenRouter provider pinning.
- Patch/edit ergonomics are still basic. Multi-file edits work, but there is no
  model-adaptive edit dialect, fallback edit tool, or post-edit formatting hook.

Eval maturity:

- The deterministic eval lane protects the trace contract, but it does not
  score live coding quality.
- The current live benchmark is valuable but still small and partly qualitative.
- Scores are human-assigned from fixed-task outputs; there is no automated
  regression suite for answer quality yet.
- We should keep using fixed target commits for comparable runs and avoid
  benchmarking against the moving LCR worktree.

## Recommended Next Steps

### P0: Make The Coding Loop Trustworthy

This is the highest-leverage track. LCAgent should become boringly reliable for
small-to-medium coding tasks before it tries to be a broader assistant.

1. Use verification semantics to drive repair loops.
   Failed, denied, timed-out, and prematurely reported verification now emit
   `verification_feedback` events and model-facing guidance. Next, use real
   traces to tune the wording, avoid repeated nudges, and turn the feedback
   signal into clearer Boss summary and UI call-to-action states.

2. Calibrate low-autonomy command policy around real coding workflows.
   The initial argv allowlist now covers common verification commands across Go,
   Make/Just, package managers, Cargo, Python, JS/TS, and format check modes.
   Next, use real traces to tighten false positives/negatives and improve denial
   messages when the model asks for a nearby but unsafe command form.

3. Improve edit application and recovery.
   Keep `apply_patch` as the primary typed mutation tool, but add better
   failure feedback, optional post-patch diff context, and eventually a
   model-adaptive fallback edit strategy for providers that struggle with strict
   patch syntax.

4. Surface trace quality in LCR.
   Make run summaries show denials, files changed, patch diff summaries,
   verification status, actual verification commands, token/cost/cached-token
   totals, and whether a continuation used summarized prior context.

### P1: Make Continuation Feel Like A Coding Session

1. Promote summarized continuation from "nice fallback" to an explicit session
   model.
   Record a continuation chain, source artifact id, compact handoff, files
   touched, verification state, and warnings when exact file contents must be
   re-read before edits.

2. Add user-visible compact/review behavior for LCAgent.
   `/compact` can create or refresh the durable handoff summary. `/review` can
   start as a read-only current-diff review task using the same trace format,
   even before it reaches Codex parity.

3. Preserve coding state across LCR surfaces.
   Boss goal runs, TODO-driven sessions, replay, and direct `/lcagent` launches
   should all show the same chain, latest artifact, files changed, verification,
   and next-step status.

### P2: Improve Setup And Model Routing

1. Add a guided LCAgent setup card.
   Cover provider, env file, model, reasoning effort, autonomy, tool/context
   profiles, request timeout, and optional web search. Include a redacted
   provider smoke check before the user spends a real task on setup debugging.

2. Replace free-text model/profile fields with curated choices plus an escape
   hatch.
   Keep direct text entry for experiments, but make the normal path hard to
   misconfigure.

3. Add model-route presets for coding work.
   Suggested starting lanes:
   - `quality`: GPT-5.5 low or the current best high-quality route.
   - `balanced`: DeepSeek V4 Pro or Kimi K2.6 with conservative context.
   - `cheap scout`: DeepSeek V4 Flash or another low-cost route for bounded
     read-only exploration, summarization, and small follow-up tasks.

### P3: Build Real Coding Evals

1. Extend the deterministic eval lane only for harness contracts.
   Keep it fast and no-network: trace shape, denials, patch summaries,
   verification accounting, continuation chains, command policy, and metrics.

2. Add a repeatable live coding eval lane.
   Use fixed target commits and a small task suite: bug fix, test failure,
   feature slice, refactor-with-tests, current-diff review, and repo
   orientation. Score correctness, unnecessary reads, failed tool calls,
   verification quality, cost, and wall time.

3. Make model-tier comparisons first-class.
   Record helm model, optional scout model, context/tool profile, reasoning
   knobs, provider pinning, cache behavior, and final human score.

### P4: Add Bounded Delegation Without Chasing Subagents

Subagents are not a priority by themselves. The useful feature is efficient
model-tiered delegation for small jobs.

1. Add a lightweight delegated-job primitive.
   It should run a bounded prompt with a specific model/profile, read/write
   scope, max turns, and expected structured handoff. It can reuse `lcagent exec`
   artifacts instead of inventing a new runtime.

2. Start with read-only scout jobs on cheaper/faster models.
   Examples: "map the relevant files", "summarize this package", "find likely
   failing test owners", or "extract API usage points". The main model decides
   whether to trust or verify the handoff.

3. Add small write jobs only after scout jobs are boring.
   Keep write delegation scoped to disjoint files or disposable worktrees, with
   explicit patch summaries and verification requirements.

4. Make Boss and LCAgent share the same delegation vocabulary.
   A delegated LCAgent job should appear as an agent task or goal-run trace with
   model, scope, files touched, denials, verification, and cost.

### P5: Defer General-Assistant Breadth

Do not spend early roadmap energy on global memory, email/calendar/docs
connectors, rich personal-assistant behavior, or broad multimodal workflows.
Those can become useful later, but the near-term product goal is a coding-first
agent that can inspect, edit, test, explain, resume, and be supervised cleanly.

## Merge Checklist

- LCAgent remains labeled experimental in docs and command summaries.
- Defaults stay conservative: low autonomy, balanced tools, balanced context.
- `make test`, `make scan`, and `make doctor` pass.
- Benchmark artifacts remain under `docs/research/`; raw provider logs and API
  keys are not committed.
