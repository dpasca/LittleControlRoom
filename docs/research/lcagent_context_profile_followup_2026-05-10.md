# LCAgent Large Context Follow-Up - 2026-05-10

This follow-up tests whether LCAgent can make better use of large-context
models by delaying loop compaction while keeping the same fixed target snapshot
used by the model-routing benchmark.

Printable report: [lcagent_context_profile_followup_2026-05-10_report.html](lcagent_context_profile_followup_2026-05-10_report.html)

## Setup

- Research project: Little Control Room, https://github.com/dpasca/LittleControlRoom
- Target snapshot inspected by the agents: `885fd24f1f24ce903b7de12d34c5166d54ebe251`
- Harness state: local `spike/lcagent-mvp` worktree after adding `--tool-profile`
  and `--context-profile`
- Prompt:

```text
please review lcagent, see whaat functionalities are missing, compared to the doc that describes how it shoudl be
```

All runs used:

- `--tool-profile generous`
- `--context-profile large`
- `--max-turns 32`
- `--request-timeout 10m`
- fixed target worktree under `/tmp`

The large context profile raises loop-compaction thresholds and retained
transcript/tool-output budgets. It does not change the model-facing task.

## Results

| Run | Provider | Status | Wall | Cost | Total Tokens | Cached | Reads | Lines | Overlap | Compactions | Forced Synthesis | Working Score |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| DeepSeek V4 Pro | DeepSeek API | complete | 337s | $0.077 | 720,504 | 555,648 | 23 | 4,666 | 0 | 0 | no | 6.8 |
| GPT-5.5 | OpenAI API | complete | 83s | $0.689 | 511,384 | 432,640 | 14 | 3,606 | 0 | 0 | no | 8.6 |
| GPT-5.5 none | OpenAI API | complete | 72s | $0.599 | 534,321 | 476,160 | 14 | 2,258 | 0 | 0 | no | 8.1 |
| GLM 5.1 | OpenRouter | complete | 305s | $0.566 | 713,163 | 420,672 | 17 | 3,467 | 0 | 0 | no | 7.1 |
| Kimi K2.6 | OpenRouter | complete | 420s | $0.236 | 543,949 | 414,590 | 20 | 3,683 | 0 | 0 | no | 7.7 |
| Kimi K2.6 | Moonshot API | complete | 172s | $0.256 | 933,634 | 825,600 | 26 | 5,509 | 0 | 0 | no | 7.4 |
| Kimi K2.6 | Moonshot API | aborted: TPD limit | 93s | $0.139 | 396,446 | 313,088 | 28 | 4,540 | 0 | 0 | n/a | n/a |

## Qualitative Notes

DeepSeek improved materially versus the earlier generous run:

- No overlapping read lines, down from 2,275.
- No loop compaction and no forced synthesis.
- Lower wall time than the previous generous run.
- Lower estimated cost than the previous generous run because cache reuse was
  strong, despite much higher total token volume.

The final answer was more useful than the earlier DeepSeek runs, but still had
calibration issues. It correctly recognized that the core milestones were mostly
implemented and stopped falsely marking `load_skill` as missing. It still
overstated some gaps and included a contradictory note around outline tools.

GPT-5.5 with low reasoning effort produced the strongest large-context answer in
this batch. It stayed efficient in the loop, used fewer reads than the Kimi and
DeepSeek large-context rows, attempted but did not overclaim denied verification,
and gave a well-calibrated priority list. The downside is cost: large-context GPT
was materially more expensive than the earlier balanced-context GPT run.

GPT-5.5 with `reasoning.effort=none` was faster and cheaper than low in this
large-context lane, with the same zero-overlap behavior. It was still useful,
but it took more model responses and the final answer was slightly less crisp.

GLM 5.1 improved substantially compared with the earlier low-effort GLM row. It
used large context well enough to avoid repeated reads and produced a detailed
spec-vs-code review. It still leaned more literal and checklist-like than the
best GPT/Kimi answers, and it spent a lot of output/reasoning tokens getting
there.

Kimi via OpenRouter remained the strongest lower-cost large-context route. It
identified implemented milestones, separated LCR-side session parity from
binary-level gaps, and found plausible concrete divergences: missing explicit
`permission_denied` events, no post-apply diff check, `go test` not allowed at
low autonomy despite the docs naming it as a normal workflow, missing fixture
scripts, and advanced LCR session stubs.

Direct Moonshot Kimi completed after the tier upgrade. It was much faster than
the OpenRouter route and preserved the same zero-overlap behavior, but it used
more total tokens and produced a broader, less precise final answer. An earlier
direct Moonshot attempt had hit the organization TPD limit before final output;
that row remains in the table as a quota/cost note rather than a scored run.
The OpenRouter/direct difference should be treated as observed route variance,
not proof that either route has intrinsically more precise inference.

## Takeaway

Large-context mode is worth keeping. It improved the central harness objective:
less duplicated reading without making the agent less informed. The best next
step is to keep `balanced` as the default context profile, use `large` for
models/routes with enough cache economics or context window, and add these
fields to future benchmark tables:

- tool profile
- context profile
- loop compaction count
- forced synthesis yes/no
- read overlap
