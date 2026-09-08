package tui

import (
	"reflect"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

type todoEditorKey struct {
	ProjectPath string
	TodoID      int64
}

type todoEditSnapshot struct {
	Text        string
	Row, Column int
	Attachments []model.TodoAttachment
}

func (d *todoEditorState) editSnapshot() todoEditSnapshot {
	_, row, col, _ := codexTextareaState(&d.Input)
	return todoEditSnapshot{d.Input.Value(), row, col, cloneTodoAttachments(d.Attachments)}
}

func (d *todoEditorState) recordEdit(before todoEditSnapshot) {
	if before.Text == d.Input.Value() && reflect.DeepEqual(before.Attachments, cloneTodoAttachments(d.Attachments)) {
		return
	}
	d.Undo = append(d.Undo, before)
	// Bound history memory even for very long editing sessions.
	if len(d.Undo) > 200 {
		d.Undo = append([]todoEditSnapshot(nil), d.Undo[len(d.Undo)-200:]...)
	}
	d.Redo = nil
}

func (d *todoEditorState) undoEdit(redo bool) {
	from, to := &d.Undo, &d.Redo
	if redo {
		from, to = to, from
	}
	if len(*from) == 0 {
		return
	}
	*to = append(*to, d.editSnapshot())
	snapshot := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	d.Input.SetValue(snapshot.Text)
	// SetValue places the cursor on the last logical line.
	for d.Input.Line() > snapshot.Row {
		d.Input.CursorUp()
	}
	d.Input.SetCursor(snapshot.Column)
	d.Attachments = cloneTodoAttachments(snapshot.Attachments)
}

func (m *Model) retainTodoEditor() {
	if m.todoEditor == nil {
		return
	}
	if m.todoEditorDrafts == nil {
		m.todoEditorDrafts = make(map[todoEditorKey]*todoEditorState)
	}
	d := m.todoEditor
	m.todoEditorDrafts[todoEditorKey{d.ProjectPath, d.TodoID}] = d
}

// Keep the actual widgets so cursor, scroll position and history survive a handoff.
func (m *Model) suspendTodoDialogs() {
	if m.todoDialog == nil && m.todoEditor == nil {
		return
	}
	if m.todoReturnDialog != nil || m.todoReturnEditor != nil {
		return
	}
	m.retainTodoEditor()
	m.todoReturnDialog, m.todoReturnEditor = m.todoDialog, m.todoEditor
	if m.todoEditor != nil {
		m.todoEditor.Input.Blur()
	}
	m.todoDialog, m.todoEditor = nil, nil
}

func (m *Model) restoreTodoDialogs() tea.Cmd {
	if m.todoReturnDialog == nil && m.todoReturnEditor == nil {
		return nil
	}
	if m.todoDialog != nil || m.todoEditor != nil {
		return nil
	}
	m.todoDialog, m.todoEditor = m.todoReturnDialog, m.todoReturnEditor
	m.todoReturnDialog, m.todoReturnEditor = nil, nil
	var focus tea.Cmd
	if m.todoEditor != nil {
		focus = m.todoEditor.Input.Focus()
	}
	var refresh tea.Cmd
	if m.todoDialog != nil {
		refresh = m.requestProjectDetailViewCmd(m.todoDialog.ProjectPath)
	}
	return batchCmds(focus, refresh)
}
