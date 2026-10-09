package boss

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	lines := []string{renderBossControlDetail("Caller", op.ProjectPath, width)}
	permissionTone := uistyle.DialogActionSecondary
	if options.Busy {
		permissionTone = uistyle.DialogActionDisabled
	}
	if eligible {
		lines = append(lines, renderBossControlDetail("Permission", string(p.Capability)+" → "+p.Target, width))
		session := renderBossControlAction("s", "this caller session", permissionTone)
		saved := renderBossControlAction("p", "future "+op.Provider+" sessions here", permissionTone)
		if lipgloss.Width(session)+3+lipgloss.Width(saved) <= width {
			lines = append(lines, session+"   "+saved)
		} else {
			lines = append(lines, session, saved)
		}
		lines = append(lines, wrappedBlockLines("Same target and settings; manage or revoke with /collab.", width)...)
		if p.Limit > 0 {
			lines = append(lines, wrappedBlockLines(fmt.Sprintf("Launch limit: %d uses including this action; renew when exhausted.", p.Limit), width)...)
		}
	}
	if op.ResumeOnSuccess {
		lines = append(lines, wrappedBlockLines("The caller requested automatic continuation after success. New input, stop, replacement, or restart cancels that continuation.", width)...)
	}
	if _, ok := control.CollaborationForOperation(op); ok {
		lines = append(lines, renderBossControlAction("a", "messages both ways, including future sessions", permissionTone))
	}
	content = strings.Join(lines, "\n") + "\n\n" + content
	type action struct {
		key, label string
		tone       uistyle.DialogActionTone
	}
	actions := []action{{"Enter", "allow once", uistyle.DialogActionPrimary}}
	if eligible {
		actions = append(actions, action{"s", "allow for session", uistyle.DialogActionSecondary}, action{"p", "save permission", uistyle.DialogActionSecondary})
	}
	if _, ok := control.CollaborationForOperation(op); ok {
		actions = append(actions, action{"a", "always allow this pair", uistyle.DialogActionSecondary})
	}
	actions = append(actions, action{"Esc", "cancel", uistyle.DialogActionCancel})
	var rows []string
	row := ""
	for _, a := range actions {
		if options.Busy {
			a.tone = uistyle.DialogActionDisabled
		}
		chip := renderBossControlAction(a.key, a.label, a.tone)
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
	if options.Busy {
		rows = append([]string{"Saving permission..."}, rows...)
	} else if options.ErrorText != "" {
		rows = append([]string{fitLine(options.ErrorText, width)}, rows...)
	}
	detailsLabel := "show JSON"
	if options.ShowDetails {
		detailsLabel = "hide JSON"
	}
	navigation := renderBossControlAction("d", detailsLabel, uistyle.DialogActionNavigate) + "   " + renderBossControlAction("↑/↓/PgUp/PgDn", "scroll · Home/End", uistyle.DialogActionNavigate)
	rows = append([]string{navigation}, rows...)
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
