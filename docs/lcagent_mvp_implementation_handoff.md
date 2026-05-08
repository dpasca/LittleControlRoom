# LCAgent MVP Implementation Handoff

Date prepared: 2026-05-09

This is a handoff for a fresh implementation agent. It assumes the research spike lives in a worktree and the actual implementation should happen in the root Little Control Room repo on a dedicated branch.

## First Instructions For The Next Agent

Read these first:

- `AGENTS.md` / project instructions in the repo root.
- `STATUS.md`.
- `docs/ai_coding_agent_feasibility.md`.
- `docs/boss_control_surface_plan.md`.
- `docs/codex_cli_footprint.md`, especially the artifact-first detector style.
- `docs/claude_code_footprint.md`, especially structured lifecycle and auxiliary activity lessons.

Do not rename `master` to `main`.

Do not implement keyword, regex, or pattern-matching heuristics for language understanding, routing, classification, or intent detection. If a language-understanding choice is needed, use structured model output later; for the first skeleton, avoid model-based routing entirely by using deterministic scripted actions.

## Branch And Workspace Plan

The current research worktree is expected to be something like:

```text
/Users/davide/dev/repos/LittleControlRoom--ai-coding-agent-feasibility
```

The implementation should happen in the root repo, expected to be something like:

```text
/Users/davide/dev/repos/LittleControlRoom
```

Suggested workflow:

```sh
cd /Users/davide/dev/repos/LittleControlRoom
git status --short --branch
git switch master
git switch -c spike/lcagent-mvp
```

If the root repo is dirty, inspect the changes and do not overwrite them. If the root branch is not `master`, verify the correct base with the user.

Bring these docs into the implementation branch:

```sh
cp ../LittleControlRoom--ai-coding-agent-feasibility/docs/ai_coding_agent_feasibility.md docs/
cp ../LittleControlRoom--ai-coding-agent-feasibility/docs/lcagent_mvp_implementation_handoff.md docs/
```

Commit the docs separately if useful:

```sh
git add docs/ai_coding_agent_feasibility.md docs/lcagent_mvp_implementation_handoff.md
git commit -m "docs: add lcagent feasibility and handoff notes"
```

## Goal

Build the first deterministic vertical slice of an LCR-native coding-agent harness:

```text
lcagent can run a scripted mini-session, emit structured JSONL artifacts,
execute bounded commands, apply a patch under policy, update a plan, and be
testable without live model calls.
```

This is not yet about beating Codex, OpenCode, Claude Code, or Droid. The purpose is to prove the harness shape and the LCR visibility model before adding real model uncertainty.

## Non-Goals For The First Slice

- No broad TUI.
- No MCP support.
- No subagents.
- No browser/computer-use support.
- No high-autonomy mode.
- No ChatGPT OAuth token plumbing inside `lcagent`.
- No dependency on GPT-5.5 as an internal runtime model.
- No detector integration until the JSONL artifact format is stable enough.
- No generic LCR control capability for arbitrary shell execution.

Use Codex/GPT-5.5 as the implementation assistant and evaluation reference, but keep `lcagent` itself model-free in the first branch.

## Core Design

Use a hybrid tool shape:

```text
shell-first, trace-first, patch-typed, plan-typed
```

The model-facing tool set should eventually be small:

- `run_command(command, cwd?, timeout_ms?)`
- `apply_patch(patch)`
- `update_plan(items)`
- `load_skill(name)` later
- `final_response(summary, files_changed?, verification?)`

For the first deterministic slice, implement only:

- `run_command`
- `apply_patch`
- `update_plan`
- `final_response`

`run_command` should allow normal developer workflows such as `rg`, `git diff`, `go test`, `sed -n`, `jq`, and shell pipelines. Do not bury these behind many tiny semantic tools. The harness should observe and constrain the command, not replace the developer substrate.

`apply_patch` should be a typed operation because LCR needs clear mutation events, diff visibility, and policy enforcement.

`update_plan` should be a typed operation because LCR can later display progress, stale steps, and goal-run traces from it.

## Suggested Package Layout

Start small and boring:

```text
cmd/lcagent
  main.go

internal/lcagent
  cli.go              flag parsing and command dispatch
  session/           JSONL writer, event types, ids, artifact paths
  policy/            autonomy levels, workspace path checks
  tools/             run_command, apply_patch, update_plan
  present/           stdout/stderr truncation, binary guard, metadata footer
  script/            deterministic scripted "model" driver for tests
```

Avoid integrating with the main `lcroom` CLI until the standalone binary works. The repo can build multiple binaries under `cmd/`.

## CLI Shape

Target command:

```sh
go run ./cmd/lcagent exec \
  --cwd /path/to/repo \
  --data-dir /tmp/lcagent-demo \
  --auto low \
  --output stream-json \
  --script internal/lcagent/testdata/simple_patch.script.jsonl \
  "Fix the failing test"
```

Flags:

- `--cwd`: workspace root. Required for MVP.
- `--data-dir`: artifact root override. Optional; default should eventually use LCR app data conventions.
- `--auto`: `off`, `low`, or `medium`. Default `off`.
- `--output`: `text`, `json`, or `stream-json`. Start with `stream-json` plus a minimal `text` final summary if easy.
- `--script`: hidden or clearly marked test/dev flag for deterministic scripted actions.

Autonomy levels:

- `off`: read-only commands only; no file writes and no patching.
- `low`: allow edits inside `--cwd` through `apply_patch`; allow read-only commands and simple local commands.
- `medium`: later, allow test/build/install commands with stronger command policy. Do not implement broad medium behavior in the first slice unless the lower levels are already solid.

## Scripted Driver

The first implementation should not call a live model. Add a simple scripted driver that consumes JSONL actions, for example:

```json
{"type":"tool_call","tool":"run_command","args":{"command":"rg \"TODO\" .","timeout_ms":5000}}
{"type":"tool_call","tool":"update_plan","args":{"items":[{"step":"Inspect TODOs","status":"completed"},{"step":"Patch docs","status":"in_progress"}]}}
{"type":"tool_call","tool":"apply_patch","args":{"patch":"*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch\n"}}
{"type":"final_response","summary":"Updated README.md","files_changed":["README.md"],"verification":["not run (scripted demo)"]}
```

This gives tests and manual smoke checks deterministic behavior. Real model adapters should come after the tool, policy, event, and presentation layers are proven.

## Session Artifact Format

The artifact file is the source of truth. Keep it easy for LCR to parse.

Suggested default root:

```text
<data-dir>/lcagent/sessions/YYYY/MM/DD/<session-id>.jsonl
```

Each session starts with `session_meta`:

```json
{
  "type": "session_meta",
  "id": "lca_...",
  "started_at": "2026-05-09T00:00:00+09:00",
  "cwd": "/path/to/repo",
  "auto": "low",
  "model": "scripted",
  "cli_version": "dev"
}
```

Useful event types for MVP:

- `user_message`
- `tool_call`
- `tool_result`
- `plan_update`
- `assistant_message`
- `files_touched`
- `turn_complete`
- `turn_aborted`

Write events as single-line JSON objects. Include stable ids and timestamps. Preserve enough data for LCR to answer:

- What project was this for?
- Is the latest turn complete, running, or aborted?
- What tool was called?
- What command ran?
- What files changed?
- What verification ran?
- Why did a tool fail?

Do not rely on natural-language transcript text for lifecycle state.

## Command Execution Presentation

Separate execution semantics from LLM presentation.

Execution layer:

- Runs the command in the workspace.
- Captures raw stdout, raw stderr, exit code, duration.
- Does not inject metadata into data that may flow through shell pipes.

Presentation layer:

- Checks binary output before returning it to the agent.
- Truncates large output.
- Stores full oversized output in an artifact file.
- Always includes stderr when a command fails.
- Appends consistent metadata: exit code and duration.

Suggested truncation behavior:

```text
[first output chunk]

--- output truncated (5000 lines, 245.3KB) ---
Full output: <artifact-output-path>
Explore: cat <artifact-output-path> | rg <pattern>
         tail -100 <artifact-output-path>
[exit:0 | 1.2s]
```

Binary guard behavior:

```text
[error] binary file output suppressed (182KB). Use an image-aware or binary-aware tool later.
[exit:1 | 8ms]
```

This is one of the most important pieces. Bad tool output makes agents waste turns.

## Policy Requirements

For every path:

- Canonicalize paths.
- Require paths to stay under `--cwd` unless a future explicit capability allows otherwise.
- Deny symlink escapes.
- Record denied attempts as `permission_denied` or failed `tool_result` events.

For `run_command`:

- Start with a short default timeout, for example 10 seconds.
- Allow caller-provided timeout up to a conservative max.
- Kill timed-out processes.
- Capture stdout and stderr separately.
- Record duration and exit code.
- Do not run long-lived processes in MVP.

For `apply_patch`:

- Deny in `--auto off`.
- In `low`, allow only files under `--cwd`.
- Prefer applying through a controlled helper. A pragmatic first implementation may write the patch to a temp file and use `git apply`, but validate paths before and after.
- Emit `files_touched` based on the patch plus a post-apply diff check.

If command policy needs command-specific allow/deny rules, treat those as execution policy, not language-understanding heuristics. Keep them explicit and tested.

## Context Management Later

Do not build complex compaction in the deterministic skeleton. But design events so it can be added cleanly.

The future context shape should be:

```text
stable prefix
project capsule
working set
recent event tail
compacted memory
```

The stable prefix should include system instructions, tool descriptions, autonomy policy, cwd, and environment shape. Avoid changing it gratuitously because prompt caching matters.

Skills should use progressive disclosure later:

- Load skill metadata first.
- Load full skill bodies only through `load_skill(name)`.
- Prefer shared conventions such as `.agents/skills`, `~/.codex/skills`, and eventually `~/.agents/skills`, but do not add broad skill loading in the first slice.

## Implementation Milestones

### Milestone 1: Deterministic Harness Skeleton

Deliverables:

- `cmd/lcagent`.
- `lcagent exec` command.
- Scripted driver.
- JSONL session writer.
- `run_command`, `apply_patch`, `update_plan`, `final_response`.
- Presentation layer for command output.
- Unit tests for policy, events, truncation, stderr, and basic patch behavior.

Acceptance:

- A scripted session can run from a temp repo.
- It writes a JSONL artifact with `session_meta`, tool events, and `turn_complete`.
- `--auto off` denies `apply_patch`.
- `--auto low` can apply a patch under `--cwd`.
- Attempts to patch outside `--cwd` fail and are logged.
- Command failures include stderr.
- Oversized output is truncated and persisted.

### Milestone 2: LCR Detector

Deliverables:

- `internal/detectors/lcagent`.
- Add `SessionSourceLCAgent` in `internal/model/model.go`.
- Add `lcagent` format/source normalization.
- Parse `session_meta`, lifecycle events, latest activity, touched files, and error counts.
- Add unit tests mirroring the Codex/OpenCode/Claude detector style.

Acceptance:

- `make scan` can discover `lcagent` artifacts from a configured or default data dir.
- LCR maps sessions to projects via `cwd`.
- Latest turn state comes from structured events, not text.

### Milestone 3: Hidden Provider Integration

Deliverables:

- Add `ProviderLCAgent` to `internal/control/types.go` behind a hidden config or feature flag.
- Let LCR launch `lcagent exec` for a project in a controlled path.
- Stream output into an embedded pane or a basic log view only if the existing session abstractions make that straightforward.

Acceptance:

- Boss/LCR can treat `lcagent` as an engineer-session provider for an explicit test command.
- The provider is opt-in and does not disturb Codex/OpenCode/Claude behavior.

### Milestone 4: First Real Model Adapter

Do this only after Milestones 1 and 2 are solid.

Recommended first adapter:

- OpenAI-compatible Chat Completions or Responses-style API via explicit API key.
- Use a boring supported API path first.
- Do not depend on copying ChatGPT/Codex OAuth credentials into `lcagent`.

GPT-5.5/Codex should remain the reference evaluator and implementation assistant. Cheaper models can become runtime options after the harness rails are reliable.

## Tests To Add

Suggested tests:

- `internal/lcagent/session`: JSONL event writing and stable session ids.
- `internal/lcagent/policy`: workspace containment, symlink escape denial, autonomy decisions.
- `internal/lcagent/tools`: command timeout, stderr preservation, patch denial/approval.
- `internal/lcagent/present`: truncation, binary guard, metadata footer.
- `internal/lcagent/script`: scripted session executes expected tool calls.
- `internal/detectors/lcagent`: parses session meta, lifecycle, latest activity, project mapping.

Use temp directories and small fixture repos. Do not require network or live model credentials for tests.

## Validation Before Finishing

Run:

```sh
make test
make scan
make doctor
```

If UI behavior is touched, also run `make tui` from a real PTY-backed terminal. In non-interactive runners, `/dev/tty` failures are usually an environment limitation unless reproduced in a real terminal.

## Commit Boundaries

Good small commits:

1. `docs: add lcagent feasibility and handoff notes`
2. `feat(lcagent): add deterministic exec skeleton`
3. `feat(lcagent): add command presentation and policy tests`
4. `feat(detectors): detect lcagent session artifacts`
5. `feat(control): add hidden lcagent provider`

Do not bundle the real model adapter into the skeleton commit.

## Pitfalls To Avoid

- Starting with live model calls before the harness is deterministic.
- Building a full TUI.
- Adding many semantic tools before proving `run_command`.
- Letting raw binary or huge command output reach the model context.
- Dropping stderr when stdout exists.
- Treating command text as the source of lifecycle truth.
- Making `lcagent` depend on Codex private auth files.
- Adding broad shell control to Boss Chat.
- Updating `STATUS.md` for branch-local progress.

## Success Definition

The first successful implementation should feel small:

```text
lcagent is a boring, observable local worker.
It can run scripted sessions, leave excellent artifacts, and enforce basic policy.
LCR can later understand it because its lifecycle is structured from day one.
```

That is enough for the first branch.
