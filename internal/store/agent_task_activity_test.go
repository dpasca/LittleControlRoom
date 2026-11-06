package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestAgentTaskActivityTracksWorkerNotWorkflow(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC().Truncate(time.Second)
	for _, provider := range []model.SessionSource{model.SessionSourceCodex, model.SessionSourceClaudeCode, model.SessionSourceOpenCode, model.SessionSourceLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			task := model.AgentTask{ID: string(provider), Provider: provider, SessionID: "worker", LastTouchedAt: now.Add(-time.Hour)}
			if _, err := st.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{ID: task.ID, Title: "worker activity"}); err != nil {
				t.Fatal(err)
			}
			old := now.Add(-35 * time.Minute)
			if err := st.RecordAgentTaskActivity(t.Context(), task.ID, task.SessionID, old, false); err != nil {
				t.Fatal(err)
			}
			got, err := st.AgentTaskActivity(t.Context(), task, now)
			if err != nil || got["state"] != "quiet" || got["age_seconds"] != int64(2100) {
				t.Fatalf("quiet = %#v, %v", got, err)
			}
			if err := st.RecordAgentTaskActivity(t.Context(), task.ID, task.SessionID, now, false); err != nil {
				t.Fatal(err)
			}
			got, err = st.AgentTaskActivity(t.Context(), task, now)
			if err != nil || got["state"] != "active" || got["age_seconds"] != int64(0) {
				t.Fatalf("active = %#v, %v", got, err)
			}
			if err := st.RecordAgentTaskActivity(t.Context(), task.ID, task.SessionID, now, true); err != nil {
				t.Fatal(err)
			}
			got, err = st.AgentTaskActivity(t.Context(), task, now)
			if err != nil || got["state"] != "exited" {
				t.Fatalf("exit = %#v, %v", got, err)
			}
			task.SessionID = "replacement"
			got, err = st.AgentTaskActivity(t.Context(), task, now)
			if err != nil || got["state"] != "unknown" {
				t.Fatalf("replacement inherited activity = %#v, %v", got, err)
			}
		})
	}
}

func TestAgentTaskActivityReadsCurrentLogWrite(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC().Truncate(time.Second)
	file := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, now, now); err != nil {
		t.Fatal(err)
	}
	_, err = st.db.Exec(`INSERT INTO projects(path, name, status, attention_score, pinned, updated_at) VALUES ('/worker', 'worker', 'idle', 0, 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.db.Exec(`INSERT INTO project_sessions (project_path, session_id, source, raw_session_id, session_file, last_event_at, format, snapshot_hash, error_count, updated_at) VALUES (?, ?, ?, ?, ?, ?, 'jsonl', '', 0, 0)`, "/worker", "worker", "codex", "worker", file, now.Add(-time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.AgentTaskActivity(t.Context(), model.AgentTask{ID: "task", Provider: model.SessionSourceCodex, SessionID: "worker"}, now)
	if err != nil || got["source"] != "provider_log_write" || got["state"] != "active" {
		t.Fatalf("log activity = %#v, %v", got, err)
	}
}
