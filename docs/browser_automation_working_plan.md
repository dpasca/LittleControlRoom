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
- Newly launched embedded Codex sessions also get a session-local `CODEX_HOME` overlay that shadows the managed `playwright` skill and, when registered, the `lcr_runtime` skill, so LCR can steer browser behavior without mutating the user's real `~/.codex`.
- On macOS, `Only when needed` now runs managed Codex browser work in a backgrounded headed browser so LCR can reveal that same browser context later for login or other human steps.
- The macOS background browser monitor waits until Chromium has finished registering as an activatable application before hiding it, then keeps re-hiding it while LCR still marks it hidden. This avoids interrupting Chromium's launch transition and leaving a live automation process with a visible but non-interactive window.
- Managed browser reveal now records a cross-session foreground marker before raising the macOS window. Background monitors using the same Chromium application respect that marker, so one hidden session cannot collapse a different session that the user just revealed.
- PID-targeted macOS reveal now retries within one bounded operation and verifies that the exact Chromium process actually became active and interactively activatable. A misleading AppKit acceptance no longer becomes a false-success status; the failed handoff is restored as an actionable browser-attention dialog.
- Managed embedded Codex submissions now wait briefly for Playwright MCP tools to become ready before starting the first turn, which avoids a fresh-session race where browser tools could be missing on the first prompt.
- Newly launched embedded OpenCode sessions now override `mcp.playwright` through `OPENCODE_CONFIG_CONTENT` to point at the same LCR-managed Playwright wrapper, using the same managed session/profile key model as Codex.
- Newly launched embedded OpenCode sessions also get a session-local `XDG_CONFIG_HOME` overlay that shadows the managed `playwright` skill and, when registered, the `lcr_runtime` skill, so OpenCode is steered toward the managed MCP path without changing the user's real `~/.config/opencode`.
- OpenCode sessions now track their live Playwright tool activity plus the current managed browser page URL, so the shared browser strip/reveal UI can surface the same current-page and reconnect guidance patterns that Codex already uses.
- OpenCode browser-backed question waits now reuse that same managed browser state, so when OpenCode pauses for user input the session can stay in a `waiting for user` browser state and keep `ctrl+o` available to reveal or refocus the managed browser window.
- LCAgent now exposes native `browser_*` tools backed by an LCR-managed Playwright MCP process, tracks current page state in the embedded UI, and shadows the `playwright` skill by browser capability.
- Already-running embedded Codex helper processes do not retroactively pick up new MCP launch wiring; they still need to be reopened or reconnected.
- Every managed Codex turn start and steer now carries LCR-owned application context for the structured browser-attention contract. A reopened or reconnected thread therefore receives current handoff guidance even when its persisted history names an older generated Playwright skill path.
- URL-based login waits already have an LCR-managed attention flow and interactive-browser lease.
- Embedded Codex and OpenCode can now make an explicit, structured `lcr_runtime/request_browser_attention` handoff after Playwright reaches a human-only step. The handoff carries a bounded user-facing instruction, survives turn idle/history replay, and is cleared by the next successfully submitted user message rather than by parsing assistant prose.
- Runtime-skill availability and managed-Playwright availability are gated independently, so `Classic browser behavior` does not advertise a managed-browser handoff that cannot succeed.
- Live browser waits are now surfaced passively in the project list, detail pane, attention reasons, and footer so the popup is not the only visible signal.
- Browser waits now raise the centered attention dialog even while the affected embedded session is visible, unless another provider input dialog already owns the foreground. Dismissing it acknowledges that specific handoff while leaving the Browser sidebar and `ctrl+o` available; a changed instruction or failed reveal can surface it again.
- Embedded Codex sessions now remember the latest Playwright page URL they reached, and the visible pane can reveal that same managed browser window with `ctrl+o`.
- `ctrl+o` now reveals or focuses a live attached managed Codex browser based on fresh managed browser state or live Codex browser activity, even when the session has not reported a current page URL, so hidden login windows do not become unreachable.
- Visible embedded sessions briefly retry managed browser-state hydration when a browser page or login handoff appears before the Playwright wrapper has written a revealable browser PID, so `ctrl+o` can become available without leaving and re-entering the session.
- Visible embedded sessions now renew managed browser liveness asynchronously from the wrapper heartbeat, independently of browser tool activity. Idle live browsers remain revealable, stale state files become detached, refreshes are coalesced, and `ctrl+o` performs a final liveness probe before revealing the existing browser context.
- Resumed embedded Codex sessions mark browser page URLs recovered from transcript history as no longer attached, so the persistent Browser panel does not offer a broken `ctrl+o` reveal or keep showing stale URLs.
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
- Project rows switch the assessment status to `browser`, show the Playwright/browser source in the summary column, pulse with browser-specific styling, and add a footer alert while any cached embedded session is waiting on browser input.
- The selected project's detail pane shows a dedicated `Browser` field and attention reason for live browser waits.
- Codex Playwright activity is tracked live enough to distinguish:
  - idle
  - active
  - waiting for user
- Browser-attention prompts appear when an embedded session needs help, including while that session is already visible. A specific dismissal is deduplicated without erasing the underlying Browser sidebar state.

### Actionable Handoffs

- Hidden embedded Codex sessions:
  - can raise a browser-attention popup
  - can reveal the managed browser window for that same session when policy is `managed + promote`
- Visible embedded Codex sessions:
  - can reveal the same managed browser window with `o`
  - show footer/request hints that explain the browser-login flow
  - can reveal the current managed browser window with `ctrl+o` after a background navigation finishes, even if the TUI's cached managed-browser state has expired while Codex is still actively waiting on browser input
- Managed Codex login flows now go through an LCR-owned interactive browser lease:
  - one session can hold the interactive browser slot at a time
  - later sessions are blocked cleanly instead of blindly opening another browser login flow
  - failed browser-reveal attempts release the slot immediately
- Managed browser metadata is now written under the LCR data dir so the TUI can find and reveal the correct browser session instead of opening the URL in a disconnected desktop browser.
- On macOS, managed browser hide/reveal now targets the browser PID through Accessibility/AppKit instead of depending on the `System Events` application host. Every `osascript` attempt, including verified activation retries and the named-process fallback, is time-bounded and preserves useful diagnostics.
- On macOS, managed browser reveal now raises the target process window after un-hiding it, which keeps parallel Chrome-backed Playwright sessions from falling back to whichever Chrome window was last active.
- On macOS, PID reveal performs its short activation retries and active-process verification inside the same bounded command, so Enter/`ctrl+o` cannot report success before the browser is ready for keyboard and pointer input.
- On macOS, managed background browsers are re-hidden until the user reveals them through LCR, which reduces focus stealing from later Playwright navigations or newly created browser windows.
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
- Browser-attention coverage now verifies the exact structured tool identity, required instruction, stale or mismatched managed state rejection, failed tool results, idle and resume persistence, successful-response clearing, inactivity protection, popup acknowledgement/retry behavior, and OpenCode parity.
- Codex turn-start and turn-steer coverage now verifies that current managed-browser guidance is supplied as application context only when both managed Playwright and the runtime MCP are available, without rewriting the user's submitted text.
- Handoff state reads use the same cross-process state lock as the managed-browser writer, and hydration coverage verifies that initially hidden OpenCode/LCAgent waits surface as soon as their revealable browser state arrives.
- macOS window-control coverage now verifies launch-safe background hiding, PID-targeted activation postconditions, bounded verified retries, retained `(-600)` diagnostics, and termination of hung commands without requiring a live UI.

## Current Provider Status

### Codex

- Best current target for managed behavior.
- Managed mode now overrides the embedded Codex `playwright` MCP launch to use the LCR wrapper plus a persistent Playwright profile.
- Embedded elicitation replies are wired.
- Managed same-context browser reveal is working in both hidden and visible session flows for newly launched embedded sessions.
- Codex reconstructs an unresolved structured browser handoff from ordered thread history after reconnect; a later structured user-message item resolves it.
- MCP launch overrides apply when an embedded helper process starts. Reopened and reconnected threads use the new helper wiring, and per-turn LCR application context prevents older persisted skill references from suppressing the current structured handoff.

### OpenCode

- Policy/status plumbing exists.
- Managed Playwright launch now overrides OpenCode's `mcp.playwright` entry to use the LCR wrapper plus persistent managed browser/session keys.
- Managed sessions now also shadow the OpenCode `playwright` skill per session, which keeps real browser tasks on the managed MCP path instead of falling back to standalone Playwright CLI/browser launches.
- OpenCode now reports live Playwright tool activity and the current managed browser page URL into the shared embedded browser UI, including `ctrl+o` reveal/focus handling and reconnect guidance when the session browser wiring no longer matches current settings.
- OpenCode browser-backed structured-input waits now keep the session in a `waiting for user` browser state, which lets the same browser-attention/reveal flow stay active while the user is answering a browser-related prompt.
- OpenCode also recognizes the exact structured runtime handoff, retains its instruction through idle/history refresh, and clears it on a successful user submission without accepting similarly named tools or failed results.
- Managed OpenCode browser smoke coverage is now isolated from the user's real OpenCode data home, so smoke verification no longer depends on or mutates the normal dashboard state.
- Embedded elicitation control is still weaker than Codex, so browser-attention/login waits are not yet at full parity.
- Treat as managed for normal Playwright browsing, with improving UI parity but still interaction-limited until the embedded reply path is clearer.

### LCAgent

- Native LCAgent `browser_*` tools are available when managed browser mode is enabled, with calls routed through an LCR-owned `mcp-server-playwright` process so LCAgent uses the same mature browser backend shape as Codex/OpenCode.
- Browser activity and current page URL flow into the shared embedded browser UI, including current-page reveal/focus affordances.
- The `playwright` skill is capability-aware: unavailable runs report the blocker, while managed runs point to native `browser_*` tools instead of CLI/MCP shell commands.
- `make lcagent-browser-smoke` runs a scripted local managed-browser smoke against an HTTP fixture and verifies snapshot, screenshot artifact, managed state, and process cleanup.

### Claude Code

- Launch-policy plumbing exists.
- Embedded approval/tool-input/elicitation control is still limited.
- Treat as observe-only for now.

## Immediate Next Steps

1. Verify the hardened backgrounded browser reveal path in real login flows.
   - Exercise login, MFA, consent, and CAPTCHA pages across Chromium updates.
   - Confirm Accessibility permission failures remain actionable in the centered dialog.

2. Evaluate whether the advisory handoff should become a mechanically blocking MCP elicitation.
   - The current tool deliberately returns immediately and tells the assistant to end its turn.
   - A future blocking version would need a bidirectional runtime-MCP dispatcher plus an explicit tool timeout and granular elicitation approval policy.

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
- Should structured browser handoffs remain advisory turn boundaries, or eventually block through MCP elicitation until the user accepts?
- When the real controller exists, should isolation default to per-task or per-project?
- What is the cleanest way to let OpenCode and Claude participate without brittle wrappers?

## Likely Follow-Up Docs

- If this grows beyond the current workstream, add a broader `docs/workstreams.md` or `docs/roadmap.md`.
- Keep provider-footprint docs separate from this one:
  - `docs/codex_cli_footprint.md`
  - `docs/claude_code_footprint.md`
