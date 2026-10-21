package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

	"github.com/charmbracelet/x/ansi"
)

func TestConfirmedEngineerSendRecoversBeforeMailboxInsert(t *testing.T) {
	svc := newControlTestService(t)
	path := t.TempDir()
	input := control.EngineerSendPromptInput{ProjectPath: path, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, FollowCurrentSession: true, Prompt: "Recover confirmed send"}
	args, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := svc.Store().CreateControlOperation(t.Context(), control.Operation{ID: "lcrop_prequeue_restart", Provider: "codex", Source: "mcp", SessionKey: "caller", ProjectPath: "/caller", Invocation: control.Invocation{Capability: control.CapabilityEngineerSendPrompt, Args: args}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store().UpsertProjectState(t.Context(), model.ProjectState{Path: path, PresentOnDisk: true, InScope: true}); err != nil {
		t.Fatal(err)
	}
	operation, err = svc.Store().UpdateControlOperationStatus(t.Context(), operation.ID, control.OperationRunning, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := svc.Store().ListUnqueuedEngineerOperations(t.Context())
	if err != nil || len(pending) != 1 {
		t.Fatalf("confirmed send disappeared: %+v %v", pending, err)
	}
	m := Model{ctx: t.Context(), svc: svc}
	loaded := m.loadExternalControlProposalCmd(operation.ID)().(externalControlProposalLoadedMsg)
	if loaded.err != nil || !loaded.automatic {
		t.Fatalf("restart lost existing confirmation: %+v", loaded)
	}
	outcome := m.executeEngineerSendPromptControlWithOutcome(operation.Invocation, input)
	queued := outcome.cmd().(engineerMessageQueuedMsg)
	if queued.err != nil || !queued.message.CreatedAt.Equal(operation.StartedAt) {
		t.Fatalf("recovered request lost its enqueue time: %+v", queued)
	}
	pending, err = svc.Store().ListUnqueuedEngineerOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("already queued send recovered again: %+v %v", pending, err)
	}
}

func TestEngineerMessageWaitsThroughProjectReloadAndMissingSession(t *testing.T) {
	svc := newControlTestService(t)
	path := t.TempDir()
	if err := svc.Store().UpsertProjectState(t.Context(), model.ProjectState{Path: path, Name: "Reloaded project", PresentOnDisk: true, InScope: true}); err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: t.Context(), svc: svc}
	input := control.EngineerSendPromptInput{ProjectPath: path, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, FollowCurrentSession: true, Prompt: "Continue the queued task"}
	outcome := m.executeEngineerSendPromptControlWithOutcome(control.Invocation{}, input)
	if outcome.err != nil || outcome.cmd == nil {
		t.Fatalf("startup rejected queued request: %+v", outcome)
	}
	queued := outcome.cmd().(engineerMessageQueuedMsg)
	if queued.err != nil {
		t.Fatal(queued.err)
	}
	if queued.message.ExpiresAt.IsZero() {
		t.Fatal("ordinary handoff has no deadline")
	}
	for index, reason := range []string{"project-unloaded", "workspace-unavailable", "no-current-session"} {
		d := m.engineerMessageDisposition(queued.message)
		if !d.wait || d.failure != nil || d.waitReason != reason {
			t.Fatalf("expected durable wait %s: %+v", reason, d)
		}
		updated, cmd := m.applyEngineerMessagesLoaded(engineerMessagesLoadedMsg{messages: []control.EngineerMessage{queued.message}})
		m = updated.(Model)
		if cmd == nil {
			t.Fatal("wait did not persist an explanatory receipt")
		}
		receipt := cmd().(engineerMessageDeliveryRecordedMsg)
		if receipt.recordErr != nil || receipt.message.State != control.EngineerMessageQueued || !strings.Contains(receipt.message.LastError, "waiting") {
			t.Fatalf("wait receipt: %+v", receipt)
		}
		queued.message = receipt.message
		updated, _ = m.applyEngineerMessageDeliveryRecorded(receipt)
		m = updated.(Model)
		if err := svc.Store().RequeueDeliveringEngineerMessages(t.Context()); err != nil {
			t.Fatal(err)
		}
		m.allProjects = []model.ProjectSummary{{Path: path, PresentOnDisk: index > 0, LatestSessionFormat: "claude_code", LatestTurnStateKnown: true}}
	}
	live := &fakeCodexSession{projectPath: path, snapshot: codexapp.Snapshot{Provider: codexapp.ProviderCodex, ThreadID: "newly-opened", Started: true}}
	m.codexManager = codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return live, nil })
	if _, _, err := m.codexManager.Open(codexapp.LaunchRequest{ProjectPath: path, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatal(err)
	}
	d := m.engineerMessageDisposition(queued.message)
	if !d.deliver || d.bindSessionID != "newly-opened" || d.wait || d.failure != nil {
		t.Fatalf("reloaded project/session never became deliverable: %+v", d)
	}
}

func TestNestedAgentTaskGroupingShowsWorkspaceAndOrigin(t *testing.T) {
	now := time.Now()
	root := model.ProjectSummary{Path: "/repo", Name: "Parent", PresentOnDisk: true, ManuallyAdded: true}
	parent := model.AgentTask{ID: "parent", Title: "Parent task", WorkspacePath: "/internal/parent", OriginProjectPath: root.Path, OriginWorktreePath: root.Path, Status: model.AgentTaskStatusActive, LastTouchedAt: now}
	child := model.AgentTask{ID: "child", Title: "Nested task", ParentTaskID: parent.ID, WorkspacePath: "/internal/child", OriginProjectPath: root.Path, OriginWorktreePath: parent.WorkspacePath, Status: model.AgentTaskStatusActive, LastTouchedAt: now,
		OriginProvider: model.SessionSourceCodex, OriginSessionID: "originating-session", Repository: model.AgentTaskRepository{Isolated: true, InitialBranch: "original-branch"}, WorkspaceGit: model.AgentTaskWorkspaceGit{Branch: "changed-branch", Dirty: true}}
	m := Model{allProjects: []model.ProjectSummary{root}, openAgentTasks: []model.AgentTask{child, parent}, visibility: visibilityAIFolders, sortMode: sortByAttention}
	m.rebuildProjectList(child.WorkspacePath)
	if len(m.projects) != 3 || m.projects[0].Path != root.Path || m.projects[1].Path != parent.WorkspacePath || m.projects[2].Path != child.WorkspacePath || m.projectRows[2].Indent != 2 {
		t.Fatalf("nested task was hidden or detached: %+v %+v", m.projects, m.projectRows)
	}
	detail := ansi.Strip(m.renderAgentTaskDetailContent(child, 140))
	for _, want := range []string{child.WorkspacePath, "changed-branch", "local changes", "Branch changed from original-branch", "originating-session"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("workspace detail missing %q: %s", want, detail)
		}
	}
	if summary := agentTaskListSummary(child); !strings.Contains(summary, "changed-branch") || !strings.Contains(summary, "dirty") {
		t.Fatalf("workspace state missing from list: %s", summary)
	}
}

func TestNestedTaskCreationInheritsCanonicalProjectAndExactParent(t *testing.T) {
	svc := newControlTestService(t)
	root := t.TempDir()
	if err := svc.Store().UpsertProjectState(t.Context(), model.ProjectState{Path: root, Name: "Root", PresentOnDisk: true, InScope: true}); err != nil {
		t.Fatal(err)
	}
	parent, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Parent task", OriginProjectPath: root})
	if err != nil {
		t.Fatal(err)
	}
	input := control.AgentTaskCreateInput{Title: "Nested task", Kind: control.AgentTaskKindAgent, Provider: control.ProviderCodex}
	args, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	op, err := svc.Store().CreateControlOperation(t.Context(), control.Operation{ID: "lcrop_nested_creation", Provider: "codex", Source: "mcp", SessionKey: "parent-control", ProjectPath: parent.WorkspacePath, Invocation: control.Invocation{Capability: control.CapabilityAgentTaskCreate, Args: args}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store().BindEngineerSession(t.Context(), parent.WorkspacePath, model.SessionSourceCodex, "parent-control", "parent-session"); err != nil {
		t.Fatal(err)
	}
	created := (Model{ctx: t.Context(), svc: svc}).createBossAgentTaskCmd(op.Invocation, input, codexapp.ProviderCodex)().(bossAgentTaskCreatedMsg)
	if created.err != nil || created.task.ParentTaskID != parent.ID || created.task.OriginProjectPath != root || created.task.OriginWorktreePath != parent.WorkspacePath || created.task.OriginSessionID != "parent-session" || created.task.WorkspacePath == parent.WorkspacePath || !created.task.Repository.Isolated {
		t.Fatalf("nested origin or workspace lost: %+v", created)
	}
}
