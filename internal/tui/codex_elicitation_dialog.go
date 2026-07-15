package tui

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"lcroom/internal/codexapp"
)

func (m Model) renderCodexElicitationDialogOverlay(body string, bodyW, bodyH int, snapshot codexapp.Snapshot) string {
	request := snapshot.PendingElicitation
	if request == nil {
		return body
	}
	panelW := min(84, max(36, bodyW-16))
	panelW = min(panelW, max(14, bodyW-6))
	panelInnerW := max(10, panelW-4)
	content := m.renderCodexElicitationDialogContent(snapshot, *request, panelInnerW)
	content = clampDialogContent(
		content,
		max(1, bodyH-2),
		3,
		detailMutedStyle.Render("... request details shortened to fit ..."),
	)
	panel := renderDialogPanel(panelW, panelInnerW, content)
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexElicitationDialogContent(snapshot codexapp.Snapshot, request codexapp.ElicitationRequest, width int) string {
	needsComposer := codexElicitationNeedsComposer(request)
	title := "Connected tool needs approval"
	switch {
	case request.Mode == codexapp.ElicitationModeURL:
		title = "Browser action required"
	case needsComposer:
		title = "Connected tool needs input"
	}

	providerLabel := embeddedProvider(snapshot).Label()
	subject := providerLabel
	if serverName := strings.TrimSpace(request.ServerName); serverName != "" {
		subject += " · " + serverName
	}
	lines := []string{
		renderDialogHeader(title, subject, "", width),
		"",
	}
	lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, request.Summary())...)
	lines = append(lines, "")
	lines = append(lines, renderWrappedDialogTextLines(
		detailWarningStyle,
		width,
		providerLabel+" is paused for this request. If it expires before you answer, the turn may continue without the connected tool.",
	)...)

	switch request.Mode {
	case codexapp.ElicitationModeURL:
		if requestURL := strings.TrimSpace(request.URL); requestURL != "" {
			lines = append(lines, "", detailSectionStyle.Render("Requested page"))
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, requestURL)...)
		}
		loginURL := managedBrowserLoginURL(embeddedProvider(snapshot), snapshot.BrowserActivity.Policy, request.Mode, request.URL)
		if loginURL != "" && strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" {
			lines = append(lines, "")
			lines = append(lines, renderWrappedDialogTextLines(
				commandPaletteHintStyle,
				width,
				"Press O to show the managed browser. Finish there, then press Enter here when you are done.",
			)...)
		}
	case codexapp.ElicitationModeForm:
		lines = append(lines, "")
		if needsComposer {
			lines = append(lines, detailSectionStyle.Render("Response"))
			lines = append(lines, renderWrappedDialogTextLines(
				commandPaletteHintStyle,
				width,
				"Type the requested JSON or text in the composer below, then press Enter to send it.",
			)...)
			if schema := codexElicitationSchemaSummary(request.RequestedSchema); schema != "" {
				lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, "Expected response: "+schema)...)
			}
		} else {
			lines = append(lines, renderWrappedDialogTextLines(
				commandPaletteHintStyle,
				width,
				"No text or JSON is required. Choose whether to allow this request.",
			)...)
		}
	}

	lines = append(lines, "")
	actions := []string{}
	if request.Mode == codexapp.ElicitationModeURL &&
		strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" &&
		managedBrowserLoginURL(embeddedProvider(snapshot), snapshot.BrowserActivity.Policy, request.Mode, request.URL) != "" {
		actions = append(actions, renderDialogAction("O", "show browser", navigateActionKeyStyle, navigateActionTextStyle))
	}
	acceptLabel := "allow"
	if request.Mode == codexapp.ElicitationModeURL {
		acceptLabel = "done / allow"
	} else if needsComposer {
		acceptLabel = "send"
	}
	actions = append(actions,
		renderDialogAction("Enter", acceptLabel, commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("d", "decline", cancelActionKeyStyle, cancelActionTextStyle),
		renderDialogAction("c", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
		renderDialogAction("Alt+Up", "hide", navigateActionKeyStyle, navigateActionTextStyle),
	)
	lines = append(lines, renderCodexElicitationActionLines(width, actions)...)
	return strings.Join(lines, "\n")
}

func codexElicitationNeedsComposer(request codexapp.ElicitationRequest) bool {
	if request.Mode != codexapp.ElicitationModeForm {
		return false
	}
	raw := []byte(strings.TrimSpace(string(request.RequestedSchema)))
	if len(raw) == 0 {
		return false
	}

	var schema map[string]json.RawMessage
	if err := json.Unmarshal(raw, &schema); err != nil {
		return true
	}
	if len(schema) == 0 {
		return false
	}
	var required []string
	if requiredJSON, ok := schema["required"]; ok {
		_ = json.Unmarshal(requiredJSON, &required)
	}
	if len(required) > 0 {
		return true
	}
	if propertiesJSON, ok := schema["properties"]; ok {
		var properties map[string]json.RawMessage
		if err := json.Unmarshal(propertiesJSON, &properties); err != nil || len(properties) > 0 {
			return true
		}
		return false
	}

	var schemaType string
	if typeJSON, ok := schema["type"]; ok {
		_ = json.Unmarshal(typeJSON, &schemaType)
	}
	return strings.TrimSpace(schemaType) != "object"
}

func codexElicitationSchemaSummary(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		if compact, err := json.Marshal(decoded); err == nil {
			return truncateText(string(compact), 360)
		}
	}
	return truncateText(string(raw), 360)
}

func renderCodexElicitationActionLines(width int, actions []string) []string {
	separator := dialogPanelFillStyle.Render("   ")
	lines := make([]string, 0, 2)
	current := ""
	for _, action := range actions {
		if strings.TrimSpace(action) == "" {
			continue
		}
		if current == "" {
			current = action
			continue
		}
		candidate := current + separator + action
		if lipgloss.Width(candidate) <= width {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = action
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
