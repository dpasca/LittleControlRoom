# LCAgent Live Smoke Script

Use this after changing LCAgent launch, provider setup, goal-run harvesting, or
tool policy. It makes one live model call against a temporary Go workspace and
keeps the LCAgent JSONL artifact under the normal Little Control Room data dir.

## Automated Smoke

Run:

```sh
make lcagent-live-smoke
```

Useful variants:

```sh
go run ./cmd/lcagent smoke --provider deepseek --env-file /path/to/provider.env
go run ./cmd/lcagent smoke --provider openai --model gpt-5.6 --reasoning-effort low
go run ./cmd/lcagent smoke --output json --keep-temp
```

Expected result:

- The command exits 0.
- `README.md` in the temporary workspace receives the exact smoke line.
- The session artifact records `verification_status=verified`.
- Metrics show at least one patch diff summary and an actual
  `purpose=verify` command trace.

If it fails before a session starts, check the selected provider key in the env
file or process environment. If it starts but fails verification, inspect the
printed artifact with:

```sh
go run ./cmd/lcagent metrics <artifact.jsonl>
```

The metrics output includes timing rollups (`observed_elapsed_seconds`,
`model_response_wait_seconds`, `tool_seconds`, and slowest gaps/runs) to help
separate provider/model latency from local tool work.

## Manual Chat Smoke

From Chat, ask for one traceable LCAgent task against a disposable project,
for example:

```text
Have LCAgent take a scoped task to inspect this temporary repo, make a one-line README change, run go test ./... as a verification command, and report the result as a Chat goal.
```

Expected Chat behavior:

- Chat proposes an `lcagent_task` goal.
- After approval, the goal trace records create, launch, await, and verify steps.
- The goal completes only after LCAgent finishes and the trace is harvested.
- `goal_run_report` shows the LCAgent session id, files changed, verification
  status, verification command, and any denials.
