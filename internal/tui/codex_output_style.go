package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lcroom/internal/claudestyle"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
)

// The sidebar value is colored like a health signal: green only when the style
// in effect is known to constrain output, red for anything expected to be
// lengthy. Red is the safe side, so a style LCR cannot classify is flagged
// rather than made to look concise.
var (
	claudeOutputStyleTerseStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	claudeOutputStyleVerboseStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
)

type codexOutputStyleMsg struct {
	action codexActionMsg
	// style is the name that was staged, persisted only once the session has
	// accepted it. Claude Code silently ignores an unknown style, so saving an
	// unvalidated name would make it the default for every future session.
	style string
}

// setVisibleClaudeOutputStyle handles /style. With no argument it reports the
// current style and the styles found on disk; with an argument it stages the
// selection for the next prompt. Claude Code has no mid-turn control for
// output styles, so a change can never apply to a turn already running.
func (m Model) setVisibleClaudeOutputStyle(snapshot codexapp.Snapshot, name string) (tea.Model, tea.Cmd) {
	if embeddedProvider(snapshot) != codexapp.ProviderClaudeCode {
		m.status = "/style is available only for Claude Code engineers"
		return m, nil
	}
	if m.claudeOutputStyleBusy {
		m.status = "Claude Code output style update already in progress"
		return m, nil
	}
	if strings.TrimSpace(name) == "" {
		return m, m.showVisibleClaudeOutputStylesCmd(snapshot)
	}
	if snapshot.BusyExternal {
		m.status = "This Claude Code turn is owned by another process; its output style cannot be changed here"
		return m, nil
	}

	m.claudeOutputStyleBusy = true
	m.status = "Selecting Claude Code output style..."
	projectPath := m.codexVisibleProject
	command := m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if current := session.Snapshot(); snapshot.ThreadID != "" && current.ThreadID != snapshot.ThreadID {
			return codexActionMsg{projectPath: projectPath, err: codexapp.ErrSessionChanged}
		}
		controller, ok := session.(interface{ StageOutputStyle(string) error })
		if !ok {
			return codexActionMsg{projectPath: projectPath, err: fmt.Errorf("this Claude Code session does not support output styles; reconnect it")}
		}
		if err := controller.StageOutputStyle(name); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err, refreshView: true}
		}
		label := strings.TrimSpace(name)
		if claudestyle.IsDefault(label) {
			label = claudestyle.DefaultName
		}
		return codexActionMsg{
			projectPath: projectPath,
			status:      "Claude Code will use the " + label + " output style from the next prompt",
			refreshView: true,
		}
	})
	if command == nil {
		m.claudeOutputStyleBusy = false
		m.status = "Embedded Claude Code session unavailable"
		return m, nil
	}
	return m, func() tea.Msg {
		return codexOutputStyleMsg{action: command().(codexActionMsg), style: name}
	}
}

// saveClaudeOutputStyleCmd persists the selection as the default for future
// Claude Code sessions, matching /model's "even after restarting LCR"
// behavior. Saving is independent of applying: a failed save is reported
// without implying the running session missed the change.
func (m Model) saveClaudeOutputStyleCmd(name string) tea.Cmd {
	style := strings.TrimSpace(name)
	if claudestyle.IsDefault(style) {
		style = ""
	}
	baseline := m.currentSettingsBaseline()
	if strings.TrimSpace(baseline.EmbeddedClaudeOutputStyle) == style {
		return nil
	}
	settings := baseline
	settings.EmbeddedClaudeOutputStyle = style
	path := m.currentWritableConfigPath()
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		err := config.SaveEditableSettings(path, settings)
		return claudeOutputStyleSavedMsg{style: style, path: path, err: err}
	}
}

// showVisibleClaudeOutputStylesCmd reads the style files from disk off the UI
// thread and reports them as a status line.
func (m Model) showVisibleClaudeOutputStylesCmd(snapshot codexapp.Snapshot) tea.Cmd {
	projectPath := m.codexVisibleProject
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		lister, ok := session.(interface {
			ListOutputStyles() ([]claudestyle.Option, error)
		})
		if !ok {
			return codexActionMsg{projectPath: projectPath, err: fmt.Errorf("this Claude Code session does not support output styles; reconnect it")}
		}
		options, err := lister.ListOutputStyles()
		if err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{
			projectPath: projectPath,
			status:      claudeOutputStyleStatusLine(session.Snapshot(), options),
			refreshView: true,
		}
	})
}

func claudeOutputStyleStatusLine(snapshot codexapp.Snapshot, options []claudestyle.Option) string {
	current := strings.TrimSpace(snapshot.OutputStyle)
	if current == "" {
		current = claudestyle.DefaultName
	}
	parts := []string{"Output style: " + current}
	if pending := strings.TrimSpace(snapshot.PendingOutputStyle); pending != "" && pending != current {
		parts = append(parts, "next: "+pending)
	}
	if len(options) <= 1 {
		// Only the built-in default exists, so name the directory the user
		// would add a style file to rather than showing an empty list.
		return strings.Join(parts, " · ") + " · no style files found in ~/.claude/" + claudestyle.StylesDirName
	}
	return strings.Join(parts, " · ") + " · available: " + strings.Join(claudestyle.Names(options), ", ")
}

// claudeOutputStyleSidebarValueStyle colors the sidebar value by the style
// that will be in effect for the next prompt, so a staged change reads as the
// state the user is moving to rather than the one they are leaving.
func claudeOutputStyleSidebarValueStyle(snapshot codexapp.Snapshot) lipgloss.Style {
	effective := strings.TrimSpace(snapshot.PendingOutputStyle)
	if effective == "" {
		effective = strings.TrimSpace(snapshot.OutputStyle)
	}
	if claudestyle.VerbosityOf(snapshot.AvailableOutputStyles, effective) == claudestyle.VerbosityConcise {
		return claudeOutputStyleTerseStyle
	}
	return claudeOutputStyleVerboseStyle
}

// claudeOutputStyleSidebarLabel renders the sidebar value for Claude Code
// panes. The default style is named rather than hidden: the row is how a user
// discovers that output styles exist at all, and a blank row would leave them
// unable to tell "default" apart from "this feature is missing".
func claudeOutputStyleSidebarLabel(snapshot codexapp.Snapshot) (label string, pending bool) {
	if snapshot.Provider != codexapp.ProviderClaudeCode {
		return "", false
	}
	current := strings.TrimSpace(snapshot.OutputStyle)
	if current == "" {
		current = claudestyle.DefaultName
	}
	if next := strings.TrimSpace(snapshot.PendingOutputStyle); next != "" && next != current {
		return current + " → " + next, true
	}
	return current, false
}
