package boss

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"lcroom/internal/control"
	"lcroom/internal/uistyle"
)

type PermissionConfirmationOptions struct {
	Preview      string
	Busy         bool
	ErrorText    string
	ScrollOffset int
	ShowDetails  bool
	// Selected indexes PermissionChoices for the same operation.
	Selected int
	// StandingUnavailable explains why saving a standing permission is not
	// offered for this request even though its arguments are otherwise eligible.
	StandingUnavailable string
}

// PermissionChoice is one approval scope offered by the permission dialog.
type PermissionChoice int

const (
	PermissionAllowOnce PermissionChoice = iota
	PermissionAllowSession
	PermissionAllowAlways
	PermissionAllowPair
)

// PermissionChoices lists approval scopes from narrowest to broadest. When a
// two-way message grant is available it replaces the one-way standing
// permission, so the dialog never offers two different kinds of "always".
func PermissionChoices(op control.Operation, standingUnavailable string) []PermissionChoice {
	choices := []PermissionChoice{PermissionAllowOnce}
	_, standing := control.PermissionForOperation(op)
	standing = standing && standingUnavailable == ""
	if standing {
		choices = append(choices, PermissionAllowSession)
	}
	if _, ok := control.CollaborationForOperation(op); ok {
		choices = append(choices, PermissionAllowPair)
	} else if standing {
		choices = append(choices, PermissionAllowAlways)
	}
	return choices
}

func permissionProviderLabel(op control.Operation) string {
	if label := control.Provider(op.Provider).Label(); label != "" {
		return label
	}
	if op.Provider != "" {
		return op.Provider
	}
	return "agent"
}

func permissionChoiceLabel(op control.Operation, choice PermissionChoice) string {
	provider := permissionProviderLabel(op)
	switch choice {
	case PermissionAllowSession:
		return "For this " + provider + " session"
	case PermissionAllowAlways:
		return "Always, from any " + provider + " session in " + filepath.Base(op.ProjectPath)
	case PermissionAllowPair:
		target := ""
		if input, err := engineerSendPromptInputFromInvocation(op.Invocation); err == nil {
			target = filepath.Base(input.ProjectPath)
		}
		return "Always, messages both ways: " + filepath.Base(op.ProjectPath) + " ↔ " + target
	default:
		return "Just this once"
	}
}

// permissionChoiceHint explains the highlighted choice in plain terms.
func permissionChoiceHint(op control.Operation, choice PermissionChoice, limit int) string {
	same := " Same action, target and settings only; revoke with /collab."
	if limit > 0 {
		same += fmt.Sprintf(" Covers %d launches including this one.", limit)
	}
	switch choice {
	case PermissionAllowSession:
		return "Matching requests from this " + permissionProviderLabel(op) + " session run without asking." + same
	case PermissionAllowAlways:
		return "Matching requests from this and future " + permissionProviderLabel(op) + " sessions in this project run without asking." + same
	case PermissionAllowPair:
		return "Messages between these two projects, in either direction and from any session, are delivered without asking. Revoke with /collab."
	default:
		return "Runs this request only; the agent asks again next time."
	}
}

// RenderPermissionConfirmationDialog keeps choices visible while details scroll.
// The second return value bounds the scroll offset. Permissions apply only to
// this capability and exact target, with reviewed settings held fixed.
func RenderPermissionConfirmationDialog(op control.Operation, options PermissionConfirmationOptions, bodyW, bodyH int) (string, int, error) {
	inv, err := control.ValidateInvocation(op.Invocation)
	if err != nil {
		return "", 0, err
	}
	p, eligible := control.PermissionForOperation(op)
	choices := PermissionChoices(op, options.StandingUnavailable)
	selected := maxInt(0, minInt(options.Selected, len(choices)-1))
	panelW := minInt(bodyW-4, 96)
	width := bossPanelInnerWidth(panelW)
	m := Model{pendingControl: &ControlProposal{Invocation: inv, Preview: options.Preview}}
	content := m.renderControlConfirmationContent(width)
	// The common renderer ends with its ordinary Enter/Esc legend. This dialog
	// owns a fixed footer instead, so it stays visible at small terminal sizes.
	if i := strings.LastIndex(content, "\n"); i >= 0 {
		content = content[:i]
	}
	// Keep the full request inspectable without making routine approvals taller.
	if options.ShowDetails {
		var details bytes.Buffer
		if err := json.Indent(&details, inv.Args, "", "  "); err == nil {
			content += "\n\n" + bossControlSectionStyle.Render("Full request details") + "\n" + strings.Join(wrappedBlockLines(details.String(), width), "\n")
		}
	}
	lines := []string{}
	if op.ProjectPath != "" {
		lines = append(lines, renderBossControlPath("From", permissionProviderLabel(op)+" · "+op.ProjectPath, width))
	}
	if eligible && options.StandingUnavailable != "" {
		lines = append(lines, wrappedBlockLines("Can't be saved as a permission: "+options.StandingUnavailable, width)...)
	}
	if op.ResumeOnSuccess {
		lines = append(lines, wrappedBlockLines("Caller continuation: enabled after success for the same eligible session. New input, stop, replacement, failure, or restart suppresses it.", width)...)
	} else {
		lines = append(lines, wrappedBlockLines("Caller continuation: disabled. After this succeeds, the caller waits for another message.", width)...)
	}
	content = strings.Join(lines, "\n") + "\n\n" + content

	var rows []string
	if len(choices) > 1 {
		rows = append(rows, bossControlSectionStyle.Render("Allow this request?"))
		for i, choice := range choices {
			marker, tone := "  ", uistyle.DialogActionSecondary
			if i == selected {
				marker, tone = "▸ ", uistyle.DialogActionPrimary
			}
			if options.Busy {
				tone = uistyle.DialogActionDisabled
			}
			rows = append(rows, fitStyledLine(marker+renderBossControlAction(strconv.Itoa(i+1), permissionChoiceLabel(op, choice), tone), width))
		}
		for _, line := range wrappedBlockLines(permissionChoiceHint(op, choices[selected], p.Limit), width) {
			rows = append(rows, bossControlLabelStyle.Render(line))
		}
	}
	if options.Busy {
		rows = append(rows, "Saving approval...")
	} else if options.ErrorText != "" {
		rows = append(rows, fitLine(options.ErrorText, width))
	}
	confirmLabel := "confirm"
	if len(choices) == 1 {
		confirmLabel = "allow once"
	}
	if op.Source == "chat" && inv.Capability == control.CapabilityTodoCreateWorktreeAndStartEngineer {
		confirmLabel = "start in worktree"
	}
	if op.Source == "chat" && inv.Capability == control.CapabilityEngineerSendPrompt {
		confirmLabel = "send"
	}
	if op.Source == "chat" && inv.Capability == control.CapabilityTodoRepairOwner {
		confirmLabel = "repair owner"
	}
	if op.Source == "chat" && inv.Capability == control.CapabilityTodoAdd {
		confirmLabel = "add"
	}
	actionTone := func(tone uistyle.DialogActionTone) uistyle.DialogActionTone {
		if options.Busy {
			return uistyle.DialogActionDisabled
		}
		return tone
	}
	detailsLabel := "show JSON"
	if options.ShowDetails {
		detailsLabel = "hide JSON"
	}
	if op.Source == "chat" && inv.Capability == control.CapabilityTodoCreateWorktreeAndStartEngineer {
		var input control.TodoCreateWorktreeAndStartEngineerInput
		_ = json.Unmarshal(inv.Args, &input)
		if input.TodoID == 0 {
			rows = append(rows, renderBossControlAction("q", "TODO only", uistyle.DialogActionSecondary))
		}
	}
	chips := []string{
		renderBossControlAction("Enter", confirmLabel, actionTone(uistyle.DialogActionPrimary)),
		renderBossControlAction("Esc", "deny", actionTone(uistyle.DialogActionCancel)),
	}
	if len(choices) > 1 {
		chips = append(chips, renderBossControlAction("↑/↓", "choose", uistyle.DialogActionNavigate))
	}
	if op.Source != "chat" {
		chips = append(chips, renderBossControlAction("d", detailsLabel, uistyle.DialogActionNavigate))
	}
	chips = append(chips, renderBossControlAction("PgUp/PgDn", "scroll", uistyle.DialogActionNavigate))
	row := ""
	for _, chip := range chips {
		if row != "" && lipgloss.Width(row)+3+lipgloss.Width(chip) > width {
			rows = append(rows, row)
			row = ""
		}
		if row != "" {
			row += "   "
		}
		row += chip
	}
	rows = append(rows, row)
	footer := strings.Join(rows, "\n")
	footerH := countBlockLines(footer)
	panelH := minInt(countBlockLines(content)+footerH+4, maxInt(8, bodyH-2))
	contentH := maxInt(0, panelH-4-footerH)
	contentLines := strings.Split(content, "\n")
	maxScroll := maxInt(0, len(contentLines)-contentH)
	offset := maxInt(0, minInt(options.ScrollOffset, maxScroll))
	content = fitRenderedBlock(strings.Join(contentLines[offset:], "\n"), width, contentH)
	if contentH > 0 {
		content += "\n"
	}
	title := m.controlConfirmationTitle() + " · LCR Permissions"
	if _, ok := control.CollaborationForOperation(op); ok {
		title = "Project Collaboration · LCR Permissions"
	}
	return renderBossControlPanel(title, content+footer, panelW, panelH), maxScroll, nil
}
