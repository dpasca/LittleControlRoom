package agentquery

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

type fakeWakeupReader struct {
	*fakeReader
	wakeups []control.EngineerMessage
}

func (r fakeWakeupReader) ListScheduledEngineerMessages(context.Context, int) ([]control.EngineerMessage, error) {
	return r.wakeups, nil
}

func TestEngineerWakeupListStaysInsideProjectScope(t *testing.T) {
	origin, other := "/repos/origin", "/repos/other"
	base := newFakeReader([]model.ProjectSummary{
		{Path: origin, Name: "Origin", InScope: true},
		{Path: other, Name: "Other", InScope: true},
	})
	soon := time.Now().Add(time.Hour)
	grant := control.UnattendedGrant{FileChanges: true, ValidMinutes: 120}
	reader := fakeWakeupReader{fakeReader: base, wakeups: []control.EngineerMessage{
		{ID: "w-origin", ProjectPath: origin, Provider: control.ProviderCodex, TargetSessionID: "s1", Prompt: "Verify.", State: control.EngineerMessageQueued, DeliverAt: soon, ExpiresAt: soon.Add(time.Hour), Unattended: &grant},
		{ID: "w-other", ProjectPath: other, Provider: control.ProviderCodex, TargetSessionID: "s2", Prompt: "Other work.", State: control.EngineerMessageQueued, DeliverAt: soon, ExpiresAt: soon.Add(time.Hour)},
	}}
	executor, err := NewExecutor(Options{Reader: reader, OriginProjectPath: origin, Scope: ScopeProject})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(t.Context(), QueryEngineerWakeupList, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result["wakeups"])
	if !strings.Contains(string(encoded), "w-origin") || strings.Contains(string(encoded), "w-other") {
		t.Fatalf("wakeups = %s, want only the origin project's wakeup", encoded)
	}
	if !strings.Contains(string(encoded), "file changes") || !strings.Contains(string(encoded), `"state":"scheduled"`) {
		t.Fatalf("wakeup record lacks authority or state: %s", encoded)
	}
	if _, err := executor.Execute(t.Context(), QueryEngineerWakeupList, json.RawMessage(`{"project_path":"/repos/other"}`)); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("cross-project wakeup list error = %v, want scope rejection", err)
	}
}

type fakeBoardWatchReader struct {
	*fakeReader
	watches []control.BoardWatch
	events  map[string][]control.BoardWatchEventStatus
}

func (r fakeBoardWatchReader) GetBoardWatch(_ context.Context, id string) (control.BoardWatch, error) {
	for _, w := range r.watches {
		if w.ID == id {
			return w, nil
		}
	}
	return control.BoardWatch{}, sql.ErrNoRows
}

func (r fakeBoardWatchReader) ListBoardWatches(_ context.Context, project string, includeUnregistered bool) ([]control.BoardWatch, error) {
	var out []control.BoardWatch
	for _, w := range r.watches {
		if w.ProjectPath == project && (includeUnregistered || w.State != control.BoardWatchUnregistered) {
			out = append(out, w)
		}
	}
	return out, nil
}

func (r fakeBoardWatchReader) ListBoardWatchEventStatuses(_ context.Context, id string, _ int) ([]control.BoardWatchEventStatus, error) {
	return r.events[id], nil
}

func TestEngineerBoardWatchStatusReportsStateAndStaysInsideProject(t *testing.T) {
	origin, other := "/repos/origin", "/repos/other"
	now := time.Now()
	reader := fakeBoardWatchReader{
		fakeReader: newFakeReader([]model.ProjectSummary{{Path: origin, Name: "Origin", InScope: true}, {Path: other, Name: "Other", InScope: true}}),
		watches: []control.BoardWatch{
			{ID: "bw-origin", BoardID: "board-1", ProjectPath: origin, Provider: control.ProviderClaudeCode, TargetSessionID: "s1", OutboxPath: origin + "/e.jsonl", State: control.BoardWatchAttention, LastError: "the outbox is shorter than the saved cursor", CursorOffset: 42},
			{ID: "bw-other", BoardID: "board-2", ProjectPath: other, State: control.BoardWatchActive},
		},
		events: map[string][]control.BoardWatchEventStatus{"bw-origin": {
			{BoardWatchEvent: control.BoardWatchEvent{EventID: "ev-1", ItemID: "LP-203", State: control.BoardWatchEventQueued, MessageID: "m1", EventCreatedAt: now}, MessageState: control.EngineerMessageDelivered, DeliveredAt: now},
			{BoardWatchEvent: control.BoardWatchEvent{EventID: "ev-2", ItemID: "LP-203", State: control.BoardWatchEventExpired, EventCreatedAt: now}},
		}},
	}
	executor, err := NewExecutor(Options{Reader: reader, OriginProjectPath: origin, Scope: ScopeProject})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(t.Context(), QueryEngineerBoardWatchStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result["board_watches"])
	text := string(encoded)
	if !strings.Contains(text, "bw-origin") || strings.Contains(text, "bw-other") {
		t.Fatalf("watches = %s, want only the origin project's", text)
	}
	for _, want := range []string{`"state":"attention"`, "shorter than the saved cursor", `"delivery":"delivered"`, `"delivery":"expired"`, `"cursor_offset":42`} {
		if !strings.Contains(text, want) {
			t.Fatalf("status missing %q: %s", want, text)
		}
	}
	if _, err := executor.Execute(t.Context(), QueryEngineerBoardWatchStatus, json.RawMessage(`{"project_path":"/repos/other"}`)); err == nil {
		t.Fatal("cross-project board watch status was allowed")
	}
}

func TestEngineerBoardWatchStatusExactWatchIDIsScopedAndUncapped(t *testing.T) {
	origin, other := "/repos/origin", "/repos/other"
	reader := fakeBoardWatchReader{
		fakeReader: newFakeReader([]model.ProjectSummary{{Path: origin, Name: "Origin", InScope: true}, {Path: other, Name: "Other", InScope: true}}),
		watches: []control.BoardWatch{
			{ID: "bw-old", BoardID: "board-old", ProjectPath: origin, State: control.BoardWatchUnregistered, OutboxPath: origin + "/old.jsonl"},
			{ID: "bw-secret", BoardID: "board-secret", ProjectPath: other, State: control.BoardWatchActive, OutboxPath: other + "/secret.jsonl", TargetSessionID: "other-session"},
		},
	}
	executor, err := NewExecutor(Options{Reader: reader, OriginProjectPath: origin, Scope: ScopeProject})
	if err != nil {
		t.Fatal(err)
	}
	// The exact lookup finds a watch in any state even though the list hides unregistered rows.
	result, err := executor.Execute(t.Context(), QueryEngineerBoardWatchStatus, json.RawMessage(`{"watch_id":"bw-old","limit":50}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result["board_watches"])
	if !strings.Contains(string(encoded), "bw-old") || !strings.Contains(string(encoded), `"state":"unregistered"`) {
		t.Fatalf("exact lookup = %s", encoded)
	}
	// Another project's watch and a missing one produce the same error and leak nothing.
	var errs []string
	for _, args := range []string{`{"watch_id":"bw-secret"}`, `{"watch_id":"bw-missing"}`} {
		_, err := executor.Execute(t.Context(), QueryEngineerBoardWatchStatus, json.RawMessage(args))
		if err == nil {
			t.Fatalf("%s was served", args)
		}
		errs = append(errs, err.Error())
		for _, leaked := range []string{"board-secret", "secret.jsonl", "other-session", "/repos/other"} {
			if strings.Contains(err.Error(), leaked) {
				t.Fatalf("error leaks %q: %v", leaked, err)
			}
		}
	}
	if strings.ReplaceAll(errs[0], "bw-secret", "X") != strings.ReplaceAll(errs[1], "bw-missing", "X") {
		t.Fatalf("existence is observable: %q vs %q", errs[0], errs[1])
	}
	// Naming another project explicitly is still rejected by project scope.
	if _, err := executor.Execute(t.Context(), QueryEngineerBoardWatchStatus, json.RawMessage(`{"project_path":"/repos/other","watch_id":"bw-secret"}`)); err == nil {
		t.Fatal("cross-project exact lookup was allowed")
	}
	// Plain listing is unchanged and still hides history.
	listed, err := executor.Execute(t.Context(), QueryEngineerBoardWatchStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := json.Marshal(listed["board_watches"]); strings.Contains(string(text), "bw-old") {
		t.Fatalf("list included history: %s", text)
	}
}
