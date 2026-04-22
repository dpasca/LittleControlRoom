# Browser Automation Working Plan

This is a living workstream document for browser automation inside Little Control Room.

It is intentionally different from `STATUS.md`:

- `STATUS.md` stays short, durable, and project-wide.
- This doc tracks the active browser-automation roadmap, current design shape, safety rules, and open questions.
- Git history remains the source of truth for detailed chronology.

## Current Snapshot

- For embedded Codex, `Only when needed` now affects the actual Playwright launch path, not just the surrounding UI.
- In managed mode, embedded Codex overrides the `playwright` MCP registration per process and launches Playwright with LCR-controlled flags.
- Newly launched embedded Codex sessions now go through an LCR-owned `playwright-mcp` wrapper instead of calling `mcp-server-playwright` directly.
- The managed wrapper now gives each Codex session a stable managed browser/session key plus a persistent Playwright profile directory.
- Newly launched embedded Codex sessions also get a session-local `CODEX_HOME` overlay that shadows only the `playwright` skill, so LCR can steer browser behavior without mutating the user's real `~/.codex`.
- On macOS, `Only when needed` now runs managed Codex browser work in a backgrounded headed browser so LCR can reveal that same browser context later for login or other human steps.
- Managed embedded Codex submissions now wait briefly for Playwright MCP tools to become ready before starting the first turn, which avoids a fresh-session race where browser tools could be missing on the first prompt.
- Newly launched embedded OpenCode sessions now override `mcp.playwright` through `OPENCODE_CONFIG_CONTENT` to point at the same LCR-managed Playwright wrapper, using the same managed session/profile key model as Codex.
- Newly launched embedded OpenCode sessions also get a session-local `XDG_CONFIG_HOME` overlay that shadows only the `playwright` skill, so OpenCode is steered toward the managed MCP path without changing the user's real `~/.config/opencode`.
- OpenCode sessions now track their live Playwright tool activity plus the current managed browser page URL, so the shared browser strip/reveal UI can surface the same current-page and reconnect guidance patterns that Codex already uses.
- OpenCode browser-backed question waits now reuse that same managed browser state, so when OpenCode pauses for user input the session can stay in a `waiting for user` browser state and keep `Ctrl+O` available to reveal or refocus the managed browser window.
- Existing embedded Codex sessions do not retroactively pick up the new launch behavior; they need to be reopened or reconnected.
- URL-based login waits already have an LCR-managed attention flow and interactive-browser lease.
- Embedded Codex sessions now remember the latest Playwright page URL they reached, and the visible pane can reveal that same managed browser window with `Ctrl+O`.
- OpenCode and Claude Code still remain behind Codex in managed-browser support.

## Maintenance Rule

Update this doc whenever browser automation behavior changes in one of these ways:

- the user-facing `Browser windows` experience changes
- the actual embedded Playwright launch path changes
- provider support materially changes
- new rollback or safety behavior is added
- the near-term roadmap meaningfully changes

## Goal

Make browser automation feel quiet and predictable by default:

- background/headless when possible
- visible only when a human step is actually needed
- consistent across embedded assistants as far as each provider allows
- always reversible back to classic provider-owned behavior

## Non-Goals For Now

- Do not build a full VM or desktop virtualization layer.
- Do not remove the provider-owned fallback path.
- Do not force OpenCode or Claude into "managed" behavior before their embedded control surfaces are ready.
- Do not turn `STATUS.md` into a running implementation log.

## What Exists Today

### Policy And Fallback

- Launch-time Playwright policy is threaded through embedded providers.
- Embedded Codex now uses per-process MCP config overrides in managed mode, so the `playwright` server can be launched with LCR-controlled flags instead of the stock provider defaults.
- Embedded Codex also uses a session-local `CODEX_HOME` overlay in managed mode, so the `playwright` skill can be shadowed per session without changing the user's global Codex install.
- The simplified `/settings` UI now centers on a plain-language `Browser windows` choice:
  - `Only when needed`
  - `Always show`
  - `Classic browser behavior`
- A raw `use config file as-is` choice can still appear when the saved Playwright policy does not fit those main modes.
- `Classic browser behavior` remains the escape hatch for original provider-owned behavior.

### Visibility

- Browser status is visible in the `Browser` settings section.
- The settings view now shows the interactive browser lease owner plus any waiting managed login flows.
- Codex Playwright activity is tracked live enough to distinguish:
  - idle
  - active
  - waiting for user
- Browser-attention prompts appear when a hidden embedded session needs help.

### Actionable Handoffs

- Hidden embedded Codex sessions:
  - can raise a browser-attention popup
  - can reveal the managed browser window for that same session when policy is `managed + promote`
- Visible embedded Codex sessions:
  - can reveal the same managed browser window with `o`
  - show footer/request hints that explain the browser-login flow
  - can reveal the current managed browser window with `Ctrl+O` after a background navigation finishes
- Managed Codex login flows now go through an LCR-owned interactive browser lease:
  - one session can hold the interactive browser slot at a time
  - later sessions are blocked cleanly instead of blindly opening another browser login flow
  - failed browser-reveal attempts release the slot immediately
- Managed browser metadata is now written under the LCR data dir so the TUI can find and reveal the correct browser session instead of opening the URL in a disconnected desktop browser.
- The shadow Playwright skill now tells embedded Codex to use the already-registered Playwright MCP tools instead of shelling out to a separate CLI browser path.
- LCR now has a small non-TUI browser control surface:
  - `lcroom browser status --session-key ...`
  - `lcroom browser reveal --session-key ...`
  This makes managed browser state and reveal behavior scriptable for debugging and smoke checks.

### Testing

- Unit coverage now verifies that the session-local `CODEX_HOME` overlay symlinks the original Codex home while replacing only `skills/playwright`.
- A Codex smoke check can now verify the overlay without a live user session by running `codex debug prompt-input` against that overlay and checking that the shadow Playwright skill is what Codex sees.
- A managed embedded Codex smoke test now builds a real `lcroom` helper binary and verifies that a fresh trusted session can see Playwright MCP tools before the first turn starts.
- The real embedded OpenCode Playwright smoke now launches with its own temporary `XDG_DATA_HOME`, so it exercises the managed browser path without polluting the user's normal OpenCode DB or leaving `tmp-oc-browser-smoke-*` projects in the dashboard.

## Current Provider Status

### Codex

- Best current target for managed behavior.
- Managed mode now overrides the embedded Codex `playwright` MCP launch to use the LCR wrapper plus a persistent Playwright profile.
- Embedded elicitation replies are wired.
- Managed same-context browser reveal is working in both hidden and visible session flows for newly launched embedded sessions.
- This launch override currently applies to newly started embedded sessions only.

### OpenCode

- Policy/status plumbing exists.
- Managed Playwright launch now overrides OpenCode's `mcp.playwright` entry to use the LCR wrapper plus persistent managed browser/session keys.
- Managed sessions now also shadow the OpenCode `playwright` skill per session, which keeps real browser tasks on the managed MCP path instead of falling back to standalone Playwright CLI/browser launches.
- OpenCode now reports live Playwright tool activity and the current managed browser page URL into the shared embedded browser UI, including `Ctrl+O` reveal/focus handling and reconnect guidance when the session browser wiring no longer matches current settings.
- OpenCode browser-backed structured-input waits now keep the session in a `waiting for user` browser state, which lets the same browser-attention/reveal flow stay active while the user is answering a browser-related prompt.
- Managed OpenCode browser smoke coverage is now isolated from the user's real OpenCode data home, so smoke verification no longer depends on or mutates the normal dashboard state.
- Embedded elicitation control is still weaker than Codex, so browser-attention/login waits are not yet at full parity.
- Treat as managed for normal Playwright browsing, with improving UI parity but still interaction-limited until the embedded reply path is clearer.

### Claude Code

- Launch-policy plumbing exists.
- Embedded approval/tool-input/elicitation control is still limited.
- Treat as observe-only for now.

## Immediate Next Steps

1. Make the backgrounded browser reveal path feel more robust in real use.
   - Verify that the macOS hide/reveal behavior is stable across actual login sites.
   - Decide whether the first reveal should do anything more explicit than just surface the managed browser window.

2. Improve passive visibility outside popups.
   - Surface browser-attention state in the project list or detail pane.
   - Avoid making the popup the only way to notice a login wait.

3. Keep tightening tests around managed Codex transitions.
   - waiting -> show browser -> accept
   - waiting -> decline
   - waiting -> cancel
   - waiting -> blocked by another interactive lease
   - classic browser behavior still avoiding managed behavior

4. Add a small manual-release / reclaim story if needed.
   - Decide whether the first version should expose a "release browser slot" action when a login flow is abandoned.
   - Keep this optional until real usage shows the lease can get stuck in practice.

## Next Phase After That

1. Introduce an LCR-owned browser controller.
   - Expand the current in-memory lease manager beyond managed Codex login URLs.
   - Move from "interactive lease only" toward fuller browser/session ownership concepts.

2. Add session/profile ownership.
   - Keep refining where per-task or per-project browser state should live.
   - Reuse auth/profile state deliberately instead of implicitly.

3. Extend provider support carefully.
   - Bring OpenCode along as far as its launch/config surface allows.
   - Revisit Claude only when embedded control surfaces improve.

4. Expand settings/status once the controller is real.
   - Active browser owner
   - waiting-for-login state
   - queued/blocked state
   - profile/auth reuse mode

## Guardrails

- `Classic browser behavior` must remain a fast rollback path.
- Do not block the Bubble Tea update/render path with live browser or session work.
- Prefer cached or non-blocking snapshot data in TUI rendering.
- Keep provider-neutral concepts in shared helpers or a dedicated browser-control package.
- Do not overfit behavior to Playwright quirks if the logic is really about "browser needs a human".

## Open Questions

- Should URL-based login waits stay manual on the final "accept/done" step, or should that eventually be guided more explicitly?
- What is the right place to surface browser-attention state when the popup is dismissed?
- When the real controller exists, should isolation default to per-task or per-project?
- What is the cleanest way to let OpenCode and Claude participate without brittle wrappers?

## Likely Follow-Up Docs

- If this grows beyond the current workstream, add a broader `docs/workstreams.md` or `docs/roadmap.md`.
- Keep provider-footprint docs separate from this one:
  - `docs/codex_cli_footprint.md`
  - `docs/claude_code_footprint.md`
