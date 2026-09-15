# LCAgent scanner task: shared harness repairs

## Incident

Task `2026-09-15-new-task-20-43-02`, thread
`lct_9eee1fd91eac7413dc3e4497`, ran with OpenAI `gpt-5.6-luna`
at `xhigh`. Its trace contains 124 main-model responses and 135 tool calls
between 20:43:59 and 20:59:54 JST. After the user asked to keep the desktop
available and consider shell tools, the model made 45 more tool calls.

The model spent most of its time navigating a scanner application's folder
chooser. All 14 image observations became failed visual checks. The final
verification failure referred to an earlier folder chooser, while the only
command verification listed filenames. It did not inspect the resulting PDF's
contents, freshness, or page count. The final answer reported one scanned side
and left the reverse side and descriptive filename unfinished.

The first plain-text answer explicitly asked the user to flip the document.
When the harness required structured final metadata, the replacement answer
omitted that actionable handoff.

## Changes

### Explicit image observation and verification

`analyze_image` now defaults to `purpose: "inspect"`. The vision model answers
the visual question with observations and limitations, without grading the
overall task. Inspection results, including provider errors, do not create or
overwrite acceptance evidence. They cannot clear a failed verification or
satisfy a required visual/temporal check.

`purpose: "verify"` retains the existing pass/fail/uncertain behavior and final
audit. The tool schema, operating instructions, and missing-evidence feedback
explain this distinction. Existing scripted acceptance calls must explicitly
add `purpose: "verify"`; existing recorded traces are not rewritten. The
screenshot schema also documents the existing artifact-directory restriction.

### Structured progress checkpoints

The shared engineer loop requires a `report_progress` call after 12 executed
tools, after a minute with tool activity since the last checkpoint, or when a
steered user message is consumed. These are checked between model/tool batches;
they do not interrupt an in-flight tool or provider request. Counters live
outside compacted conversation history.

Only the report tool is exposed during a checkpoint. The host validates its
schema and rejects all execution calls, including actions batched alongside a
valid report. One invalid response gets a retry; a second ends the run with an
explicit error. The report contains the overall objective, current user
constraints, new evidence, blocker, decision, next action, and a user-facing
update. The update is emitted through the existing assistant-message event.
Choosing `finish` leads to finalization; it does not assert completion or bypass
the final audit.

The model interprets relevance and whether to continue or change strategy.
No keyword/regex intent gates or scanner-specific routing were added. Steering
no longer mechanically replaces the saved objective with the latest sentence;
the model's structured assessment reconciles it with the original task.

### Stable command evidence

Command results include a hash of raw stdout and stderr before truncation and
presentation. The repetition tracker compares that hash, working directory,
success, exit code, and timeout state, rather than command spelling, elapsed
time, or generated output-artifact filenames. Output changes beyond the inline
limit remain detectable. Existing file-change and verification counters remain
separate progress signals. Older results without a hash keep the previous
comparison behavior.

### Preserve final answers during metadata conversion

When structured finalization follows a plain-text answer, the host retains the
original answer verbatim and asks the model for outcome and verification
metadata. It rejects additional execution during conversion. A failed audit
reopens the normal repair path and clears the retained draft; a new user message
also cancels pending conversion. Format-only retries are bounded.

This preserves human handoffs without classifying questions by their wording.
It does not prove that every factual claim in the retained answer is true.

## Coverage and limits

Local HTTP tests exercise the checkpoint and finalization flow through all seven
provider adapters: OpenAI Responses, and DeepSeek, OpenRouter, Moonshot, Xiaomi,
Ollama, and MLX Chat Completions. Additional regressions cover compaction,
steering, invalid reports, rejected execution, audit-triggered repair, image
inspection versus verification, and real command output with timing changes
and truncated tails.

These changes apply to models using the shared LCAgent engineer loop. They do
not add a scanner driver, a desktop automation API, or a semantic judge of
artifact correctness. The model can still make a poor progress assessment or
choose weak verification; shared instructions now explicitly require checks
of the actual requested result. A checkpoint costs a model turn and a provider
request, and a tool batch can exceed the checkpoint interval before returning.
No paid live model comparison or production deployment is claimed.

## Validation

- `make test`: passed, including `go vet ./...`. The first concurrent run hit
  the existing browser-version inherited-pipe test's two-second timeout; that
  test passed in isolation, and the subsequent full run passed.
- `make scan`: passed with this worktree as the include path and an isolated
  config/database under `dist/harness-validation`.
- `make doctor`: passed against that isolated database.
- No TUI implementation changed; progress uses the existing assistant-message
  event and image results use the existing trace rendering.
- Validation logs remain under the ignored `dist/harness-validation` directory.
