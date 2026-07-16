package codexapp

const (
	managedBrowserContextSource = "little-control-room/browser-attention"
	applicationContextKind      = "application"
)

const managedBrowserTurnContextText = `Little Control Room managed-browser contract for this turn:
- Use the registered playwright MCP tools for browser work; do not launch a separate Playwright CLI or desktop browser.
- If the managed page reaches a login, MFA, consent, CAPTCHA, or another human-only browser step, first navigate the managed browser to the exact page where the user must act.
- Then call lcr_runtime/request_browser_attention with a short message that says exactly what the user should do. Little Control Room will surface that same browser through its attention dialog and Browser sidebar.
- After the attention call succeeds, stop the turn immediately. Do not poll, wait, or use another tool until the user sends a new message.
- If the attention call fails, report that failure instead of claiming the browser was shown.`

// managedBrowserTurnContext is attached as app-server application context on
// every submitted turn. Unlike a generated skill path captured in an older
// rollout, this context is supplied by the currently attached LCR process, so
// reconnecting a thread also refreshes its browser-attention contract.
func (s *appServerSession) managedBrowserTurnContext() map[string]additionalContextEntry {
	if s == nil || !s.playwrightMCPExpected || !s.runtimeMCPExpected {
		return nil
	}
	return map[string]additionalContextEntry{
		managedBrowserContextSource: {
			Kind:  applicationContextKind,
			Value: managedBrowserTurnContextText,
		},
	}
}
