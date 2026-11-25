package script

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/session"
	"lcroom/internal/model"
	"lcroom/internal/projectbrief"
	"lcroom/internal/store"
)

func TestNativeProjectBriefRecordsForBoundThread(t *testing.T) {
	ctx := t.Context()
	path := t.TempDir()
	db, err := store.Open(filepath.Join(path, "fixture.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpsertProjectState(ctx, model.ProjectState{Path: path, Name: "fixture", InScope: true, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := db.BindEngineerSession(ctx, path, model.SessionSourceLCAgent, "thread", "thread"); err != nil {
		t.Fatal(err)
	}
	events, sessionID, err := session.NewWriter(path, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	runner := Runner{
		Session: events, SessionID: sessionID,
		LCRBriefs: &projectbrief.Writer{Store: db, CheckoutPath: path, Provider: model.SessionSourceLCAgent, SessionKey: "thread"},
	}
	result, err := runner.RunTool(ctx, Action{
		Type: "tool_call", Tool: projectbrief.ToolName,
		Args: raw(`{"goal":"Run fixture","now":"Ready for review","needs":null,"next":"Review it"}`),
	})
	if err != nil || !result.Success {
		t.Fatalf("native brief = %#v, %v", result, err)
	}
	rows, err := db.ListProjectBriefs(ctx, []string{path})
	if err != nil || len(rows) != 1 || !strings.Contains(string(rows[0].Payload), "Ready for review") {
		t.Fatalf("brief rows = %#v, %v", rows, err)
	}
}
