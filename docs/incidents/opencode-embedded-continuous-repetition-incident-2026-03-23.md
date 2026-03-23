# Little Control Room Incident Log: Embedded OpenCode Continuous Repetition / Potential Loop

## Incident overview

- **Date (JST):** 2026-03-23
- **Area:** Embedded OpenCode session UX and transcript streaming
- **Reporter symptom:** Output appeared to repeat continuously during an embedded OpenCode turn; required `Ctrl+C` to stop the turn.
- **Question:** Was the model stuck in a loop (e.g., GLM-5), or was this a rendering/streaming artifact?

## Data inspected

- OpenCode artifact DB: `~/.local/share/opencode/opencode.db`
- OpenCode log: `~/.local/share/opencode/log/2026-03-23T045604.log`
- Session under investigation (identified from title / project context):
  - `ses_2e6fee238ffeLrC6Ta0jkvvzOn`
  - Title: "Plan to compact details and runtime info panels"
  - Project: `/Users/davide/dev/repos/LittleControlRoom`
- Transcript tables read for the session:
  - `part`
  - `message`

## Findings

1. The transcript for this session does **not** contain repeated identical assistant text payloads.
   - A duplicate-content check was run over `part` rows with `type='text'` for this session.
   - No repeated exact chunks were found.
2. There is a normal long analysis/tool-heavy sequence before completion.
   - The sequence appears consistent with a legitimate multi-step turn including tool calls.
3. There is one explicit abort marker in session message history:
   - `MessageAbortedError: The operation was aborted.`
4. User follow-up (`"you're stuck ina loop"` / similar) appears **after** the abort in the transcript; the assistant replied that it was still analyzing and then completed.
5. OpenCode server logs around that period show ordinary prompt/loop steps and a bounded completion cycle, not an unbounded process exception loop.

## Conclusion

- Evidence does **not** support a model infinite-loop diagnosis.
- The best explanation is a **UI/streaming perception issue** during a long, tool-driven turn, plus a manual abort (`Ctrl+C`).
- In practical terms: likely a rendering/update noise issue, not an active model self-reproduction loop.

## Follow-up hypothesis for deeper analysis

- Some streamed fragments may be repeatedly visible because of partial update churn in the UI path.
- The transcript reconciliation logic (`append/merge by item ID`) can still behave like “repetition” to users if:
  - the same logical update arrives in rapid succession, or
  - the display applies additional refresh without smoothing deduplication at the view layer.

## Recommended deep-dive actions

1. Capture a raw stream trace for a reproducer turn:
   - record server-side deltas and the displayed UI rows together with timestamps.
2. Add a lightweight UI guard:
   - suppress consecutive visually-identical rendered rows when item identity has not changed.
3. Add a short-lived debug flag for streaming diagnostics:
   - log dedupe/merge decisions in debug builds only.
4. If this recurs, capture both:
   - model-side tool call timeline and
   - client render diff timeline,
   then correlate to confirm whether duplicate text originates in model output or display refresh behavior.

## Current status

- Logged for deeper analysis.
- No code changes made beyond documentation in this step.
- No new runtime assumptions changed; no footprint doc updates were required.
