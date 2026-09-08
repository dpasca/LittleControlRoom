package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/integrations"
	"lcroom/internal/uistyle"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type skillsDialogState struct {
	Loading       bool
	Busy          bool
	Inventory     integrations.Inventory
	Target        integrations.Target
	ProjectPath   string
	Kind          string
	Err           error
	Selected      int
	Offset        int
	RequestID     int64
	Notice        string
	LastResult    *integrations.Result
	Pending       *control.Invocation
	Preview       string
	PreviewOffset int
	ShowingResult bool
	DetailText    string
	Editor        *integrationEditor
}

type integrationEditor struct {
	Kind   string
	Labels []string
	Fields []textinput.Model
	Focus  int
	Err    string
}
type skillsInventoryMsg struct {
	requestID int64
	inventory integrations.Inventory
	err       error
}

func (m *Model) openSkillsDialog() tea.Cmd {
	target := integrations.Target{Provider: "codex", Scope: "user"}
	projectPath := ""
	if project, ok := m.selectedProject(); ok {
		projectPath = project.Path
	}
	if m.codexVisibleProject != "" {
		projectPath = m.codexVisibleProject
		if snapshot, ok := m.codexSnapshots[projectPath]; ok {
			target.Provider = string(embeddedProvider(snapshot))
		}
	}
	if projectPath != "" {
		target.Scope = "project"
		target.ProjectPath = projectPath
	}
	m.skillsInventorySeq++
	m.skillsDialog = &skillsDialogState{Loading: true, Busy: m.integrationDialogBusy, RequestID: m.skillsInventorySeq, Target: target, ProjectPath: projectPath, Kind: "skill"}
	m.commandMode = false
	m.err = nil
	m.status = "Loading agent integrations..."
	return m.loadSkillsInventoryCmd(m.skillsInventorySeq)
}

func (m Model) loadSkillsInventoryCmd(requestID int64) tea.Cmd {
	manager := m.integrationManager()
	target := m.skillsDialog.Target
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		inv, err := manager.Inventory(ctx, target)
		return skillsInventoryMsg{requestID: requestID, inventory: inv, err: err}
	}
}

func (m Model) applySkillsInventoryMsg(msg skillsInventoryMsg) (tea.Model, tea.Cmd) {
	if m.skillsDialog == nil || m.skillsDialog.RequestID != msg.requestID {
		return m, nil
	}
	d := m.skillsDialog
	d.Loading = false
	d.Inventory = msg.inventory
	d.Err = msg.err
	if msg.err != nil {
		m.status = "Integration inventory failed: " + msg.err.Error()
	} else {
		m.status = fmt.Sprintf("Loaded %d integration entries for %s", len(msg.inventory.Entries), d.Target.Provider)
	}
	m.syncSkillsDialogSelection()
	return m, nil
}

func (m *Model) refreshSkillsDialog() tea.Cmd {
	if m.skillsDialog == nil || m.skillsDialog.Loading || m.skillsDialog.Busy {
		return nil
	}
	m.skillsInventorySeq++
	d := m.skillsDialog
	d.RequestID = m.skillsInventorySeq
	d.Loading = true
	d.Err = nil
	return m.loadSkillsInventoryCmd(d.RequestID)
}

func (m Model) integrationRows() []integrations.Entry {
	rows := []integrations.Entry{}
	if m.skillsDialog == nil {
		return rows
	}
	for _, entry := range m.skillsDialog.Inventory.Entries {
		if entry.Kind == m.skillsDialog.Kind {
			rows = append(rows, entry)
		}
	}
	return rows
}

func (m Model) selectedIntegration() (integrations.Entry, bool) {
	rows := m.integrationRows()
	if m.skillsDialog == nil || len(rows) == 0 {
		return integrations.Entry{}, false
	}
	return rows[min(max(m.skillsDialog.Selected, 0), len(rows)-1)], true
}

func (m *Model) syncSkillsDialogSelection() {
	if m.skillsDialog == nil {
		return
	}
	d := m.skillsDialog
	count := len(m.integrationRows())
	d.Selected = min(max(d.Selected, 0), max(count-1, 0))
	height := m.skillsDialogListHeight()
	d.Offset = min(max(d.Offset, 0), max(count-height, 0))
	if d.Selected < d.Offset {
		d.Offset = d.Selected
	}
	if d.Selected >= d.Offset+height {
		d.Offset = d.Selected - height + 1
	}
}

func (m Model) updateSkillsDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.skillsDialog
	if d == nil {
		return m, nil
	}
	if d.Busy {
		if msg.String() == "esc" {
			m.skillsDialog = nil
			m.status = "Integration action continues in the background"
		}
		return m, nil
	}
	if d.ShowingResult || d.DetailText != "" {
		switch msg.String() {
		case "esc", "enter", "v":
			d.ShowingResult = false
			d.DetailText = ""
		case "down", "j":
			d.PreviewOffset++
		case "up", "k":
			d.PreviewOffset = max(0, d.PreviewOffset-1)
		case "pgdown":
			d.PreviewOffset += 5
		case "pgup":
			d.PreviewOffset = max(0, d.PreviewOffset-5)
		}
		return m, nil
	}
	if d.Pending != nil {
		switch msg.String() {
		case "esc":
			d.Pending = nil
			d.Preview = ""
		case "enter":
			inv := *d.Pending
			d.Pending = nil
			d.Busy = true
			m.integrationDialogBusy = true
			d.Notice = "Applying integration change..."
			return m, m.applyIntegrationCmd(inv, true)
		case "down", "j":
			d.PreviewOffset++
		case "up", "k":
			d.PreviewOffset = max(0, d.PreviewOffset-1)
		case "pgdown":
			d.PreviewOffset += 5
		case "pgup":
			d.PreviewOffset = max(0, d.PreviewOffset-5)
		}
		return m, nil
	}
	if d.Editor != nil {
		return m.updateIntegrationEditor(msg)
	}
	switch msg.String() {
	case "esc", "q":
		m.skillsDialog = nil
		m.status = "Integrations closed"
		return m, nil
	case "r":
		return m, m.refreshSkillsDialog()
	case "v":
		if d.LastResult != nil {
			d.ShowingResult = true
			d.PreviewOffset = 0
		}
	case "tab":
		kinds := []string{"skill", "mcp", "plugin"}
		for i, kind := range kinds {
			if d.Kind == kind {
				d.Kind = kinds[(i+1)%len(kinds)]
				break
			}
		}
		d.Selected = 0
		d.Offset = 0
	case "p":
		if d.Loading {
			return m, nil
		}
		providers := []string{"codex", "claude_code", "opencode", "lcagent"}
		for i, p := range providers {
			if d.Target.Provider == p {
				d.Target.Provider = providers[(i+1)%len(providers)]
				break
			}
		}
		d.Selected = 0
		d.Offset = 0
		return m, m.refreshSkillsDialog()
	case "s":
		if d.Loading {
			return m, nil
		}
		if d.Target.Scope == "project" {
			d.Target.Scope = "user"
			d.Target.ProjectPath = ""
		} else if d.ProjectPath != "" {
			d.Target.Scope = "project"
			d.Target.ProjectPath = d.ProjectPath
		} else {
			d.Notice = "Select a project before choosing project scope"
			return m, nil
		}
		d.Selected = 0
		d.Offset = 0
		return m, m.refreshSkillsDialog()
	case "up", "k":
		d.Selected--
	case "down", "j":
		d.Selected++
	case "pgup", "ctrl+u":
		d.Selected -= m.skillsDialogListHeight()
	case "pgdown", "ctrl+d":
		d.Selected += m.skillsDialogListHeight()
	case "home":
		d.Selected = 0
	case "end":
		d.Selected = len(m.integrationRows()) - 1
	case "enter":
		if entry, ok := m.selectedIntegration(); ok {
			d.DetailText = fmt.Sprintf("%s\n\n%s\n\nSource: %s / %s\nPath: %s\nConfiguration: %s\nState: %s\n%s\nActions: %s", entry.Name, entry.Description, entry.Scope, entry.Source, entry.Path, entry.ConfigPath, entry.State, entry.Detail, strings.Join(entry.Actions, ", "))
		} else {
			d.DetailText = "No integration entries at this scope."
		}
		for _, warning := range d.Inventory.Warnings {
			d.DetailText += "\n\nWarning: " + warning
		}
		d.DetailText += "\n\n" + d.Inventory.Activation
		d.PreviewOffset = 0
	case "c":
		if entry, ok := m.selectedIntegration(); ok {
			path := entry.Path
			if path == "" {
				path = entry.ConfigPath
			}
			if err := clipboardTextWriter(path); err != nil {
				d.Notice = "Copy failed"
			} else {
				d.Notice = "Copied integration path"
			}
		}
	case "a":
		if d.Loading || d.Err != nil {
			return m, nil
		}
		return m, m.openIntegrationEditor()
	case " ", "d", "t":
		if d.Loading || d.Err != nil {
			return m, nil
		}
		entry, ok := m.selectedIntegration()
		if !ok {
			return m, nil
		}
		action := "set_enabled"
		if msg.String() == "d" {
			action = "remove"
		}
		if msg.String() == "t" {
			action = "check_mcp"
		}
		if !entry.Can(action) {
			d.Notice = "This action is unavailable for the selected source or scope"
			return m, nil
		}
		change := integrations.Change{Target: d.Target, Action: action, ExpectedRevision: d.Inventory.Revision, EntryID: entry.ID, EntryName: entry.Name}
		if action == "set_enabled" {
			enabled := !entry.Enabled
			change.Enabled = &enabled
		}
		m.prepareIntegrationChange(change)
	}
	m.syncSkillsDialogSelection()
	return m, nil
}

func (m *Model) prepareIntegrationChange(change integrations.Change) {
	inv, err := integrationInvocation(change)
	if err != nil {
		m.skillsDialog.Notice = err.Error()
		return
	}
	d := m.skillsDialog
	d.Pending = &inv
	d.Preview = integrations.Preview(change)
	d.PreviewOffset = 0
	d.Editor = nil
}

func (m *Model) openIntegrationEditor() tea.Cmd {
	d := m.skillsDialog
	editor := &integrationEditor{Kind: d.Kind}
	switch d.Kind {
	case "skill":
		editor.Labels = []string{"Local skill directory (or leave empty for Git)", "Git HTTPS URL (alternative to local directory)", "Git revision", "Subdirectory"}
	case "mcp":
		editor.Labels = []string{"Server name", "Remote URL (or leave empty for local command)", "Command as JSON array, e.g. [\"uvx\",\"server\"]", "Environment variable names, comma separated", "Header environment references as JSON object"}
	case "plugin":
		editor.Labels = []string{"Plugin name@marketplace"}
	}
	for _, label := range editor.Labels {
		field := textinput.New()
		field.Placeholder = label
		field.CharLimit = 2000
		field.Width = 70
		editor.Fields = append(editor.Fields, field)
	}
	editor.Fields[0].Focus()
	d.Editor = editor
	return textinput.Blink
}

func (m Model) updateIntegrationEditor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.skillsDialog
	e := d.Editor
	switch msg.String() {
	case "esc":
		d.Editor = nil
		return m, nil
	case "tab", "shift+tab":
		e.Fields[e.Focus].Blur()
		delta := 1
		if msg.String() == "shift+tab" {
			delta = -1
		}
		e.Focus = (e.Focus + delta + len(e.Fields)) % len(e.Fields)
		return m, e.Fields[e.Focus].Focus()
	case "enter":
		get := func(i int) string { return strings.TrimSpace(e.Fields[i].Value()) }
		change := integrations.Change{Target: d.Target, ExpectedRevision: d.Inventory.Revision}
		switch e.Kind {
		case "skill":
			change.Action = "install_skill"
			change.SourcePath = get(0)
			change.GitURL = get(1)
			change.GitRef = get(2)
			change.Subdirectory = get(3)
		case "plugin":
			change.Action = "install_plugin"
			change.Plugin = get(0)
		case "mcp":
			change.Action = "add_mcp"
			change.MCP = &integrations.MCPConfig{Name: get(0), URL: get(1)}
			if get(2) != "" {
				if err := json.Unmarshal([]byte(get(2)), &change.MCP.Command); err != nil {
					e.Err = "Command must be a JSON array of strings"
					return m, nil
				}
			}
			for _, name := range strings.Split(get(3), ",") {
				if name = strings.TrimSpace(name); name != "" {
					change.MCP.EnvVars = append(change.MCP.EnvVars, name)
				}
			}
			if get(4) != "" {
				if err := json.Unmarshal([]byte(get(4)), &change.MCP.HeaderEnv); err != nil {
					e.Err = "Header references must be a JSON object mapping headers to environment names"
					return m, nil
				}
			}
		}
		if _, err := integrations.ValidateChange(change); err != nil {
			e.Err = err.Error()
			return m, nil
		}
		m.prepareIntegrationChange(change)
		return m, nil
	}
	var cmd tea.Cmd
	e.Fields[e.Focus], cmd = e.Fields[e.Focus].Update(msg)
	return m, cmd
}

func (m Model) renderSkillsDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSkillsDialog(bodyW, bodyH)
	return overlayBlock(body, panel, bodyW, bodyH, max(0, (bodyW-lipgloss.Width(panel))/2), max(0, (bodyH-lipgloss.Height(panel))/3))
}

func (m Model) renderSkillsDialog(bodyW, bodyH int) string {
	panelWidth := max(1, min(bodyW-2, min(max(64, bodyW-8), 112)))
	inner := max(1, panelWidth-2)
	return renderDialogPanel(panelWidth, inner, m.renderSkillsDialogContent(inner))
}

func (m Model) renderSkillsDialogContent(width int) string {
	d := m.skillsDialog
	if d == nil {
		return ""
	}
	lines := []string{commandPaletteTitleStyle.Render("Agent Integrations"), commandPaletteHintStyle.Render(skillsFitLine(fmt.Sprintf("%s  |  %s scope  |  %s", d.Target.Provider, d.Target.Scope, d.Kind), width))}
	tabs := []string{}
	for _, tab := range []struct{ kind, label string }{{"skill", "Skills"}, {"mcp", "MCP"}, {"plugin", "Plugins"}} {
		style := detailMutedStyle
		if tab.kind == d.Kind {
			style = detailLabelStyle.Underline(true)
		}
		tabs = append(tabs, style.Render(tab.label))
	}
	lines = append(lines, skillsFitLine(strings.Join(tabs, "   "), width))
	if d.Target.ProjectPath != "" {
		lines = append(lines, skillsFitLine(d.Target.ProjectPath, width))
	}
	if d.Pending != nil || d.ShowingResult || d.DetailText != "" {
		content := d.Preview
		footer := strings.Join(skillsActionRows(width, skillsAction("↑↓", "scroll", uistyle.DialogActionNavigate), skillsAction("Enter", "confirm", uistyle.DialogActionPrimary), skillsAction("Esc", "cancel", uistyle.DialogActionCancel)), "\n")
		if d.ShowingResult && d.LastResult != nil {
			content = d.Notice + "\n\n" + d.LastResult.Activation
			for _, path := range d.LastResult.ChangedPaths {
				content += "\nChanged: " + path
			}
			for _, path := range d.LastResult.BackupPaths {
				content += "\nRecovery: " + path
			}
			if check := d.LastResult.Check; check != nil {
				content += "\nTools: " + strings.Join(check.Tools, ", ")
			}
			footer = strings.Join(skillsActionRows(width, skillsAction("↑↓", "scroll", uistyle.DialogActionNavigate), skillsAction("Enter / Esc", "return", uistyle.DialogActionCancel)), "\n")
		}
		if d.DetailText != "" {
			content = d.DetailText
			footer = strings.Join(skillsActionRows(width, skillsAction("↑↓", "scroll", uistyle.DialogActionNavigate), skillsAction("Enter / Esc", "return", uistyle.DialogActionCancel)), "\n")
		}
		parts := strings.Split(lipgloss.NewStyle().Width(width).Render(content), "\n")
		height := max(3, m.height-12)
		start := min(d.PreviewOffset, max(0, len(parts)-height))
		end := min(len(parts), start+height)
		lines = append(lines, "")
		lines = append(lines, parts[start:end]...)
		lines = append(lines, "", footer)
		return strings.Join(lines, "\n")
	}
	if d.Editor != nil {
		e := d.Editor
		lines = append(lines, "", detailMutedStyle.Render("Use environment-variable names for credentials."))
		visible := max(1, (m.height-13)/2)
		start := max(0, e.Focus-visible+1)
		for i := start; i < min(len(e.Labels), start+visible); i++ {
			label := e.Labels[i]
			field := e.Fields[i]
			field.Width = max(1, width-2)
			lines = append(lines, skillsFitLine(label, width), field.View())
		}
		if e.Err != "" {
			lines = append(lines, detailDangerStyle.Render(skillsFitLine(e.Err, width)))
		}
		lines = append(lines, "", strings.Join(skillsActionRows(width, skillsAction("Tab", "field", uistyle.DialogActionNavigate), skillsAction("Enter", "review", uistyle.DialogActionPrimary), skillsAction("Esc", "cancel", uistyle.DialogActionCancel)), "\n"))
		return strings.Join(lines, "\n")
	}
	if d.Busy {
		lines = append(lines, "", detailMutedStyle.Render("Applying change"+skillsSpinnerDots(m.spinnerFrame)), "Esc hides this panel; the action continues.")
		return strings.Join(lines, "\n")
	}
	if d.Loading {
		lines = append(lines, "", detailMutedStyle.Render("Loading"+skillsSpinnerDots(m.spinnerFrame)))
		return strings.Join(lines, "\n")
	}
	if d.Err != nil {
		lines = append(lines, "", detailDangerStyle.Render(skillsFitLine(d.Err.Error(), width)))
	} else {
		rows := m.integrationRows()
		lines = append(lines, "")
		if len(rows) == 0 {
			lines = append(lines, detailMutedStyle.Render("No entries found at this scope."))
		}

		lines = append(lines, skillsTableRow(width, []string{"Name", "State", "Scope", "Source"}, false, true))
		lines = append(lines, detailMutedStyle.Render(strings.Repeat("─", width)))
		for i := d.Offset; i < min(len(rows), d.Offset+m.skillsDialogListHeight()); i++ {
			entry := rows[i]
			lines = append(lines, skillsTableRow(width, []string{entry.Name, entry.State, entry.Scope, entry.Source}, i == d.Selected, false))
		}
		if len(rows) > 0 {
			lines = append(lines, detailMutedStyle.Render(fmt.Sprintf("%d–%d of %d", d.Offset+1, min(len(rows), d.Offset+m.skillsDialogListHeight()), len(rows))))
		}
		if entry, ok := m.selectedIntegration(); ok {
			lines = append(lines, "", skillsDialogField("Name", entry.Name, width))
			path := entry.Path
			if path == "" {
				path = entry.ConfigPath
			}
			lines = append(lines, skillsDialogField("Path", path, width))
			if entry.Description != "" {
				lines = append(lines, skillsDialogField("About", entry.Description, width))
			}
			lines = append(lines, skillsDialogField("State", entry.Detail, width), skillsDialogField("Actions", strings.Join(entry.Actions, ", "), width))
		}
		if len(d.Inventory.Warnings) > 0 {
			lines = append(lines, detailDangerStyle.Render(skillsFitLine(d.Inventory.Warnings[0], width)))
		}
	}
	if d.Notice != "" {
		lines = append(lines, "", skillsFitLine(d.Notice, width))
	}

	lines = append(lines, "", commandPaletteHintStyle.Render(skillsFitLine("Disk configuration; reconnect idle sessions after changes.", width)))
	lines = append(lines, skillsActionRows(width,
		skillsAction("↑↓", "select", uistyle.DialogActionNavigate),
		skillsAction("Tab", "kind", uistyle.DialogActionNavigate),
		skillsAction("p", "agent", uistyle.DialogActionNavigate),
		skillsAction("s", "scope", uistyle.DialogActionNavigate),
		skillsAction("a", "add", uistyle.DialogActionPrimary),
		skillsAction("Space", "toggle", uistyle.DialogActionSecondary),
		skillsAction("Enter", "details", uistyle.DialogActionNavigate),
		skillsAction("t", "test", uistyle.DialogActionSecondary),
		skillsAction("d", "remove", uistyle.DialogActionCancel),
		skillsAction("v", "result", uistyle.DialogActionNavigate),
		skillsAction("c", "copy", uistyle.DialogActionNavigate),
		skillsAction("r", "refresh", uistyle.DialogActionNavigate),
		skillsAction("Esc", "close", uistyle.DialogActionCancel),
	)...)
	return strings.Join(lines, "\n")
}

func skillsDialogField(label, value string, width int) string {
	if value == "" {
		value = "-"
	}
	return detailLabelStyle.Render(label+": ") + skillsFitLine(value, max(0, width-lipgloss.Width(label)-2))
}
func (m Model) skillsDialogListHeight() int {
	if m.height <= 0 {
		return 8
	}
	return min(10, max(2, m.height-27))
}
func skillsFitLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(text, width, "...")
}
func skillsSpinnerDots(frame int) string { return strings.Repeat(".", frame%4) }

// skillsTableRow uses terminal cell widths so long and wide-character names
// cannot push status or provenance out of their columns.
func skillsTableRow(width int, values []string, selected, header bool) string {
	widths := []int{max(1, width-37), 10, 9, 7}
	if width < 48 {
		widths = []int{max(1, width-15), 10}
	}
	cells := make([]string, len(widths))
	for i, w := range widths {
		style := detailValueStyle
		if i > 1 {
			style = detailMutedStyle
		}
		if i == 1 {
			switch values[i] {
			case "invalid":
				style = detailDangerStyle
			case "disabled", "cached":
				style = detailWarningStyle
			case "configured", "discovered", "bundled":
				style = detailLabelStyle
			}
		}
		if header {
			style = detailLabelStyle
		}
		if selected {
			style = style.Background(lipgloss.Color("238")).Bold(true)
		}
		cells[i] = style.Width(w).Render(skillsFitLine(values[i], w))
	}
	marker := "  "
	if selected {
		marker = detailLabelStyle.Render("› ")
	}
	separator := detailMutedStyle.Render(" │ ")
	return skillsFitLine(marker+strings.Join(cells, separator), width)
}

func skillsAction(key, label string, tone uistyle.DialogActionTone) string {
	return uistyle.RenderDialogActionTone(key, label, tone, dialogPanelFillStyle)
}

func skillsActionRows(width int, actions ...string) []string {
	lines := []string{}
	line := ""
	for _, action := range actions {
		if line != "" && lipgloss.Width(line)+2+lipgloss.Width(action) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += "  "
		}
		line += skillsFitLine(action, width)
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
