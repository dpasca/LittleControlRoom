package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"lcroom/internal/selfupdate"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	selfUpdateCheckTimeout   = 20 * time.Second
	selfUpdateInstallTimeout = 15 * time.Minute
)

type selfUpdateManager interface {
	Check(context.Context, bool) (selfupdate.CheckResult, error)
	Install(context.Context, selfupdate.Release) (selfupdate.InstallResult, error)
}

type selfUpdateDialogPhase int

const (
	selfUpdateDialogChecking selfUpdateDialogPhase = iota
	selfUpdateDialogAvailable
	selfUpdateDialogInstalling
	selfUpdateDialogInfo
	selfUpdateDialogError
)

type selfUpdateDialogFocus int

const (
	selfUpdateDialogFocusPrimary selfUpdateDialogFocus = iota
	selfUpdateDialogFocusLater
)

type selfUpdateDialogState struct {
	Phase        selfUpdateDialogPhase
	Result       selfupdate.CheckResult
	Release      *selfupdate.Release
	Err          error
	RetryInstall bool
	Selected     selfUpdateDialogFocus
}

type selfUpdateCheckMsg struct {
	result selfupdate.CheckResult
	forced bool
	err    error
}

type selfUpdateInstallMsg struct {
	result  selfupdate.InstallResult
	release selfupdate.Release
	err     error
}

func (m Model) checkSelfUpdateCmd(force bool) tea.Cmd {
	manager := m.selfUpdater
	if manager == nil {
		return nil
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, selfUpdateCheckTimeout)
		defer cancel()
		result, err := manager.Check(ctx, force)
		return selfUpdateCheckMsg{result: result, forced: force, err: err}
	}
}

func (m Model) installSelfUpdateCmd(release selfupdate.Release) tea.Cmd {
	manager := m.selfUpdater
	if manager == nil {
		return nil
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, selfUpdateInstallTimeout)
		defer cancel()
		result, err := manager.Install(ctx, release)
		return selfUpdateInstallMsg{result: result, release: release, err: err}
	}
}

func (m Model) openSelfUpdateDialog() (tea.Model, tea.Cmd) {
	dialog := &selfUpdateDialogState{
		Phase:    selfUpdateDialogChecking,
		Result:   m.selfUpdateStatus,
		Selected: selfUpdateDialogFocusLater,
	}
	if m.availableSelfUpdate != nil {
		release := *m.availableSelfUpdate
		dialog.Phase = selfUpdateDialogAvailable
		dialog.Release = &release
		m.selfUpdateDialog = dialog
		return m, nil
	}
	m.selfUpdateDialog = dialog
	if m.selfUpdateCheckInFlight {
		return m, nil
	}
	m.selfUpdateCheckInFlight = true
	return m, m.checkSelfUpdateCmd(true)
}

func (m Model) applySelfUpdateCheckMsg(msg selfUpdateCheckMsg) (tea.Model, tea.Cmd) {
	m.selfUpdateCheckInFlight = false
	m.selfUpdateStatus = msg.result
	if msg.result.Release != nil {
		release := *msg.result.Release
		m.availableSelfUpdate = &release
	} else if msg.err == nil {
		m.availableSelfUpdate = nil
	}
	if m.selfUpdateDialog == nil {
		return m, nil
	}
	dialog := m.selfUpdateDialog
	dialog.Result = msg.result
	dialog.Err = nil
	dialog.RetryInstall = false
	dialog.Selected = selfUpdateDialogFocusLater
	if msg.result.Release != nil {
		release := *msg.result.Release
		dialog.Release = &release
		dialog.Phase = selfUpdateDialogAvailable
		return m, nil
	}
	dialog.Release = nil
	if msg.err != nil {
		dialog.Phase = selfUpdateDialogError
		dialog.Err = msg.err
		m.status = "Update check failed"
		return m, nil
	}
	dialog.Phase = selfUpdateDialogInfo
	return m, nil
}

func (m Model) applySelfUpdateInstallMsg(msg selfUpdateInstallMsg) (tea.Model, tea.Cmd) {
	m.selfUpdateInstallInFlight = false
	if msg.err != nil {
		if m.selfUpdateDialog == nil {
			m.selfUpdateDialog = &selfUpdateDialogState{}
		}
		release := msg.release
		m.selfUpdateDialog.Phase = selfUpdateDialogError
		m.selfUpdateDialog.Release = &release
		m.selfUpdateDialog.Err = msg.err
		m.selfUpdateDialog.RetryInstall = true
		m.selfUpdateDialog.Selected = selfUpdateDialogFocusLater
		m.status = "Update install failed"
		return m, nil
	}
	for _, warning := range msg.result.Warnings {
		m.appendBackgroundErrorLogEntry("Update installed with a cleanup warning", errors.New(warning), "")
	}
	m.availableSelfUpdate = nil
	m.selfUpdateDialog = nil
	m.relaunchAfterUpdate = true
	m.installedUpdate = msg.result.Version
	return m.beginGracefulQuit()
}

func (m Model) updateSelfUpdateDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.selfUpdateDialog
	if dialog == nil {
		return m, nil
	}
	busy := dialog.Phase == selfUpdateDialogChecking || dialog.Phase == selfUpdateDialogInstalling
	if busy {
		return m, nil
	}
	hasPrimary := dialog.Phase == selfUpdateDialogAvailable || dialog.Phase == selfUpdateDialogError
	switch msg.String() {
	case "esc", "q":
		m.selfUpdateDialog = nil
		return m, nil
	case "ctrl+c":
		m.selfUpdateDialog = nil
		return m.updateNormalMode(msg)
	case "left", "h", "right", "l", "tab", "shift+tab":
		if hasPrimary {
			if dialog.Selected == selfUpdateDialogFocusPrimary {
				dialog.Selected = selfUpdateDialogFocusLater
			} else {
				dialog.Selected = selfUpdateDialogFocusPrimary
			}
		}
		return m, nil
	case "enter":
		if !hasPrimary || dialog.Selected == selfUpdateDialogFocusLater {
			m.selfUpdateDialog = nil
			return m, nil
		}
		if dialog.Phase == selfUpdateDialogError && !dialog.RetryInstall {
			dialog.Phase = selfUpdateDialogChecking
			dialog.Err = nil
			m.selfUpdateCheckInFlight = true
			return m, m.checkSelfUpdateCmd(true)
		}
		if dialog.Release == nil {
			dialog.Phase = selfUpdateDialogError
			dialog.Err = errors.New("release metadata is no longer available; check again")
			dialog.RetryInstall = false
			return m, nil
		}
		release := *dialog.Release
		dialog.Phase = selfUpdateDialogInstalling
		dialog.Err = nil
		m.selfUpdateInstallInFlight = true
		m.status = "Downloading and verifying " + release.Version + "..."
		return m, m.installSelfUpdateCmd(release)
	}
	return m, nil
}

func (m Model) renderSelfUpdateDialogOverlay(body string, bodyW, bodyH int) string {
	panelW := min(bodyW, min(max(62, bodyW-18), 92))
	panelInnerW := max(30, panelW-4)
	content := m.renderSelfUpdateDialogContent(panelInnerW, bodyH)
	panel := lipgloss.NewStyle().
		Width(panelW).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("42")).
		Padding(0, 1).
		Background(dialogPanelBackground).
		Foreground(lipgloss.Color("252")).
		Render(fillDialogBlock(content, panelInnerW))
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSelfUpdateDialogContent(width, bodyH int) string {
	dialog := m.selfUpdateDialog
	if dialog == nil {
		return ""
	}
	lines := []string{renderDialogHeader("Little Control Room Update", "", "", width), ""}
	current := strings.TrimSpace(dialog.Result.CurrentVersion)
	if current != "" {
		lines = append(lines, detailField("Current", detailValueStyle.Render(current)))
	}
	if dialog.Release != nil {
		lines = append(lines, detailField("Available", detailAttentionValueStyle.Render(dialog.Release.Version)))
		if !dialog.Release.PublishedAt.IsZero() {
			lines = append(lines, detailField("Published", detailMutedStyle.Render(dialog.Release.PublishedAt.Local().Format("2006-01-02 15:04 MST"))))
		}
		lines = append(lines, detailField("Download", detailMutedStyle.Render(formatUpdateBytes(dialog.Release.Archive.Size))))
	}
	if installPath := strings.TrimSpace(dialog.Result.InstallPath); installPath != "" {
		lines = append(lines, detailField("Install", detailMutedStyle.Render(truncateText(m.displayPathWithHomeTilde(installPath), max(18, width-9)))))
	}

	switch dialog.Phase {
	case selfUpdateDialogChecking:
		lines = append(lines, "", detailValueStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Checking GitHub for the latest stable release..."))
	case selfUpdateDialogInstalling:
		lines = append(lines, "", detailWarningStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Downloading, verifying, and installing both binaries..."))
		lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, "Keep Little Control Room open. Active engineer turns will be saved before the app restarts.")...)
	case selfUpdateDialogAvailable:
		lines = append(lines, "", detailSectionStyle.Render("Ready to update"))
		securityText := "After confirmation, LCR downloads the release archive and checksums from GitHub, verifies SHA-256 integrity, replaces lcroom and lcagent together, saves active engineer turns, and restarts."
		if dialog.Release != nil && strings.Contains(dialog.Release.Archive.Name, "Darwin") {
			securityText = "After confirmation, LCR downloads the release archive and checksums from GitHub, verifies SHA-256 integrity and Apple Developer signatures, replaces lcroom and lcagent together, saves active engineer turns, and restarts."
		}
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, securityText)...)
		if dialog.Release != nil && strings.TrimSpace(dialog.Release.Notes) != "" {
			lines = append(lines, "", detailSectionStyle.Render("Release notes"))
			notes := sanitizeUpdateNotes(dialog.Release.Notes)
			rendered := strings.Join(renderWrappedDialogTextLines(detailMutedStyle, width, notes), "\n")
			maxNotesLines := max(3, min(9, bodyH-20))
			rendered = clampDialogContent(rendered, maxNotesLines, 0, detailMutedStyle.Render("... more on the GitHub release page"))
			lines = append(lines, strings.Split(rendered, "\n")...)
		}
		lines = append(lines, "", renderSelfUpdateButtons("Update & restart", dialog.Selected))
		lines = append(lines,
			renderDialogAction("Tab", "switch", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Enter", "use highlighted", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "later", cancelActionKeyStyle, cancelActionTextStyle),
		)
	case selfUpdateDialogError:
		lines = append(lines, "", detailDangerStyle.Render("Update failed"))
		if dialog.Err != nil {
			lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, sanitizeUpdateNotes(dialog.Err.Error()))...)
		}
		lines = append(lines, "", renderSelfUpdateButtons("Retry", dialog.Selected))
		lines = append(lines,
			renderDialogAction("Tab", "switch", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Enter", "use highlighted", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "later", cancelActionKeyStyle, cancelActionTextStyle),
		)
	case selfUpdateDialogInfo:
		message := strings.TrimSpace(dialog.Result.Reason)
		if message == "" {
			message = "You already have the latest stable GitHub release."
		}
		lines = append(lines, "")
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, message)...)
		lines = append(lines, "", renderDialogButton("Close", true), renderDialogAction("Enter / Esc", "close", cancelActionKeyStyle, cancelActionTextStyle))
	}
	return strings.Join(lines, "\n")
}

func renderSelfUpdateButtons(primary string, selected selfUpdateDialogFocus) string {
	return lipgloss.JoinHorizontal(
		lipgloss.Left,
		renderDialogButton(primary, selected == selfUpdateDialogFocusPrimary),
		" ",
		renderDialogButton("Later", selected == selfUpdateDialogFocusLater),
	)
}

func sanitizeUpdateNotes(value string) string {
	value = ansi.Strip(strings.ReplaceAll(value, "\r\n", "\n"))
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (!unicode.IsControl(r) && r != unicode.ReplacementChar) {
			return r
		}
		return -1
	}, value))
}

func formatUpdateBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
}

func (m Model) renderSelfUpdateTopStatusIndicator() string {
	if m.availableSelfUpdate == nil {
		return ""
	}
	version := strings.TrimSpace(m.availableSelfUpdate.Version)
	if version == "" {
		version = "available"
	}
	return topStatusUpdateBadgeStyle.Render("/update " + version)
}

// RelaunchAfterUpdate tells the CLI host to exec the newly installed lcroom
// after the TUI and its background services have shut down cleanly.
func (m Model) RelaunchAfterUpdate() (string, bool) {
	return strings.TrimSpace(m.installedUpdate), m.relaunchAfterUpdate
}
