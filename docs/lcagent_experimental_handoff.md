# LCAgent Experimental Handoff

Date updated: 2026-05-16

This note describes the current LCAgent state after the MVP branch was made
merge-ready. The older `docs/lcagent_mvp_implementation_handoff.md` is still a
useful historical implementation brief, but it no longer describes what is
missing today.

For the forward-looking Codex-parity tracker, see
[`docs/lcagent_codex_parity_goals.md`](lcagent_codex_parity_goals.md).

## Current State

LCAgent is an experimental, LCR-native coding-agent harness. It is usable from
the TUI through `/lcagent` and `/lcagent-new`, and it emits local JSONL session
artifacts that LCR can scan and replay.

Implemented pieces:

- `cmd/lcagent exec` with scripted and live provider loops, plus
  `cmd/lcagent scout` for cheap read-only exploration handoffs.
- Structured JSONL events for session metadata, tool calls/results, plans,
  final responses, touched files, provider usage, tool profiles, and context
  profiles.
- Verification traces distinguish actual `run_command` checks marked with
  `purpose=verify` from verification text merely reported in `final_response`.
- Verification feedback events nudge the live model loop after failed, denied,
  timed-out, or prematurely reported verification, so the next turn can repair,
  choose a safer command, or explain why verification is blocked.
- Patch feedback events add structured recovery hints for failed `apply_patch`
  attempts, especially stale hunk context. When possible, they include exact
  `read_file` suggestions for the current target range and feed those hints
  back into the live model loop before the next retry.
- Low-autonomy command policy permits argv-only verification forms across common
  stacks: Go test/list/vet and whole-repo build, Make/Just verification
  targets, package-manager test/check/build scripts, controlled `pnpm exec`
  wrappers around local JS/TS verifier CLIs, Cargo checks, Python test and
  typecheck tools, JS/TS checks, and read-only formatter modes.
- Workspace-contained tools: `read_file`, `file_outline`, `module_outline`,
  `list_files`, literal `search`, optional `web_search`, `load_skill`,
  `run_command`, `apply_patch`, literal `replace_text`, `update_plan`, and
  `final_response`.
- Provider adapters for OpenRouter, OpenAI, DeepSeek, and Moonshot/Kimi routes.
- Coding route presets for `balanced`, `quality`, and `cheap-scout` CLI lanes,
  with traceable `route_preset` events, explicit flag overrides, and optional
  embedded-launch wiring through `lcagent_route_preset`.
- `lcagent scout` defaults to the `cheap-scout` route, low max turns, autonomy
  off, a read-only handoff prompt, and a `delegation_mode` trace event.
- Explicit summarized continuation via `--continue-from` with backward-compatible
  `--resume`, `continuation` trace events, parent/root chain metadata, handoff
  source, and pending verification/file state.
- LCAgent saved-session resume choices include continuation/pending-verification
  hints and compact trace-quality badges.
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
  elicitation, or goal state in the embedded pane.
- Embedded `/review` now starts a read-only current-diff LCAgent review run
  using the same JSONL trace path, with `--auto off` and no continuation resume.
- Embedded `/compact` now writes a durable Markdown handoff summary from the
  latest LCAgent JSONL trace under the app data dir and shows it in the pane.
- Replayed LCAgent history can seed a continuing run through summarized context,
  with explicit continuation-chain metadata in the artifact and embedded
  transcript. It is still not a true persistent model thread with full transcript
  state, branching, or user-controlled compaction.
- The model picker now offers curated coding choices for the configured
  provider plus a custom-model escape hatch. It still does not discover provider
  models or validate provider credentials.
- `web_search` is off by default. Exa-backed search needs an Exa API key;
  Google-backed search needs a Programmable Search API key and search engine
  ID; SearXNG-backed search needs a base URL.

Setup and product UX:

- `/settings` exposes the LCAgent knobs, but there is no dedicated guided
  `/setup` card with provider smoke actions yet.
- The env-file path is checked for existence, but LCR does not validate the
  selected provider key or perform a lightweight provider smoke call.
- Provider/model/profile choices now have curated route presets and embedded
  model options, but the setup flow still needs a more guided picker/smoke
  experience.
- Cost/cached-token feedback is visible through session artifacts, metrics, and
  LCAgent trace-quality summaries, but not yet surfaced in a guided setup card.
- `sessionmetrics` emits a derived `trace_quality` block with score, grade,
  findings, tool failure rate, repair pressure, verification rate, read overlap
  rate, cached-token rate, and estimated cost. `live-eval` reports the same
  score/grade per case for trace-driven calibration across routes. The project
  detail pane now loads recent LCAgent artifacts asynchronously and shows a
  compact `LCAgent trace` rollup for latest checks, verification, provider
  failures/retries, repairs, pending state, and continuations.

Harness and policy hardening:

- Permission denials, patch feedback, patch diff summaries, actual
  verification commands, and token/cached-token usage are now harvested into
  shared LCAgent trace-quality summaries and Boss goal-run reports. Dedicated
  visual treatment in the main TUI can still improve scanability.
- Verification traces now feed first-pass repair guidance back into the live
  model loop. Duplicate repair feedback is suppressed and recorded as an
  explicit trace signal so stuck retry loops are easier to spot.
- The command policy has a common-stack low-autonomy allowlist plus
  command-specific denial hints for nearby unsafe forms such as shell
  verification, output-writing test flags, mutating formatters, dependency
  installs, watch modes, and path escapes. It still needs ongoing
  trace-driven calibration against real coding tasks and provider mistakes.
- Provider-specific quirks still need hardening beyond the first foundation:
  provider failures now emit typed `provider_failure` events, transient failures
  can retry with bounded backoff, retry attempts are traced, and metrics fold
  provider instability into `trace_quality`. Embedded transcripts now add
  short actionable hints for quota, auth, rate-limit, timeout, transient HTTP,
  and malformed-response failures. Remaining work includes broader provider
  coverage, richer blocked-state UI, timeout recovery, prompt-cache behavior,
  and OpenRouter provider pinning/fallback calibration.
- Patch/edit ergonomics now include first-pass failure feedback, concrete
  re-read suggestions for stale hunks, and a literal `replace_text` fallback
  tool for small exact replacements when strict patch syntax is failing. There
  is still no model-adaptive edit dialect or post-edit formatting hook.

Eval maturity:

- The deterministic eval lane protects the trace contract, but it does not
  score live coding quality.
- The deterministic eval lane now includes a max-turn handoff continuation case
  so summarized continuation from handoff artifacts is covered by the trace
  regression suite.
- The repeatable `lcagent live-eval` lane now runs fixed live-provider coding
  tasks for README edit, Go bug fix, small feature implementation,
  dependency-light JavaScript, Python unittest, and Rust cargo bug fixes,
  read-only orientation, current-diff review, and a multi-file refactor, with
  per-case correctness, verification status, tool-churn, token, cost, artifact,
  workspace, and wall-time reporting.
- The current archived live benchmark is valuable but still small and partly
  qualitative.
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
   `verification_feedback` events and model-facing guidance. Duplicate repair
   feedback is suppressed and recorded as `repair_feedback_suppressed`. Next,
   use real traces to tune the wording and turn the feedback signal into
   clearer blocked/retry UI call-to-action states.

2. Calibrate low-autonomy command policy around real coding workflows.
   The initial argv allowlist now covers common verification commands across Go,
   Make/Just, package managers, Cargo, Python, JS/TS, and format check modes.
   Denials now include command-specific retry guidance for common unsafe nearby
   forms, and controlled `pnpm exec` wrappers cover local JS/TS verifier CLIs
   without allowing broad package execution. Next, use real traces to tighten
   false positives/negatives.

3. Improve edit application and recovery.
   `apply_patch` now returns typed `patch_failure` metadata and emits
   `patch_feedback` guidance for stale context and malformed patches, including
   concrete suggested `read_file` ranges when a failed hunk can be localized.
   `replace_text` now provides a simpler exact-replacement fallback for small
   edits after reading current file text. Next, tune retry behavior against live
   traces and eventually add a model-adaptive edit dialect for providers that
   struggle with strict patch syntax.

4. Surface trace quality in LCR.
   Shared LCAgent summaries and Boss goal-run reports now show denials, files
   changed, patch feedback, patch diff summaries, verification status, actual
   verification commands, token/cached-token totals, duplicate repair feedback,
   per-tool success/failure counts, derived trace-quality findings, and
   continuation-chain state. Embedded LCAgent runs append a compact
   trace-quality score/grade line when the final artifact is available, and the
   LCAgent resume picker now shows continuation/pending-verification hints plus
   compact trace-quality badges. The project detail pane now adds a recent
   LCAgent trace-quality rollup without blocking rendering. Next, add
   project-list trends and trace-event drill-downs in the interactive TUI.

### P1: Make Continuation Feel Like A Coding Session

1. Promote summarized continuation from "nice fallback" to an explicit session
   model.
   LCAgent launches now have explicit `--continue-from` continuation, enriched
   session metadata, dedicated `continuation` events, pending file/verification
   state, embedded status text, compact summaries, shared trace parsing, and
   regression coverage for max-turn handoff continuation. Next, add browsing
   and branch/restart affordances across session pickers and keep the warning
   that exact file contents must be re-read before edits.

2. Add user-visible compact/review behavior for LCAgent.
   `/review` now starts as a read-only current-diff review task using the same
   trace format. `/compact` now creates or refreshes a durable handoff summary
   before LCAgent reaches full Codex parity. Next, make the summary easier to
   browse from replay/session pickers and tune the handoff shape from use.

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
   The embedded model picker now exposes curated choices for normal LCAgent
   routes while preserving custom model entry. `/settings` can also apply the
   named route preset bundle to embedded launches through
   `lcagent_route_preset`. Next, make this friendlier with a picker/setup card
   rather than requiring exact text entry.

3. Add model-route presets for coding work.
   The CLI and embedded LCAgent launch path now support these starting lanes
   through `--route-preset`, `lcagent presets`, and `lcagent_route_preset`:
   - `quality`: GPT-5.5 low or the current best high-quality route.
   - `balanced`: DeepSeek V4 Pro or Kimi K2.6 with conservative context.
   - `cheap scout`: DeepSeek V4 Flash or another low-cost route for bounded
     read-only exploration, summarization, and small follow-up tasks. For
     direct CLI use, `lcagent scout ...` applies this lane and asks for a
     compact handoff.

### P3: Build Real Coding Evals

1. Extend the deterministic eval lane only for harness contracts.
   Keep it fast and no-network: trace shape, denials, patch summaries,
   verification accounting, continuation chains, command policy, and metrics.

2. Keep extending the repeatable live coding eval lane.
   The first live suite covers a small edit, bug fix, feature slice, repo
   orientation, current-diff review, and multi-file refactor. Next, add
   framework-specific cases and tune pass/fail scoring against repeated model
   runs.

3. Make model-tier comparisons first-class.
   Record helm model, optional scout model, context/tool profile, reasoning
   knobs, provider pinning, cache behavior, and final human score.

### P4: Add Bounded Delegation Without Chasing Subagents

Subagents are not a priority by themselves. The useful feature is efficient
model-tiered delegation for small jobs.

1. Add a lightweight delegated-job primitive.
   `lcagent scout` is now the first primitive: it runs a bounded cheap-scout
   prompt with autonomy off, low max turns, expected structured handoff, and a
   `delegation_mode` event while reusing normal `lcagent exec` artifacts.
   Remaining work is to expose it cleanly from embedded/Boss workflows.

2. Start with read-only scout jobs on cheaper/faster models.
   Examples: "map the relevant files", "summarize this package", "find likely
   failing test owners", or "extract API usage points". The main model decides
   whether to trust or verify the handoff. This is the current supported
   delegation shape.

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
