# LCAgent search churn: 2026-09-15

## Conclusion

The model repeatedly chose more searches, but the harness has concrete defects
that make this an unreliable basis for comparing models. Fix instruction
continuation and search-filter optionality first, then evaluate convergence.
The initial investigation did not change runtime behavior or interrupt the
active task. The subsequent repair is described below.

## Evidence

- Task: `2026-09-15-new-task-17-26-25` (Find Aerei da Guerra Texture Mapping Issue).
- Thread: `lct_fcdf860a7e4425dd0af33169`.
- Run: `lca_eb334e2873670db129e56bed`.
- Trace: `~/.little-control-room/lcagent/sessions/2026/09/15/lca_eb334e2873670db129e56bed.jsonl`.
- Model/provider: `gpt-5.6-luna`, OpenAI, reasoning `xhigh`.
- Request: identify an issue of a 1990s magazine mentioning texture mapping in
  an Evans & Sutherland simulator.

At the trace cutoff **17:43:07 JST**, approximately 16 minutes after startup:

| Measurement | Observed |
| --- | ---: |
| Model responses | 32 |
| Tool calls started | 126 |
| Web searches | 114 |
| Browser navigations | 11 |
| Browser snapshots | 1 |
| Searches requesting `recency_days: 365` | 114 / 114 |
| Searches requesting maximum 10 results | 114 / 114 |
| Assistant progress messages or final responses | 0 |
| Synthesis requests or final-response feedback events | 0 |

Search queries were mostly different variants, with one exact query repeated.
The model commonly requested six searches per response; the CLI executed them
serially. All 114 completed web searches reported tool success. They returned
about 1.17 million characters, including many irrelevant contemporary results.
The loop compacted context at turn 23. The run was still active at the cutoff;
these figures are a bounded snapshot, not final totals.

## Findings

### 1. OpenAI continuation drops operating instructions

In `internal/lcagent/modeladapter/openrouter.go`, `responsesInput` returns empty
instructions when it builds a continuation, and `completeResponses` only sends
`instructions` when `usedPrevious` is false. The continuation test in
`openrouter_test.go` explicitly expects the second request to omit instructions.

OpenAI documents that instructions apply only to the current request and do
not carry over through `previous_response_id`. Consequently, this request path
drops system/developer instructions on continued tool turns. The original user
request, tool results, tool definitions, and transient budget notes can remain;
that does not preserve the operating instructions.

Source: [OpenAI instruction semantics](https://developers.openai.com/api/docs/guides/prompt-engineering#message-roles-and-instruction-following).

### 2. Every search applied an unsuitable publication-date filter

The `web_search` schema in `modeladapter/tool_definitions.go` describes
`recency_days` as optional but permits only integers from 1 through 365. It has
no explicit unrestricted value such as zero or null. `responsesTools` omits the
`strict` setting. OpenAI Responses attempts to normalize such schemas into
strict mode, where optional values need an explicit nullable representation.

This is a likely explanation for the model always filling in 365, but the trace
does not retain the provider-normalized schema, so that particular causal link
is not directly proven. The actual filter application is certain:
`tools/web_search.go` converts every positive value into Exa's
`startPublishedDate`. Thus these requests excluded pages published more than a
year earlier while trying to identify a historical magazine issue. The first
result set included contemporary aircraft and gaming articles.

Source: [OpenAI function-calling strict mode](https://developers.openai.com/api/docs/guides/function-calling#strict-mode).

### 3. The stall detector measures changed output, not useful progress

`openRouterLoopProgressTracker.Observe` in `internal/lcagent/cli.go` treats each
previously unseen tool-result hash as progress. It strips duration, but retains
the output, including the echoed search query. A different query therefore
counts as progress even if the evidence is equivalent or empty. Changed search
summaries and browser snapshots also prevent the stall counter from advancing.

The existing detector requires six no-progress turns after turn 12. That
condition does not capture this run's continued query reformulation.

### 4. Convergence guidance arrives late and ignores search count

The run's hard ceiling is 160 model responses. `budget_guidance.go` starts
consolidation at 50% (turn 80) and endgame at 85% (turn 136). One response can
request several searches, so 114 searches at turn 32 still leave the run in
the exploration phase. There is no evidence here of the harness rejecting a
completed answer and ordering further searches; the model kept requesting them.

## Repair plan from the initial investigation

1. Resend current system/developer instructions on every Responses request,
   including continuations; correct the test that expects their omission.
2. Give web search an explicit unrestricted date-filter representation and
   preserve optional values through the Responses schema contract. Verify that
   unrestricted Exa searches omit `startPublishedDate`.
3. Add earlier progress checkpoints based on actual work performed. Use a
   structured model assessment to distinguish new evidence, unanswered
   questions, a justified next search, and an honest partial answer. Avoid
   keyword or regex rules for research intent or relevance.
4. Compare the same model before and after those repairs with bounded search
   and latency measurements; then compare models if needed. This trace alone
   cannot quantify the model's independent contribution.

The existing test suite cannot establish correct live API behavior when a
mocked provider test encodes the wrong instruction-continuation contract.

## Implemented repair

- The Responses adapter now rebuilds and resends all current system/developer
  instructions on each request, while retaining `previous_response_id` and the
  incremental tool-output payload. Updated instructions replace stale ones.
- Responses function tools explicitly set `strict: false`, preserving the
  shared schemas' optional arguments. This matches the existing non-strict
  Chat Completions contract; it does not alter strict JSON-output schemas in
  the separate `internal/llm` client. A future strict function-schema compiler
  would need to preserve optionality and support the whole tool catalog.
- Shared web-search schemas and operating instructions now describe historical
  research and explicit unrestricted dates (`recency_days` omitted or zero).
  The search runner rejects values outside 0–365, and all search backends retain
  their existing unrestricted behavior for zero.
- Exa, Google, and SearXNG result formatting no longer repeats the query already
  present in the tool-call record. Identical results no longer receive distinct
  progress hashes solely because of a rephrased query. Browser-generated
  snapshots retain their native output format.
- After 12 web-search calls, the shared engineer loop starts consolidation
  guidance, independent of the model-turn budget. It asks for a user-facing
  update, an evidence assessment, and a specific reason for further searching.
  The host counter survives context compaction and resets on a steered request.
  This is advisory model guidance, not a search cap or semantic stop classifier.

### Provider coverage

| LCAgent provider | Current transport | Instruction handling |
| --- | --- | --- |
| OpenAI, any selected model | Responses | Current instructions resent alongside incremental continuation |
| DeepSeek | Chat Completions | Full current message history resent |
| OpenRouter, including models from other vendors | Chat Completions | Full current message history resent |
| Moonshot | Chat Completions | Full current message history resent |
| Xiaomi | Chat Completions | Full current message history resent |
| Ollama | Chat Completions compatibility endpoint | Full current message history resent |
| MLX | Chat Completions compatibility endpoint | Full current message history resent |

The seven-provider HTTP contract test checks a complete tool round trip,
updated system/developer instructions, and the shared optional date-filter
schema. Existing Responses tests also cover a compacted standalone request.
The separate JSON-schema inference clients send self-contained prompts and
do not use the faulty continuation path.

DeepSeek would have avoided this instruction-loss defect on our existing Chat
Completions path. Its task outcome is unknown: the same search tools and loop
still apply, and no controlled live model comparison was performed. The defect
is tied to our handling of OpenAI Responses continuation, not to model weights
or to an assumption that all vendors implement Responses identically.
For example, [DeepSeek's Responses compatibility documentation](https://api-docs.deepseek.com/guides/responses_api/#compatibility-details)
states that its endpoint is stateless and does not support `previous_response_id`.
LCAgent continues to use Chat Completions for DeepSeek.

### Remaining limits

The repetition detector still measures exact tool evidence, not semantic
relevance. Changed snippets, novel but irrelevant results, or browser snapshots
can still look new. The model now receives its instructions and earlier
convergence guidance, but an enforced structured progress assessment remains a
separate improvement. No live provider/search spending or production deployment
was performed to claim that every research task now converges.

## Validation

- `make test`: passed, including `go vet ./...`.
- `make scan`: passed with an isolated database/config under
  `dist/search-churn-validation` and this worktree as the include path.
- `make doctor`: passed against that isolated database/config.
- Regression coverage includes a real local HTTP tool loop with two batches of
  six searches and intervening context compaction; the third model request
  receives consolidation guidance with the preserved count of 12.
- Date-filter tests check omission and positive values for Exa, Google, and
  SearXNG, zero forwarding to browser search, and rejection of invalid values.
- No TUI implementation changes; no interactive TUI check needed.
