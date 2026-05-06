package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/bossrun"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

func TestExecuteControlEngineerSendPromptRoutesOpenCodeHidden(t *testing.T) {
	projectPath := "/tmp/control-opencode"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "oc-session-1",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "control-opencode",
			PresentOnDisk: true,
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationForTest(t, control.EngineerSendPromptInput{
		ProjectPath: projectPath,
		Provider:    control.ProviderOpenCode,
		SessionMode: control.SessionModeResumeOrNew,
		Prompt:      "please run the next step",
		Reveal:      false,
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeControlInvocation() cmd = nil, want embedded open command")
	}
	if got.codexPendingOpen == nil {
		t.Fatalf("codexPendingOpen = nil, want hidden pending open")
	}
	if got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("pending provider = %q, want opencode", got.codexPendingOpen.provider)
	}
	if got.codexPendingOpen.showWhilePending {
		t.Fatalf("showWhilePending = true, want hidden background open")
	}
	if !got.codexPendingOpen.hideOnOpen {
		t.Fatalf("hideOnOpen = false, want background result to stay hidden")
	}

	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	for _, msg := range msgs {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			break
		}
	}
	if opened.projectPath == "" {
		t.Fatalf("command messages = %#v, want codexSessionOpenedMsg", msgs)
	}
	if opened.err != nil {
		t.Fatalf("codexSessionOpenedMsg.err = %v", opened.err)
	}
	if opened.status != "Prompt sent to embedded OpenCode in the background." {
		t.Fatalf("opened.status = %q, want hidden background prompt status", opened.status)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("request provider = %q, want opencode", requests[0].Provider)
	}
	if requests[0].ForceNew {
		t.Fatalf("request ForceNew = true, want resume_or_new")
	}
	if requests[0].Prompt != "please run the next step" {
		t.Fatalf("request Prompt = %q, want control prompt", requests[0].Prompt)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.codexHiddenProject != projectPath {
		t.Fatalf("codexHiddenProject = %q, want %q", got.codexHiddenProject, projectPath)
	}
}

func TestExecuteControlEngineerSendPromptAutoProviderUsesProjectPreference(t *testing.T) {
	projectPath := "/tmp/control-auto"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "oc-session-2",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:                projectPath,
			Name:                "Control Auto",
			PresentOnDisk:       true,
			LatestSessionFormat: "opencode_db",
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationForTest(t, control.EngineerSendPromptInput{
		ProjectName: "control auto",
		Provider:    control.ProviderAuto,
		SessionMode: control.SessionModeNew,
		Prompt:      "start fresh please",
		Reveal:      true,
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeControlInvocation() cmd = nil, want embedded open command")
	}
	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	for _, msg := range msgs {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			break
		}
	}
	if opened.err != nil {
		t.Fatalf("codexSessionOpenedMsg.err = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("request provider = %q, want opencode from project preference", requests[0].Provider)
	}
	if !requests[0].ForceNew {
		t.Fatalf("request ForceNew = false, want new session")
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != projectPath {
		t.Fatalf("codexVisibleProject = %q, want %q", got.codexVisibleProject, projectPath)
	}
}

func TestExecuteControlEngineerSendPromptRejectsDisabledClaudeCode(t *testing.T) {
	projectPath := "/tmp/control-claude"
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "control-claude",
			PresentOnDisk: true,
		}},
	}

	updated, cmd := m.executeControlInvocation(controlInvocationForTest(t, control.EngineerSendPromptInput{
		ProjectPath: projectPath,
		Provider:    control.ProviderClaudeCode,
		SessionMode: control.SessionModeResumeOrNew,
		Prompt:      "try claude",
		Reveal:      true,
	}))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("executeControlInvocation() cmd = %#v, want nil for disabled provider", cmd)
	}
	if !strings.Contains(got.status, "Claude Code") || !strings.Contains(got.status, "disabled") {
		t.Fatalf("status = %q, want disabled Claude Code message", got.status)
	}
}

func TestExecuteBossControlInvocationBatchesOpenAndBossResult(t *testing.T) {
	projectPath := "/tmp/cn3"
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "oc-session-result",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "cn3",
			PresentOnDisk: true,
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationForTest(t, control.EngineerSendPromptInput{
			ProjectPath: projectPath,
			ProjectName: "cn3",
			Provider:    control.ProviderCodex,
			SessionMode: control.SessionModeResumeOrNew,
			Prompt:      "please run the next step",
			Reveal:      true,
		}),
	})
	_ = updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want wrapped open command")
	}
	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		switch typed := msg.(type) {
		case codexSessionOpenedMsg:
			opened = typed
		case bossui.ControlInvocationResultMsg:
			result = typed
		}
	}
	if opened.projectPath == "" {
		t.Fatalf("wrapped command messages = %#v, want codexSessionOpenedMsg", msgs)
	}
	if !strings.Contains(opened.status, "Esc hides it") {
		t.Fatalf("opened.status = %q, want embedded-pane status to remain visible-pane specific", opened.status)
	}
	if result.Invocation.Capability != control.CapabilityEngineerSendPrompt {
		t.Fatalf("result invocation = %#v", result.Invocation)
	}
	if result.Err != nil {
		t.Fatalf("result err = %v", result.Err)
	}
	wantStatus := "Ok, " + bossui.EngineerNameForKey("project", projectPath, "oc-session-result") + " is working on cn3."
	if result.Status != wantStatus {
		t.Fatalf("result status = %q, want %q", result.Status, wantStatus)
	}
	if result.Activity == nil || result.Activity.Kind != "project" || result.Activity.Title != "cn3" || !result.Activity.Active {
		t.Fatalf("result activity = %#v, want active project activity", result.Activity)
	}
	if strings.Contains(result.Status, "Esc hides it") || strings.Contains(result.Status, "Prompt sent to embedded") || strings.Contains(result.Status, "Boss Chat stayed open") {
		t.Fatalf("result status leaked embedded-pane copy: %q", result.Status)
	}
}

func TestExecuteBossControlInvocationReportsBlockedLaunch(t *testing.T) {
	projectPath := "/tmp/control-boss-blocked"
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:                     projectPath,
			Name:                     "control-boss-blocked",
			PresentOnDisk:            true,
			LatestSessionFormat:      "modern",
			LatestSessionLastEventAt: time.Now(),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      false,
		}},
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationForTest(t, control.EngineerSendPromptInput{
			ProjectPath: projectPath,
			Provider:    control.ProviderOpenCode,
			SessionMode: control.SessionModeResumeOrNew,
			Prompt:      "please run the next step",
			Reveal:      false,
		}),
	})
	got := updated.(Model)
	if got.attentionDialog != nil {
		t.Fatalf("attentionDialog = %#v, want boss control failure to stay in boss transcript", got.attentionDialog)
	}
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want immediate result")
	}
	msgs := collectCmdMsgs(cmd)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = typed
			break
		}
	}
	if result.Err == nil {
		t.Fatalf("result err = nil, want blocked launch error")
	}
	if !strings.Contains(result.Status, "unfinished Codex session") {
		t.Fatalf("result status = %q, want unfinished Codex session", result.Status)
	}
}

func TestExecuteScratchTaskArchiveControlArchivesTask(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	cfg.ScratchRoot = filepath.Join(cfg.DataDir, "tasks")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := service.New(cfg, st, events.NewBus(), nil)

	created, err := svc.CreateScratchTask(ctx, service.CreateScratchTaskRequest{Title: "Hex accessibility issue"})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}
	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: projects,
		projects:    projects,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationRawForTest(t, control.CapabilityScratchTaskArchive, control.ScratchTaskArchiveInput{
		ProjectPath: created.TaskPath,
	}))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("executeControlInvocation() cmd = %#v, want immediate archive", cmd)
	}
	if !strings.Contains(got.status, "Archived scratch task") || !strings.Contains(got.status, "Hex accessibility issue") {
		t.Fatalf("status = %q, want archive status", got.status)
	}
	if _, err := os.Stat(created.TaskPath); !os.IsNotExist(err) {
		t.Fatalf("original task path should be gone after archive, stat err = %v", err)
	}
	if _, ok := got.projectSummaryByPathAllProjects(created.TaskPath); ok {
		t.Fatalf("archived scratch task should be removed from in-memory project list")
	}
}

func TestExecuteBossControlInvocationSteersActiveCodexSessionPrompt(t *testing.T) {
	projectPath := "/tmp/control-active-session"
	liveSession := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider:     codexapp.ProviderCodex,
			ThreadID:     "thread-live",
			Started:      true,
			Busy:         true,
			Phase:        codexapp.SessionPhaseRunning,
			ActiveTurnID: "turn-live",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return liveSession, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: projectPath,
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "control-active-session",
			PresentOnDisk: true,
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationForTest(t, control.EngineerSendPromptInput{
			ProjectPath: projectPath,
			Provider:    control.ProviderCodex,
			SessionMode: control.SessionModeResumeOrNew,
			Prompt:      "I can log in to Appfigures if necessary.",
			Reveal:      false,
		}),
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want wrapped open command")
	}
	if !strings.Contains(got.status, "Opening embedded Codex session") {
		t.Fatalf("status = %q, want background open/steer status", got.status)
	}
	if len(liveSession.submitted) != 0 {
		t.Fatalf("submission should happen inside the returned command, got early submissions: %#v", liveSession.submitted)
	}
	msgs := collectCmdMsgs(cmd)
	if len(liveSession.submitted) != 1 || liveSession.submitted[0] != "I can log in to Appfigures if necessary." {
		t.Fatalf("active session submissions = %#v, want steering note", liveSession.submitted)
	}
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = typed
			break
		}
	}
	if result.Err != nil {
		t.Fatalf("result err = %v, want successful steering note", result.Err)
	}
	if !strings.Contains(result.Status, "is working on control-active-session") {
		t.Fatalf("result status = %q, want engineer work status", result.Status)
	}
}

func TestExecuteBossControlInvocationRefusesNonSteerableActiveEmbeddedSessionPrompt(t *testing.T) {
	projectPath := "/tmp/control-active-opencode"
	liveSession := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider:     codexapp.ProviderOpenCode,
			ThreadID:     "thread-live",
			Started:      true,
			Busy:         true,
			Phase:        codexapp.SessionPhaseRunning,
			ActiveTurnID: "turn-live",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return liveSession, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: projectPath,
		Provider:    codexapp.ProviderOpenCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "control-active-opencode",
			PresentOnDisk: true,
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationForTest(t, control.EngineerSendPromptInput{
			ProjectPath: projectPath,
			Provider:    control.ProviderOpenCode,
			SessionMode: control.SessionModeResumeOrNew,
			Prompt:      "please take over this machine-level cleanup",
			Reveal:      false,
		}),
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want immediate result")
	}
	if !strings.Contains(got.status, "embedded OpenCode engineer session is already running") {
		t.Fatalf("status = %q, want active-session refusal", got.status)
	}
	if len(liveSession.submitted) != 0 {
		t.Fatalf("active session received submissions: %#v", liveSession.submitted)
	}
	msgs := collectCmdMsgs(cmd)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = typed
			break
		}
	}
	if result.Err == nil {
		t.Fatalf("result err = nil, want active-session refusal")
	}
	if !strings.Contains(result.Status, "embedded OpenCode engineer session is already running") {
		t.Fatalf("result status = %q, want active-session refusal", result.Status)
	}
}

func TestExecuteBossControlInvocationCreatesAgentTaskAndTracksSession(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "thread-agent-1",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		ctx:          ctx,
		svc:          svc,
		codexManager: manager,
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationRawForTest(t, control.CapabilityAgentTaskCreate, control.AgentTaskCreateInput{
			Title:        "Clean suspicious local processes",
			Kind:         control.AgentTaskKindAgent,
			Provider:     control.ProviderCodex,
			Prompt:       "Inspect the suspicious processes and stop only the clearly stale ones.",
			Reveal:       false,
			Capabilities: []string{"process.inspect", "process.terminate"},
			Resources: []control.ResourceRef{
				{Kind: control.ResourceProcess, PID: 93624, Label: "hot python"},
				{Kind: control.ResourcePort, Port: 9229, Label: "debug listener"},
			},
		}),
	})
	_ = updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want task launch command")
	}
	msgs := collectCmdMsgs(cmd)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = typed
			break
		}
	}
	if result.Err != nil {
		t.Fatalf("result err = %v", result.Err)
	}
	if !strings.Contains(result.Status, "is working on Clean suspicious local processes") ||
		strings.Contains(result.Status, "Created agent task") ||
		strings.Contains(result.Status, "prompt sent") ||
		strings.Contains(result.Status, "Esc hides it") {
		t.Fatalf("result status = %q, want high-level task launch status", result.Status)
	}
	if result.Activity == nil || result.Activity.Kind != "agent_task" || result.Activity.Title != "Clean suspicious local processes" || !result.Activity.Active {
		t.Fatalf("result activity = %#v, want active agent task activity", result.Activity)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if !requests[0].ForceNew {
		t.Fatalf("request ForceNew = false, want fresh task session")
	}
	if !strings.Contains(requests[0].Prompt, "Little Control Room agent task:") ||
		!strings.Contains(requests[0].Prompt, "process.terminate") ||
		!strings.Contains(requests[0].Prompt, "pid 93624 hot python") {
		t.Fatalf("launch prompt missing task context:\n%s", requests[0].Prompt)
	}
	tasks, err := svc.ListOpenAgentTasks(ctx, 5)
	if err != nil {
		t.Fatalf("ListOpenAgentTasks() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("open tasks = %#v, want one task", tasks)
	}
	task := tasks[0]
	if task.Kind != model.AgentTaskKindAgent || task.Provider != model.SessionSourceCodex || task.SessionID != "thread-agent-1" {
		t.Fatalf("tracked task = %#v", task)
	}
	if task.WorkspacePath == "" || requests[0].ProjectPath != task.WorkspacePath {
		t.Fatalf("workspace/request path = %q/%q", task.WorkspacePath, requests[0].ProjectPath)
	}
	var sawSession bool
	for _, resource := range task.Resources {
		if resource.Kind == model.AgentTaskResourceEngineerSession && resource.SessionID == "thread-agent-1" {
			sawSession = true
		}
	}
	if !sawSession {
		t.Fatalf("task resources missing engineer session: %#v", task.Resources)
	}
}

func TestExecuteBossControlInvocationContinuesAgentTaskWithTrackedSession(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title:        "Keep checking temp process cleanup",
		Kind:         model.AgentTaskKindAgent,
		Capabilities: []string{"process.inspect"},
	})
	if err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}
	if _, err := svc.AttachAgentTaskEngineerSession(ctx, task.ID, model.SessionSourceCodex, "thread-agent-existing"); err != nil {
		t.Fatalf("AttachAgentTaskEngineerSession() error = %v", err)
	}
	task, err = svc.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetAgentTask() error = %v", err)
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "thread-agent-existing",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		ctx:          ctx,
		svc:          svc,
		codexManager: manager,
	}

	_, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationRawForTest(t, control.CapabilityAgentTaskContinue, control.AgentTaskContinueInput{
			TaskID:      task.ID,
			Provider:    control.ProviderAuto,
			SessionMode: control.SessionModeResumeOrNew,
			Prompt:      "Check whether the ports stayed clear.",
			Reveal:      false,
		}),
	})
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want continue command")
	}
	msgs := collectCmdMsgs(cmd)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = typed
			if result.Err != nil {
				t.Fatalf("result err = %v", result.Err)
			}
		}
	}
	engineerName := bossui.EngineerNameForKey("agent_task", task.ID)
	if !strings.Contains(result.Status, "Ok, "+engineerName+" is working on Keep checking temp process cleanup") ||
		strings.Contains(result.Status, "Continued agent task") ||
		strings.Contains(result.Status, "Attention row shows") {
		t.Fatalf("result status = %q, want high-level continued task launch status without UI narration", result.Status)
	}
	if result.Activity == nil || result.Activity.TaskID != task.ID || result.Activity.Title != "Keep checking temp process cleanup" || result.Activity.EngineerName != engineerName {
		t.Fatalf("result activity = %#v, want continued agent task activity", result.Activity)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].ProjectPath != task.WorkspacePath {
		t.Fatalf("request ProjectPath = %q, want task workspace %q", requests[0].ProjectPath, task.WorkspacePath)
	}
	if requests[0].ResumeID != "thread-agent-existing" {
		t.Fatalf("request ResumeID = %q, want tracked session", requests[0].ResumeID)
	}
	for _, want := range []string{
		"Report contract:",
		"Answer the user's exact request directly",
		"Preserve source, metric, timeframe, scope, negations, and explicit exclusions",
		"what was compared, what was kept, what was discarded, and the substantive differences",
		"User request:\nCheck whether the ports stayed clear.",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("launch prompt missing %q:\n%s", want, requests[0].Prompt)
		}
	}
	if requests[0].ForceNew {
		t.Fatalf("request ForceNew = true, want resume")
	}
}

func TestExecuteBossGoalRunArchivesMultipleAgentTasksAndVerifies(t *testing.T) {
	t.Parallel()

	svc := newControlTestService(t)
	ctx := context.Background()
	taskOne, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Old review",
		Kind:  model.AgentTaskKindAgent,
	})
	if err != nil {
		t.Fatalf("CreateAgentTask(one) error = %v", err)
	}
	taskTwo, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Old follow-up",
		Kind:  model.AgentTaskKindAgent,
	})
	if err != nil {
		t.Fatalf("CreateAgentTask(two) error = %v", err)
	}
	proposal, err := bossrun.NormalizeGoalProposal(bossrun.GoalProposal{
		Run: bossrun.GoalRun{
			Kind:      bossrun.GoalKindAgentTaskCleanup,
			Title:     "Clear stale delegated agents",
			Objective: "Archive stale delegated agent task records.",
		},
		ArchiveResources: []control.ResourceRef{
			{Kind: control.ResourceAgentTask, ID: taskOne.ID, Label: taskOne.Title},
			{Kind: control.ResourceAgentTask, ID: taskTwo.ID, Label: taskTwo.Title},
		},
	})
	if err != nil {
		t.Fatalf("NormalizeGoalProposal() error = %v", err)
	}
	m := Model{
		ctx:            ctx,
		svc:            svc,
		openAgentTasks: []model.AgentTask{taskOne, taskTwo},
	}

	updated, cmd := m.executeBossGoalRun(bossui.GoalRunConfirmedMsg{Proposal: proposal})
	got := updated.(Model)
	if got.status != "Running goal: Clear stale delegated agents" {
		t.Fatalf("status = %q, want running goal status", got.status)
	}
	if cmd == nil {
		t.Fatalf("executeBossGoalRun() cmd = nil, want execution command")
	}
	msg := cmd()
	result, ok := msg.(bossui.GoalRunResultMsg)
	if !ok {
		t.Fatalf("goal command returned %T, want GoalRunResultMsg", msg)
	}
	if result.Err != nil {
		t.Fatalf("goal result err = %v", result.Err)
	}
	if !result.Result.Verified {
		t.Fatalf("goal result should be verified: %#v", result.Result)
	}
	if len(result.Result.ArchivedTaskIDs) != 2 {
		t.Fatalf("archived ids = %#v, want two tasks", result.Result.ArchivedTaskIDs)
	}
	if result.Result.RunID == "" {
		t.Fatalf("goal result RunID is empty, want persisted run id")
	}
	for _, taskID := range []string{taskOne.ID, taskTwo.ID} {
		task, err := svc.GetAgentTask(ctx, taskID)
		if err != nil {
			t.Fatalf("GetAgentTask(%s) error = %v", taskID, err)
		}
		if task.Status != model.AgentTaskStatusArchived {
			t.Fatalf("task %s status = %q, want archived", taskID, task.Status)
		}
	}
	record, err := svc.Store().GetGoalRun(ctx, result.Result.RunID)
	if err != nil {
		t.Fatalf("GetGoalRun() error = %v", err)
	}
	if record.Proposal.Run.Status != bossrun.GoalStatusCompleted {
		t.Fatalf("stored goal status = %q, want completed", record.Proposal.Run.Status)
	}
	if len(record.Trace) != 3 {
		t.Fatalf("stored trace length = %d, want two archive entries plus verification", len(record.Trace))
	}
	if !record.Result.Verified || len(record.Result.ArchivedTaskIDs) != 2 {
		t.Fatalf("stored result = %#v, want verified archive result", record.Result)
	}

	pruned := got.applyBossGoalRunResultToHost(result)
	if len(pruned.openAgentTasks) != 0 {
		t.Fatalf("openAgentTasks = %#v, want archived tasks pruned", pruned.openAgentTasks)
	}
}

func controlInvocationForTest(t *testing.T, input control.EngineerSendPromptInput) control.Invocation {
	t.Helper()
	if input.Provider == "" {
		input.Provider = control.ProviderAuto
	}
	if input.SessionMode == "" {
		input.SessionMode = control.SessionModeResumeOrNew
	}
	args, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal control input: %v", err)
	}
	return control.Invocation{
		RequestID:  "test-request",
		Capability: control.CapabilityEngineerSendPrompt,
		Args:       args,
	}
}

func controlInvocationRawForTest(t *testing.T, capability control.CapabilityName, input any) control.Invocation {
	t.Helper()
	args, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal control input: %v", err)
	}
	return control.Invocation{
		RequestID:  "test-request",
		Capability: capability,
		Args:       args,
	}
}

func newControlTestService(t *testing.T) *service.Service {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	cfg.ScratchRoot = filepath.Join(cfg.DataDir, "tasks")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return service.New(cfg, st, events.NewBus(), nil)
}
