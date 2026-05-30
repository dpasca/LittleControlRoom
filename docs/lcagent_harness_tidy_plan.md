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

- A model-facing eval or replay that verifies the model chooses the right tool path, not only that scripted traces record the right evidence.
- A final-response audit eval once the audit boundary exists.

Likely files:

- Existing LCAgent eval or replay test paths.
- `internal/lcagent/..._test.go`
- `testdata/...` only if there is already a suitable fixture pattern.

## Phase 2: Operational Action Contract

Move from prompt-only guidance toward a generic contract for long-running operational actions.

Changes:

- Prefer an explicit model-chosen operation intent or typed tool parameter over keyword scanning.
- For an operational action, retain:
  - process label
  - command
  - PID when available
  - exit code
  - recent output
  - full output artifact path
- After completion, require a separate verification probe before success claims.
- If managed process support is unavailable, final response should say the operation is blocked or can only be attempted as a bounded command with clear risk.

Lean note:

- Start with evals and existing managed-process integration. Add a new abstraction only if the tests reveal repeated orchestration logic that cannot live cleanly in current tool/session code.

## Phase 3: Final-Response Audit

Add a lightweight audit boundary for final responses on operational tasks.

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

Lean note:

- Prefer a small deterministic audit over another model call at first. The audit should inspect structured tool/result metadata, not parse arbitrary language with brittle regexes.

## Phase 4: Capability Handoff

Decide how LCAgent should behave when the user asks for unavailable capabilities.

Options:

- Stop and report the missing capability.
- Offer the nearest available evidence source.
- Route through a provider/tool set that has the capability, if LCR can do that explicitly.

Open question:

- Should capability routing be a generic Boss/LCR responsibility rather than something LCAgent owns?

## Phase 5: Active Objective Summary Quality

The current active objective is a trimmed user-turn excerpt. That is simple and auditable.

Possible improvement:

- Store both the raw latest user turn and a model-generated concise objective.

Constraint:

- Do not add a model-generated objective until there is a clear eval showing that the raw excerpt is too noisy for resume/trace behavior.

## Validation

Before finishing each implementation slice, run:

- `go test ./internal/lcagent/...`
- `make test`
- `make scan`
- `make doctor`

When touching only docs, targeted tests are optional; still check `git diff` carefully for accidental code churn.
