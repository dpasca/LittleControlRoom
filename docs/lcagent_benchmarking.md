# LCAgent Benchmarking

Use a fixed repository snapshot for before/after LCAgent comparisons. Running
against the active Little Control Room checkout is useful for ad hoc smoke tests,
but it makes cost and tool-use baselines drift as the source changes.

There are two commits to keep straight:

- Harness commit: the Little Control Room/LCAgent version under test.
- Target commit: the repository snapshot LCAgent inspects or edits.

For harness comparisons, vary the harness commit and keep the target commit,
prompt, model, provider, autonomy level, and data-dir freshness fixed.

## Archived Research Runs

Research artifacts should keep a compact human-readable note, a printable report
when useful, and structured summary data. Avoid committing raw stream logs by
default; they are large and usually belong in a temporary scratch root unless a
specific debugging task needs them.

- 2026-05-10 model-routing comparison:
  [research note](research/lcagent_model_benchmark_2026-05-10.md),
  [printable report](research/lcagent_model_benchmark_2026-05-10_report.html),
  [CSV results](research/lcagent_model_benchmark_2026-05-10_results.csv).
- 2026-05-10 large-context follow-up:
  [research note](research/lcagent_context_profile_followup_2026-05-10.md),
  [printable report](research/lcagent_context_profile_followup_2026-05-10_report.html),
  [CSV results](research/lcagent_context_profile_followup_2026-05-10_results.csv).

## Fixed Snapshot Workflow

## Deterministic Regression Eval

Run the no-network scripted eval lane before comparing live models:

```sh
make lcagent-eval
```

This creates temporary fixture repos and checks the LCAgent trace contract for:

- actual `purpose=verify` command traces plus final verification summaries
- patch diff summaries plus final verification summaries
- literal `replace_text` fallback edits plus diff summaries
- explicit permission-denial events
- low-autonomy `go test ./...` verification
- missing-verification contract detection after edits
- resume-context trace emission

For machine-readable output:

```sh
go run ./cmd/lcagent eval --output json
```

The lane is intentionally small. It protects harness behavior and metrics
columns; it does not score live model quality.

## Live Smoke

Before spending money on a comparison run, use the live smoke script:

```sh
make lcagent-live-smoke
```

The smoke command creates a temporary Go workspace, asks the configured live
provider to make one README edit, runs `go test ./...`, and checks the resulting
artifact for verified `purpose=verify` command traces. See
[docs/lcagent_live_smoke_script.md](lcagent_live_smoke_script.md) for provider
variants and the Boss goal-run smoke path.

## Repeatable Live Coding Eval

Use the live eval lane when the smoke test passes and you want a comparable
coding-quality run across models or harness commits:

```sh
make lcagent-live-eval
```

The live lane runs a fixed task suite in temporary Git workspaces and writes
LCAgent artifacts to a timestamped data dir under the Little Control Room data
root unless `--data-dir` is provided. It currently covers:

- `readme_edit_verify`: small edit plus explicit verification
- `go_bug_fix`: fix a failing unit test
- `feature_slice`: implement a small missing feature against tests
- `repo_orientation`: read-only repo orientation with verification
- `current_diff_review`: read-only review of a seeded uncommitted regression
  with expected failed verification
- `multi_file_price_refactor`: multi-file implementation refactor against tests

Selected live run notes are archived in
[docs/lcagent_live_eval_runs.md](lcagent_live_eval_runs.md).

List the suite without making provider calls:

```sh
go run ./cmd/lcagent live-eval --list
```

List the current coding route bundles without making provider calls:

```sh
go run ./cmd/lcagent presets
```

Run one case while keeping model-control knobs explicit:

```sh
go run ./cmd/lcagent live-eval \
  --case go_bug_fix \
  --route-preset balanced \
  --output json
```

For route-level comparisons, `lcagent exec --route-preset
balanced|quality|mimo-2.5-pro-low|mimo-2.5-pro-high|mimo-2.5-pro-max|cheap-scout ...` and `lcagent live-eval --route-preset
balanced|quality|mimo-2.5-pro-low|mimo-2.5-pro-high|mimo-2.5-pro-max|cheap-scout ...` apply the provider, model, autonomy,
reasoning, tool/context profile, timeout, and temperature bundle where the
command supports those knobs. Explicit flags still override preset values, so
record both the preset name and any overrides in benchmark notes. The balanced
DeepSeek lane uses explicit high reasoning because the model is inexpensive
enough that eval reliability is usually worth the extra tokens.

The MiMo lanes use direct Xiaomi model `mimo-v2.5-pro`, `temperature=0.2`,
and the `large` context profile.
Run `mimo-2.5-pro-low`, `mimo-2.5-pro-high`, and `mimo-2.5-pro-max` when
comparing reasoning effort. The `max` lane sends the accepted top reasoning
value, `xhigh`. When comparing with archived OpenRouter-pinned runs, record
the provider path because latency, routing, and billing may differ.

Each case reports correctness, recorded verification, the observed and expected
verification status, expected files touched, failed tool results, permission
denials, repair feedback, read volume, overlapping reads, trace quality
score/grade, token usage, estimated cost, artifact path, workspace path, and
wall time. Keep provider/model/profile/request-timeout values fixed when
comparing harness commits.

Pick a target commit and create an isolated worktree:

```sh
BENCH_REF=<commit-sha>
BENCH_ROOT=$(mktemp -d /tmp/lcagent-bench-XXXXXX)
git worktree add --detach "$BENCH_ROOT/repo" "$BENCH_REF"
```

Run one MiMo lane against that checked-out snapshot, writing artifacts to a
separate data dir:

```sh
BENCH_DATA="$BENCH_ROOT/data"
go run ./cmd/lcagent exec \
  --cwd "$BENCH_ROOT/repo" \
  --data-dir "$BENCH_DATA" \
  --auto off \
  --output stream-json \
  --route-preset mimo-2.5-pro-max \
  "review lcagent and compare it with the design docs"
```

For the three-way MiMo effort comparison, repeat that command with
`mimo-2.5-pro-low`, `mimo-2.5-pro-high`, and `mimo-2.5-pro-max`, keeping the
target commit, prompt, tool profile, context profile, timeout, and data-dir
freshness otherwise fixed.

Use `--tool-profile generous` for a qualitative read-budget lane. The default
`balanced` profile keeps read/list/search/outline output conservative. The
`generous` profile keeps the same tool set and harness behavior, but raises the
file-inspection budgets so a capable model can read larger contiguous ranges
after outline/search has identified central files. Treat it as a benchmark
variable, not as a free apples-to-apples replacement for balanced runs.

Use `--context-profile large` when the model and provider have enough context
window to justify delaying loop compaction. This keeps the default harness
conservative while allowing large-window benchmark lanes to preserve more raw
tool evidence before the harness summarizes the transcript. It is especially
useful when measuring whether a model can reduce duplicate reads once earlier
tool outputs remain available in context longer.

The `balanced` context profile now compacts provider-loop history at roughly
200k characters and keeps about 50k characters of transcript evidence in the
compacted continuation. The `large` profile raises those loop budgets to roughly
600k and 240k characters respectively.

Summarize the resulting session artifact:

```sh
go run ./cmd/lcagent metrics "$BENCH_DATA"/lcagent/sessions/*/*/*/*.jsonl
```

The metrics output includes timing rollups plus a `trace_quality` block. Treat
it as a calibration signal, not a product-grade leaderboard: it combines
observed elapsed time, model-response wait time, tool runtime by tool,
verified-session coverage, failed tool results, repair feedback, duplicate
repair suppression, read overlap, cached-token rate, and estimated cost so
route/profile changes can be compared without re-reading every JSONL trace by
hand.

Clean up when finished:

```sh
git worktree remove "$BENCH_ROOT/repo"
rm -rf "$BENCH_ROOT"
```

## What To Compare

Track at least:

- `tool_calls`
- `read_file_calls`
- `read_file_lines`
- `read_file_output_bytes`
- `read_file_overlapping_calls`
- `read_file_overlapping_lines`
- `tool_successes`
- `tool_failures`
- `trace_quality`
- `token_usage`
- `max_input_tokens`

Also record model-control knobs separately from provider labels:

- requested reasoning effort (`low`, `default`, `disabled`, etc.)
- temperature, including whether it was omitted
- provider routing/pinning, such as OpenRouter `provider.only`
- whether provider fallbacks were allowed
- prompt-cache strategy, especially explicit Anthropic `cache_control`
  breakpoints and cache read/write token accounting
- tool profile (`balanced` or `generous`), since it changes read/list/search
  and module-outline budgets
- context profile (`balanced` or `large`), since it changes when loop/final
  transcript compaction occurs and how much tool evidence is retained

For fair comparisons, keep the task prompt, model, provider, autonomy level,
data-dir freshness, and benchmark commit fixed.
