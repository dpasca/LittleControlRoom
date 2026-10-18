package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/control"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const wakeupsListLimit = 50

// wakeupsDialog lists scheduled wakeups that have not been delivered yet and
// lets the operator cancel one. An empty project lists every project.
type wakeupsDialog struct {
	project   string
	items     []control.EngineerMessage
	selected  int
	busy      bool
	errorText string
}

type wakeupsLoadedMsg struct {
	project  string
	items    []control.EngineerMessage
	canceled *control.EngineerMessage
	err      error
}

// wakeupCanceledMsg reports a host-confirmed engineer.cancel_wakeup control.
type wakeupCanceledMsg struct {
	message control.EngineerMessage
	status  string
	err     error
}

func (m *Model) openWakeups(project string) tea.Cmd {
	m.wakeupsDialog = &wakeupsDialog{project: strings.TrimSpace(project), busy: true}
	return m.wakeupsCmd(m.wakeupsDialog.project, "")
}

func (m Model) wakeupsCmd(project, cancelID string) tea.Cmd {
	svc, parent := m.svc, m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		msg := wakeupsLoadedMsg{project: project}
		if svc == nil || svc.Store() == nil {
			msg.err = errors.New("store unavailable")
			return msg
		}
		ctx, cancel := context.WithTimeout(parent, engineerMessageStoreTimeout)
		defer cancel()
		if cancelID != "" {
			canceled, ok, err := svc.Store().CancelScheduledEngineerMessage(ctx, cancelID)
			if err != nil {
				msg.err = err
				return msg
			}
			if !ok {
				msg.err = fmt.Errorf("wakeup %s already started delivery or was settled", shortID(cancelID))
			} else {
				msg.canceled = &canceled
			}
		}
		items, err := svc.Store().ListScheduledEngineerMessages(ctx, wakeupsListLimit)
		msg.err = errors.Join(msg.err, err)
		msg.items = filterWakeupsByProject(items, project)
		return msg
	}
}

func filterWakeupsByProject(items []control.EngineerMessage, project string) []control.EngineerMessage {
	if project == "" {
		return items
	}
	out := make([]control.EngineerMessage, 0, len(items))
	for _, item := range items {
		if normalizeProjectPath(item.ProjectPath) == normalizeProjectPath(project) {
			out = append(out, item)
		}
	}
	return out
}

func (m Model) applyWakeupsLoaded(msg wakeupsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.canceled != nil {
		m.status = "Wakeup canceled for " + projectTitle(msg.canceled.ProjectPath, "") + "; it will not be delivered."
	}
	if m.wakeupsDialog == nil || m.wakeupsDialog.project != msg.project {
		return m, nil
	}
	d := *m.wakeupsDialog
	d.busy = false
	d.errorText = ""
	if msg.err != nil {
		d.errorText = "Wakeups: " + msg.err.Error()
	}
	d.items = msg.items
	d.selected = min(d.selected, max(0, len(d.items)-1))
	m.wakeupsDialog = &d
	return m, nil
}

func (m Model) updateWakeups(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := *m.wakeupsDialog
	switch key.String() {
	case "esc", "q":
		m.wakeupsDialog = nil
		return m, nil
	}
	if d.busy {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		d.selected = max(0, d.selected-1)
	case "down", "j":
		d.selected = min(max(0, len(d.items)-1), d.selected+1)
	case "c":
		if d.selected < len(d.items) && d.items[d.selected].State == control.EngineerMessageQueued {
			id := d.items[d.selected].ID
			d.busy = true
			m.wakeupsDialog = &d
			return m, m.wakeupsCmd(d.project, id)
		}
	}
	m.wakeupsDialog = &d
	return m, nil
}

func (m Model) renderWakeups(body string, width, height int) string {
	d := m.wakeupsDialog
	panelW := min(96, max(36, width-8))
	panelInnerW := max(20, panelW-4)
	subject := "all projects"
	if d.project != "" {
		subject = projectTitle(d.project, "")
	}
	lines := []string{renderDialogHeader("Scheduled Wakeups", subject, "", panelInnerW), ""}
	switch {
	case d.busy:
		lines = append(lines, detailMutedStyle.Render("Loading..."))
	case d.errorText != "":
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, panelInnerW, d.errorText)...)
	}
	if !d.busy && len(d.items) == 0 && d.errorText == "" {
		lines = append(lines, detailMutedStyle.Render("No pending wakeups. Engineers schedule them with engineer.send_prompt and deliver_at."))
	}
	now := m.currentTime()
	visible := max(1, min(len(d.items), height/2-8))
	start := max(0, d.selected-visible+1)
	for i := start; i < min(len(d.items), start+visible); i++ {
		lines = append(lines, renderWakeupRow(d.items[i], now, i == d.selected, panelInnerW))
	}
	if !d.busy && d.selected < len(d.items) {
		lines = append(lines, "")
		lines = append(lines, renderWakeupDetail(d.items[d.selected], panelInnerW)...)
	}
	lines = append(lines, "", strings.Join([]string{
		renderDialogAction("c", "cancel wakeup", cancelActionKeyStyle, cancelActionTextStyle),
		renderDialogAction("Up/Down", "choose", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}, "   "))
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (width-lipgloss.Width(panel))/2)
	top := max(0, (height-lipgloss.Height(panel))/3)
	return overlayBlock(body, panel, width, height, left, top)
}

func renderWakeupRow(item control.EngineerMessage, now time.Time, selected bool, width int) string {
	when := item.DeliverAt.Local().Format("Mon Jan 2 15:04")
	left := when + "  " + projectTitle(item.ProjectPath, "")
	right := wakeupDueLabel(item, now)
	marker := " "
	if selected {
		marker = ">"
	}
	left = truncateText(left, max(12, width-lipgloss.Width(right)-4))
	row := marker + " " + left
	if pad := width - lipgloss.Width(row) - lipgloss.Width(right); pad > 0 {
		row += strings.Repeat(" ", pad) + right
	}
	if selected {
		return commandPaletteSelectStyle.Width(width).Render(row)
	}
	return commandPaletteRowStyle.Width(width).Render(row)
}

func wakeupDueLabel(item control.EngineerMessage, now time.Time) string {
	if item.State == control.EngineerMessageDelivering {
		return "delivering"
	}
	if until := item.DeliverAt.Sub(now); until > 0 {
		return "in " + compactWakeupDuration(until)
	}
	return "due; waiting for session"
}

func compactWakeupDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "<1m"
	}
	days := int(d / (24 * time.Hour))
	hours := int(d % (24 * time.Hour) / time.Hour)
	minutes := int(d % time.Hour / time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

func renderWakeupDetail(item control.EngineerMessage, width int) []string {
	grant := control.UnattendedGrant{}
	if item.Unattended != nil {
		grant = *item.Unattended
	}
	lines := []string{detailSectionStyle.Render("Selected wakeup")}
	add := func(label, value string) {
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, label+": "+value)...)
	}
	add("Session", item.Provider.Label()+" "+item.TargetSessionID)
	if !item.ExpiresAt.IsZero() {
		add("Missed after", item.ExpiresAt.Local().Format("Mon Jan 2 15:04"))
	}
	add("Authority", grant.Summary())
	if item.LastError != "" {
		add("Last attempt", item.LastError)
	}
	prompt := strings.Join(strings.Fields(item.Prompt), " ")
	lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, truncateText(prompt, width*2))...)
	return lines
}

// executeCancelWakeupControl runs an operator-confirmed engineer.cancel_wakeup.
// Unlike the embedded-agent path it may cancel any pending wakeup.
func (m Model) executeCancelWakeupControl(inv control.Invocation) controlInvocationOutcome {
	var input control.EngineerCancelWakeupInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		m.status = "Control request invalid: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	svc, parent := m.svc, m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return controlInvocationOutcome{model: m, cmd: func() tea.Msg {
		msg := wakeupCanceledMsg{}
		if svc == nil || svc.Store() == nil {
			msg.err = errors.New("store unavailable")
			msg.status = msg.err.Error()
			return msg
		}
		ctx, cancel := context.WithTimeout(parent, engineerMessageStoreTimeout)
		defer cancel()
		message, ok, err := svc.Store().CancelScheduledEngineerMessage(ctx, input.WakeupID)
		msg.message = message
		switch {
		case err != nil:
			msg.err = err
		case !ok:
			msg.err = fmt.Errorf("wakeup %s is not pending (state %s)", input.WakeupID, message.State)
		}
		if msg.err != nil {
			msg.status = "Wakeup was not canceled: " + msg.err.Error()
		} else {
			msg.status = "Wakeup canceled for " + projectTitle(message.ProjectPath, "") + "; it will not be delivered."
		}
		return msg
	}}
}

func (m Model) applyWakeupCanceled(msg wakeupCanceledMsg) (tea.Model, tea.Cmd) {
	m.status = msg.status
	if m.wakeupsDialog != nil {
		d := *m.wakeupsDialog
		d.busy = true
		m.wakeupsDialog = &d
		return m, m.wakeupsCmd(d.project, "")
	}
	return m, nil
}
