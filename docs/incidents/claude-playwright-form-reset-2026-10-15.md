# Claude Code loses a managed Playwright form after a conversational follow-up

## Evidence

Session `d8beafe5-596c-49ab-80cd-e331ea95769f` in
`lcr-intercept--publisher-demo-roadmap` filled the MicroProse publishing form
three times on October 15, 2026. The durable Claude transcript records this
sequence (JST):

| Time | Event |
| --- | --- |
| 10:24:54 | Successful browser-attention handoff, Chromium PID 26827. |
| 10:29:59–10:30:38 | User asks about localisation; Claude answers and edits the draft without browser tools or another handoff. |
| 10:32:04 | `browser_tabs` returns only `about:blank`. |
| 10:32:36 | Form refilled, successful handoff, Chromium PID 54039. |
| 10:40:06–10:40:13 | User asks about the production stage; Claude answers without browser tools or another handoff. |
| 10:41:30 | `browser_tabs` again returns only `about:blank`. |
| 10:42:00 | Third form fill and successful handoff, Chromium PID 86219. |
| 10:45:07–10:46:04 | Follow-up browser tools find the existing form, change it, and request attention again with PID 86219. |

All four attention receipts identify the same managed-browser session key,
`dm4ya04zrt20-019f9e31363bb6ff`. This is stronger evidence than a hidden window:
the first two browser processes were replaced and the filled page was lost.
No `browser_close` call appears in this sequence.

## Cause

Claude owns Playwright as a stdio MCP child. LCR normally closes Claude's stdin
when its work settles, which also shuts down that browser. A successful
`request_browser_attention` prevented this shutdown only while
`browserHandoffPending` remained true.

Submitting any ordinary follow-up clears that attention flag and, under the
background-window policy, requests hiding. When a conversational answer then
finished without another attention call, the stream completion path closed
stdin. The next Playwright call launched a new browser with a blank page. The
saved profile can retain cookies, but it does not preserve the former live
tabs and unsaved form values.

Hiding on submission is an intentional policy action. Destroying the browser
when that conversational turn completes is the lifecycle bug. Repeatedly
requesting attention happened to avoid it, explaining why the final browser
follow-up retained PID 86219.

## Change

Browser ownership is now separate from attention. A successful managed
Playwright operation retains Claude and its MCP children between turns;
browser installation alone does not acquire ownership. Successful browser
close or session close releases it. Failed close keeps the page and handoff.
Inactivity cleanup also preserves an owned browser.

Current stream idle events and the older result, terminal-assistant, and
verified-transcript completion paths settle the turn without closing stdin
while the browser remains open. Legacy result handling does not consume a new
submission when a late result follows an already-settled terminal message.
Settings notices explain that staged model, effort, and style changes wait for
browser closure. Compaction explicitly refuses while the browser is retained,
because that operation currently needs a fresh process.

The browser ownership flag follows live MCP results, not transcript replay or
conversation-text classification. It does not restore pages after an explicit
session close, process crash, or LCR restart. Closing the browser window outside
MCP is not a release signal; an explicit browser/session close still releases
the retained process.

## Verification

Regression coverage exercises conversation follow-ups with and without an
attention handoff across all four completion paths, explicit and failed close,
inactivity, deferred settings, compaction, and late legacy results. A real
subprocess fixture verifies one PID across filling, a conversation-only
follow-up, and inspection, followed by normal exit after browser close, for
both supported stream modes. It does not contact the publishing site or alter
the original session's browser.

`make test`, `make scan`, and `make doctor` passed. Scan and doctor used an
isolated database/config path under this worktree's ignored
`dist/browser-lifetime-check/` directory. The focused browser lifetime tests
also passed under Go's race detector. An isolated PTY-backed `make tui`
rendered successfully; `/perf` showed no captured UI stalls, and the temporary
TUI was shut down with SIGTERM afterward.

Deployment requires rebuilding and restarting LCR; running sessions are not
patched by changing this checkout.
