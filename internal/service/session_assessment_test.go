package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/claudeartifact"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/store"
)

// Use the real queue manager: a recording classifier alone cannot catch a
// missing artifact silently rejected by BuildClassificationRequest.
func TestEmbeddedAssessmentProviders(t *testing.T) {
	t.Parallel()
	for _, source := range []model.SessionSource{model.SessionSourceClaudeCode, model.SessionSourceOpenCode, model.SessionSourceLCAgent} {
		for _, refresh := range []bool{false, true} {
			name := string(source) + "/settled"
			if refresh {
				name = string(source) + "/repair_missing_artifact"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				root := t.TempDir()
				project := filepath.Join(root, "project_with spaces")
				if err := os.MkdirAll(project, 0o755); err != nil {
					t.Fatal(err)
				}
				cfg := config.Default()
				cfg.DataDir = root
				cfg.ClaudeCodeHome = filepath.Join(root, "claude")
				cfg.OpenCodeHome = filepath.Join(root, "opencode")
				now := time.Now().UTC().Truncate(time.Second)
				id, artifact := seedAssessmentArtifact(t, cfg, source, project, now)
				st, err := store.Open(filepath.Join(root, "state.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				state := model.ProjectState{Path: project, Name: "project", PresentOnDisk: true, InScope: true, UpdatedAt: now}
				if refresh {
					state.Sessions = []model.SessionEvidence{{
						Source: source, SessionID: id, Format: embeddedActivityDefaultFormat(source),
						ProjectPath: project, LastEventAt: now, LatestTurnStateKnown: true, LatestTurnCompleted: true,
					}}
				}
				if err := st.UpsertProjectState(ctx, state); err != nil {
					t.Fatal(err)
				}
				svc := New(cfg, st, events.NewBus(), nil)
				svc.SetSessionClassifier(sessionclassify.NewManager(st, nil, sessionclassify.Options{Client: unusedIntegrationClassifier{}}))
				if refresh {
					err = svc.RefreshProjectStatus(ctx, project)
				} else {
					err = svc.RecordEmbeddedSessionActivity(ctx, EmbeddedSessionActivity{
						ProjectPath: project, Source: source, SessionID: id, LastActivityAt: now,
						LatestTurnStateKnown: true, LatestTurnCompleted: true,
					})
				}
				if err != nil {
					t.Fatal(err)
				}
				queued, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
				if err != nil {
					t.Fatalf("assessment was not queued: %v", err)
				}
				if queued.Source != source || queued.ExternalID() != id || queued.SessionFile != artifact || queued.SnapshotHash == "" {
					t.Fatalf("wrong assessment identity/artifact: %+v", queued)
				}
				detail, err := st.GetProjectDetail(ctx, project, 1)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := sessionclassify.ExtractSnapshot(ctx, queued, detail.Sessions[0], sessionclassify.GitStatusSnapshot{})
				if err != nil {
					t.Fatal(err)
				}
				if len(snapshot.Transcript) != 2 || snapshot.Transcript[0].Text != "Fix the issue." || snapshot.Transcript[1].Text != "Fixed and tested." || !snapshot.LatestTurnCompleted {
					t.Fatalf("unexpected assessment input: %+v", snapshot)
				}
			})
		}
	}
}

func seedAssessmentArtifact(t *testing.T, cfg config.AppConfig, source model.SessionSource, project string, now time.Time) (string, string) {
	t.Helper()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	switch source {
	case model.SessionSourceClaudeCode:
		id := "claude-assessment"
		path := filepath.Join(cfg.ClaudeCodeHome, "projects", claudeartifact.ProjectDirectoryName(project), id+".jsonl")
		write(path, `{"type":"user","message":{"role":"user","content":"Fix the issue."}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Fixed and tested."}],"stop_reason":"end_turn"}}
`)
		return id, path
	case model.SessionSourceLCAgent:
		id, runID := "lct_assessment", "lca_latest_run"
		// This run began well before the latest activity. Filename lookup must
		// resolve the checkpoint's run ID even outside nearby date directories.
		path := filepath.Join(cfg.DataDir, "lcagent", "sessions", now.AddDate(0, 0, -10).Format("2006/01/02"), runID+".jsonl")
		write(path, `{"type":"session_meta","id":"`+runID+`","thread_id":"`+id+`"}
{"type":"user_message","message":"Fix the issue."}
{"type":"turn_complete","summary":"Fixed and tested."}
`)
		state, err := json.Marshal(map[string]any{"thread_id": id, "project_path": project, "last_run_id": runID, "updated_at": now})
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(cfg.DataDir, "lcagent", "threads", id, "state.json"), string(state))
		return id, path
	case model.SessionSourceOpenCode:
		path := filepath.Join(cfg.OpenCodeHome, "opencode.db")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		_, err = db.Exec(`
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, time_created INTEGER, data TEXT);
INSERT INTO message VALUES ('u', 'ses_assessment', 1, '{"role":"user"}'), ('a', 'ses_assessment', 2, '{"role":"assistant"}');
INSERT INTO part VALUES ('up', 'u', 1, '{"type":"text","text":"Fix the issue."}'), ('ap', 'a', 2, '{"type":"text","text":"Fixed and tested."}');
`)
		if err != nil {
			t.Fatal(err)
		}
		return "ses_assessment", path + "#session:ses_assessment"
	default:
		t.Fatalf("unsupported test provider: %s", source)
		return "", ""
	}
}

func TestLCAgentAssessmentRejectsAnotherProjectsThread(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	now := time.Now()
	id, _ := seedAssessmentArtifact(t, cfg, model.SessionSourceLCAgent, "/original-project", now)
	if path := resolveEmbeddedSessionFile(model.SessionSourceLCAgent, id, id, "/other-project", now, now, cfg); path != "" {
		t.Fatalf("resolved another project's artifact: %s", path)
	}
}
