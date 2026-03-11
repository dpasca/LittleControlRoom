package codexcli

import (
	"fmt"
	"os/exec"
	"strings"
)

type Preset string

const (
	PresetYolo     Preset = "yolo"
	PresetFullAuto Preset = "full-auto"
	PresetSafe     Preset = "safe"
)

func DefaultPreset() Preset {
	return PresetYolo
}

func ParsePreset(raw string) (Preset, error) {
	switch normalizePreset(raw) {
	case string(PresetYolo):
		return PresetYolo, nil
	case string(PresetFullAuto):
		return PresetFullAuto, nil
	case string(PresetSafe):
		return PresetSafe, nil
	default:
		return "", fmt.Errorf("codex launch preset must be one of: yolo, full-auto, safe")
	}
}

func normalizePreset(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	return trimmed
}

func (p Preset) DisplayName() string {
	switch p {
	case PresetFullAuto:
		return "Full Auto"
	case PresetSafe:
		return "Safe"
	default:
		return "YOLO"
	}
}

func (p Preset) StartNotice() string {
	switch p {
	case PresetFullAuto:
		return "Full Auto mode; expect approval prompts and extra interaction."
	case PresetSafe:
		return "Safe mode; expect frequent approval prompts and a lot of manual interaction."
	default:
		return "YOLO mode."
	}
}

func (p Preset) CLIArgs() []string {
	switch p {
	case PresetFullAuto:
		return []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}
	case PresetSafe:
		return []string{"--sandbox", "read-only", "--ask-for-approval", "on-request"}
	default:
		// Official long-form equivalent of the supported --yolo alias.
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	}
}

type LaunchKind string

const (
	LaunchNew    LaunchKind = "new"
	LaunchResume LaunchKind = "resume"
)

type LaunchPlan struct {
	Kind        LaunchKind
	Preset      Preset
	ProjectPath string
	SessionID   string
	Prompt      string
}

func BuildLaunchPlan(projectPath, sessionID, prompt string, forceNew bool, preset Preset) (LaunchPlan, error) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return LaunchPlan{}, fmt.Errorf("project path required")
	}
	if preset == "" {
		preset = DefaultPreset()
	}
	if _, err := ParsePreset(string(preset)); err != nil {
		return LaunchPlan{}, err
	}

	plan := LaunchPlan{
		Kind:        LaunchNew,
		Preset:      preset,
		ProjectPath: projectPath,
		Prompt:      strings.TrimSpace(prompt),
	}
	if !forceNew {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID != "" {
			plan.Kind = LaunchResume
			plan.SessionID = sessionID
		}
	}

	return plan, nil
}

func (p LaunchPlan) StartStatus() string {
	if p.Kind == LaunchResume && p.SessionID != "" {
		return fmt.Sprintf("Resuming Codex session %s. %s", shortID(p.SessionID), p.Preset.StartNotice())
	}
	return fmt.Sprintf("Starting a new Codex session. %s", p.Preset.StartNotice())
}

func (p LaunchPlan) EndStatus(err error) string {
	if err != nil {
		return fmt.Sprintf("Codex exited with error: %v", err)
	}
	if p.Kind == LaunchResume && p.SessionID != "" {
		return fmt.Sprintf("Codex session %s closed", shortID(p.SessionID))
	}
	return "Codex session closed"
}

func (p LaunchPlan) Command() *exec.Cmd {
	args := make([]string, 0, 10)
	switch p.Kind {
	case LaunchResume:
		args = append(args, "resume")
		args = append(args, p.Preset.CLIArgs()...)
		args = append(args, "-C", p.ProjectPath, p.SessionID)
	default:
		args = append(args, p.Preset.CLIArgs()...)
		args = append(args, "-C", p.ProjectPath)
	}
	if p.Prompt != "" {
		args = append(args, p.Prompt)
	}

	cmd := exec.Command("codex", args...)
	cmd.Dir = p.ProjectPath
	return cmd
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
