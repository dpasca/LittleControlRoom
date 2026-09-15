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

### Direct image input in the main conversation

Image transport and the saved Main Model image-input check already existed,
but only independent vision requests used them. The main tool conversation
previously contained text descriptions of images, even with Vision Provider
set to Main Model.

When the configured vision route matches the main provider and model,
`view_image` now loads pixels into the ongoing conversation without auxiliary
inference. This includes Auto after the existing Main Model image check has
passed, or explicit `--vision-provider main` with no different vision model.
Capability is declared through that existing configuration; it is not guessed
from model names. A separate configured vision model remains the text-only
main model's fallback. Off still disables the image tools.

Legacy `analyze_image` inspection calls take the same direct path, including
two-image comparisons. `analyze_image purpose=verify` deliberately retains an
independent request and the acceptance audit. Loading pixels cannot satisfy
an acceptance check or erase a failed one. Prompts and tool descriptions direct
ordinary screenshot reading and navigation to `view_image`.

The adapters send Chat Completions `image_url` blocks or Responses `input_image`
blocks in the ongoing history, with the current instructions and tools. All
tool results in a batch precede the image messages. Responses continuation
retains its main response ID across independent QA. The OpenAI encoding follows
the [official image-input guide](https://developers.openai.com/api/docs/guides/images-vision).

The host copies viewed pixels to session artifacts and saves their paths,
content hashes, sizes, and MIME types in conversation checkpoints. Base64 is
created only for provider requests. Changing the source screenshot does not
change earlier evidence; missing or modified saved artifacts produce an explicit
unavailable-pixels message. Files must be nonempty regular PNG, JPEG, GIF, or
WebP inputs of at most 25 MiB and obey the run's read scope.

Recent image attachments survive loop compaction and exact resume. At most four
images totaling 25 MiB stay attached; older references remain available for
reloading. Evicting pixels resets provider continuation so server-side history
cannot defeat that bound. Image messages carry a host origin field so they do
not replace the user's objective. Switching to a route without native input
removes attached pixels explicitly. Final synthesis remains a text handoff and
states that it relies on recorded observations rather than a new inspection.

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

Native image tests also cover all seven adapters, mixed tool batches, direct
inspection through both tool names, independent QA, text-only fallback, exact
checkpoint reload, immutable saved pixels, compaction, model-route changes,
missing/modified artifacts, payload bounds, and workspace read restrictions.
These are transport and harness regressions, not live qualification of each
provider's current model vision capabilities.

These changes apply to models using the shared LCAgent engineer loop. They do
not add a scanner driver, a desktop automation API, or a semantic judge of
artifact correctness. The model can still make a poor progress assessment or
choose weak verification; shared instructions now explicitly require checks
of the actual requested result. A checkpoint costs a model turn and a provider
request, and a tool batch can exceed the checkpoint interval before returning.
No paid live model comparison or production deployment is claimed.

## Validation of initial repairs

- `make test`: passed, including `go vet ./...`. The first concurrent run hit
  the existing browser-version inherited-pipe test's two-second timeout; that
  test passed in isolation, and the subsequent full run passed.
- `make scan`: passed with this worktree as the include path and an isolated
  config/database under `dist/harness-validation`.
- `make doctor`: passed against that isolated database.
- No TUI implementation changed; progress uses the existing assistant-message
  event and image results use the existing trace rendering.
- Validation logs remain under the ignored `dist/harness-validation` directory.

## Validation of native image input

- `make test`: passed, including `go vet ./...` and the new image regressions.
- `make scan` and `make doctor`: passed with isolated config/database paths
  under `dist/native-vision-validation` and this worktree as the include path.
- Logs remain in that ignored validation directory. No live model benchmark
  or production deployment was performed.
