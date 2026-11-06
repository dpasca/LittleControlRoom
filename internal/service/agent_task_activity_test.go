package service

import (
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

func TestTaskRefreshPersistsLiveWorkerActivity(t *testing.T) {
	svc, _, _ := repositoryTestFixture(t)
	st := svc.Store()
	task, err := st.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "live worker", Provider: model.SessionSourceCodex, SessionID: "worker", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	before := task.LastTouchedAt
	at := time.Now().Add(time.Minute)
	snap := codexapp.Snapshot{Provider: codexapp.ProviderCodex, ProjectPath: task.WorkspacePath, ThreadID: task.SessionID, Busy: true, LastBusyActivityAt: at}
	svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return []codexapp.Snapshot{snap} }, nil)
	if _, err := svc.ListOpenAgentTasks(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	activity, err := st.AgentTaskActivity(t.Context(), task, at)
	if err != nil || activity["state"] != "active" || activity["last_activity_at"] != at.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("activity = %#v, %v", activity, err)
	}
	fresh, err := st.GetAgentTask(t.Context(), task.ID)
	if err != nil || !fresh.LastTouchedAt.Equal(before) {
		t.Fatalf("activity changed workflow touch time: %#v %v", fresh, err)
	}
	snap.Busy, snap.Closed = false, true
	if _, err := svc.ListOpenAgentTasks(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	activity, err = st.AgentTaskActivity(t.Context(), task, at)
	if err != nil || activity["state"] != "exited" {
		t.Fatalf("exit = %#v, %v", activity, err)
	}
}
