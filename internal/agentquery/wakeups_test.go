package agentquery

import (
	"context"
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
