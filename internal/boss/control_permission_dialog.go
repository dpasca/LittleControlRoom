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
	// Summary renderers intentionally shorten prompts and omit advanced settings.
	// Keep the complete validated request reachable before granting reuse.
	var details bytes.Buffer
	if err := json.Indent(&details, inv.Args, "", "  "); err == nil {
		content += "\n\n" + bossControlSectionStyle.Render("Full request details") + "\n" + strings.Join(wrappedBlockLines(details.String(), width), "\n")
	}
	lines := []string{renderBossControlDetail("Caller", op.ProjectPath, width)}
	if eligible {
		lines = append(lines, renderBossControlDetail("Permission", string(p.Capability)+" → "+p.Target, width))
		for _, line := range []string{"s allows matching requests from this calling session; p saves that permission for future sessions of this provider in this caller project.", "Exact target and reviewed model/resource settings stay fixed. Manage or revoke with /collab."} {
			lines = append(lines, wrappedBlockLines(line, width)...)
		}
		if p.Limit > 0 {
			lines = append(lines, wrappedBlockLines(fmt.Sprintf("Launch/continuation limit: %d uses including this action; renew explicitly when exhausted.", p.Limit), width)...)
		}
	}
	if op.ResumeOnSuccess {
		lines = append(lines, wrappedBlockLines("The caller requested automatic continuation after success. New input, stop, replacement, or restart cancels that continuation.", width)...)
	}
	if _, ok := control.CollaborationForOperation(op); ok {
		lines = append(lines, wrappedBlockLines("a allows messages in both directions between these projects, including future sessions.", width)...)
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
	rows = append([]string{renderBossControlAction("↑/↓/PgUp/PgDn", "scroll · Home/End", uistyle.DialogActionNavigate)}, rows...)
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
