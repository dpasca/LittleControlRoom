# Browser Automation Working Plan

This is a living workstream document for browser automation inside Little Control Room.

It is intentionally different from `STATUS.md`:

- `STATUS.md` stays short, durable, and project-wide.
- This doc tracks the active browser-automation roadmap, current design shape, safety rules, and open questions.
- Git history remains the source of truth for detailed chronology.

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
  - can open a managed login URL directly in the default browser when policy is `managed + promote`
- Visible embedded Codex sessions:
  - can open the same login URL with `o`
  - show footer/request hints that explain the browser-login flow
- Managed Codex login flows now go through an LCR-owned interactive browser lease:
  - one session can hold the interactive browser slot at a time
  - later sessions are blocked cleanly instead of blindly opening another browser login flow
  - failed browser-open attempts release the slot immediately

## Current Provider Status

### Codex

- Best current target for managed behavior.
- Managed mode now overrides the embedded Codex `playwright` MCP launch to enforce headless/isolated browser startup in `Only when needed`.
- Embedded elicitation replies are wired.
- Managed login URL handoff is working in both hidden and visible session flows.

### OpenCode

- Policy/status plumbing exists.
- Embedded elicitation control is still weaker.
- Treat as observe-first until the launch/config and reply path are clearer.

### Claude Code

- Launch-policy plumbing exists.
- Embedded approval/tool-input/elicitation control is still limited.
- Treat as observe-only for now.

## Immediate Next Steps

1. Polish the post-login flow.
   - Make "open browser", "waiting for browser", "blocked by another login", and "ready to continue" wording feel fully consistent.
   - Check whether URL-based accept/done copy in the visible pane can be clearer without becoming noisy.

2. Improve passive visibility outside popups.
   - Surface browser-attention state in the project list or detail pane.
   - Avoid making the popup the only way to notice a login wait.

3. Keep tightening tests around managed Codex transitions.
   - waiting -> open browser -> accept
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
   - Define where per-task or per-project browser state should live.
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
