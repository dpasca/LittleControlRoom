package uisurface

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
)

// PanelSection is the shared semantic unit used by compact mobile sheets. It
// intentionally reuses DetailBlock so terminal and web surfaces can share the
// same labels, values, tones, and ordering while rendering their own chrome.
type PanelSection struct {
	ID      string        `json:"id"`
	Title   string        `json:"title"`
	Summary string        `json:"summary,omitempty"`
	Blocks  []DetailBlock `json:"blocks"`
}

type SessionSidebarSurface struct {
	Project   ProjectItem         `json:"project"`
	Session   EngineerSessionItem `json:"session"`
	UpdatedAt time.Time           `json:"updated_at"`
	Sections  []PanelSection      `json:"sections"`
}

// BuildSessionSidebar turns the live embedded-session snapshot into the same
// conceptual groups used by the TUI sidebar. Runtime and diff sections are
// appended by the host because they require live OS and git reads.
func BuildSessionSidebar(snapshot codexapp.Snapshot, project ProjectItem, now time.Time) SessionSidebarSurface {
	if now.IsZero() {
		now = time.Now()
	}
	item := BuildLiveEngineerSession(snapshot, now)
	base := BuildLiveEngineerSessionDetail(snapshot, now)

	sessionBlocks := make([]DetailBlock, 0, len(base.Instruments)+8)
	for _, field := range base.Instruments {
		sessionBlocks = append(sessionBlocks, DetailBlock{Kind: DetailBlockField, Label: field.Label, Text: field.Text, Tone: field.Tone})
	}
	if cwd := strings.TrimSpace(snapshot.CurrentCWD); cwd != "" {
		sessionBlocks = append(sessionBlocks, DetailBlock{Kind: DetailBlockWrappedField, Label: "Working directory", Text: cwd, Tone: ToneValue})
	}
	if tier := strings.TrimSpace(snapshot.ServiceTier); tier != "" {
		sessionBlocks = append(sessionBlocks, DetailBlock{Kind: DetailBlockField, Label: "Service tier", Text: tier, Tone: ToneValue})
	}
	if snapshot.TokenUsage != nil {
		usage := snapshot.TokenUsage
		if usage.ModelContextWindow > 0 {
			sessionBlocks = append(sessionBlocks, DetailBlock{Kind: DetailBlockField, Label: "Context", Text: fmt.Sprintf("%d%% left · %s / %s tokens", usage.ContextLeftPercent(), compactCount(usage.EstimatedContextTokens()), compactCount(usage.ModelContextWindow)), Tone: contextTone(usage.ContextLeftPercent())})
		}
		if usage.Total.TotalTokens > 0 {
			sessionBlocks = append(sessionBlocks, DetailBlock{Kind: DetailBlockField, Label: "Total usage", Text: compactCount(usage.Total.TotalTokens) + " tokens", Tone: ToneMuted})
		}
	}
	for _, window := range snapshot.UsageWindows {
		label := strings.TrimSpace(window.Window)
		if label == "" {
			label = strings.TrimSpace(window.Limit)
		}
		if label == "" {
			label = "Usage window"
		}
		text := fmt.Sprintf("%d%% left", window.LeftPercent)
		if !window.ResetsAt.IsZero() {
			text += " · resets " + window.ResetsAt.Format("Jan 2 15:04")
		}
		sessionBlocks = append(sessionBlocks, DetailBlock{Kind: DetailBlockField, Label: label, Text: text, Tone: contextTone(window.LeftPercent)})
	}

	sections := []PanelSection{{
		ID:      "session",
		Title:   "Session",
		Summary: item.Status.Label,
		Blocks:  sessionBlocks,
	}}
	sections = append(sections, buildQualitySection(snapshot), buildVisionSection(snapshot), buildBrowserSection(snapshot), buildMCPSection(snapshot))

	summaryBlocks := []DetailBlock{{Kind: DetailBlockWrappedField, Label: "Project summary", Text: project.Summary, Tone: project.Assessment.Tone}}
	if notice := strings.TrimSpace(snapshot.LastSystemNotice); notice != "" {
		summaryBlocks = append(summaryBlocks, DetailBlock{Kind: DetailBlockWrappedField, Label: "System notice", Text: notice, Tone: ToneInfo})
	}
	if lastError := strings.TrimSpace(snapshot.LastError); lastError != "" {
		summaryBlocks = append(summaryBlocks, DetailBlock{Kind: DetailBlockWrappedField, Label: "Last error", Text: lastError, Tone: ToneDanger})
	}
	sections = append(sections, PanelSection{ID: "summary", Title: "Summary", Summary: project.Assessment.Label, Blocks: summaryBlocks})

	return SessionSidebarSurface{
		Project:   project,
		Session:   item,
		UpdatedAt: now,
		Sections:  sections,
	}
}

func buildQualitySection(snapshot codexapp.Snapshot) PanelSection {
	blocks := make([]DetailBlock, 0, len(snapshot.QualityPlanPhaseItems)+4)
	if snapshot.QualityPlanUpdates == 0 && snapshot.QualityPlanPhases == 0 {
		blocks = append(blocks, DetailBlock{Kind: DetailBlockText, Text: "No quality plan telemetry yet.", Tone: ToneMuted})
	} else {
		blocks = append(blocks, DetailBlock{Kind: DetailBlockFieldGroup, Fields: []DetailFieldValue{
			FieldValue("Phases", fmt.Sprintf("%d", snapshot.QualityPlanPhases), ToneValue),
			FieldValue("Verified", fmt.Sprintf("%d", snapshot.QualityPlanVerified), TonePositive),
			FieldValue("Needs repair", fmt.Sprintf("%d", snapshot.QualityPlanNeedsRepair), qualityRepairTone(snapshot.QualityPlanNeedsRepair)),
		}})
		if summary := strings.TrimSpace(snapshot.QualityPlanLastSummary); summary != "" {
			blocks = append(blocks, DetailBlock{Kind: DetailBlockWrappedField, Label: "Latest", Text: summary, Tone: ToneValue})
		}
		for _, phase := range snapshot.QualityPlanPhaseItems {
			text := strings.TrimSpace(phase.Name)
			if text == "" {
				text = "Phase"
			}
			if status := strings.TrimSpace(phase.Status); status != "" {
				text += " · " + status
			}
			if phase.EvidenceCount > 0 {
				text += fmt.Sprintf(" · %d evidence", phase.EvidenceCount)
			}
			if notes := strings.TrimSpace(phase.Notes); notes != "" {
				text += " — " + notes
			}
			blocks = append(blocks, DetailBlock{Kind: DetailBlockBullet, Text: text, Tone: qualityPhaseTone(phase.Status)})
		}
	}
	return PanelSection{ID: "quality", Title: "Quality", Summary: qualitySummary(snapshot), Blocks: blocks}
}

func buildVisionSection(snapshot codexapp.Snapshot) PanelSection {
	blocks := []DetailBlock{}
	modelLabel := strings.TrimSpace(strings.Join([]string{snapshot.VisionModelProvider, snapshot.VisionModel}, " "))
	if modelLabel != "" {
		blocks = append(blocks, DetailBlock{Kind: DetailBlockField, Label: "Vision model", Text: modelLabel, Tone: ToneValue})
	}
	if snapshot.ImageAnalysisActive || snapshot.ImageAnalyses > 0 || snapshot.ImageAnalysisFailures > 0 {
		blocks = append(blocks, DetailBlock{Kind: DetailBlockFieldGroup, Fields: []DetailFieldValue{
			FieldValue("State", visionState(snapshot), visionTone(snapshot)),
			FieldValue("Analyses", fmt.Sprintf("%d", snapshot.ImageAnalyses), ToneValue),
			FieldValue("Failures", fmt.Sprintf("%d", snapshot.ImageAnalysisFailures), failureTone(snapshot.ImageAnalysisFailures)),
		}})
		if summary := strings.TrimSpace(snapshot.ImageAnalysisLastSummary); summary != "" {
			blocks = append(blocks, DetailBlock{Kind: DetailBlockWrappedField, Label: "Latest", Text: summary, Tone: ToneValue})
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, DetailBlock{Kind: DetailBlockText, Text: "No vision activity in this session.", Tone: ToneMuted})
	}
	return PanelSection{ID: "vision", Title: "Vision", Summary: visionState(snapshot), Blocks: blocks}
}

func buildBrowserSection(snapshot codexapp.Snapshot) PanelSection {
	activity := snapshot.BrowserActivity.Normalize()
	blocks := []DetailBlock{{Kind: DetailBlockWrappedField, Label: "Activity", Text: activity.Summary(), Tone: browserTone(string(activity.State))}}
	if source := activity.SourceLabel(); source != "" {
		blocks = append(blocks, DetailBlock{Kind: DetailBlockField, Label: "Source", Text: source, Tone: ToneValue})
	}
	if pageURL := strings.TrimSpace(snapshot.CurrentBrowserPageURL); pageURL != "" {
		label := "Current page"
		if snapshot.CurrentBrowserPageStale {
			label = "Last page"
		}
		blocks = append(blocks, DetailBlock{Kind: DetailBlockWrappedField, Label: label, Text: pageURL, Tone: ToneInfo})
	}
	if attention := strings.TrimSpace(activity.AttentionMessage); attention != "" {
		blocks = append(blocks, DetailBlock{Kind: DetailBlockWrappedField, Label: "Input needed", Text: attention, Tone: ToneWarning})
	}
	return PanelSection{ID: "browser", Title: "Browser", Summary: activity.Summary(), Blocks: blocks}
}

func buildMCPSection(snapshot codexapp.Snapshot) PanelSection {
	usage := append([]codexapp.MCPUsageSnapshot(nil), snapshot.MCPUsage...)
	sort.SliceStable(usage, func(i, j int) bool {
		if usage[i].ToolCalls != usage[j].ToolCalls {
			return usage[i].ToolCalls > usage[j].ToolCalls
		}
		return strings.ToLower(usage[i].ServerName) < strings.ToLower(usage[j].ServerName)
	})
	blocks := make([]DetailBlock, 0, len(usage))
	total := 0
	for _, server := range usage {
		total += server.ToolCalls
		name := strings.TrimSpace(server.ServerName)
		if name == "" {
			name = "MCP server"
		}
		text := fmt.Sprintf("%d call(s)", server.ToolCalls)
		if tool := strings.TrimSpace(server.LastTool); tool != "" {
			text += " · last " + tool
		}
		blocks = append(blocks, DetailBlock{Kind: DetailBlockField, Label: name, Text: text, Tone: ToneValue})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, DetailBlock{Kind: DetailBlockText, Text: "No MCP tools used yet.", Tone: ToneMuted})
	}
	return PanelSection{ID: "mcps", Title: "Used MCPs", Summary: fmt.Sprintf("%d call(s)", total), Blocks: blocks}
}

type TodoSurface struct {
	Project      ProjectItem `json:"project"`
	ScopeProject ProjectItem `json:"scope_project"`
	ScopeLabel   string      `json:"scope_label"`
	WriteEnabled bool        `json:"write_enabled"`
	OpenCount    int         `json:"open_count"`
	DoneCount    int         `json:"done_count"`
	Todos        []TodoItem  `json:"todos"`
}

type TodoItem struct {
	ID              int64     `json:"id"`
	Text            string    `json:"text"`
	Done            bool      `json:"done"`
	Position        int       `json:"position"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
	AttachmentCount int       `json:"attachment_count,omitempty"`
	WorkState       string    `json:"work_state,omitempty"`
	WorkProvider    string    `json:"work_provider,omitempty"`
	WorkProjectPath string    `json:"work_project_path,omitempty"`
}

func BuildTodoSurface(project, scopeProject ProjectItem, todos []model.TodoItem, writeEnabled bool) TodoSurface {
	items := make([]TodoItem, 0, len(todos))
	openCount := 0
	doneCount := 0
	for _, todo := range todos {
		if todo.Done {
			doneCount++
		} else {
			openCount++
		}
		items = append(items, TodoItem{
			ID:              todo.ID,
			Text:            strings.TrimSpace(todo.Text),
			Done:            todo.Done,
			Position:        todo.Position,
			UpdatedAt:       todo.UpdatedAt,
			AttachmentCount: len(todo.Attachments),
			WorkState:       string(model.NormalizeTodoWorkState(todo.WorkState)),
			WorkProvider:    string(model.NormalizeSessionSource(todo.WorkProvider)),
			WorkProjectPath: strings.TrimSpace(todo.WorkProjectPath),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Done != items[j].Done {
			return !items[i].Done
		}
		if items[i].Position != items[j].Position {
			return items[i].Position < items[j].Position
		}
		return items[i].ID < items[j].ID
	})
	scopeLabel := "This project"
	if strings.TrimSpace(project.Path) != strings.TrimSpace(scopeProject.Path) {
		scopeLabel = "Repository TODOs"
	}
	return TodoSurface{
		Project:      project,
		ScopeProject: scopeProject,
		ScopeLabel:   scopeLabel,
		WriteEnabled: writeEnabled,
		OpenCount:    openCount,
		DoneCount:    doneCount,
		Todos:        items,
	}
}

type RuntimeSurface struct {
	Project      ProjectItem          `json:"project"`
	RunCommand   string               `json:"run_command,omitempty"`
	WriteEnabled bool                 `json:"write_enabled"`
	UpdatedAt    time.Time            `json:"updated_at"`
	Processes    []RuntimeProcessItem `json:"processes"`
	Warnings     []string             `json:"warnings,omitempty"`
}

type RuntimeProcessItem struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Command       string    `json:"command"`
	CWD           string    `json:"cwd,omitempty"`
	PID           int       `json:"pid,omitempty"`
	Managed       bool      `json:"managed"`
	Running       bool      `json:"running"`
	Status        Status    `json:"status"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	ExitedAt      time.Time `json:"exited_at,omitempty"`
	ExitCode      *int      `json:"exit_code,omitempty"`
	Ports         []int     `json:"ports,omitempty"`
	ConflictPorts []int     `json:"conflict_ports,omitempty"`
	URLs          []string  `json:"urls,omitempty"`
	RecentOutput  []string  `json:"recent_output,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	CanStop       bool      `json:"can_stop"`
	CanRestart    bool      `json:"can_restart"`
}

func BuildRuntimeSurface(project ProjectItem, runCommand string, managed []projectrun.Snapshot, report procinspect.ProjectReport, writeEnabled bool, now time.Time) RuntimeSurface {
	if now.IsZero() {
		now = time.Now()
	}
	processes := make([]RuntimeProcessItem, 0, len(managed)+len(report.Instances))
	for _, snapshot := range managed {
		status := Status{Label: "Stopped", Tone: ToneMuted}
		if snapshot.Running {
			status = Status{Label: "Running", Tone: TonePositive}
		} else if strings.TrimSpace(snapshot.LastError) != "" || (snapshot.ExitCodeKnown && snapshot.ExitCode != 0) {
			status = Status{Label: "Failed", Tone: ToneDanger}
		}
		var exitCode *int
		if snapshot.ExitCodeKnown {
			code := snapshot.ExitCode
			exitCode = &code
		}
		processes = append(processes, RuntimeProcessItem{
			ID:            snapshot.ID,
			Name:          runtimeName(snapshot.Name, snapshot.Default, snapshot.ID),
			Command:       strings.TrimSpace(snapshot.Command),
			CWD:           strings.TrimSpace(snapshot.CWD),
			PID:           snapshot.PID,
			Managed:       !snapshot.External,
			Running:       snapshot.Running,
			Status:        status,
			StartedAt:     snapshot.StartedAt,
			ExitedAt:      snapshot.ExitedAt,
			ExitCode:      exitCode,
			Ports:         append([]int(nil), snapshot.Ports...),
			ConflictPorts: append([]int(nil), snapshot.ConflictPorts...),
			URLs:          runtimeURLs(snapshot.AnnouncedURLs, snapshot.Ports),
			RecentOutput:  append([]string(nil), snapshot.RecentOutput...),
			LastError:     strings.TrimSpace(snapshot.LastError),
			CanStop:       writeEnabled && snapshot.Running && !snapshot.External,
			CanRestart:    writeEnabled && !snapshot.External && strings.TrimSpace(snapshot.Command) != "",
		})
	}
	for _, instance := range report.Instances {
		processes = append(processes, RuntimeProcessItem{
			ID:         fmt.Sprintf("external-%d", instance.PID),
			Name:       fmt.Sprintf("Local listener %d", instance.PID),
			Command:    strings.TrimSpace(instance.Command),
			CWD:        strings.TrimSpace(instance.CWD),
			PID:        instance.PID,
			Running:    true,
			Status:     Status{Label: "External", Tone: ToneInfo},
			Ports:      append([]int(nil), instance.Ports...),
			URLs:       runtimeURLs(nil, instance.Ports),
			CanStop:    false,
			CanRestart: false,
		})
	}
	warnings := make([]string, 0, len(report.Findings))
	for _, finding := range report.Findings {
		text := fmt.Sprintf("PID %d", finding.PID)
		if command := strings.TrimSpace(finding.Command); command != "" {
			text += " · " + command
		}
		if len(finding.Reasons) > 0 {
			text += " — " + strings.Join(finding.Reasons, ", ")
		}
		warnings = append(warnings, text)
	}
	sort.SliceStable(processes, func(i, j int) bool {
		if processes[i].Running != processes[j].Running {
			return processes[i].Running
		}
		if processes[i].Managed != processes[j].Managed {
			return processes[i].Managed
		}
		return strings.ToLower(processes[i].Name) < strings.ToLower(processes[j].Name)
	})
	return RuntimeSurface{
		Project:      project,
		RunCommand:   strings.TrimSpace(runCommand),
		WriteEnabled: writeEnabled,
		UpdatedAt:    now,
		Processes:    processes,
		Warnings:     warnings,
	}
}

func runtimeName(name string, defaultRuntime bool, id string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	if defaultRuntime {
		return "Project runtime"
	}
	if id = strings.TrimSpace(id); id != "" {
		return id
	}
	return "Runtime"
}

func runtimeURLs(announced []string, ports []int) []string {
	seen := map[string]struct{}{}
	urls := make([]string, 0, len(announced)+len(ports))
	for _, value := range announced {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		urls = append(urls, value)
	}
	for _, port := range ports {
		if port <= 0 {
			continue
		}
		value := fmt.Sprintf("http://127.0.0.1:%d", port)
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		urls = append(urls, value)
	}
	return urls
}

func qualitySummary(snapshot codexapp.Snapshot) string {
	switch {
	case snapshot.QualityPlanNeedsRepair > 0:
		return fmt.Sprintf("%d need repair", snapshot.QualityPlanNeedsRepair)
	case snapshot.QualityPlanVerified > 0:
		return fmt.Sprintf("%d verified", snapshot.QualityPlanVerified)
	case snapshot.QualityPlanUpdates > 0:
		return fmt.Sprintf("%d update(s)", snapshot.QualityPlanUpdates)
	default:
		return "No plan telemetry"
	}
}

func qualityRepairTone(count int) Tone {
	if count > 0 {
		return ToneWarning
	}
	return ToneMuted
}

func qualityPhaseTone(status string) Tone {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "verified", "complete", "completed", "passed":
		return TonePositive
	case "failed", "blocked", "needs_repair", "needs repair":
		return ToneDanger
	case "skipped":
		return ToneMuted
	default:
		return ToneValue
	}
}

func visionState(snapshot codexapp.Snapshot) string {
	if snapshot.ImageAnalysisActive {
		return "Analyzing"
	}
	if snapshot.ImageAnalysisFailures > 0 {
		return "Attention"
	}
	if snapshot.ImageAnalyses > 0 {
		return "Ready"
	}
	return "Idle"
}

func visionTone(snapshot codexapp.Snapshot) Tone {
	if snapshot.ImageAnalysisFailures > 0 {
		return ToneWarning
	}
	if snapshot.ImageAnalysisActive {
		return ToneInfo
	}
	if snapshot.ImageAnalyses > 0 {
		return TonePositive
	}
	return ToneMuted
}

func failureTone(count int) Tone {
	if count > 0 {
		return ToneDanger
	}
	return ToneMuted
}

func contextTone(percent int) Tone {
	switch {
	case percent <= 10:
		return ToneDanger
	case percent <= 25:
		return ToneWarning
	default:
		return TonePositive
	}
}

func browserTone(state string) Tone {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "waiting_for_user", "waiting for user":
		return ToneWarning
	case "active":
		return TonePositive
	default:
		return ToneMuted
	}
}

func compactCount(value int64) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}
