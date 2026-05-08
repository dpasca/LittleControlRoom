# LCR-Native AI Coding Agent Feasibility

Research date: 2026-05-08

This note explores whether Little Control Room should grow a small AI coding agent harness of its own, either as a separate binary or as an internal provider that LCR can launch and supervise.

## Summary

Building a useful LCR-native harness is feasible if the first target is deliberately modest: a headless, inspectable, provider-flexible coding agent that LCR can run, trace, pause, and score. It should not try to compete with Codex, OpenCode, Claude Code, or Droid as a general terminal product.

The strongest product angle is not "better than Codex." It is:

- local-first and LCR-observable by design
- cheap-model-friendly, especially for background analysis and small patches
- simple enough to reason about when it fails
- integrated into Boss goal runs, project attention, managed worktrees, and verification
- compatible with existing instruction/skill conventions where possible

The best MVP is a separate `lcagent` binary in the same repo/package, launched and monitored by LCR as another `engineer` provider. The binary can start headless only; an extremely small TUI can come later if direct use becomes valuable.

## Current LCR Fit

LCR is already pointed in this direction:

- `docs/boss_control_surface_plan.md` defines `engineer.send_prompt`, typed capabilities, resource refs, operation lifecycle, provider-neutral engineer sessions, and confirmation.
- `STATUS.md` names provider-neutral session abstraction and durable goal-run runtime as current priorities.
- `internal/control/types.go` already models providers, sessions, risk levels, confirmations, operations, and resources.
- `internal/store/goal_runs.go` and `internal/store/agent_tasks.go` already provide durable envelopes for goal execution and delegated work.
- `internal/codexskills/skills.go` inventories Codex-style skills under `~/.codex/skills`, system skills, and plugin skills.

The main missing pieces are not UI. They are:

- an agent-loop runtime LCR owns
- a model adapter layer
- an execution/tool sandbox boundary
- a session artifact format
- a detector for that artifact format
- a small provider adapter so Boss can choose `lcagent` alongside Codex/OpenCode/Claude Code

## What The Strong Agents Teach Us

### Codex

OpenAI describes Codex CLI as a local Rust coding agent that can read, change, and run code in the selected directory, with interactive CLI usage, model/reasoning control, image inputs, review, subagents, web search, cloud tasks, scripting, MCP, and approval modes. Its public "agent loop" article is especially useful because it shows the practical harness concerns: keep the loop explicit, pass tool schemas to the model, append tool outputs, end each turn with an assistant message, and manage prompt growth.

Important design lessons:

- Put stable instructions, tool definitions, and environment context early to preserve prompt caching.
- Avoid changing tool lists, model, sandbox mode, approval mode, or cwd mid-conversation unless represented as new context.
- Treat sandbox instructions as part of the model-visible contract.
- Keep MCP and external tools responsible for their own guardrails; the harness cannot magically sandbox arbitrary external tools.
- Use compaction before context overflow, and make compaction a first-class event.
- AGENTS.md and skills are prompt/context features, not just filesystem conventions.

Sources:

- https://developers.openai.com/codex/cli
- https://openai.com/index/unrolling-the-codex-agent-loop/
- https://github.com/openai/codex

### OpenCode

OpenCode is useful as the "open, provider-flexible, embeddable" reference. Its docs emphasize terminal, desktop, and IDE surfaces; 75+ providers through AI SDK and Models.dev; local models; JS/TS SDK; OpenAPI-generated types; custom agents; subagents; permissions; and skills.

Important design lessons:

- Provider flexibility is a real user value, not an afterthought.
- A server/client or SDK boundary makes embedding and external control much easier.
- Built-in agent types are a cheap way to create user-legible control: build, plan, explore, compact, summarize.
- Permissions should be per-agent and per-tool, not just global.
- Skills should be loaded on demand, listed by metadata first, and permission-gated.
- Subagents are valuable, but read-only exploration is the safest first subagent type.

Sources:

- https://opencode.ai/docs/
- https://opencode.ai/docs/providers
- https://opencode.ai/docs/sdk/
- https://opencode.ai/docs/agents/
- https://opencode.ai/docs/skills/
- https://github.com/anomalyco/opencode

### Pi

Pi is the most relevant inspiration for a small LCR-owned harness. It explicitly positions itself as a minimal terminal coding harness whose core stays small while workflows are built with TypeScript extensions, skills, prompt templates, themes, packages, RPC, SDK, and JSON event streams. It deliberately skips built-in MCP, subagents, plan mode, permission popups, todos, and background bash, expecting users to build or install those pieces.

Important design lessons:

- Minimal core can be a product strategy if extension points are first-class.
- Programmatic modes matter: interactive, print/JSON, RPC, and SDK.
- Session history as a tree is interesting for rewinds, branches, and shareable traces.
- The prompt can stay tiny if skills, instructions, and dynamic context are progressively disclosed.
- If LCR is already the control surface, the agent harness does not need to own every UX feature.

Sources:

- https://pi.dev/
- https://pi.dev/docs/latest
- https://github.com/earendil-works/pi

### Factory Droid

Droid is the most relevant reference for serious headless execution. Droid Exec is a one-shot mode for CI/CD and automation. It is read-only by default, uses explicit autonomy levels, supports structured outputs and stream-json/jsonrpc formats, can continue sessions by id, selects model and reasoning effort from flags, and supports tool enable/disable lists. Factory also emphasizes skills, MCP, custom subagents, hooks, mixed models, and enterprise controls.

Factory's benchmark claims should be treated as vendor claims, but the design writeup is useful. Their Terminal-Bench post argues that agent harness design matters as much as model choice, especially tool schemas, environment discovery, speed, plan updates, and model-specific tool/prompt adaptation.

Important design lessons:

- A headless agent should be secure-by-default, read-only by default, and fail fast on permission violations.
- Autonomy tiers are clearer than one giant "auto" switch.
- Structured event streams are essential for automation and supervision.
- Tool runtime awareness and short default timeouts can improve iteration speed.
- A concise plan tool doubles as both agent memory and human supervision surface.
- Model-specific adapters are probably necessary; do not force every model through the same edit dialect.
- Hooks are an underrated control point for deterministic policies, formatting, logging, and file protection.

Sources:

- https://docs.factory.ai/welcome
- https://docs.factory.ai/cli/droid-exec/overview
- https://docs.factory.ai/cli/user-guides/auto-run
- https://docs.factory.ai/cli/configuration/skills
- https://docs.factory.ai/cli/configuration/mcp
- https://docs.factory.ai/cli/configuration/custom-droids
- https://docs.factory.ai/cli/configuration/hooks-guide
- https://docs.factory.ai/cli/configuration/mixed-models
- https://factory.ai/news/terminal-bench
- https://docs.factory.ai/leaderboards/index

## Model Strategy

DeepSeek V4 Pro and V4 Flash appear to be current official DeepSeek API model ids as of this research date. DeepSeek's docs say they are exposed through OpenAI Chat Completions and Anthropic-compatible interfaces, which makes them plausible early targets for a low-cost harness. Pricing and availability are volatile, so implementation should read model/pricing config from user settings and avoid baking assumptions into code.

Sources:

- https://api-docs.deepseek.com/updates
- https://api-docs.deepseek.com/api/list-models
- https://api-docs.deepseek.com/news/news260424
- https://api-docs.deepseek.com/quick_start/pricing

Recommended model abstraction:

- `ChatCompletionsAdapter`: DeepSeek, OpenRouter, Ollama-compatible servers, many cheap providers.
- `ResponsesAdapter`: OpenAI-native models and Codex-style reasoning/tool event streams.
- `AnthropicAdapter`: optional if we want Claude-compatible endpoints without OpenCode/Codex.

Do not make model choice part of core logic. Make it configuration:

```toml
[agent.models]
default = "deepseek/deepseek-v4-pro"
spec = "deepseek/deepseek-v4-flash"
review = "deepseek/deepseek-v4-pro"

[[agent.providers]]
id = "deepseek"
kind = "openai_chat_completions"
base_url = "https://api.deepseek.com"
api_key_env = "DEEPSEEK_API_KEY"
```

## Proposed Architecture

```text
cmd/lcagent
  headless command, JSON/JSONL/RPC modes

internal/lcagent
  loop/              model-tool loop and turn lifecycle
  model/             provider adapters
  prompt/            AGENTS.md, skills metadata, environment context
  tools/             read, list, search, patch, shell, plan, skill_load
  policy/            autonomy tiers, path rules, command approval/risk
  session/           JSONL artifacts and compaction summaries
  events/            typed stream events
  hooks/             deterministic pre/post tool hooks

internal/detectors/lcagent
  source-of-truth artifact detector for LCR

internal/tui / internal/control
  provider enum and adapter glue for `lcagent`
```

MVP data flow:

```text
Boss/LCR control operation
  -> launch lcagent exec/rpc in repo/worktree
  -> lcagent emits JSONL session events
  -> LCR detector ingests session state
  -> Boss goal run links operation, session id, files touched, tests run
```

## MVP Scope

Build only the parts that make LCR materially better:

- Headless `lcagent exec "prompt"` with `--cwd`, `--model`, `--auto off|low|medium`, `--output text|json|stream-json`.
- Session JSONL artifact format with stable ids, cwd, model, tool calls, tool results, final text, errors, touched files, and test commands.
- Basic tools: `read_file`, `list_files`, `search`, `apply_patch`, `run_command`, `update_plan`, `load_skill`.
- Read-only default. `low` allows project-local file edits. `medium` allows local test/build/install commands. No `high` tier until the policy engine has real tests.
- Skills metadata loading from project `.agents/skills`, project `.codex/skills` only if explicitly configured, global `~/.codex/skills`, and global `~/.agents/skills`. Load bodies only via a tool call.
- AGENTS.md loading using Codex-like scope rules.
- No broad TUI. Start with LCR embedded pane plus log streaming.
- No subagents in v1. Add a read-only explorer subagent only after the main loop is stable.
- No MCP in v1 unless it comes through an LCR-managed allowlist. Tool safety should be locally inspectable first.

## Tool And Policy Design

Use structured model tool calls as the primary control mechanism. Do not use regex or keyword heuristics to decide intent, route tasks, or classify language. This aligns with the repo's LLM/chat policy.

Suggested tool set:

```text
read_file(path, offset?, limit?)
list_files(path, glob?, max_entries?)
search(query, path?, file_glob?, max_matches?)
apply_patch(patch)
run_command(argv, cwd?, timeout_ms?)
update_plan(items)
load_skill(name)
final_response(summary, files_changed?, verification?)
```

Policy should be explicit and boring:

- All paths canonicalized and checked against the workspace.
- External-directory reads/writes denied by default.
- Shell commands use argv arrays, not shell strings, unless a shell is explicitly required and policy allows it.
- Default command timeout should be short, with model-visible timeout failures.
- Long-running processes are out of MVP unless launched through LCR managed runtime.
- Every tool call emits a trace event before and after execution.
- Hooks can deny, rewrite, or annotate tool calls, but should not silently mutate source files.

## Session Artifact Format

LCR should make the harness observable through files, not just process pipes.

Suggested root:

```text
~/Library/Application Support/lcroom/lcagent/sessions/YYYY/MM/DD/<session-id>.jsonl
```

Each session starts with:

```json
{"type":"session_meta","id":"lca_...","cwd":"/repo","started_at":"...","model":"deepseek/deepseek-v4-pro","cli_version":"..."}
```

Useful event types:

- `user_message`
- `assistant_delta`
- `assistant_message`
- `tool_call`
- `tool_result`
- `plan_update`
- `skill_loaded`
- `permission_denied`
- `compaction`
- `files_touched`
- `turn_complete`
- `turn_aborted`

This mirrors what LCR already values from Codex/OpenCode/Claude artifacts: structured lifecycle signals, cwd mapping, bounded transcript extraction, and transparent reasons.

## Feasibility Assessment

High confidence:

- Headless single-agent loop
- DeepSeek/OpenAI-compatible provider adapter
- LCR detector and dashboard integration
- Skill metadata and on-demand loading
- AGENTS.md/project instruction loading
- Read-only and low-autonomy modes
- JSONL trace artifacts

Medium confidence:

- Reliable patching across cheap models
- Multi-turn RPC with interruption/resume
- Good compaction on non-OpenAI APIs
- Model-specific tool dialect optimization
- Policy tiers that are useful without being annoying

Low confidence for early scope:

- Matching Codex/OpenCode UX quality
- Robust MCP support with third-party tools
- Safe high-autonomy shell execution
- Multi-agent orchestration
- Desktop/browser control
- Cloud execution environments

## Recommended First Milestone

Create a spike that proves one vertical slice:

1. Add `lcagent exec` with JSONL stream output.
2. Support one provider: DeepSeek/OpenAI-compatible chat completions.
3. Implement read/search/apply_patch/run_command/update_plan tools.
4. Write an LCR detector for `lcagent` artifacts.
5. Add `ProviderLCAgent` behind a feature flag or hidden config.
6. Run it on small repo tasks and compare:
   - tokens
   - wall time
   - tool-call count
   - patch correctness
   - test pass rate
   - LCR visibility quality

The exit criterion should not be "is it better than Codex?" It should be "does LCR gain a cheap, observable, controllable worker for narrow tasks?"

## Open Questions

- Should `lcagent` live entirely under LCR app data, or should it honor a future `~/.lcagent` home for direct use?
- Should skills be shared with Codex by default, or should LCR copy/index metadata and require explicit opt-in for full skill loading?
- Should LCR launch `lcagent` only inside TODO worktrees at first?
- Should `lcagent` support OpenCode-compatible `.agents/skills` before Codex-style `~/.codex/skills`, given OpenCode/Pi/Droid all converge on agent-compatible skill paths?
- Should we build the model loop in Go for repo cohesion, or use TypeScript because model/provider SDKs and extension ecosystems are richer?
- What is the minimum useful direct CLI UX if LCR is the main interface?

## Recommendation

Proceed with a small spike, not a product commitment.

The design should follow Pi's minimalism, Droid's headless execution and autonomy tiers, Codex's context/prompt-cache discipline, and OpenCode's provider/skill/agent configurability. LCR's differentiator should be supervision and orchestration: traces, goal runs, worktree safety, project attention, and inspectable reasons.
