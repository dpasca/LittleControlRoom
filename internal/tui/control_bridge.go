package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) executeBossControlInvocation(msg bossui.ControlInvocationConfirmedMsg) (tea.Model, tea.Cmd) {
	inv, err := control.ValidateInvocation(msg.Invocation)
	if err != nil {
		status := "Control request invalid: " + err.Error()
		m.status = status
		return m, bossControlResultCmd(msg.Invocation, status, err)
	}
	outcome := m.executeControlInvocationWithOutcome(inv)
	m = outcome.model
	if outcome.cmd == nil {
		return m, bossControlResultCmd(inv, m.status, outcome.err)
	}
	return m, bossControlExecutionCmd(inv, outcome.cmd)
}

func (m Model) executeControlInvocation(inv control.Invocation) (tea.Model, tea.Cmd) {
	outcome := m.executeControlInvocationWithOutcome(inv)
	return outcome.model, outcome.cmd
}

type controlInvocationOutcome struct {
	model Model
	cmd   tea.Cmd
	err   error
}

func (m Model) executeControlInvocationWithOutcome(inv control.Invocation) controlInvocationOutcome {
	normalized, err := control.ValidateInvocation(inv)
	if err != nil {
		m.status = "Control request invalid: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}

	switch normalized.Capability {
	case control.CapabilityEngineerSendPrompt:
		var input control.EngineerSendPromptInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return m.executeEngineerSendPromptControlWithOutcome(input)
	default:
		err := fmt.Errorf("unsupported capability: %s", normalized.Capability)
		m.status = "Control request unsupported: " + string(normalized.Capability)
		return controlInvocationOutcome{model: m, err: err}
	}
}

func bossControlExecutionCmd(inv control.Invocation, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		msg := cmd()
		status := "Control request sent to the engineer session."
		var err error
		if opened, ok := msg.(codexSessionOpenedMsg); ok {
			status = strings.TrimSpace(opened.status)
			err = opened.err
		}
		result := bossui.ControlInvocationResultMsg{
			Invocation: copyControlInvocationForBoss(inv),
			Status:     status,
			Err:        err,
		}
		if msg == nil {
			return result
		}
		return tea.BatchMsg{
			func() tea.Msg { return msg },
			func() tea.Msg { return result },
		}
	}
}

func bossControlResultCmd(inv control.Invocation, status string, err error) tea.Cmd {
	return func() tea.Msg {
		return bossui.ControlInvocationResultMsg{
			Invocation: copyControlInvocationForBoss(inv),
			Status:     strings.TrimSpace(status),
			Err:        err,
		}
	}
}

func copyControlInvocationForBoss(inv control.Invocation) control.Invocation {
	out := inv
	if inv.Args != nil {
		out.Args = append([]byte(nil), inv.Args...)
	}
	return out
}

func (m Model) executeEngineerSendPromptControl(input control.EngineerSendPromptInput) (tea.Model, tea.Cmd) {
	outcome := m.executeEngineerSendPromptControlWithOutcome(input)
	return outcome.model, outcome.cmd
}

func (m Model) executeEngineerSendPromptControlWithOutcome(input control.EngineerSendPromptInput) controlInvocationOutcome {
	project, err := m.resolveControlProject(input)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	provider, err := m.resolveControlEngineerProvider(input.Provider, project)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if !project.PresentOnDisk {
		err := fmt.Errorf("%s launch requires a folder present on disk", provider.Label())
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	if block, blocked := m.embeddedLaunchBlock(project, provider); blocked {
		err := errors.New(block.Message)
		m.status = block.Message
		return controlInvocationOutcome{model: m, err: err}
	}

	updated, cmd := m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{
		forceNew: input.SessionMode == control.SessionModeNew,
		prompt:   input.Prompt,
		reveal:   input.Reveal,
	})
	m = normalizeUpdateModel(updated)
	if cmd == nil {
		status := strings.TrimSpace(m.status)
		if status == "" {
			status = "engineer session launch did not start"
		}
		err := errors.New(status)
		return controlInvocationOutcome{model: m, err: err}
	}
	return controlInvocationOutcome{model: m, cmd: cmd}
}

func (m Model) resolveControlProject(input control.EngineerSendPromptInput) (model.ProjectSummary, error) {
	if path := normalizeProjectPath(input.ProjectPath); path != "" {
		if project, ok := m.projectSummaryByPathAllProjects(path); ok {
			return project, nil
		}
		return model.ProjectSummary{}, fmt.Errorf("project is not loaded: %s", path)
	}

	name := strings.TrimSpace(input.ProjectName)
	if name == "" {
		return model.ProjectSummary{}, errors.New("project_path or project_name required")
	}
	var (
		matched model.ProjectSummary
		found   bool
	)
	for _, project := range append(append([]model.ProjectSummary(nil), m.allProjects...), m.projects...) {
		if !controlProjectNameMatches(project, name) {
			continue
		}
		if found && normalizeProjectPath(matched.Path) != normalizeProjectPath(project.Path) {
			return model.ProjectSummary{}, fmt.Errorf("project name is ambiguous: %s", name)
		}
		matched = project
		found = true
	}
	if !found {
		return model.ProjectSummary{}, fmt.Errorf("project is not loaded: %s", name)
	}
	return matched, nil
}

func controlProjectNameMatches(project model.ProjectSummary, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	candidates := []string{
		strings.TrimSpace(project.Name),
		projectNameForPicker(project, project.Path),
		strings.TrimSpace(filepath.Base(project.Path)),
	}
	for _, candidate := range candidates {
		if strings.EqualFold(strings.TrimSpace(candidate), name) {
			return true
		}
	}
	return false
}

func (m Model) resolveControlEngineerProvider(provider control.Provider, project model.ProjectSummary) (codexapp.Provider, error) {
	switch provider.Normalized() {
	case control.ProviderAuto:
		resolved := m.preferredEmbeddedProviderForProject(project)
		if resolved.Normalized() == codexapp.ProviderClaudeCode {
			return "", errors.New("Claude Code is present in the protocol but disabled for control execution")
		}
		return resolved, nil
	case control.ProviderCodex:
		return codexapp.ProviderCodex, nil
	case control.ProviderOpenCode:
		return codexapp.ProviderOpenCode, nil
	case control.ProviderClaudeCode:
		return "", errors.New("Claude Code is present in the protocol but disabled for control execution")
	default:
		return "", fmt.Errorf("unsupported engineer provider: %s", provider)
	}
}
