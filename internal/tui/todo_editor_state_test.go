package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

func editTodoKey(m Model, key tea.KeyMsg) Model {
	updated, _ := m.updateTodoEditorMode(key)
	return normalizeUpdateModel(updated)
}

func TestTodoDraftEscapePreservesHistoryAndProjectIsolation(t *testing.T) {
	m := newTodoEditorModelWithLines(t, 2)
	initial := m.todoEditor.Input.Value()
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" 日本語")})
	edited := m.todoEditor.Input.Value()
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.todoEditor != nil {
		t.Fatal("Escape should return to the TODO list")
	}
	m.todoDialog = &todoDialogState{ProjectPath: "/tmp/other"}
	m.openTodoEditor(0, "other", nil)
	if m.todoEditor.Input.Value() != "other" {
		t.Fatal("draft leaked across projects")
	}
	m.todoDialog = &todoDialogState{ProjectPath: "/tmp/demo"}
	m.openTodoEditor(0, "", nil)
	if m.todoEditor.Input.Value() != edited {
		t.Fatal("draft was lost")
	}
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	if m.todoEditor.Input.Value() != initial {
		t.Fatal("undo history was lost")
	}
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyCtrlY})
	if m.todoEditor.Input.Value() != edited {
		t.Fatal("redo did not restore text")
	}
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("new")})
	value := m.todoEditor.Input.Value()
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyCtrlY})
	if m.todoEditor.Input.Value() != value {
		t.Fatal("new edit must invalidate redo")
	}
}

func TestTodoDraftSurvivesEngineerHandoffAndUnrelatedCompletion(t *testing.T) {
	m := newTodoEditorModelWithLines(t, 3)
	editor, list := m.todoEditor, m.todoDialog
	list.Selected, list.Offset = 7, 3
	m.beginCodexPendingOpenWithOptions("/tmp/engineer", codexapp.ProviderCodex, true, true, true)
	if m.todoEditor != nil || m.todoDialog != nil {
		t.Fatal("TODO dialogs should be suspended")
	}
	updated, _ := m.Update(todoActionMsg{projectPath: "/tmp/other", status: "Updated"})
	m = normalizeUpdateModel(updated)
	updated, _ = m.hidePendingCodexOpen("/tmp/engineer")
	m = normalizeUpdateModel(updated)
	if m.todoEditor != editor || m.todoDialog != list || list.Selected != 7 || list.Offset != 3 {
		t.Fatal("handoff lost the editor or list position")
	}
	updated, _ = m.Update(todoActionMsg{projectPath: "/tmp/demo"})
	m = normalizeUpdateModel(updated)
	if m.todoEditor != editor {
		t.Fatal("background TODO action closed active editor")
	}
}

func TestTodoDraftClipboardCompletesWhileSuspendedAndUndoes(t *testing.T) {
	m := newTodoEditorModelWithLines(t, 1)
	initial := m.todoEditor.Input.Value()
	m.todoEditor.ClipboardBusy = true
	m.suspendTodoDialogs()
	updated, _ := m.applyTodoClipboardPasteMsg(todoClipboardPasteMsg{projectPath: "/tmp/demo", text: "\npasted"})
	m = normalizeUpdateModel(updated)
	m.restoreTodoDialogs()
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	if m.todoEditor.Input.Value() != initial {
		t.Fatal("clipboard undo failed")
	}
	m.todoEditor.ClipboardBusy = true
	updated, _ = m.applyTodoClipboardPasteMsg(todoClipboardPasteMsg{projectPath: "/tmp/demo", attachment: &model.TodoAttachment{Path: "/tmp/image.png"}})
	m = normalizeUpdateModel(updated)
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	if len(m.todoEditor.Attachments) != 0 {
		t.Fatal("attachment undo failed")
	}
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyCtrlY})
	if len(m.todoEditor.Attachments) != 1 {
		t.Fatal("attachment redo failed")
	}
}

func TestTodoSaveFailureRetainsHistoryAndSuccessClearsDraft(t *testing.T) {
	m := newTodoEditorModelWithLines(t, 1)
	initial := m.todoEditor.Input.Value()
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" edited")})
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	updated, _ := m.Update(todoActionMsg{projectPath: "/tmp/demo", err: errors.New("save failed")})
	m = normalizeUpdateModel(updated)
	if m.todoEditor == nil || m.todoEditor.Submitting {
		t.Fatal("failed save did not reopen editor")
	}
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	if m.todoEditor.Input.Value() != initial {
		t.Fatal("save failure lost history")
	}
	m = editTodoKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	updated, _ = m.Update(todoActionMsg{projectPath: "/tmp/demo"})
	m = normalizeUpdateModel(updated)
	m.openTodoEditor(0, "", nil)
	if m.todoEditor.Input.Value() != "" {
		t.Fatal("successful save left stale draft")
	}
}

func TestLegacyRecoveryTaskNestsUnderWorktreeAndRemainsDiscoverable(t *testing.T) {
	for _, status := range []model.AgentTaskStatus{model.AgentTaskStatusWaiting, model.AgentTaskStatusCompleted} {
		t.Run(string(status), func(t *testing.T) {
			task := model.AgentTask{
				ID: "recovery", Title: "Resolve submodule publish", Status: status,
				WorkspacePath: "/tmp/recovery", Capabilities: []string{"worktree.merge.recover"},
				Resources: []model.AgentTaskResource{
					{Kind: model.AgentTaskResourceProject, ProjectPath: "/tmp/repo--feature"},
					{Kind: model.AgentTaskResourceProject, ProjectPath: "/tmp/repo"},
				},
			}
			m := Model{
				openAgentTasks: []model.AgentTask{task}, visibility: visibilityAIFolders,
				allProjects: []model.ProjectSummary{
					{Path: "/tmp/repo", Kind: model.ProjectKindProject, WorktreeKind: model.WorktreeKindMain, WorktreeRootPath: "/tmp/repo", PresentOnDisk: true, ManuallyAdded: true},
					{Path: "/tmp/repo--feature", Kind: model.ProjectKindProject, WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: "/tmp/repo", PresentOnDisk: true, ManuallyAdded: true},
				},
			}
			m.rebuildProjectList(task.WorkspacePath)
			if len(m.projectRows) != 3 || m.projectRows[2].Kind != projectListRowAgentTask || m.projectRows[2].Indent != 2 {
				t.Fatalf("recovery task did not nest beneath its worktree: %#v", m.projectRows)
			}
			if got, ok := m.worktreeMergeRecoveryTaskForProjectPath("/tmp/repo--feature"); !ok || got.ID != task.ID {
				t.Fatal("recovery task inaccessible from worktree")
			}
		})
	}
}

func TestTaskRefreshFailureDoesNotRemoveRecoveryTask(t *testing.T) {
	task := model.AgentTask{ID: "recovery", Title: "Ask Engineer", WorkspacePath: "/tmp/recovery", Status: model.AgentTaskStatusWaiting}
	m := Model{openAgentTasks: []model.AgentTask{task}}
	updated, _ := m.Update(projectsMsg{agentTaskErr: errors.New("temporary task load failure")})
	m = normalizeUpdateModel(updated)
	if len(m.openAgentTasks) != 1 || m.indexByPath(task.WorkspacePath) < 0 {
		t.Fatal("temporary refresh failure removed the recovery task")
	}
	updated, _ = m.Update(projectsMsg{})
	m = normalizeUpdateModel(updated)
	if len(m.openAgentTasks) != 0 {
		t.Fatal("successful refresh should reconcile removed tasks")
	}
}
