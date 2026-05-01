package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) executeControlInvocation(inv control.Invocation) (tea.Model, tea.Cmd) {
	normalized, err := control.ValidateInvocation(inv)
	if err != nil {
		m.status = "Control request invalid: " + err.Error()
		return m, nil
	}

	switch normalized.Capability {
	case control.CapabilityEngineerSendPrompt:
		var input control.EngineerSendPromptInput
		if err := json.Unmarshal(normalized.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return m, nil
		}
		return m.executeEngineerSendPromptControl(input)
	default:
		m.status = "Control request unsupported: " + string(normalized.Capability)
		return m, nil
	}
}

func (m Model) executeEngineerSendPromptControl(input control.EngineerSendPromptInput) (tea.Model, tea.Cmd) {
	project, err := m.resolveControlProject(input)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return m, nil
	}
	provider, err := m.resolveControlEngineerProvider(input.Provider, project)
	if err != nil {
		m.status = "Control request failed: " + err.Error()
		return m, nil
	}
	return m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{
		forceNew: input.SessionMode == control.SessionModeNew,
		prompt:   input.Prompt,
		reveal:   input.Reveal,
	})
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
