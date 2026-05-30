# LCAgent Managed Browser Support Plan

## Purpose

This plan covers two near-term fixes:

1. Add first-class LCAgent browser control, internally backed by LCR-managed
   Playwright.
2. Stop the `playwright` skill from leading LCAgent into non-working CLI or MCP
   shell commands.

The goal is not generic MCP support. LCAgent should expose a small, typed browser
tool surface that Little Control Room can supervise, trace, and reveal through
the same browser UX already used for embedded Codex and OpenCode.

## Current Problem

The FractalMech release session showed this failure shape:

- LCAgent correctly started with `browser control available: no`.
- The skill catalog still listed `playwright`.
- After the user pointed that out, the model loaded the skill and tried the
  skill's terminal-oriented workflow.
- The installed skill described `playwright-cli` style commands, but the wrapper
  currently runs `playwright-mcp`, which behaves like an MCP server rather than
  a standalone command CLI.
- LCAgent has no MCP client bridge, so the model could only fail through shell
  probes and then report the browser was unavailable.

Important existing code points:

- `internal/codexapp/browser_launch.go` only treats Codex and OpenCode as
  managed-Playwright providers.
- `internal/lcagent/modeladapter/system_prompt.go` has a `BrowserAvailable`
  capability flag, but it is never enabled by the LCAgent launcher today.
- `internal/lcagent/modeladapter/tool_definitions.go` has no browser tools.
- `internal/codexapp/lcagent_session.go` launches the external `lcagent exec`
  subprocess and streams its JSONL events back into the embedded pane.
- `internal/codexapp/codex_home_overlay.go` already shadows the global
  Playwright skill for embedded Codex/OpenCode sessions, but LCAgent reads the
  normal skill catalog directly.

## Design Choice

Implement direct browser support for LCAgent first.

Direct support means:

- The model sees explicit tools such as `browser_navigate`,
  `browser_snapshot`, and `browser_click`.
- LCAgent never has to shell out to `npx`, `playwright-mcp`,
  `playwright_cli.sh`, or a provider-specific MCP tool name.
- The implementation can still use Playwright internally.
- LCR keeps ownership of browser policy, session keys, profile directories,
  reveal behavior, current-page state, and attention signals.

Do not implement a generic MCP registry as part of this work. A narrow internal
adapter may reuse Playwright infrastructure if that is the smallest path, but
the user/model-facing contract should remain LCAgent-native browser tools.

## Desired Behavior

When browser support is enabled for a LCAgent run:

- The system prompt says `browser control available: yes`.
- Browser tools are present in the tool schema.
- The `playwright` skill, if visible, tells the model to use the `browser_*`
  tools and not terminal wrappers.
- Browser state is written under the existing managed Playwright data dir.
- The embedded snapshot includes a managed browser session key and current page
  URL when known.
- TUI reveal/refocus affordances reuse the same browser window/profile that the
  LCAgent tools are driving.

When browser support is not enabled:

- The system prompt says `browser control available: no`.
- Browser tools are absent from the tool schema.
- The `playwright` skill is absent or shadowed with an unavailable-capability
  message.
- If the user asks for browser work, LCAgent should report the blocker and use
  non-browser evidence only when that evidence actually answers the task.

## Phase 1: Capability And Launch Plumbing

Add launch-time browser capability plumbing before adding actual browser tools.

Implementation steps:

1. Extend the LCAgent session state in `internal/codexapp/lcagent_session.go`.
   Store:
   - `playwrightPolicy browserctl.Policy`
   - `managedBrowserSessionKey string`
   - `browserProfileKey string`
   - `browserLaunchMode browserctl.ManagedLaunchMode`
   - current browser activity/current-page fields, matching the existing
     `codexapp.Snapshot` fields.

2. Update `newLCAgentSession` to copy `LaunchRequest.PlaywrightPolicy` and
   `LaunchRequest.ManagedBrowserSessionKey`.

3. Let `ensureManagedPlaywrightSessionKey` include `ProviderLCAgent`, or add a
   LCAgent-specific session-key initializer if the existing helper remains
   MCP-provider-only.

4. Compute a managed profile key with `browserctl.ManagedProfileKey`, using
   provider `lcagent`, the project path, the resume/thread id when available,
   and the managed session key.

5. Add `lcagent exec` flags:
   - `--browser-control managed|off`
   - `--browser-session-key <key>`
   - `--browser-profile-key <key>`
   - `--browser-launch-mode headless|headed|background`
   - optionally `--browser-policy <json>` if the CLI needs the full policy.

6. In `internal/lcagent/cli.go`, parse those flags and derive
   `BrowserAvailable`.

7. Pass `BrowserAvailable` into:
   - `modeladapter.SystemPromptWithOptions`
   - `modeladapter.ToolsWithOptions`

8. Emit trace events early in each run:
   - `browser_capability` with enabled/disabled, launch mode, session key,
     profile key, and reason when disabled.
   - `tool_profile` and existing capability traces should remain unchanged.

Tests:

- `internal/codexapp/lcagent_session_test.go`: embedded LCAgent launch passes
  browser flags when managed policy is enabled.
- `internal/lcagent/cli_test.go`: prompt shows browser available only when the
  browser flags are present.
- `internal/lcagent/modeladapter/openrouter_test.go`: system prompt and tool
  list reflect browser availability.

## Phase 2: Browser Tool Surface

Expose a small browser tool set in `internal/lcagent/modeladapter`.

MVP tools:

- `browser_navigate`
  - args: `url`
  - output: current URL, title if available, navigation status.
- `browser_snapshot`
  - args: optional `max_chars`
  - output: accessibility-style snapshot with stable refs.
- `browser_click`
  - args: `ref`
  - output: action status and current URL.
- `browser_fill`
  - args: `ref`, `value`
  - output: action status.
- `browser_press`
  - args: `key`
  - output: action status.
- `browser_screenshot`
  - args: optional `path`
  - output: artifact path and current URL.
- `browser_current_page`
  - args: none
  - output: current URL/title and whether the page state is fresh.
- `browser_wait_for_user`
  - args: `message`, optional `url`
  - output: the user's resume message after they finish the browser step.
  - emits `browser_waiting_for_user` and keeps the managed browser/session open
    while waiting.

Nice-to-have after MVP:

- `browser_hover`
- `browser_select`
- `browser_upload`
- `browser_back`
- `browser_forward`
- `browser_reload`

Tool design rules:

- Browser tools should only appear when `BrowserAvailable` is true.
- Tool descriptions should explicitly say not to use terminal Playwright
  commands.
- Use refs from the latest `browser_snapshot`; stale refs should produce a clear
  error asking the model to snapshot again.
- Screenshot artifacts should go under the managed browser session output dir.
- Tool results should be concise, but include enough state for trace replay:
  current URL, title when known, and artifact path when relevant.

Tests:

- Tool schema contains browser tools only when enabled.
- Tool schema rejects extra properties and requires the key args.
- Deterministic scripted LCAgent runs can call browser tools through a fake
  browser runner.
- Replay text summarizes browser tool calls without dumping huge snapshots.

## Phase 3: Browser Runtime

Add a direct runtime under `internal/browserctl` or a nearby package.

Preferred MVP shape:

1. Create a `browserctl.BrowserSession` interface:
   - `Navigate(ctx, url)`
   - `Snapshot(ctx)`
   - `Click(ctx, ref)`
   - `Fill(ctx, ref, value)`
   - `Press(ctx, key)`
   - `Screenshot(ctx, path)`
   - `CurrentPage(ctx)`
   - `Close()`

2. Implement it with a small LCR-owned Playwright worker process.
   - The Go side keeps the LCAgent tool API typed and deterministic.
   - The worker can be a Node script using Playwright directly.
   - It should speak newline-delimited JSON over stdin/stdout.
   - It should use `browserctl.ManagedPlaywrightPathsFor` for profile,
     output, state, and locking paths.

3. Reuse existing managed-browser state files.
   - Initialize `ManagedPlaywrightState` at browser-session creation.
   - Update browser PID/app metadata when detected.
   - Update `Hidden`, `RevealSupported`, and `UpdatedAt` exactly like the
     existing managed MCP wrapper where practical.

4. Reuse the hide/reveal behavior.
   - Background launch mode should keep the browser hidden until LCR reveals it.
   - `lcroom browser reveal --session-key ...` should work for LCAgent sessions
     the same way it works for Codex/OpenCode.

5. Keep worker lifecycle per LCAgent run at first.
   - Start lazily on the first browser tool call.
   - Close when the LCAgent process exits.
   - Reuse profile state through `profileKey`, not through a long-lived worker.

Implementation files likely to change:

- New `internal/browserctl/session.go`
- New `internal/browserctl/playwright_worker.go`
- New worker asset, for example `internal/browserctl/playwright_worker.js`
- `internal/lcagent/cli.go`
- `internal/lcagent/script/script.go`
- `internal/lcagent/modeladapter/tool_definitions.go`
- `internal/codexapp/lcagent_session.go`
- `internal/codexapp/lcagent_replay.go`
- `internal/codexapp/lcagent_trace.go`

Alternative if direct Playwright worker is too expensive:

- Build a narrow internal Playwright-MCP client adapter that maps only the known
  Playwright calls to LCAgent `browser_*` tools.
- Keep that adapter private to the browser runtime.
- Do not expose arbitrary MCP server configuration or arbitrary MCP tools to the
  model in this slice.

Tests:

- Unit-test the Go browser session with a fake worker.
- Unit-test worker protocol parsing and error handling.
- Smoke-test one real navigation to a local static page, not an external site.
- Verify state files are written in the existing managed browser layout.
- Verify no browser process is left running after tool/session close.

## Phase 4: Embedded UI And Trace Integration

Wire browser events back into embedded LCAgent snapshots and TUI affordances.

Implementation steps:

1. Add LCAgent browser trace events:
   - `browser_activity_started`
   - `browser_activity_finished`
   - `browser_page`
   - `browser_waiting_for_user` when the model or tool detects a human step is
     required.

2. Extend `lcagentSession.handleEvent` to update:
   - `BrowserActivity`
   - `ManagedBrowserSessionKey`
   - `CurrentBrowserPageURL`
   - `CurrentBrowserPageStale`

3. Reuse existing TUI rendering:
   - current page strip
   - `ctrl+o` reveal/refocus
   - project detail browser attention
   - footer browser attention segment
   - interactive browser lease where a user handoff is active

4. Add a small model-facing instruction:
   - If a browser step needs login/MFA/human judgment, call
     `browser_wait_for_user` with a short instruction instead of
     `final_response`, then inspect the current page after the user replies.

Tests:

- Replay parser turns browser events into visible transcript/status entries.
- Snapshot reflects current URL after a browser event.
- TUI browser attention tests include an LCAgent provider case.
- Browser reveal command works with an LCAgent managed session key.

## Phase 5: Skill Catalog Fix

Fix the skill confusion independently of browser runtime completion.

The model-facing rule should be capability-aware:

- Browser unavailable: do not advertise a usable `playwright` workflow.
- Browser available: advertise the native LCAgent `browser_*` tools, not shell
  commands or MCP setup.

Implementation options:

1. Preferred: add LCAgent-specific skill shadowing in the skill catalog.
   - Extend `internal/lcagent/skills/catalog.go` with an options field such as
     `BrowserMode: "unavailable" | "native-tools" | "passthrough"`.
   - When the discovered skill name is `playwright`, replace it with a shadow
     skill body based on the current capability.
   - In unavailable mode, the shadow skill says browser control is not available
     in this run and the agent must report that blocker.
   - In native-tools mode, the shadow skill says to use `browser_*` tools and
     not `npx`, `playwright-mcp`, `playwright_cli.sh`, or MCP setup commands.

2. Acceptable short-term alternative: filter out the `playwright` skill when
   `BrowserAvailable` is false.
   - This is less helpful if the user asks "do you see the playwright skill?",
     but it avoids the broken CLI path.

3. Also update the installed/global skill source separately.
   - The current `~/.codex/skills/playwright` body describes a standalone
     `playwright-cli` workflow that does not match the wrapper's current
     behavior.
   - Either make the wrapper actually provide that CLI, or rewrite the skill to
     say it requires an MCP-capable client.
   - This may live outside the Little Control Room repo, so LCR should not rely
     on this global fix for correctness.

Recommended shadow text:

- Unavailable:
  - "Browser control is not available in this LCAgent run."
  - "Do not run Playwright CLI, `playwright-mcp`, or `npx @playwright/mcp`."
  - "Use non-browser APIs or report the blocker."

- Native-tools:
  - "This LCAgent run has native browser tools."
  - "Use `browser_navigate`, `browser_snapshot`, `browser_click`, etc."
  - "Do not launch a separate browser from the terminal."
  - "If login/MFA is required, ask LCR/user to reveal the managed browser."

Tests:

- Catalog tests cover shadowing in unavailable and native-tools modes.
- `load_skill playwright` returns the shadow body, not the global stale body,
  for LCAgent runs.
- A regression test simulates the FractalMech failure shape and verifies the
  model is not instructed to shell out to `playwright_cli.sh`.

## Phase 6: Manual Smoke Script

Add a manual smoke path after the unit slices pass.

Suggested local smoke:

1. Start a fresh LCAgent run in a disposable project with managed browser mode.
2. Ask it to open a local HTML page or `https://example.com`.
3. Verify the model uses `browser_navigate`, then `browser_snapshot`.
4. Verify LCR shows the current page URL.
5. Press `ctrl+o` and confirm the same managed browser is revealed.
6. Ask it to take a screenshot and verify the artifact lands under the managed
   browser session output dir.
7. Close the session and verify no worker/browser process is leaked.

Suggested real-world smoke after that:

1. Start LCAgent in FractalMech.
2. Ask it to open Play Console.
3. If auth is required, verify it stops and asks for browser handoff rather than
   trying terminal Playwright.
4. Reveal the managed browser, finish login manually, then resume the prompt.
5. Verify the agent continues in the same browser profile/session.

## Validation Per Slice

For code slices:

- `go test ./internal/browserctl/...`
- `go test ./internal/lcagent/...`
- `go test ./internal/codexapp/...`
- `go test ./internal/tui/...`
- `make test`
- `make scan`
- `make doctor`

For docs-only slices:

- Review `git diff --check`.
- Run full validation only if code or generated artifacts changed.

## Rollback Strategy

- Keep `Classic browser behavior` unchanged for Codex/OpenCode.
- Gate LCAgent browser support behind launch-time availability. If browser setup
  fails, omit browser tools and set `browser control available: no`.
- The shadow `playwright` skill should fail closed. It is better for LCAgent to
  say "browser unavailable" than to create a second browser profile or shell out
  to a server it cannot drive.

## Done Criteria

This work is done when:

- A fresh embedded LCAgent run can navigate, snapshot, click/fill, and capture a
  screenshot through native browser tools.
- LCR can reveal the same managed browser session used by LCAgent.
- Browser activity and current URL show in the embedded UI.
- `load_skill playwright` no longer points LCAgent at broken CLI/MCP shell
  commands.
- A FractalMech Play Console prompt either uses the managed browser correctly or
  clearly asks for human login handoff without claiming browser verification ran.
