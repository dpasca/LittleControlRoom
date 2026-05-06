# Little Control Room Incident Log: Managed Playwright Browser Reveal Immediately Re-Hides

## Incident overview

- **Date (JST):** 2026-05-06
- **Area:** Managed Playwright browser handoff / human login flow
- **Reporter symptom:** The managed browser window briefly flashed on screen, then immediately disappeared again.
- **Impact:** The user could not interact with a login page that needed manual credentials, so browser-assisted work stalled.

## Reproduction context

- Project being worked from: `/Users/davide/dev/poncle_repos/proj_if`
- Target URL: `https://bsserver19:3012/BadSeed/Leaf`
- Flow:
  1. Embedded Codex used the LCR-provided Playwright MCP tools.
  2. Codex navigated to the Gitea repository URL.
  3. The page redirected to the Gitea login form.
  4. Codex asked the user to log in through the managed browser window.
  5. User observed the browser window flash visible and then hide again immediately.

## Expected behavior

- When a human login/manual browser step is requested, the managed browser should become visible and stay visible until the user finishes the handoff or LCR deliberately hides it again.

## Initial findings

- The issue appears likely to be a race in managed browser visibility state, not a Gitea-specific problem.
- Follow-up observation: changing Browser windows to `Always show` and then closing/reopening the Playwright page inside the existing embedded Codex session did **not** stop the browser from disappearing.
  - If this setting is expected to affect an already-running managed Playwright session, that path is not working.
  - If the setting only applies after relaunching LCR or starting a new embedded provider session, the UI/status copy should say so explicitly instead of implying the change is live.
- In both reveal paths inspected:
  - `internal/cli/browser.go` calls `RevealManagedPlaywrightState` before `MarkManagedPlaywrightStateRevealed`.
  - `internal/tui/browser_open.go` does the same in `revealManagedBrowserSession`.
- The monitor path in `internal/cli/playwright_mcp.go` continues enforcing hidden state while `state.Hidden` remains true:
  - `managedBrowserHiddenEnforcementInterval` is 250ms.
  - `shouldHideManagedBrowser` allows repeated hide attempts while the browser is marked hidden.
- This means a monitor tick can land after the macOS reveal/focus call but before the state file is updated to `Hidden=false`, producing exactly the observed "flash visible, then hidden again" behavior.

## Suspected root cause

The reveal operation is not represented in durable state before the OS-level window reveal. The browser monitor sees stale hidden state during the reveal window and re-applies hiding.

## Recommended fix direction

1. Make the reveal transition atomic from the monitor's perspective.
   - Persist reveal intent before raising the browser window, or add an explicit `RevealRequested` / hide-suppression state.
   - Ensure failed reveals do not leave LCR in a confusing state.
2. Centralize the reveal operation so CLI and TUI reveal paths cannot drift.
3. Clarify or fix runtime settings application.
   - Either apply Browser windows policy changes to existing managed Playwright sessions, or clearly mark them as taking effect only for newly launched sessions / after restart.
   - The current user-facing behavior made `Always show` look like a plausible immediate workaround, but it did not help the active session.
4. Add a regression test that simulates:
   - browser state starts as `Hidden=true`;
   - reveal begins;
   - monitor reconcile runs between state read and OS reveal completion;
   - browser is not hidden again once a reveal has been requested.

## Workarounds

- In `/settings`, switch browser windows to `Always show` for the session if the user needs to complete a login.
- Use `Classic browser behavior` as a fallback if managed reveal remains unreliable.
- For Git/Gitea migration specifically, use HTTPS with a personal access token or configure SSH outside the managed browser flow.

## Current status

- Fix implemented in this branch.
  - CLI and TUI reveal paths now use one centralized managed-session reveal operation.
  - Reveal intent is persisted before the macOS raise/focus call.
  - The reveal transition and monitor hide/write path share a managed Playwright state lock, so the monitor cannot re-hide the browser during the handoff race.
  - Settings copy now makes clear that Browser windows policy changes apply to newly opened or reconnected embedded sessions.
- Validation passed on 2026-05-07 JST: `make test`, `make scan`, and `make doctor`.
