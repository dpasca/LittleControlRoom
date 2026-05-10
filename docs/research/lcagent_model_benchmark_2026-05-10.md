# LCAgent Model Benchmark, 2026-05-10

This is a research artifact for the May 2026 LCAgent harness/model-routing
tests in the Little Control Room project:
<https://github.com/dpasca/LittleControlRoom>. It is intentionally lightweight:
keep the printable report and compact summary data in git, but do not commit
the raw stream logs unless a future investigation needs line-by-line replay
evidence.

## Artifacts

- Printable report: [lcagent_model_benchmark_2026-05-10_report.html](lcagent_model_benchmark_2026-05-10_report.html)
- Summary data: [lcagent_model_benchmark_2026-05-10_results.csv](lcagent_model_benchmark_2026-05-10_results.csv)
- Benchmarking workflow notes: [../lcagent_benchmarking.md](../lcagent_benchmarking.md)

## Setup

- Report date: 2026-05-10 JST
- Harness branch at artifact creation: `spike/lcagent-mvp`
- Harness commit for low/default-effort runs: `f5908ac384818831d9cb29bdd910ae96ed02be7a`
- Harness commit for medium-effort runs: `3bdb7b2e41581814716195d76eddab8c26fcaa42`
- Artifact commit: the repository commit that contains this file
- Target snapshot inspected by the agents: `885fd24f1f24ce903b7de12d34c5166d54ebe251`
- Benchmark operator/evaluator: Codex running GPT-5 with XHigh reasoning; no
  delegated subagents were used for scoring or report edits.
- Turn budget for final comparison runs: 32
- Timeout for final comparison runs: 10 minutes
- Provider routing is recorded in the CSV. Completed Claude Sonnet 4.6, Claude
  Opus 4.7, and MiniMax M2.7 rows used strict OpenRouter origin-provider
  pins with fallbacks disabled and required parameter support (`anthropic` and
  `minimax`). Other completed OpenRouter rows used default OpenRouter routing
  (`provider_pin=none`).
- Temperature was explicit for most chat-completions runs. OpenRouter and direct DeepSeek
  chat-completions runs used `temperature=0.2`; OpenAI Responses API runs,
  direct Moonshot runs, and Claude Opus 4.7 runs omitted temperature.
- Prompt caching was treated as part of the harness configuration. Anthropic
  Claude rows used explicit `cache_control` breakpoints, because Anthropic
  caching is not automatic by default in OpenRouter the way it is for several
  other providers.

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

The task simulates typical headless coding-agent work: inspect a fixed source
snapshot, use search and file-read tools, compare the implementation against a
project document, preserve enough context to synthesize a final review, and
finish with a structured `final_response`. No file edits were expected. That
made it a useful benchmark for source-reading discipline, cache behavior,
tool-call overhead, final-answer calibration, and route reliability.

Cost matters because this is repeated agent-routing work, not a one-off demo.
That is why low reasoning effort stays in the comparison as a first-class route,
with medium effort sampled when there is a plausible quality or reliability
return.

## Scoring Method

The score is a 0-10 operator routing score, not a pure model leaderboard. It was
assigned after inspecting each final answer alongside the run metrics in the
CSV. The rough weighting was:

- Answer correctness, calibration, and concrete gap identification: 40%.
- Tool discipline, evidence gathering, and using the right comparison source:
  20%.
- Structured-output and harness reliability: 15%.
- Cost and cache behavior for this harness: 15%.
- Latency and practical routing ergonomics: 10%.

Runs were penalized heavily for invalid or missing final output, reviewing the
wrong document, confident false missing-feature claims, or forcing a synthesis
after wandering. Concise, actionable, well-calibrated findings were rewarded.
The evidence excerpts below show representative reasons for scoring runs up or
down, but the excerpts are illustrative rather than a separate formula.

## Takeaways

**GPT-5.5** with `reasoning.effort=low` was the best default. It produced the best
overall answer with reliable tool behavior and good cache reuse.

**Claude Opus 4.7** produced the most detailed audit-style review, but GPT-5.5 low
still scored higher overall because the score is a default-routing score, not a
pure answer-quality score. Opus used Anthropic prompt caching and still remained
a premium verification lane rather than the default.

Notable mentions: **Kimi K2.6** was the best budget secondary in this batch. It
was cheaper than GPT and produced a usable answer, but it wandered more.

**Gemini 3.1 Flash Lite** was very fast and cheap, but too shallow to be the
default review model. **DeepSeek V4 Pro** was extremely cheap but weak and slow
in this task. **Grok 4.3** completed but inspected too little evidence.

Strict OpenRouter origin routing was used for **Claude Sonnet 4.6** and
**MiniMax M2.7**. Claude rows used explicit Anthropic cache controls; the cache
reads were real, but cache writes are not free and Sonnet was still slow and
uneven, with the low-effort run forced into synthesis and the medium-effort run
too narrow. MiniMax completed reliably, but its final answers missed important
gaps and added false tool/skill claims.

The sibling ChatNext3 legal-intent eval reached a similar operational lesson:
origin-provider routing is a useful control variable, especially with fallbacks
disabled and required parameters enabled, but it does not by itself rescue model
behavior on tasks where the model drifts or overclaims.

No model landed in the ideal quadrant of high score and low cost. The practical
shape from this run is:

- Default: **GPT-5.5** low
- Budget secondary: **Kimi K2.6**
- Expensive verification: **Claude Opus 4.7**

## Medium Effort Runs

The medium-effort runs did not change the routing recommendation. They kept
Claude Opus 4.7 strong, gave Claude Sonnet 4.6 strong cache reads but a narrower
answer, and made GPT-5.5 more expensive without making it more useful than the
low-effort run.

Kimi K2.6 was not rerun in this pass because the direct Moonshot adapter does
not accept the LCAgent `reasoning_effort` option.

| Run | Score | Cost | Verdict |
|---|---:|---:|---|
| Claude Opus 4.7 | 7.8 | $2.104 | Strong milestone calibration, but cache writes and longer context made it more expensive than Opus low. |
| GPT-5.5 | 7.4 | $0.900 | Useful answer, but more expensive and less calibrated than GPT-5.5 low. |
| Claude Sonnet 4.6 | 6.3 | $0.986 | Strong Anthropic cache reads, but the final answer narrowed onto tool-surface gaps and missed broader doc drift. |
| Gemini 3.1 Flash Lite | 5.7 | $0.036 | Still cheap and fast, but under-called real gaps and became less useful than low. |
| DeepSeek V4 Pro | 5.3 | $0.069 | Cheap again, but medium did not fix false tool and architecture claims. |
| Gemini 3 Flash Preview | 5.1 | $0.123 | More substantial than low, but included unearned verification claims and factual misses. |
| Gemini 3.1 Pro Custom Tools | 4.4 | $0.520 | Medium effort increased cost and still made false missing-tool claims. |
| GLM 5.1 | 3.8 | $0.394 | Slower and more confidently wrong about implemented tools. |
| Grok 4.3 | 3.0 | $0.227 | Read the right docs this time, but falsely marked core implemented tools and LCR wiring as absent. |

Rows with invalid or missing final output are excluded from the main scoring
table and chart because they are not comparable as route candidates.

## Anthropic Prompt Caching

OpenAI, Moonshot, DeepSeek, Gemini, Grok, and several OpenRouter routes can rely
on implicit prompt caching. Anthropic Claude routes need `cache_control` to make
prompt caching intentional. This benchmark used an explicit per-block cache
breakpoint on the stable system message and pinned OpenRouter to the Anthropic
provider for Claude rows.

This produced measurable cache reuse:

| Run | Cache Reads | Cache Writes | Cost | Note |
|---|---:|---:|---:|---|
| Claude Sonnet 4.6 low | 195,190 | 67,135 | $1.094 | Completed with cache reads, but the run still needed forced synthesis and overclaimed. |
| Claude Sonnet 4.6 medium | 227,415 | 52,008 | $0.986 | Cheapest Claude run, but final answer was too narrow. |
| Claude Opus 4.7 low | 96,242 | 82,348 | $1.644 | Best Claude route: strong audit quality with substantial cache reads. |
| Claude Opus 4.7 medium | 134,897 | 120,938 | $2.104 | Completed cleanly, but cache writes and longer context made it more expensive than Opus low. |

Anthropic cache reads are discounted, but cache writes are billed at a premium.
That makes the cache strategy useful but not automatically ideal: it works best
when a stable prompt prefix is reused enough to amortize writes. In this
benchmark, **Claude Opus 4.7 low** is the Claude verification lane, while
**GPT-5.5 low** remains the best default.

## Scoring Evidence Excerpts

These excerpts are short snippets from the generated final answers, quoted to
make the subjective scores inspectable. They are not treated as ground truth;
they show why a run was scored up or down.

| Run | Excerpt | Why It Mattered |
|---|---|---|
| GPT-5.5 low | "the MVP is largely implemented and in several places goes beyond the original first-slice handoff" | Correctly framed the task: not a missing-skeleton story, but a smaller set of harness-quality gaps. |
| GPT-5.5 low | "lcagent is not missing the core MVP skeleton" | Strong bottom-line calibration, with specific gaps called out after that. |
| Claude Opus 4.7 | "Most of the four milestones are implemented" | Best audit-style structure and lifecycle awareness, with substantial Anthropic cache reads. |
| Claude Opus 4.7 | "permission_denied event, the LCR-side launcher... future-context scaffolding" | Found nuanced follow-up gaps, but the run was still too expensive for default routing. |
| Kimi K2.6 | "The biggest confirmed holes are the two missing outline tools" | Useful budget answer, but it also wandered into weaker claims like `plan_item` and piped stdin. |
| Grok 4.3 | "vs. `docs/ai_coding_agent_feasibility.md`" | Penalized because the user asked for the implementation handoff doc; this targeted the wrong comparison source. |
| Claude Sonnet 4.6 low | "`internal/lcagent/script/` missing as a distinct package" | False in the benchmark snapshot; cache reads did not fix overclaiming. |
| MiniMax M2.7 default | "`load_skill` implementation ... MISSING" | Also false in the benchmark snapshot; the run completed cheaply but missed important implemented behavior. |
| MiniMax M2.7 low | "`search` tool ... No `search` tool implementation" | A broader false-missing-feature claim than the default-effort MiniMax run. |
| Gemini 3 Flash Preview | "Tool-Call Markup Guardrail ... Missing" | Fast, but it confidently missed existing provider-markup guardrail work. |
| Claude Opus 4.7 medium | "Milestones 1-3 are essentially in place" | Strong medium-effort calibration, but still too expensive for default routing. |
| GPT-5.5 medium | "`--dry-run` is documented but not implemented" | Penalized because `dry-run` was not actually in the benchmark docs; medium effort added a false headline gap. |
| Claude Sonnet 4.6 medium | "`search` tool missing `context_before` / `context_after` parameters" | A plausible harness-improvement note, but the answer over-focused on tool-surface details and missed broader doc drift. |
| Gemini 3.1 Pro Custom Tools medium | "`load_skill` tool logic ... appears to be missing or incomplete" | False in the benchmark snapshot; medium effort did not fix overclaiming. |
| GLM 5.1 medium | "no `apply_patch` or diff-application tool" | False in the benchmark snapshot and more damaging than the low-effort GLM answer. |
| Grok 4.3 medium | "`update_plan` ... absent" | False in the benchmark snapshot; it read the right docs but missed implemented core tools. |

## Caveats

This is a single-task, single-snapshot benchmark with subjective scoring. Treat
the scores as a useful routing signal, not a pure model-quality leaderboard.

Provider pricing, model aliases, cache accounting, and structured-output
behavior can change. If this artifact is used for a future decision, rerun the
same prompt against the same target snapshot and compare with the CSV here.

Anthropic cache rows use explicit prompt-cache breakpoints. Cache read tokens
are shown in the CSV `cached_tokens` column; cache write tokens are recorded in
the Anthropic cache table above because they affect Anthropic pricing but are
not part of the original CSV schema.

The `reasoning_effort` CSV column records only the requested effort setting
(`low`, `default`, or `disabled`). It is separate from reported
`reasoning_tokens`, because some providers report internal reasoning tokens even
when no explicit effort was requested.

The raw stream logs are intentionally not part of this artifact because they are
large, provider-specific, and mostly useful for debugging one run at a time.
