# macOS hidden-browser screenshot timeout

## Evidence

Claude Code called `browser_take_screenshot` three times in the masamuse session.
Each call reached Playwright and timed out after 5 seconds, after fonts loaded.
Codex reproduced the same failure, including on a static text-only page and with
a fixed viewport. Pausing the museum animation did not solve the failure.

LCR's managed background mode hides the Chromium application on macOS. A normal
follow-up message requests hiding again, so a browser-attention handoff followed
by a "ready" reply is **not** a valid visible-window comparison. Revealing with
Ctrl+O during the active turn produced a successful capture in 0.91 seconds at
the same timeout.

## Change

The shared `playwright-mcp` wrapper intercepts the exact MCP screenshot tool call
and acquires a short-lived capture lease. On macOS, a hidden background browser
is temporarily unhidden without requesting activation or enabling audio. The
monitor honors the lease; the response releases it and restores hiding before
reaching the agent. JSON-RPC IDs correlate responses, while unrelated messages
and image payloads pass through unchanged. Tool errors and process termination
also release captures. Overlapping screenshots hold separate leases, and leases
expire after one minute so a live monitor can recover from abandoned captures.

LCR's native MCP and worker screenshot paths use the same guard. Headless,
always-visible, and non-macOS sessions retain their normal capture behavior.
An explicit user reveal takes precedence over automatic restoration. No page
reload, viewport emulation, replacement browser, or human handoff is required.

This is a visibility workaround, not an offscreen renderer: a browser window
may briefly appear during capture. It does not request keyboard focus. Existing
running wrappers are pinned to their old binary and need to be restarted with
the updated build to acquire leases and honor them in the monitor.

## Verification

The capture guard was exercised against the existing managed museum browser,
with the screenshot still taken through its registered Playwright MCP tool. It
succeeded in 0.93 seconds and restored hidden, muted state afterward. Because
the live wrapper was the pre-fix binary, this smoke check used the existing
visibility lock to keep its old monitor from re-hiding during the capture.
Production uses leases rather than holding that lock across a tool call.

The opt-in `TestManagedScreenshotExistingSession` supports repeating this check
without launching another browser. Set `LCR_SCREENSHOT_TEST_DATA_DIR`,
`LCR_SCREENSHOT_TEST_SESSION`, and `LCR_SCREENSHOT_TEST_SIGNALS` (an empty existing
directory), then run that test. Once it writes `ready`, take a screenshot through
the session's registered MCP tool and create `release` in the signal directory.
Use `LCR_SCREENSHOT_TEST_LEGACY_MONITOR=1` only when testing against a pinned old
wrapper. The smoke check times out and restores visibility if no release arrives.

Regression tests cover monitor exclusion, overlapping leases, explicit reveal,
failed unhide, lease expiry, unchanged visible/headless behavior, large fragmented
image responses, server requests, tool errors, and transport shutdown.

`make test`, `make scan`, `make doctor`, and
`go test -race ./internal/browserctl ./internal/cli` passed. Scan and doctor used
an isolated database in this worktree's ignored `dist/` directory. A local
`dist/lcroom-screenshot-fix` build is ready for activation; the running LCR and
its pinned wrapper were not replaced during verification.
An isolated `make tui` session also launched in a real PTY, rendered the dashboard,
and exited through the quit dialog.
