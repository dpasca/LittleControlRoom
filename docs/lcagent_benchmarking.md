# LCAgent Benchmarking

Use a fixed repository snapshot for before/after LCAgent comparisons. Running
against the active Little Control Room checkout is useful for ad hoc smoke tests,
but it makes cost and tool-use baselines drift as the source changes.

There are two commits to keep straight:

- Harness commit: the Little Control Room/LCAgent version under test.
- Target commit: the repository snapshot LCAgent inspects or edits.

For harness comparisons, vary the harness commit and keep the target commit,
prompt, model, provider, autonomy level, and data-dir freshness fixed.

## Fixed Snapshot Workflow

Pick a target commit and create an isolated worktree:

```sh
BENCH_REF=<commit-sha>
BENCH_ROOT=$(mktemp -d /tmp/lcagent-bench-XXXXXX)
git worktree add --detach "$BENCH_ROOT/repo" "$BENCH_REF"
```

Run LCAgent against that checked-out snapshot, writing artifacts to a separate
data dir:

```sh
BENCH_DATA="$BENCH_ROOT/data"
go run ./cmd/lcagent exec \
  --cwd "$BENCH_ROOT/repo" \
  --data-dir "$BENCH_DATA" \
  --auto off \
  --output stream-json \
  --provider openrouter \
  --model deepseek/deepseek-v4-pro \
  "review lcagent and compare it with the design docs"
```

Summarize the resulting session artifact:

```sh
go run ./cmd/lcagent metrics "$BENCH_DATA"/lcagent/sessions/*/*/*/*.jsonl
```

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
- `token_usage`
- `max_input_tokens`

For fair comparisons, keep the task prompt, model, provider, autonomy level,
data-dir freshness, and benchmark commit fixed.
