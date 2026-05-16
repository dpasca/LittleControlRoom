# LCAgent Live Eval Runs

This file records selected live-provider eval runs that are useful for route
calibration and harness priorities. Keep raw provider secrets out of this file;
record only commands, artifact locations, scores, and conclusions.

## 2026-05-16 Expanded Tough Cases

Purpose:

- Exercise the new `current_diff_review` and `multi_file_price_refactor` cases.
- Compare the intended `balanced` and `quality` coding lanes.
- Use the result to choose the next coding-agent parity slice.

Command shape:

```sh
go run ./cmd/lcagent live-eval \
  --case current_diff_review,multi_file_price_refactor \
  --provider <provider> \
  --model <model> \
  --auto low \
  --tool-profile balanced \
  --context-profile <profile> \
  --request-timeout 10m \
  --output json
```

Local run root:

- `/tmp/lcagent-expanded-live-eval-kAQnnW`

### Balanced Route Attempt

Route:

- Provider: `openrouter`
- Model: `deepseek/deepseek-v4-pro`
- Autonomy: `low`
- Tool profile: `balanced`
- Context profile: `balanced`

Result:

- Not a model-quality result.
- Both cases stopped before provider use because `OPENROUTER_API_KEY` was not
  available in the shell environment.

Artifacts:

- Report: `/tmp/lcagent-expanded-live-eval-kAQnnW/balanced.json`
- Data dir: `/tmp/lcagent-expanded-live-eval-kAQnnW/balanced-data`

### Quality Route

Route:

- Provider: `openai`
- Model: `gpt-5.5`
- Resolved model in trace: `gpt-5.5-2026-04-23`
- Autonomy: `low`
- Tool profile: `balanced`
- Context profile: `large`

Summary:

- Overall: failed, 1 of 2 cases passed.
- Total tokens: 51,399.
- Estimated cost: `$0.167475`.
- Provider failures/retries: 0.
- Trace quality: 50, `needs_attention`.

| Case | Result | Verification Status | Tokens | Cost | Notes |
| --- | --- | --- | ---: | ---: | --- |
| `current_diff_review` | PASS | `failed` expected and observed | 19,958 | `$0.055890` | Correctly kept the workspace read-only, found the seeded regression, and recorded failed verification as evidence. |
| `multi_file_price_refactor` | FAIL | `failed` | 31,441 | `$0.111585` | Reached correct final code, but hit the turn budget before rerunning verification and before emitting `final_response` with the changed files. |

Artifacts:

- Report: `/tmp/lcagent-expanded-live-eval-kAQnnW/quality.json`
- Data dir: `/tmp/lcagent-expanded-live-eval-kAQnnW/quality-data`
- `current_diff_review` artifact:
  `/tmp/lcagent-expanded-live-eval-kAQnnW/quality-data/lcagent/sessions/2026/05/16/lca_53440eae0b9dfc8f71986167.jsonl`
- `multi_file_price_refactor` artifact:
  `/tmp/lcagent-expanded-live-eval-kAQnnW/quality-data/lcagent/sessions/2026/05/16/lca_57188f9c0d005cac9f1a74c3.jsonl`

Follow-up inspection:

- The kept `multi_file_price_refactor` workspace passed a manual post-run
  `go test ./...` after the final `receipt.go` patch.
- The failure is therefore not primarily edit correctness; it is a continuation
  and verification-loop issue. The agent needed one more turn to rerun
  verification and report `item.go`, `cart.go`, and `receipt.go` in
  `files_changed`.

Conclusion:

- The next highest-leverage slice is real session continuity / continuation
  after turn budget, especially preserving pending verification state and files
  touched after the final tool call.
- A secondary harness follow-up is to rerun this route with a larger
  `--max-turns` value to separate route quality from the default live-eval turn
  budget.

Immediate follow-up implemented:

- Max-turn final handoff now carries harness-known files touched and recorded
  verification details into the compacted prompt and structured final events.
- Live-eval changed-file scoring now accepts provider `assistant_message` and
  `turn_complete` events, not only scripted `final_response` events.
