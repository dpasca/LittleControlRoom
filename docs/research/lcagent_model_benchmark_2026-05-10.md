# LCAgent Model Benchmark, 2026-05-10

This is a research artifact for the May 2026 LCAgent harness/model-routing
tests. It is intentionally lightweight: keep the printable report and compact
summary data in git, but do not commit the raw stream logs unless a future
investigation needs line-by-line replay evidence.

## Artifacts

- Printable report: [lcagent_model_benchmark_2026-05-10_report.html](lcagent_model_benchmark_2026-05-10_report.html)
- Summary data: [lcagent_model_benchmark_2026-05-10_results.csv](lcagent_model_benchmark_2026-05-10_results.csv)
- Benchmarking workflow notes: [../lcagent_benchmarking.md](../lcagent_benchmarking.md)

## Setup

- Report date: 2026-05-10 JST
- Harness branch at artifact creation: `spike/lcagent-mvp`
- Harness commit before benchmark and docs wrapping edits: `f5908ac384818831d9cb29bdd910ae96ed02be7a`
- Medium-effort follow-up harness commit: `3bdb7b2e41581814716195d76eddab8c26fcaa42`
- Archival artifact state: the repository commit that contains this file
- Target snapshot inspected by the agents: `885fd24f1f24ce903b7de12d34c5166d54ebe251`
- Original scratch root: `/tmp/lcagent-bench-20260509T203500`
- Medium-effort scratch root: `/tmp/lcagent-bench-medium-20260510T104803`
- Turn budget for final comparison runs: 32
- Timeout for final comparison runs: 10 minutes
- OpenRouter retry routing used strict origin-provider pins with fallbacks
  disabled and required parameter support:
  `anthropic` for Claude Sonnet 4.6 and `minimax` for MiniMax M2.7.
- Temperature was explicit for the retry runs. OpenRouter and direct DeepSeek
  chat-completions runs used `temperature=0.2`; OpenAI Responses API runs and
  direct Moonshot runs omitted temperature.

The original prompt, including typos, was:

```text
please review lcagent, see whaat functionalities are missing, compared to the doc that describes how it shoudl be
```

## What This Test Was For

The benchmark was not meant to prove a universal model ranking. It was meant to
answer a narrower routing question for LCAgent:

- Which model should be the default for this harness?
- Which lower-cost model is plausible as a secondary lane?
- Which expensive model is worth keeping as a verification pass?
- Do structured-output reliability, cache behavior, and tool discipline change
  the choice, not just final answer quality?

## Takeaways

GPT-5.5 with `reasoning.effort=low` was the best default. It produced the best
overall answer with reliable tool behavior and good cache reuse.

Claude Opus 4.7 produced the most detailed audit-style review, but GPT-5.5 low
still scored higher overall because the score is a default-routing score, not a
pure answer-quality score. Opus's cost and lack of reported cache hits make it a
verification lane rather than the default.

Kimi K2.6 was the best budget secondary in this batch. It was cheaper than GPT
and produced a usable answer, but it wandered more.

Gemini 3.1 Flash Lite was very fast and cheap, but too shallow to be the default
review model. DeepSeek V4 Pro was extremely cheap but weak and slow in this
task. Grok 4.3 completed but inspected too little evidence.

Strict OpenRouter origin routing improved reliability for Claude Sonnet 4.6 and
MiniMax M2.7: both completed when pinned to their native providers. That did
not change the recommendation. Sonnet was slow, costly, forced into synthesis,
and still made false missing-feature claims. MiniMax became a valid completed
run, but its final answers missed important gaps and added false tool/skill
claims.

The sibling ChatNext3 legal-intent eval reached a similar operational lesson:
origin-provider routing is a useful control variable, especially with fallbacks
disabled and required parameters enabled, but it does not by itself rescue model
behavior on tasks where the model drifts or overclaims.

No model landed in the ideal quadrant of high score and low cost. The practical
shape from this run is:

- Default: GPT-5.5 low
- Budget secondary: Kimi K2.6
- Expensive verification: Claude Opus 4.7

## Medium Effort Follow-up

The medium-effort follow-up did not change the routing recommendation. It helped
Claude Sonnet 4.6 substantially, kept Claude Opus 4.7 strong, and made GPT-5.5
more expensive without making it more useful than the low-effort run.

Kimi K2.6 was not rerun in this pass because the direct Moonshot adapter does
not accept the LCAgent `reasoning_effort` option.

| Run | Status | Score | Cost | Verdict |
|---|---:|---:|---:|---|
| Claude Opus 4.7 medium | complete | 8.2 | $1.817 | Excellent audit structure again; one false gap and premium cost keep it in the verification lane. |
| GPT-5.5 medium | complete | 7.4 | $0.900 | Useful answer, but more expensive and less calibrated than GPT-5.5 low. |
| Claude Sonnet 4.6 pinned medium | complete | 7.2 | $1.726 | Medium effort substantially improved the final answer, but it stayed slow and costly. |
| Gemini 3.1 Flash Lite medium | complete | 5.7 | $0.036 | Still cheap and fast, but under-called real gaps and became less useful than low. |
| DeepSeek V4 Pro medium | complete | 5.3 | $0.069 | Cheap again, but medium did not fix false tool and architecture claims. |
| Gemini 3 Flash Preview medium | complete | 5.1 | $0.123 | More substantial than low, but included unearned verification claims and factual misses. |
| Gemini 3.1 Pro Custom Tools medium | complete | 4.4 | $0.520 | Medium effort increased cost and still made false missing-tool claims. |
| GLM 5.1 medium | complete | 3.8 | $0.394 | Slower and more confidently wrong about implemented tools. |
| Grok 4.3 medium | complete | 3.0 | $0.227 | Read the right docs this time, but falsely marked core implemented tools and LCR wiring as absent. |

Aborted or unpinned attempts are excluded from the main scoring table and chart.
The excluded set was useful diagnostically but not comparable as route
candidates: unpinned MiniMax and Sonnet attempts failed final-output reliability,
and the pinned MiniMax medium retry also emitted an invalid `final_response`
schema.

## Scoring Evidence Excerpts

These excerpts are short snippets from the generated final answers, quoted to
make the subjective scores inspectable. They are not treated as ground truth;
they show why a run was scored up or down.

| Run | Excerpt | Why It Mattered |
|---|---|---|
| GPT-5.5 low | "the MVP is largely implemented and in several places goes beyond the original first-slice handoff" | Correctly framed the task: not a missing-skeleton story, but a smaller set of harness-quality gaps. |
| GPT-5.5 low | "lcagent is not missing the core MVP skeleton" | Strong bottom-line calibration, with specific gaps called out after that. |
| Claude Opus 4.7 | "Milestone 1 is essentially complete, Milestone 2 is in place, Milestone 3 is partially wired" | Best audit-style structure and lifecycle awareness, which is why answer quality was high. |
| Claude Opus 4.7 | "permission_denied event, the LCR-side launcher... future-context scaffolding" | Found nuanced follow-up gaps, but the run was too expensive for default routing. |
| Kimi K2.6 | "The biggest confirmed holes are the two missing outline tools" | Useful budget answer, but it also wandered into weaker claims like `plan_item` and piped stdin. |
| Grok 4.3 | "vs. `docs/ai_coding_agent_feasibility.md`" | Penalized because the user asked for the implementation handoff doc; this targeted the wrong comparison source. |
| Claude Sonnet 4.6 pinned | "no search finds `\"turn_aborted\"` written as a structured event anywhere" | This was false in the benchmark snapshot; strict provider routing fixed completion but not overclaiming. |
| MiniMax M2.7 pinned default | "`load_skill` implementation ... MISSING" | Also false in the benchmark snapshot; the run completed cheaply but missed important implemented behavior. |
| MiniMax M2.7 pinned low | "`search` tool ... No `search` tool implementation" | A broader false-missing-feature claim than the default-effort MiniMax retry. |
| Gemini 3 Flash Preview | "Tool-Call Markup Guardrail ... Missing" | Fast, but it confidently missed existing provider-markup guardrail work. |
| Claude Opus 4.7 medium | "Milestones 1-3 are essentially in place" | Strong medium-effort calibration, but still too expensive for default routing. |
| GPT-5.5 medium | "`--dry-run` is documented but not implemented" | Penalized because `dry-run` was not actually in the benchmark docs; medium effort added a false headline gap. |
| Claude Sonnet 4.6 pinned medium | "No approval/interrupt loop for autonomy `low`" | Medium effort gave a much more relevant final than the low run, including real lifecycle gaps. |
| Gemini 3.1 Pro Custom Tools medium | "`load_skill` tool logic ... appears to be missing or incomplete" | False in the benchmark snapshot; medium effort did not fix overclaiming. |
| GLM 5.1 medium | "no `apply_patch` or diff-application tool" | False in the benchmark snapshot and more damaging than the low-effort GLM answer. |
| Grok 4.3 medium | "`update_plan` ... absent" | False in the benchmark snapshot; it read the right docs but missed implemented core tools. |

## Caveats

This is a single-task, single-snapshot benchmark with subjective scoring. Treat
the scores as a useful routing signal, not a pure model-quality leaderboard.

Provider pricing, model aliases, cache accounting, and structured-output
behavior can change. If this artifact is used for a future decision, rerun the
same prompt against the same target snapshot and compare with the CSV here.

The `reasoning_effort` CSV column records only the requested effort setting
(`low`, `default`, or `disabled`). It is separate from reported
`reasoning_tokens`, because some providers report internal reasoning tokens even
when no explicit effort was requested.

The raw stream logs were left in the local scratch root during the experiment.
They are intentionally not part of this artifact because they are large,
provider-specific, and mostly useful for debugging one run at a time.
