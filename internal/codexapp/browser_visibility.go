package codexapp

import (
	"strings"

	"lcroom/internal/browserctl"
)

var managedBrowserHideRequester = browserctl.RequestManagedPlaywrightSessionHide

func requestManagedBrowserHideAfterSubmission(dataDir, sessionKey string, policy browserctl.Policy) {
	normalized := policy.Normalize()
	if normalized.ManagementMode != browserctl.ManagementModeManaged ||
		normalized.DefaultBrowserMode != browserctl.BrowserModeHeadless ||
		strings.TrimSpace(sessionKey) == "" {
		return
	}
	_, _, _ = managedBrowserHideRequester(dataDir, sessionKey)
}
