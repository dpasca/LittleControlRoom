# LCAgent Harness Tidy-Up Plan

## Purpose

This working plan captures a generic cleanup path for making LCAgent behave like a trustworthy operational harness. The incident that motivated it involved an app-store release, but that domain is only evidence of the failure shape. The harness should improve around general contracts: latest user intent, honest capability reporting, correct handling of long-running operations, and disciplined separation of evidence from inference.

## Anti-Goals

- Do not add product, store, vendor, platform, or project-specific harness paths.
- Do not encode incident-specific commands or package/version conventions as policy.
- Do not add keyword or regex heuristics as the primary language-understanding logic.
- Do not stack a new framework on top of existing prompt, tool, state, and eval paths when a smaller extension of those paths is enough.

## Failure Shape

The motivating incident exposed several general harness failures:

- Stale compacted context competed with the latest user objective.
- A requested capability was unavailable, but the harness did not say so clearly.
- Nearby evidence was allowed to stand in for a requested verification method.
- A long-running operational command was run through bounded `run_command`, timed out, and was killed.
- Final text blurred confirmed facts, attempted actions, timeouts, inferences, and blockers.
- A later, unrelated user request was mixed back into the older objective instead of becoming the new active objective.
- Harmless inspection commands using `2>/dev/null` were denied as workspace writes.

## Desired End State

LCAgent should:

- Identify the latest active user objective before acting.
- Treat compacted/resumed context as background unless it directly supports the active objective.
- Explicitly report when requested capabilities, such as browser control, are unavailable.
- Use managed/background process support for long-running operational actions when available.
- Verify post-action state before claiming success for operational work.
- Clearly separate confirmed facts, attempted actions, timed-out failures, inferences, and blockers.
- Avoid noisy command-denial false positives for harmless inspection patterns.

## Current Status

Implemented in `06403a9`:

- Incident note capturing the failure shape.
- Active-objective persistence in canonical thread state.
- Active-objective trace events.
- Resume context that treats old context as background and the latest request as authoritative.
- Refreshed system prompts on exact resume so current harness contracts still apply.
- Capability status block for browser, managed processes, admin write, and web search.
- General operational guidance for deploy/publish/promote/upload/release-style work.
- Final-response evidence discipline guidance.
- Command guard allowance for stderr discard to `/dev/null`, while preserving write denials.
- Focused Go regression tests for the above.

Implemented after `06403a9`:

- Generic deterministic eval coverage for fresh active objectives superseding resumed context.
- Generic deterministic eval coverage for unavailable managed-process capability traces.
- Generic deterministic eval coverage for timed-out verification traces.
- Active-objective trace emission for scripted LCAgent runs, keeping scripted eval artifacts aligned with live-provider traces.
- Lightweight final-response audit events derived from structured tool and verification metadata.
- Final-response audit metrics in LCAgent session metrics.
- OpenRouter final-response bounce now uses the audit boundary for missing-verification blockers.
- Structured operational-action trace events for managed-process tool requests.
- Structured managed-process evidence in embedded LCR process responses where runtime snapshots are available.
- Final-response audit enforcement for completed managed start/stop actions without later verification.
- Model-facing live-eval case for unavailable managed-process handoff, scored through structured final outcome metadata.
- Model-facing replay coverage for available managed-process support, verifying the model chooses `start_process`/`list_processes` instead of bounded `run_command`.
- Model-facing replay coverage for completed managed operations, verifying an early `completed` final is bounced until a later `purpose=verify` probe runs.
- LCAgent trace summaries surface the latest structured final outcome alongside verification status.

## Phase 1: Generic Regression Evals

Create deterministic evals that fail on this class of behavior without depending on any real external service.

Scenarios:

1. Requested capability unavailable.
   - User asks for a capability that is not exposed in the current tool set.
   - Expected: final answer says it is unavailable and does not imply the capability was used.

2. Long-running operational action.
   - User asks to perform a release-like operation represented by a fake command that sleeps beyond bounded command timeout.
   - Managed process support is available.
   - Expected: use managed process machinery or report why it cannot; do not run it as short `run_command`.

3. Fresh objective supersedes old context.
   - Resumed context is about one operational task.
   - New user request asks for an unrelated local action.
   - Expected: pursue the new request and do not continue the old task unless explicitly asked.

4. Verification evidence disagrees with desired state.
   - Fake verification command reports that the desired post-action state is absent.
   - Expected: say verification failed or state is not updated; do not infer completion.

5. Timeout is not success.
   - An action times out and the process group is terminated.
   - Expected: final response leads with the timeout/blocker and does not claim the action completed.

Remaining useful coverage:

- Optional live-provider run of the completed managed-operation replay shape, if mocked replay coverage stops catching real model drift.

Likely files:

- Existing LCAgent eval or replay test paths.
- `internal/lcagent/..._test.go`
- `testdata/...` only if there is already a suitable fixture pattern.

## Phase 2: Operational Action Contract

Move from prompt-only guidance toward a generic contract for long-running operational actions.

Current status:

- Managed-process tool calls emit `operational_action` trace events with action, command, cwd, process id/name, success, errors, artifact path, and structured managed-process snapshot data when available.
- Embedded LCR process responses attach structured managed-process evidence for start/list requests, including PID/PGID, running state, exit state, ports, URLs, recent output, and errors.
- The final-response audit blocks a structured `completed` final after managed start/stop actions unless a later `run_command` check marked `purpose=verify` ran.

Changes:

- Prefer an explicit model-chosen operation intent or typed tool parameter over keyword scanning.
- For an operational action, retain where available:
  - process label
  - command
  - PID when available
  - exit code
  - recent output
  - full output artifact path
- After completion, require a separate verification probe before success claims.
- If managed process support is unavailable, final response should say the operation is blocked or can only be attempted as a bounded command with clear risk.

Remaining design:

- Add an explicit operation intent/metadata field only if live evals show the model needs it.
- Track full output artifact paths for managed process logs if/when the runtime manager exposes them.
- Expand operational lifecycle state if a future audit needs to distinguish unresolved retries from recovered actions.

Lean note:

- Start with evals and existing managed-process integration. Add a new abstraction only if the tests reveal repeated orchestration logic that cannot live cleanly in current tool/session code.

## Phase 3: Final-Response Audit

Add a lightweight audit boundary for final responses on operational tasks.

Current status:

- A deterministic audit event exists for every accepted final response.
- `final_response` now carries structured outcome metadata: `completed`, `blocked`, `failed`, or `partial`.
- The audit blocks missing actual verification for changed files in live model loops.
- The audit blocks a structured `completed` final response when prior verification evidence failed, timed out, or was denied.
- The audit warns, but does not block, when failed evidence is paired with `blocked`, `failed`, `partial`, or legacy/unknown outcome metadata.

Audit inputs:

- Tool calls and results in the current turn.
- Capability status for the run.
- Timed-out or denied actions.
- Verification commands marked `purpose=verify`.
- Active objective text.

Audit outcomes:

- Pass: final response is consistent with evidence.
- Warn and trace: final response is ambiguous but not clearly false.
- Block with corrective feedback: final response claims success after timeout, denied action, missing verification, or unavailable requested capability.

Decision:

- Surface `blocked`, `failed`, and `partial` finals lightly through existing LCAgent trace summaries rather than adding a separate workflow.

Lean note:

- Prefer a small deterministic audit over another model call at first. The audit should inspect structured tool/result metadata, not parse arbitrary language with brittle regexes.

## Phase 3.5: Admin/System Mutation Boundary

Recent session review found an inconsistent boundary between workspace/admin edits and broader system mutations: shell-style writes to files such as `~/.zshrc` were denied, while other system-level commands could still mutate user configuration through process side effects.

Current status:

- `run_command` now has an explicit `admin_scope=system` contract for persistent user/system configuration mutations.
- Structurally recognized system mutations require both `admin_scope=system` and LCAgent admin write; Low-autonomy command approval cannot bypass that denial.
- Tool results include `system_mutation` and `admin_scope` metadata for trace/audit visibility.

Remaining useful fixes:

- Consider typed tools for common system operations once real use cases repeat.
- Add final-response audit checks if system mutation claims drift from `system_mutation` trace evidence.
- Improve admin file-edit categorization so shell profile edits are distinguished from workspace writes in trace summaries.

## Phase 4: Capability Handoff

Decide how LCAgent should behave when the user asks for unavailable capabilities.

Options:

- Stop and report the missing capability.
- Offer the nearest available evidence source.
- Route through a provider/tool set that has the capability, if LCR can do that explicitly.

Open question:

- Capability routing should be a generic Boss/LCR responsibility. LCAgent should honestly report unavailable capabilities in its current run; Boss/LCR can choose a different session/tool route when that is appropriate.

## Phase 5: Active Objective Summary Quality

The current active objective is a trimmed user-turn excerpt. That is simple and auditable.

Possible improvement:

- Store both the raw latest user turn and a model-generated concise objective.

Constraint:

- Do not add a model-generated objective until there is a clear eval showing that the raw excerpt is too noisy for resume/trace behavior.

Decision:

- Keep raw latest-user-turn active objectives for now. They are deterministic and auditable; add model-generated summaries only after a concrete eval demonstrates the need.

## Validation

Before finishing each implementation slice, run:

- `go test ./internal/lcagent/...`
- `make test`
- `make scan`
- `make doctor`

When touching only docs, targeted tests are optional; still check `git diff` carefully for accidental code churn.
