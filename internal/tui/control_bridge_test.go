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
	"lcroom/internal/projectrun"
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
	for _, want := range []string{
		"Little Control Room engineer task:",
		"Report contract:",
		"Do not final-report only success/failure plus artifact links",
		"User request:\nplease run the next step",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("request prompt missing %q:\n%s", want, requests[0].Prompt)
		}
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

func TestExecuteControlEngineerSendPromptRoutesLCAgentHidden(t *testing.T) {
	projectPath := "/tmp/control-lcagent"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "lca-session-1",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "control-lcagent",
			PresentOnDisk: true,
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationForTest(t, control.EngineerSendPromptInput{
		ProjectPath: projectPath,
		Provider:    control.ProviderLCAgent,
		SessionMode: control.SessionModeNew,
		Prompt:      "try this through lcagent",
		Reveal:      false,
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeControlInvocation() cmd = nil, want embedded lcagent command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderLCAgent {
		t.Fatalf("pending provider = %#v, want lcagent", got.codexPendingOpen)
	}
	if got.codexPendingOpen.showWhilePending || !got.codexPendingOpen.hideOnOpen {
		t.Fatalf("pending visibility = show:%v hide:%v, want hidden background open", got.codexPendingOpen.showWhilePending, got.codexPendingOpen.hideOnOpen)
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
	if opened.status != "Prompt sent to fresh embedded LCAgent session lca-sess in the background." {
		t.Fatalf("opened.status = %q, want hidden background LCAgent prompt status", opened.status)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderLCAgent || !requests[0].ForceNew {
		t.Fatalf("request provider/ForceNew = %q/%v, want lcagent/true", requests[0].Provider, requests[0].ForceNew)
	}
	if !strings.Contains(requests[0].Prompt, "User request:\ntry this through lcagent") {
		t.Fatalf("request prompt missing user request:\n%s", requests[0].Prompt)
	}
}

func TestExecuteControlEngineerSendPromptIncludesRuntimeTestingContext(t *testing.T) {
	projectPath := "/tmp/control-runtime"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "runtime-session-1",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "Control Runtime",
			PresentOnDisk: true,
			RunCommand:    "npm run dev",
		}},
		runtimeSnapshots: map[string]projectrun.Snapshot{
			projectPath: {
				ProjectPath:   projectPath,
				Command:       "npm run dev",
				Running:       true,
				Ports:         []int{3000},
				AnnouncedURLs: []string{"http://127.0.0.1:3000/demo"},
			},
		},
		codexManager: manager,
	}

	_, cmd := m.executeControlInvocation(controlInvocationForTest(t, control.EngineerSendPromptInput{
		ProjectPath: projectPath,
		Provider:    control.ProviderCodex,
		SessionMode: control.SessionModeNew,
		Prompt:      "please test the app",
		Reveal:      false,
	}))
	if cmd == nil {
		t.Fatalf("executeControlInvocation() cmd = nil, want embedded open command")
	}
	_ = collectCmdMsgs(cmd)
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	for _, want := range []string{
		"Little Control Room engineer task:",
		"Report contract:",
		"whether content changed and summarize the meaningful changes",
		"please test the app",
		"Little Control Room testing context:",
		"use runtime/test URL http://127.0.0.1:3000/demo",
		"detected listening ports: 3000",
		"managed runtime command: npm run dev",
		"managed runtime status: running",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, requests[0].Prompt)
		}
	}
}

func TestExecuteControlEngineerSendPromptCallsOutMissingRuntimeURL(t *testing.T) {
	projectPath := "/tmp/control-runtime-no-url"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "runtime-session-no-url",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "Control Runtime",
			PresentOnDisk: true,
			RunCommand:    "npm run dev",
		}},
		codexManager: manager,
	}

	_, cmd := m.executeControlInvocation(controlInvocationForTest(t, control.EngineerSendPromptInput{
		ProjectPath: projectPath,
		Provider:    control.ProviderCodex,
		SessionMode: control.SessionModeNew,
		Prompt:      "please test the app",
		Reveal:      false,
	}))
	if cmd == nil {
		t.Fatalf("executeControlInvocation() cmd = nil, want embedded open command")
	}
	_ = collectCmdMsgs(cmd)
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	for _, want := range []string{
		"Little Control Room engineer task:",
		"Report contract:",
		"Do not final-report only success/failure plus artifact links",
		"please test the app",
		"Little Control Room testing context:",
		"no runtime/test URL detected",
		"managed runtime command: npm run dev",
		"managed runtime status: configured",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, requests[0].Prompt)
		}
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
	if !strings.Contains(opened.status, "Alt+Up hides it") {
		t.Fatalf("opened.status = %q, want embedded-pane status to remain visible-pane specific", opened.status)
	}
	if result.Invocation.Capability != control.CapabilityEngineerSendPrompt {
		t.Fatalf("result invocation = %#v", result.Invocation)
	}
	if result.Err != nil {
		t.Fatalf("result err = %v", result.Err)
	}
	wantStatus := "Work on cn3 is underway."
	if result.Status != wantStatus {
		t.Fatalf("result status = %q, want %q", result.Status, wantStatus)
	}
	if result.Activity == nil || result.Activity.Kind != "project" || result.Activity.Title != "cn3" || !result.Activity.Active {
		t.Fatalf("result activity = %#v, want active project activity", result.Activity)
	}
	if strings.Contains(result.Status, "Alt+Up hides it") || strings.Contains(result.Status, "Prompt sent to embedded") || strings.Contains(result.Status, "Chat stayed open") {
		t.Fatalf("result status leaked embedded-pane copy: %q", result.Status)
	}
}

func TestExecuteBossControlInvocationLinksEngineerWorkToTodo(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	projectPath := filepath.Join(t.TempDir(), "alpha")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := svc.Store().UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "Alpha",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       true,
		LastActivity:  time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProjectState() error = %v", err)
	}
	todo, err := svc.AddTodo(ctx, projectPath, "Add Boss-managed TODO tracking.")
	if err != nil {
		t.Fatalf("AddTodo() error = %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, svc.Store(), todo.ID)
	suggestion, err := svc.Store().ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ClaimNextQueuedTodoWorktreeSuggestion() error = %v", err)
	}
	suggestion.BranchName = "feat/help-todo-tracking"
	suggestion.WorktreeSuffix = "boss-todo-tracking"
	suggestion.Kind = "feat"
	suggestion.Reason = "Short generated TODO name."
	suggestion.Confidence = 0.86
	suggestion.Model = "test-model"
	if completed, err := svc.Store().CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil || !completed {
		t.Fatalf("CompleteTodoWorktreeSuggestion() = (%t, %v), want completed", completed, err)
	}
	projects, err := svc.Store().ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "thread-alpha-todo",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		ctx:          ctx,
		svc:          svc,
		allProjects:  projects,
		projects:     projects,
		codexManager: manager,
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationForTest(t, control.EngineerSendPromptInput{
			ProjectPath: projectPath,
			ProjectName: "Alpha",
			Provider:    control.ProviderCodex,
			SessionMode: control.SessionModeNew,
			Prompt:      "Implement the tracked TODO and report what remains.",
			TodoID:      todo.ID,
			TodoText:    todo.Text,
			Reveal:      false,
		}),
	})
	_ = updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want wrapped open command")
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
	if !strings.Contains(result.Status, "Alpha #") || !strings.Contains(result.Status, "boss todo tracking") {
		t.Fatalf("result status = %q, want project plus short TODO label", result.Status)
	}
	if strings.Contains(result.Status, "Add Boss-managed TODO tracking") {
		t.Fatalf("result status = %q, should use the short TODO label instead of the full text", result.Status)
	}
	if result.Activity == nil || result.Activity.TodoID != todo.ID || result.Activity.TodoText != todo.Text ||
		result.Activity.TodoLabel != "boss todo tracking" ||
		!strings.Contains(result.Activity.Title, "Alpha #") || !strings.Contains(result.Activity.Title, "boss todo tracking") {
		t.Fatalf("result activity = %#v, want active TODO context", result.Activity)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	for _, want := range []string{
		"Tracked project TODO:",
		"TODO text: Add Boss-managed TODO tracking.",
		"complete, partial, blocked, or still open",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("launch prompt missing %q:\n%s", want, requests[0].Prompt)
		}
	}
	recorded := updated.(Model).recordBossTrackedTodoFromControlResult(result)
	tracked, ok := recorded.bossTrackedTodoForSnapshot(projectPath, codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		ThreadID: "thread-alpha-todo",
	})
	if !ok || tracked.ID != todo.ID || tracked.Label != "boss todo tracking" {
		t.Fatalf("tracked TODO = %#v/%t, want #%d", tracked, ok, todo.ID)
	}
}

func TestExecuteBossControlInvocationContinuesTodoInRecordedWorktree(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	parent := t.TempDir()
	projectPath := filepath.Join(parent, "KeyMaster")
	worktreePath := filepath.Join(parent, "KeyMaster--initialize")
	for _, path := range []string{projectPath, worktreePath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", path, err)
		}
	}
	for _, state := range []model.ProjectState{
		{Path: projectPath, Name: "KeyMaster", Status: model.StatusIdle, PresentOnDisk: true, InScope: true, UpdatedAt: time.Now()},
		{Path: worktreePath, Name: "KeyMaster--initialize", Status: model.StatusIdle, PresentOnDisk: true, InScope: true, WorktreeRootPath: projectPath, WorktreeKind: model.WorktreeKindLinked, UpdatedAt: time.Now()},
	} {
		if err := svc.Store().UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("UpsertProjectState(%s) error = %v", state.Path, err)
		}
	}
	todo, err := svc.AddTodo(ctx, projectPath, "Initialize KeyMaster")
	if err != nil {
		t.Fatalf("AddTodo() error = %v", err)
	}
	if err := svc.Store().AttachTodoWorkSession(ctx, todo.ID, worktreePath, model.SessionSourceCodex, "codex:thread-keymaster-initialize", model.TodoWorkStateIdle, time.Now()); err != nil {
		t.Fatalf("AttachTodoWorkSession() error = %v", err)
	}

	projects, err := svc.Store().ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "thread-keymaster-followup",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{ctx: ctx, svc: svc, allProjects: projects, projects: projects, codexManager: manager}

	_, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationForTest(t, control.EngineerSendPromptInput{
			ProjectPath: projectPath,
			ProjectName: "KeyMaster",
			Provider:    control.ProviderCodex,
			SessionMode: control.SessionModeResumeOrNew,
			Prompt:      "KeyMaster is an API key ledger; continue the existing creation task with these requirements.",
			TodoID:      todo.ID,
			TodoText:    todo.Text,
		}),
	})
	if cmd == nil {
		t.Fatal("executeBossControlInvocation() cmd = nil, want worktree continuation")
	}
	msgs := collectCmdMsgs(cmd)
	for _, msg := range msgs {
		if result, ok := msg.(bossui.ControlInvocationResultMsg); ok && result.Err != nil {
			t.Fatalf("control result error = %v", result.Err)
		}
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %#v, want one worktree continuation", requests)
	}
	if requests[0].ProjectPath != worktreePath || requests[0].ForceNew {
		t.Fatalf("launch request = %#v, want resume-or-new in %s", requests[0], worktreePath)
	}
	if !strings.Contains(requests[0].Prompt, "KeyMaster is an API key ledger") || !strings.Contains(requests[0].Prompt, "TODO text: Initialize KeyMaster") {
		t.Fatalf("launch prompt = %q, want clarification plus tracked TODO context", requests[0].Prompt)
	}
}

func TestExecuteBossTrackedWorktreeLaunchCreatesTodoWorktreeAndEngineer(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	projectPath := filepath.Join(t.TempDir(), "tracked-repo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.email", "test@example.com")
	runTUITestGit(t, projectPath, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("tracked repo\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial")
	if err := svc.Store().UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "Tracked Repo",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    runTUITestGit(t, projectPath, "branch", "--show-current"),
		UpdatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProjectState() error = %v", err)
	}
	projects, err := svc.Store().ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "thread-tracked-worktree",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{ctx: ctx, svc: svc, allProjects: projects, projects: projects, codexManager: manager}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: trackedWorktreeControlInvocationForTest(t, control.TodoCreateWorktreeAndStartEngineerInput{
		ProjectPath: projectPath,
		ProjectName: "Tracked Repo",
		TodoText:    "Add durable Chat engineer launch feedback.",
		Prompt:      "Implement durable Chat engineer launch feedback and verify it.",
		Provider:    control.ProviderCodex,
	})})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want TODO creation command")
	}
	created, ok := cmd().(bossTodoWorktreeTodoCreatedMsg)
	if !ok || created.err != nil || created.todo.ID <= 0 {
		t.Fatalf("TODO creation result = %#v, want created tracked TODO", created)
	}

	updated, cmd = got.Update(created)
	got = updated.(Model)
	if len(got.pendingBossHostNotices) != 1 || !strings.Contains(got.pendingBossHostNotices[0].Content, "Starting TODO #") {
		t.Fatalf("pending Chat notices = %#v, want durable starting receipt", got.pendingBossHostNotices)
	}
	var prepared bossTodoWorktreePreparedMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossTodoWorktreePreparedMsg); ok {
			prepared = candidate
			break
		}
	}
	if prepared.err != nil || prepared.result.WorktreePath == "" {
		t.Fatalf("worktree preparation = %#v, want prepared worktree", prepared)
	}
	if prepared.result.WorktreePath == projectPath {
		t.Fatalf("worktree path = root path %q, want isolated checkout", projectPath)
	}

	updated, cmd = got.Update(prepared)
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("handling prepared worktree returned nil command")
	}
	var result bossui.ControlInvocationResultMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = candidate
		}
	}
	if result.Err != nil || result.Activity == nil {
		t.Fatalf("launch result = %#v, want active tracked engineer", result)
	}
	if !strings.Contains(result.Status, "AI engineer launched") || !strings.Contains(result.Status, "TODO #") || !strings.Contains(result.Status, filepath.Base(prepared.result.WorktreePath)) {
		t.Fatalf("launch status = %q, want durable task/worktree receipt", result.Status)
	}
	if len(requests) != 1 || requests[0].ProjectPath != prepared.result.WorktreePath || !requests[0].ForceNew {
		t.Fatalf("launch requests = %#v, want one fresh worktree session", requests)
	}
	tracked, err := svc.Store().GetTodo(ctx, created.todo.ID)
	if err != nil {
		t.Fatalf("GetTodo() error = %v", err)
	}
	if tracked.WorkProjectPath != prepared.result.WorktreePath || tracked.WorkSessionID != "codex:thread-tracked-worktree" {
		t.Fatalf("tracked TODO work = path:%q session:%q, want worktree/codex session", tracked.WorkProjectPath, tracked.WorkSessionID)
	}
	waitForControlAsyncRefreshes(t, svc)
}

func TestExecuteBossProjectCreateAndStartEngineerCreatesRepositoryTodoWorktreeAndEngineer(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	parentPath := t.TempDir()
	projectPath := filepath.Join(parentPath, "KeyMaster")
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "thread-keymaster-create",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{ctx: ctx, svc: svc, codexManager: manager}
	inv := controlInvocationRawForTest(t, control.CapabilityProjectCreateAndStartEngineer, control.ProjectCreateAndStartEngineerInput{
		ParentPath:  parentPath,
		ProjectName: "KeyMaster",
		TodoText:    "Build the initial KeyMaster repository.",
		Prompt:      "Create the initial project structure and verify it.",
		Provider:    control.ProviderCodex,
	})

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want repository creation command")
	}
	created, ok := cmd().(bossProjectCreateAndStartEngineerCreatedMsg)
	if !ok || created.err != nil {
		t.Fatalf("repository creation result = %#v, want success", created)
	}
	if !created.folderExists || !created.gitInitialized || !created.projectTracked {
		t.Fatalf("repository creation stages = folder:%v git:%v tracked:%v", created.folderExists, created.gitInitialized, created.projectTracked)
	}
	if created.result.ProjectPath != projectPath || created.project.Path != projectPath {
		t.Fatalf("created project paths = result:%q summary:%q, want %q", created.result.ProjectPath, created.project.Path, projectPath)
	}

	updated, cmd = got.Update(created)
	got = updated.(Model)
	if _, ok := got.projectSummaryByPathAllProjects(projectPath); !ok {
		t.Fatalf("new repository should be available in the host project cache")
	}
	var todoCreated bossTodoWorktreeTodoCreatedMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossTodoWorktreeTodoCreatedMsg); ok {
			todoCreated = candidate
			break
		}
	}
	if todoCreated.err != nil || todoCreated.todo.ID <= 0 || todoCreated.inv.Capability != control.CapabilityProjectCreateAndStartEngineer {
		t.Fatalf("tracked TODO creation = %#v, want new-project control progress", todoCreated)
	}

	updated, cmd = got.Update(todoCreated)
	got = updated.(Model)
	var prepared bossTodoWorktreePreparedMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossTodoWorktreePreparedMsg); ok {
			prepared = candidate
			break
		}
	}
	if prepared.err != nil || prepared.result.WorktreePath == "" || prepared.inv.Capability != control.CapabilityProjectCreateAndStartEngineer {
		t.Fatalf("worktree preparation = %#v, want new-project control progress", prepared)
	}

	updated, cmd = got.Update(prepared)
	got = updated.(Model)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = candidate
		}
	}
	if result.Err != nil || result.Activity == nil || result.Invocation.Capability != control.CapabilityProjectCreateAndStartEngineer {
		t.Fatalf("launch result = %#v, want successful new-project activity", result)
	}
	if !strings.Contains(result.Status, "Created Git repository "+projectPath) || !strings.Contains(result.Status, "AI engineer launched") {
		t.Fatalf("launch status = %q, want repository and engineer receipt", result.Status)
	}
	if len(requests) != 1 || requests[0].ProjectPath != prepared.result.WorktreePath || !requests[0].ForceNew {
		t.Fatalf("launch requests = %#v, want one fresh worktree session", requests)
	}
	tracked, err := svc.Store().GetTodo(ctx, todoCreated.todo.ID)
	if err != nil || tracked.ProjectPath != projectPath || tracked.WorkProjectPath != prepared.result.WorktreePath {
		t.Fatalf("tracked TODO = %#v, err=%v", tracked, err)
	}
	waitForControlAsyncRefreshes(t, svc)
}

func TestExecuteBossProjectSetCategoryRegistersExistingFolderWithoutCreatingProjectWork(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	category, err := svc.CreateProjectCategory(ctx, "Private")
	if err != nil {
		t.Fatalf("CreateProjectCategory() error = %v", err)
	}
	if _, err := svc.SetProjectCategoryPrivate(ctx, category.Name, true); err != nil {
		t.Fatalf("SetProjectCategoryPrivate() error = %v", err)
	}
	projectPath := filepath.Join(t.TempDir(), "career-private")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("create existing project folder: %v", err)
	}
	m := Model{ctx: ctx, svc: svc}
	inv := controlInvocationRawForTest(t, control.CapabilityProjectSetCategory, control.ProjectSetCategoryInput{
		ProjectPath:  projectPath,
		ProjectName:  "career-private",
		CategoryName: "Private",
	})

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want category command")
	}
	set, ok := cmd().(bossProjectCategorySetMsg)
	if !ok || set.err != nil {
		t.Fatalf("project category result = %#v, want success", set)
	}
	if set.result.Action != service.CreateOrAttachProjectAdded || set.project.CategoryName != "Private" || !set.project.CategoryPrivate {
		t.Fatalf("category setup = action:%q project:%#v", set.result.Action, set.project)
	}

	updated, cmd = got.Update(set)
	got = updated.(Model)
	if !strings.Contains(got.status, "Added \"career-private\" to Little Control Room in the Private category") || !strings.Contains(got.status, "No project work was created") {
		t.Fatalf("status = %q, want category-only receipt", got.status)
	}
	var result bossui.ControlInvocationResultMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = candidate
		}
	}
	if result.Err != nil || result.Invocation.Capability != control.CapabilityProjectSetCategory {
		t.Fatalf("control result = %#v", result)
	}
	detail, err := svc.Store().GetProjectDetail(ctx, projectPath, 0)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if detail.Summary.CategoryName != "Private" || !detail.Summary.ManuallyAdded {
		t.Fatalf("stored project = %#v", detail.Summary)
	}
	if len(detail.Todos) != 0 || detail.Summary.TotalTODOCount != 0 {
		t.Fatalf("category-only action created TODOs: %#v", detail.Todos)
	}
	if _, err := os.Stat(filepath.Join(projectPath, ".git")); !os.IsNotExist(err) {
		t.Fatalf("category-only action should not initialize Git, stat err = %v", err)
	}
	waitForControlAsyncRefreshes(t, svc)
}

func TestExecuteBossProjectSetCategoryRejectsMissingFolderWithoutCreatingIt(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	if _, err := svc.CreateProjectCategory(ctx, "Private"); err != nil {
		t.Fatalf("CreateProjectCategory() error = %v", err)
	}
	projectPath := filepath.Join(t.TempDir(), "missing-project")
	m := Model{ctx: ctx, svc: svc}
	inv := controlInvocationRawForTest(t, control.CapabilityProjectSetCategory, control.ProjectSetCategoryInput{
		ProjectPath:  projectPath,
		ProjectName:  "missing-project",
		CategoryName: "Private",
	})

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
	got := updated.(Model)
	set, ok := cmd().(bossProjectCategorySetMsg)
	if !ok || set.err == nil || !strings.Contains(set.err.Error(), "does not exist") {
		t.Fatalf("missing project category result = %#v", set)
	}
	updated, resultCmd := got.Update(set)
	got = updated.(Model)
	if resultCmd == nil || !strings.Contains(got.status, "does not exist") {
		t.Fatalf("missing project status = %q", got.status)
	}
	if _, err := os.Stat(projectPath); !os.IsNotExist(err) {
		t.Fatalf("category-only action created missing project folder, stat err = %v", err)
	}
}

func TestExecuteBossProjectCreateAndStartEngineerRegistersExistingRepositoryAndStartsWork(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	parentPath := t.TempDir()
	projectPath := filepath.Join(parentPath, "career-private")
	runTUITestGit(t, "", "init", projectPath)

	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "thread-career-private",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{ctx: ctx, svc: svc, codexManager: manager}
	inv := controlInvocationRawForTest(t, control.CapabilityProjectCreateAndStartEngineer, control.ProjectCreateAndStartEngineerInput{
		ParentPath:  parentPath,
		ProjectName: "career-private",
		TodoText:    "Set up the existing private career project.",
		Prompt:      "Inspect the existing repository and continue the requested work.",
		Provider:    control.ProviderCodex,
	})

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
	got := updated.(Model)
	created, ok := cmd().(bossProjectCreateAndStartEngineerCreatedMsg)
	if !ok || created.err != nil {
		t.Fatalf("existing repository setup result = %#v, want success", created)
	}
	if created.result.Action != service.CreateOrAttachProjectAdded || created.result.GitRepoCreated {
		t.Fatalf("existing repository result = %#v, want registration without Git initialization", created.result)
	}
	if !created.folderExists || !created.gitInitialized || !created.projectTracked {
		t.Fatalf("existing repository stages = folder:%v git:%v tracked:%v", created.folderExists, created.gitInitialized, created.projectTracked)
	}

	updated, cmd = got.Update(created)
	got = updated.(Model)
	var todoCreated bossTodoWorktreeTodoCreatedMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossTodoWorktreeTodoCreatedMsg); ok {
			todoCreated = candidate
			break
		}
	}
	if todoCreated.err != nil || todoCreated.todo.ID <= 0 || todoCreated.projectSetupAction != service.CreateOrAttachProjectAdded {
		t.Fatalf("existing repository TODO creation = %#v, want attached-project progress", todoCreated)
	}

	updated, cmd = got.Update(todoCreated)
	got = updated.(Model)
	var prepared bossTodoWorktreePreparedMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossTodoWorktreePreparedMsg); ok {
			prepared = candidate
			break
		}
	}
	if prepared.err != nil || prepared.result.WorktreePath == "" || prepared.projectSetupAction != service.CreateOrAttachProjectAdded {
		t.Fatalf("existing repository worktree preparation = %#v, want success", prepared)
	}

	updated, cmd = got.Update(prepared)
	got = updated.(Model)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = candidate
		}
	}
	if result.Err != nil || result.Activity == nil {
		t.Fatalf("existing repository launch result = %#v, want active tracked engineer", result)
	}
	if !strings.Contains(result.Status, "Registered existing Git repository "+projectPath) || !strings.Contains(result.Status, "AI engineer launched") {
		t.Fatalf("launch status = %q, want existing-repository registration and engineer receipt", result.Status)
	}
	if len(requests) != 1 || requests[0].ProjectPath != prepared.result.WorktreePath || !requests[0].ForceNew {
		t.Fatalf("launch requests = %#v, want one fresh worktree session", requests)
	}
	waitForControlAsyncRefreshes(t, svc)
}

func TestExecuteBossProjectCreateAndStartEngineerRejectsExistingNonGitFolderBeforeRegistration(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	parentPath := t.TempDir()
	projectPath := filepath.Join(parentPath, "career-private")
	if err := os.Mkdir(projectPath, 0o755); err != nil {
		t.Fatalf("create existing non-Git folder: %v", err)
	}
	m := Model{ctx: ctx, svc: svc}
	inv := controlInvocationRawForTest(t, control.CapabilityProjectCreateAndStartEngineer, control.ProjectCreateAndStartEngineerInput{
		ParentPath:  parentPath,
		ProjectName: "career-private",
		TodoText:    "Set up the private career project.",
		Prompt:      "Inspect the repository.",
		Provider:    control.ProviderCodex,
	})

	_, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
	created, ok := cmd().(bossProjectCreateAndStartEngineerCreatedMsg)
	if !ok || created.err == nil || !strings.Contains(created.err.Error(), "not a Git repository") {
		t.Fatalf("existing non-Git setup result = %#v, want preflight rejection", created)
	}
	if !created.folderExists || created.gitInitialized || created.projectTracked {
		t.Fatalf("non-Git rejection stages = folder:%v git:%v tracked:%v", created.folderExists, created.gitInitialized, created.projectTracked)
	}
	if _, err := svc.Store().GetProjectSummary(ctx, projectPath, true); err == nil {
		t.Fatalf("existing non-Git folder should not be registered")
	}
}

func TestExecuteBossProjectCreateAndStartEngineerRejectsLoadedTarget(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "KeyMaster")
	m := Model{allProjects: []model.ProjectSummary{{Path: projectPath, Name: "KeyMaster", PresentOnDisk: true}}}
	inv := controlInvocationRawForTest(t, control.CapabilityProjectCreateAndStartEngineer, control.ProjectCreateAndStartEngineerInput{
		ParentPath:  filepath.Dir(projectPath),
		ProjectName: filepath.Base(projectPath),
		TodoText:    "Build it.",
		Prompt:      "Build it.",
		Provider:    control.ProviderCodex,
	})

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("loaded-target rejection should emit a Chat result")
	}
	result, ok := cmd().(bossui.ControlInvocationResultMsg)
	if !ok || result.Err == nil || !strings.Contains(result.Status, "already loaded") {
		t.Fatalf("loaded-target result = %#v", result)
	}
	if !strings.Contains(got.status, "already loaded") {
		t.Fatalf("host status = %q, want loaded-target rejection", got.status)
	}
}

func TestExecuteBossProjectCreateAndStartEngineerReportsNoSideEffectsWhenParentIsMissing(t *testing.T) {
	svc := newControlTestService(t)
	parentPath := filepath.Join(t.TempDir(), "missing-parent")
	projectPath := filepath.Join(parentPath, "KeyMaster")
	m := Model{ctx: context.Background(), svc: svc}
	inv := controlInvocationRawForTest(t, control.CapabilityProjectCreateAndStartEngineer, control.ProjectCreateAndStartEngineerInput{
		ParentPath:  parentPath,
		ProjectName: "KeyMaster",
		TodoText:    "Build it.",
		Prompt:      "Build it.",
		Provider:    control.ProviderCodex,
	})

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
	got := updated.(Model)
	created, ok := cmd().(bossProjectCreateAndStartEngineerCreatedMsg)
	if !ok || created.err == nil || created.folderExists || created.gitInitialized || created.projectTracked {
		t.Fatalf("missing-parent result = %#v, want failure before side effects", created)
	}
	updated, cmd = got.Update(created)
	got = updated.(Model)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = candidate
		}
	}
	if result.Err == nil || !strings.Contains(result.Status, "No folder, TODO, worktree, or engineer was created") {
		t.Fatalf("missing-parent receipt = %#v", result)
	}
	if _, err := os.Stat(projectPath); !os.IsNotExist(err) {
		t.Fatalf("target path should not exist, stat err = %v", err)
	}
	if !strings.Contains(got.status, "check repository parent path") {
		t.Fatalf("host status = %q, want parent-path failure", got.status)
	}
	waitForControlAsyncRefreshes(t, svc)
}

func TestExecuteBossTrackedWorktreeLaunchKeepsTodoWhenWorktreeFails(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	projectPath := filepath.Join(t.TempDir(), "not-a-git-repo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := svc.Store().UpsertProjectState(ctx, model.ProjectState{Path: projectPath, Name: "No Git", PresentOnDisk: true, InScope: true, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertProjectState() error = %v", err)
	}
	projects, err := svc.Store().ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	launches := 0
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		launches++
		return nil, nil
	})
	m := Model{ctx: ctx, svc: svc, allProjects: projects, projects: projects, codexManager: manager}
	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: trackedWorktreeControlInvocationForTest(t, control.TodoCreateWorktreeAndStartEngineerInput{
		ProjectPath: projectPath,
		ProjectName: "No Git",
		TodoText:    "Keep this request tracked if isolation is unavailable.",
		Prompt:      "Try to start this safely.",
		Provider:    control.ProviderCodex,
	})})
	got := updated.(Model)
	created := cmd().(bossTodoWorktreeTodoCreatedMsg)
	updated, cmd = got.Update(created)
	got = updated.(Model)
	var prepared bossTodoWorktreePreparedMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(bossTodoWorktreePreparedMsg); ok {
			prepared = candidate
		}
	}
	if prepared.err == nil {
		t.Fatalf("worktree preparation error = nil, want non-Git failure")
	}
	updated, cmd = got.Update(prepared)
	_ = updated.(Model)
	result, ok := cmd().(bossui.ControlInvocationResultMsg)
	if !ok || result.Err == nil || !strings.Contains(result.Status, "No engineer was launched") {
		t.Fatalf("failure result = %#v, want explicit no-launch receipt", result)
	}
	if launches != 0 {
		t.Fatalf("engineer launches = %d, want no root fallback", launches)
	}
	tracked, err := svc.Store().GetTodo(ctx, created.todo.ID)
	if err != nil || tracked.Done {
		t.Fatalf("tracked TODO after failure = %#v, err=%v", tracked, err)
	}
	waitForControlAsyncRefreshes(t, svc)
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

func TestExecuteSettingsUpdateControlAddsExcludeProjectPattern(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	cfg.ConfigPath = filepath.Join(cfg.DataDir, "config.toml")
	cfg.ScratchRoot = filepath.Join(cfg.DataDir, "tasks")
	cfg.ExcludeProjectPatterns = []string{"vendor"}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	svc := service.New(cfg, st, events.NewBus(), nil)
	settings := config.EditableSettingsFromAppConfig(cfg)
	m := Model{
		ctx:                    ctx,
		svc:                    svc,
		settingsBaseline:       &settings,
		settingsConfigPath:     cfg.ConfigPath,
		excludeProjectPatterns: []string{"vendor"},
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationRawForTest(t, control.CapabilitySettingsUpdate, control.SettingsUpdateInput{
			Changes: []control.SettingsChange{{
				Field:     control.SettingsFieldExcludeProjectPatterns,
				Operation: control.SettingsUpdateAppendUnique,
				Values:    []string{"tmp-*"},
			}},
		}),
	})
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want settings save command")
	}
	msgs := collectCmdMsgs(cmd)
	var saved settingsSavedMsg
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		switch typed := msg.(type) {
		case settingsSavedMsg:
			saved = typed
		case bossui.ControlInvocationResultMsg:
			result = typed
		}
	}
	if saved.err != nil {
		t.Fatalf("settings save error = %v", saved.err)
	}
	if result.Err != nil {
		t.Fatalf("control result error = %v", result.Err)
	}
	if !strings.Contains(result.Status, "added tmp-* to exclude project patterns") {
		t.Fatalf("result status = %q, want exclude pattern update summary", result.Status)
	}
	if got, want := saved.settings.ExcludeProjectPatterns, []string{"vendor", "tmp-*"}; !stringSlicesEqual(got, want) {
		t.Fatalf("saved exclude project patterns = %#v, want %#v", got, want)
	}
	raw, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if text := string(raw); !strings.Contains(text, `"tmp-*"`) || !strings.Contains(text, `"vendor"`) {
		t.Fatalf("saved config missing exclude project patterns: %q", text)
	}

	got := updated.(Model)
	updatedAfterSave, _ := got.Update(saved)
	got = updatedAfterSave.(Model)
	if got.excludeProjectPatterns == nil || !stringSlicesEqual(got.excludeProjectPatterns, []string{"vendor", "tmp-*"}) {
		t.Fatalf("model exclude project patterns = %#v, want saved patterns", got.excludeProjectPatterns)
	}
}

func TestExecuteBossGitPrepareCommitControlOpensPreview(t *testing.T) {
	projectPath := t.TempDir()
	project := model.ProjectSummary{
		Path:                 projectPath,
		Name:                 "talk_gamedev_lessons",
		PresentOnDisk:        true,
		LatestSessionSummary: "Recent talk outline cleanup",
	}
	m := Model{
		ctx:          context.Background(),
		svc:          newControlTestService(t),
		allProjects:  []model.ProjectSummary{project},
		projects:     []model.ProjectSummary{project},
		helpChatMode: true,
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationRawForTest(t, control.CapabilityGitPrepareCommit, control.GitPrepareCommitInput{
			ProjectPath:     projectPath,
			ProjectName:     "talk_gamedev_lessons",
			Message:         "Publish talk cleanup",
			PushAfterCommit: true,
		}),
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want commit preview command")
	}
	if got.helpChatMode {
		t.Fatalf("helpChatMode = true, want Chat hidden while commit preview opens")
	}
	if got.commitPreview == nil {
		t.Fatalf("commitPreview = nil, want loading preview")
	}
	if got.commitPreview.Intent != service.GitActionFinish {
		t.Fatalf("commit preview intent = %q, want finish", got.commitPreview.Intent)
	}
	if got.commitPreview.ProjectPath != projectPath || got.commitPreview.ProjectName != "talk_gamedev_lessons" {
		t.Fatalf("commit preview target = %#v, want talk project", got.commitPreview)
	}
	if got.commitPreview.Message != "Publish talk cleanup" || got.commitPreviewMessageOverride != "Publish talk cleanup" {
		t.Fatalf("commit preview message = %q/%q, want seeded message", got.commitPreview.Message, got.commitPreviewMessageOverride)
	}
	if !got.commitPreviewRefreshing {
		t.Fatalf("commitPreviewRefreshing = false, want async preview loading")
	}
	if got.status != "Preparing finish preview..." {
		t.Fatalf("status = %q, want preparing finish preview", got.status)
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

func TestExecuteProjectArchiveControlArchivesAndUnarchivesProject(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := filepath.Join(t.TempDir(), "regular-project")
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "regular-project",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active projects: %v", err)
	}
	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: projects,
		projects:    projects,
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationRawForTest(t, control.CapabilityProjectArchive, control.ProjectArchiveInput{
		ProjectPath: projectPath,
		Action:      control.ProjectArchiveActionArchive,
	}))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("executeControlInvocation() cmd = %#v, want immediate archive", cmd)
	}
	if got.status != `Archived "regular-project"` {
		t.Fatalf("archive status = %q, want archive confirmation", got.status)
	}
	active, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active after archive: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("archived project should leave active list, got %#v", active)
	}
	archived, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("list historical after archive: %v", err)
	}
	if len(archived) != 1 || !archived[0].Archived {
		t.Fatalf("historical projects = %#v, want archived project", archived)
	}
	if _, ok := got.projectSummaryByPathAllProjects(projectPath); !ok {
		t.Fatalf("archived project should remain in local archived summaries")
	}

	updated, cmd = got.executeControlInvocation(controlInvocationRawForTest(t, control.CapabilityProjectArchive, control.ProjectArchiveInput{
		ProjectName: "regular-project",
		Action:      control.ProjectArchiveActionUnarchive,
	}))
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("executeControlInvocation() cmd = %#v, want immediate unarchive", cmd)
	}
	if got.status != `Unarchived "regular-project"` {
		t.Fatalf("unarchive status = %q, want unarchive confirmation", got.status)
	}
	active, err = st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active after unarchive: %v", err)
	}
	if len(active) != 1 || active[0].Archived {
		t.Fatalf("unarchived project should return to active list, got %#v", active)
	}
}

func TestExecuteProjectArchiveControlMovesLinkedWorktreeFamilyImmediately(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	parent := t.TempDir()
	rootPath := filepath.Join(parent, "repo")
	worktreePath := filepath.Join(parent, "repo--feature")
	for _, state := range []model.ProjectState{
		{
			Path:             rootPath,
			Name:             "repo",
			Status:           model.StatusIdle,
			PresentOnDisk:    true,
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindMain,
			InScope:          true,
			UpdatedAt:        time.Now(),
		},
		{
			Path:             worktreePath,
			Name:             "repo--feature",
			Status:           model.StatusIdle,
			PresentOnDisk:    true,
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindLinked,
			InScope:          true,
			UpdatedAt:        time.Now(),
		},
	} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("seed project %s: %v", state.Path, err)
		}
	}
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active projects: %v", err)
	}
	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: projects,
		projects:    projects,
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationRawForTest(t, control.CapabilityProjectArchive, control.ProjectArchiveInput{
		ProjectPath: rootPath,
		Action:      control.ProjectArchiveActionArchive,
	}))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("executeControlInvocation() cmd = %#v, want immediate archive", cmd)
	}
	if len(got.allProjects) != 0 || len(got.archivedProjects) != 2 {
		t.Fatalf("local project buckets after root archive = active %#v archived %#v, want whole family archived", got.allProjects, got.archivedProjects)
	}
	for _, project := range got.archivedProjects {
		if !project.Archived {
			t.Fatalf("local archived project %s retained archived=false", project.Path)
		}
	}

	updated, cmd = got.executeControlInvocation(controlInvocationRawForTest(t, control.CapabilityProjectArchive, control.ProjectArchiveInput{
		ProjectPath: rootPath,
		Action:      control.ProjectArchiveActionUnarchive,
	}))
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("executeControlInvocation() cmd = %#v, want immediate unarchive", cmd)
	}
	if len(got.allProjects) != 2 || len(got.archivedProjects) != 0 {
		t.Fatalf("local project buckets after root unarchive = active %#v archived %#v, want whole family active", got.allProjects, got.archivedProjects)
	}
	for _, project := range got.allProjects {
		if project.Archived {
			t.Fatalf("local active project %s retained archived=true", project.Path)
		}
	}
}

func TestExecuteProjectArchiveControlRejectsOutOfScopeUnarchive(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := filepath.Join(t.TempDir(), "outside-scope-project")
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "outside-scope-project",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       false,
		Archived:      false,
		UpdatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	archivedProjects, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("list historical projects: %v", err)
	}
	m := Model{
		ctx:              ctx,
		svc:              svc,
		archivedProjects: archivedProjects,
		projects:         archivedProjects,
		archiveMode:      projectArchiveArchived,
		sortMode:         sortByAttention,
		visibility:       visibilityAllFolders,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationRawForTest(t, control.CapabilityProjectArchive, control.ProjectArchiveInput{
		ProjectPath: projectPath,
		Action:      control.ProjectArchiveActionUnarchive,
	}))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("executeControlInvocation() cmd = %#v, want immediate rejection", cmd)
	}
	if got.status != `"outside-scope-project" is outside project scope` {
		t.Fatalf("status = %q, want outside-scope explanation", got.status)
	}
	active, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active after rejected unarchive: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("out-of-scope project should stay hidden from active list, got %#v", active)
	}
	summary, ok := got.projectSummaryByPathAllProjects(projectPath)
	if !ok || summary.Archived || summary.InScope || summary.ManuallyAdded {
		t.Fatalf("local out-of-scope project summary = %#v, ok=%v", summary, ok)
	}
}

func TestExecuteProjectArchiveControlArchivesBatch(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectOne := filepath.Join(t.TempDir(), "quickgame_01")
	projectTwo := filepath.Join(t.TempDir(), "quickgame_02")
	for _, project := range []model.ProjectState{
		{Path: projectOne, Name: "quickgame_01", Status: model.StatusIdle, PresentOnDisk: true, InScope: true, UpdatedAt: time.Now()},
		{Path: projectTwo, Name: "quickgame_02", Status: model.StatusIdle, PresentOnDisk: true, InScope: true, UpdatedAt: time.Now()},
	} {
		if err := st.UpsertProjectState(ctx, project); err != nil {
			t.Fatalf("seed project: %v", err)
		}
	}
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active projects: %v", err)
	}
	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: projects,
		projects:    projects,
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationRawForTest(t, control.CapabilityProjectArchive, control.ProjectArchiveInput{
		Action: control.ProjectArchiveActionArchive,
		Resources: []control.ResourceRef{
			{Kind: control.ResourceProject, ProjectPath: projectOne, Label: "quickgame_01"},
			{Kind: control.ResourceProject, ProjectPath: projectTwo, Label: "quickgame_02"},
		},
	}))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("executeControlInvocation() cmd = %#v, want immediate archive", cmd)
	}
	if got.status != "Archived 2 projects" {
		t.Fatalf("status = %q, want batch archive confirmation", got.status)
	}
	active, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active after archive: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("archived projects should leave active list, got %#v", active)
	}
	all, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("list historical after archive: %v", err)
	}
	for _, project := range all {
		if !project.Archived {
			t.Fatalf("project %s archived = false, want true", project.Path)
		}
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
	if !strings.Contains(result.Status, "Work on control-active-session is underway") {
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

func TestExecuteBossControlInvocationBlocksFreshPromptWhileSameEngineerTurnActive(t *testing.T) {
	projectPath := "/tmp/control-active-fresh-block"
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
	openCalls := 0
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		openCalls++
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
			Name:          "control-active-fresh-block",
			PresentOnDisk: true,
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationForTest(t, control.EngineerSendPromptInput{
			ProjectPath: projectPath,
			Provider:    control.ProviderCodex,
			SessionMode: control.SessionModeNew,
			Prompt:      "Start a separate fresh investigation.",
			Reveal:      false,
		}),
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want immediate result")
	}
	if !strings.Contains(got.status, "already running for project") {
		t.Fatalf("status = %q, want fresh-turn active-session refusal", got.status)
	}
	if openCalls != 1 {
		t.Fatalf("manager opens = %d, want only setup open", openCalls)
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
	if !strings.Contains(result.Status, "current turn to finish") {
		t.Fatalf("result status = %q, want current-turn wait guidance", result.Status)
	}
}

func TestExecuteBossControlInvocationDoesNotReplaceIdleEngineerSession(t *testing.T) {
	projectPath := "/tmp/control-idle-fresh-allowed"
	liveSession := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			ThreadID: "thread-idle",
			Started:  true,
			Phase:    codexapp.SessionPhaseIdle,
		},
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		if req.ForceNew {
			return &fakeCodexSession{
				projectPath: projectPath,
				snapshot: codexapp.Snapshot{
					Provider: codexapp.ProviderCodex,
					ThreadID: "thread-fresh",
					Started:  true,
					Phase:    codexapp.SessionPhaseIdle,
				},
			}, nil
		}
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
			Name:          "control-idle-fresh-allowed",
			PresentOnDisk: true,
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationForTest(t, control.EngineerSendPromptInput{
			ProjectPath: projectPath,
			Provider:    control.ProviderCodex,
			SessionMode: control.SessionModeNew,
			Prompt:      "Start a separate fresh investigation.",
			Reveal:      false,
		}),
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want refusal result")
	}
	if !strings.Contains(got.status, "idle turn does not show that its task is finished") {
		t.Fatalf("status = %q, want idle-session safety explanation", got.status)
	}
	msgs := collectCmdMsgs(cmd)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = typed
			break
		}
	}
	if result.Err == nil || !strings.Contains(result.Status, "dedicated worktree") {
		t.Fatalf("result = %#v, want refusal with worktree guidance", result)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %#v, want no replacement launch", requests)
	}
}

func TestExecuteBossControlInvocationBlocksFreshPromptWhenLatestSameProviderTurnUnfinished(t *testing.T) {
	projectPath := "/tmp/control-latest-turn-block"
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:                     projectPath,
			Name:                     "control-latest-turn-block",
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
			Provider:    control.ProviderCodex,
			SessionMode: control.SessionModeNew,
			Prompt:      "Start a separate fresh investigation.",
			Reveal:      false,
		}),
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want immediate result")
	}
	if !strings.Contains(got.status, "latest Codex engineer turn is still unfinished") {
		t.Fatalf("status = %q, want latest-turn refusal", got.status)
	}
	msgs := collectCmdMsgs(cmd)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = typed
			break
		}
	}
	if result.Err == nil || !strings.Contains(result.Status, "current turn to finish") {
		t.Fatalf("result = %#v, want current-turn wait guidance", result)
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
	if !strings.Contains(result.Status, "Work on Clean suspicious local processes is underway") ||
		strings.Contains(result.Status, "Created agent task") ||
		strings.Contains(result.Status, "prompt sent") ||
		strings.Contains(result.Status, "Alt+Up hides it") {
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
	if !strings.Contains(result.Status, "Work on Keep checking temp process cleanup is underway") ||
		strings.Contains(result.Status, "Continued agent task") ||
		strings.Contains(result.Status, "Attention row shows") {
		t.Fatalf("result status = %q, want high-level continued task launch status without UI narration", result.Status)
	}
	if result.Activity == nil || result.Activity.TaskID != task.ID || result.Activity.Title != "Keep checking temp process cleanup" {
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
		"Resume signal:",
		"explicit instruction to resume that goal now",
		"Report contract:",
		"Answer the user's exact request directly",
		"Preserve source, metric, timeframe, scope, negations, and explicit exclusions",
		"what was compared, what was kept, what was discarded, and the substantive differences",
		"whether content changed and summarize the meaningful changes",
		"Do not final-report only success/failure plus artifact links",
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

func TestExecuteBossControlInvocationBlocksFreshAgentTaskContinueWhileTurnActive(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Keep checking temp process cleanup",
		Kind:  model.AgentTaskKindAgent,
	})
	if err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}
	liveSession := &fakeCodexSession{
		projectPath: task.WorkspacePath,
		snapshot: codexapp.Snapshot{
			Provider:     codexapp.ProviderCodex,
			ThreadID:     "thread-agent-live",
			Started:      true,
			Busy:         true,
			Phase:        codexapp.SessionPhaseRunning,
			ActiveTurnID: "turn-agent-live",
		},
	}
	openCalls := 0
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		openCalls++
		return liveSession, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: task.WorkspacePath,
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	m := Model{
		ctx:          ctx,
		svc:          svc,
		codexManager: manager,
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationRawForTest(t, control.CapabilityAgentTaskContinue, control.AgentTaskContinueInput{
			TaskID:      task.ID,
			Provider:    control.ProviderCodex,
			SessionMode: control.SessionModeNew,
			Prompt:      "Start a fresh pass on the cleanup.",
			Reveal:      false,
		}),
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want immediate result")
	}
	if !strings.Contains(got.status, "already running for agent task "+task.ID) {
		t.Fatalf("status = %q, want active agent task refusal", got.status)
	}
	if openCalls != 1 {
		t.Fatalf("manager opens = %d, want only setup open", openCalls)
	}
	msgs := collectCmdMsgs(cmd)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = typed
			break
		}
	}
	if result.Err == nil || !strings.Contains(result.Status, "current turn to finish") {
		t.Fatalf("result = %#v, want current-turn wait guidance", result)
	}
}

func TestExecuteTodoAddControlAddsProjectTodo(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	projectPath := filepath.Join(t.TempDir(), "alpha")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := svc.Store().UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "Alpha",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       true,
		LastActivity:  time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProjectState() error = %v", err)
	}
	projects, err := svc.Store().ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: projects,
		projects:    projects,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationRawForTest(t, control.CapabilityTodoAdd, control.TodoAddInput{
		ProjectName: "Alpha",
		Text:        "Add the Boss Desk TODO list.",
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeControlInvocation() cmd = nil, want background TODO add")
	}
	created, ok := cmd().(bossTodoAddedMsg)
	if !ok || created.err != nil {
		t.Fatalf("TODO add command result = %#v", created)
	}
	updated, cmd = got.Update(created)
	got = updated.(Model)
	if !strings.Contains(got.status, "Added TODO #") || !strings.Contains(got.status, "It was not started") {
		t.Fatalf("status = %q, want durable TODO-only status", got.status)
	}
	if cmd == nil {
		t.Fatalf("TODO add result should emit Chat receipt and refresh")
	}
	detail, err := svc.Store().GetProjectDetail(ctx, projectPath, 0)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if len(detail.Todos) != 1 || detail.Todos[0].Text != "Add the Boss Desk TODO list." {
		t.Fatalf("todos = %#v, want added TODO", detail.Todos)
	}
	waitForControlAsyncRefreshes(t, svc)
}

func TestExecuteTodoCompleteControlMarksTodoDone(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	projectPath := filepath.Join(t.TempDir(), "alpha")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := svc.Store().UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "Alpha",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       true,
		LastActivity:  time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProjectState() error = %v", err)
	}
	todo, err := svc.AddTodo(ctx, projectPath, "Add Boss-managed TODO tracking.")
	if err != nil {
		t.Fatalf("AddTodo() error = %v", err)
	}
	projects, err := svc.Store().ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: projects,
		projects:    projects,
		bossTrackedTodos: map[string]bossTrackedTodo{
			bossTrackedTodoKey(projectPath, model.SessionSourceCodex, "thread-alpha-todo"): {
				ID:          todo.ID,
				Text:        todo.Text,
				ProjectPath: projectPath,
				Provider:    model.SessionSourceCodex,
				SessionID:   "thread-alpha-todo",
			},
		},
	}

	updated, cmd := m.executeControlInvocation(controlInvocationRawForTest(t, control.CapabilityTodoComplete, control.TodoCompleteInput{
		ProjectPath: projectPath,
		ProjectName: "Alpha",
		TodoID:      todo.ID,
		TodoText:    todo.Text,
		Evidence:    "Engineer reported the tracked flow is implemented.",
	}))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("executeControlInvocation() cmd = %#v, want immediate TODO completion", cmd)
	}
	if !strings.Contains(got.status, "Marked TODO #") || !strings.Contains(got.status, "Alpha") {
		t.Fatalf("status = %q, want TODO complete status", got.status)
	}
	completed, err := svc.Store().GetTodo(ctx, todo.ID)
	if err != nil {
		t.Fatalf("GetTodo() error = %v", err)
	}
	if !completed.Done || completed.CompletedAt.IsZero() {
		t.Fatalf("completed TODO = %#v, want done with timestamp", completed)
	}
	if len(got.bossTrackedTodos) != 0 {
		t.Fatalf("bossTrackedTodos = %#v, want cleared linked TODO", got.bossTrackedTodos)
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

func TestExecuteBossGoalRunStartsLCAgentTaskAndPersistsTrace(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		writeTUILCAgentTraceArtifact(t, firstNonEmptyTrimmed(req.AppDataDir, svc.Config().DataDir), req.ProjectPath, "lca-goal-session", "verified release diff")
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "lca-goal-session",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	proposal, err := bossrun.NormalizeGoalProposal(bossrun.GoalProposal{
		Run: bossrun.GoalRun{
			Kind:            bossrun.GoalKindLCAgentTask,
			Title:           "Verify release diff",
			Objective:       "Inspect the current diff and verify the relevant Go tests.",
			SuccessCriteria: "Report changed files and verification commands.",
		},
		Authority: bossrun.AuthorityGrant{
			Resources: []control.ResourceRef{{Kind: control.ResourceProject, ProjectPath: "/tmp/release", Label: "release repo"}},
		},
	})
	if err != nil {
		t.Fatalf("NormalizeGoalProposal() error = %v", err)
	}
	m := Model{
		ctx:          ctx,
		svc:          svc,
		codexManager: manager,
	}

	updated, cmd := m.executeBossGoalRun(bossui.GoalRunConfirmedMsg{Proposal: proposal})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossGoalRun() cmd = nil, want LCAgent launch command")
	}
	if !strings.Contains(got.status, "Starting LCAgent goal task") {
		t.Fatalf("status = %q, want LCAgent goal launch status", got.status)
	}
	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	var result bossui.GoalRunResultMsg
	for _, msg := range msgs {
		switch typed := msg.(type) {
		case codexSessionOpenedMsg:
			opened = typed
		case bossui.GoalRunResultMsg:
			result = typed
		}
	}
	if opened.err != nil || opened.projectPath == "" {
		t.Fatalf("opened = %#v, want successful LCAgent session open", opened)
	}
	if result.Err != nil {
		t.Fatalf("goal result err = %v", result.Err)
	}
	if !result.Result.Verified || len(result.Result.CreatedTaskIDs) != 1 {
		t.Fatalf("goal result = %#v, want verified created task", result.Result)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderLCAgent || !requests[0].ForceNew {
		t.Fatalf("launch request provider/forceNew = %q/%v, want LCAgent fresh", requests[0].Provider, requests[0].ForceNew)
	}
	for _, want := range []string{
		"Little Control Room agent task:",
		"LCR goal run:",
		"Inspect the current diff",
		"Report changed files and verification commands.",
		"release repo",
		"Run inside LCAgent low-autonomy",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("launch prompt missing %q:\n%s", want, requests[0].Prompt)
		}
	}
	task, err := svc.GetAgentTask(ctx, result.Result.CreatedTaskIDs[0])
	if err != nil {
		t.Fatalf("GetAgentTask() error = %v", err)
	}
	if task.Provider != model.SessionSourceLCAgent || task.SessionID != "lca-goal-session" {
		t.Fatalf("task tracking = %#v, want LCAgent session attached", task)
	}
	record, err := svc.Store().GetGoalRun(ctx, result.Result.RunID)
	if err != nil {
		t.Fatalf("GetGoalRun() error = %v", err)
	}
	if record.Proposal.Run.Status != bossrun.GoalStatusCompleted || !record.Result.Verified {
		t.Fatalf("stored goal = %#v, want completed verified LCAgent goal", record)
	}
	if record.Result.LCAgentSessionID != "lca-goal-session" || record.Result.LCAgentVerificationStatus != "verified" {
		t.Fatalf("stored LCAgent result = %#v, want harvested session and verification", record.Result)
	}
	if len(record.Result.LCAgentActualChecks) != 1 || record.Result.LCAgentActualChecks[0] != "go test ./... passed" ||
		record.Result.LCAgentPatchFeedback != 1 ||
		record.Result.LCAgentVerificationFeedback != 1 ||
		!strings.Contains(record.Result.LCAgentTokenUsage, "tokens: 150") ||
		!strings.Contains(record.Result.LCAgentTraceQuality, "patch feedback: 1") {
		t.Fatalf("stored LCAgent trace quality = %#v, want checks, feedback, and usage", record.Result)
	}
	if len(record.Trace) != 4 {
		t.Fatalf("stored trace length = %d, want create launch await verify trace", len(record.Trace))
	}
}

func writeTUILCAgentTraceArtifact(t *testing.T, dataDir, cwd, sessionID, summary string) {
	t.Helper()
	started := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
	path := filepath.Join(dataDir, "lcagent", "sessions", started.Format("2006"), started.Format("01"), started.Format("02"), sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir trace artifact: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create trace artifact: %v", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	events := []map[string]any{
		{"type": "session_meta", "id": sessionID, "cwd": cwd, "started_at": started.Format(time.RFC3339Nano)},
		{"type": "model_response", "session_id": sessionID, "timestamp": started.Add(500 * time.Millisecond).Format(time.RFC3339Nano), "model": "deepseek/test-model", "usage_summary": map[string]any{"input_tokens": 100, "output_tokens": 50, "total_tokens": 150, "cached_input_tokens": 25}},
		{"type": "patch_diff_summary", "session_id": sessionID, "timestamp": started.Add(time.Second).Format(time.RFC3339Nano), "summary": "README.md +1 -0"},
		{"type": "patch_feedback", "session_id": sessionID, "timestamp": started.Add(1500 * time.Millisecond).Format(time.RFC3339Nano), "stage": "apply", "path": "README.md", "message": "Patch feedback: README.md failed during apply."},
		{"type": "verification_check", "session_id": sessionID, "timestamp": started.Add(2 * time.Second).Format(time.RFC3339Nano), "command": "go test ./...", "status": "passed", "success": true},
		{"type": "verification_feedback", "session_id": sessionID, "timestamp": started.Add(2250 * time.Millisecond).Format(time.RFC3339Nano), "status": "failed", "command": "go test ./...", "message": "Verification feedback: go test ./... failed."},
		{"type": "verification_summary", "session_id": sessionID, "timestamp": started.Add(2500 * time.Millisecond).Format(time.RFC3339Nano), "status": "verified", "message": "Verification checks passed: go test ./..."},
		{"type": "turn_complete", "session_id": sessionID, "timestamp": started.Add(3 * time.Second).Format(time.RFC3339Nano), "summary": summary, "files_changed": []string{"README.md"}, "verification": []string{"go test ./..."}, "verification_status": "verified", "actual_checks": []map[string]any{{"command": "go test ./...", "status": "passed", "success": true}}},
	}
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatalf("write trace artifact: %v", err)
		}
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

func trackedWorktreeControlInvocationForTest(t *testing.T, input control.TodoCreateWorktreeAndStartEngineerInput) control.Invocation {
	t.Helper()
	if input.Provider == "" {
		input.Provider = control.ProviderAuto
	}
	return controlInvocationRawForTest(t, control.CapabilityTodoCreateWorktreeAndStartEngineer, input)
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

func waitForControlAsyncRefreshes(t *testing.T, svc *service.Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.WaitForAsyncProjectRefreshes(ctx); err != nil {
		t.Fatalf("wait for async project refreshes: %v", err)
	}
}
