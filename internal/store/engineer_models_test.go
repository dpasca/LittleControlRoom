package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/control"
)

func TestEngineerSelectionSurvivesRestartAndIdempotency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	selection := control.EngineerModelSelection{Model: "exact-id", ReasoningEffort: "medium"}
	input := control.EngineerMessage{EngineerModelSelection: selection, OperationID: "operation", ProjectPath: "/repo", Provider: control.ProviderCodex, SessionMode: control.SessionModeNew, Prompt: "work"}
	msg, err := st.CreateEngineerMessage(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	catalog := control.EngineerModelCatalog{Provider: control.ProviderCodex, Source: "provider_listing", ObservedAt: time.Now(), Models: []control.EngineerModel{{Model: "exact-id", ReasoningEfforts: []string{"medium"}}}}
	if err := st.SaveEngineerModelCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.GetEngineerMessage(ctx, msg.ID)
	if err != nil || got.EngineerModelSelection != selection {
		t.Fatalf("lost durable choice: %+v %v", got, err)
	}
	input.ReasoningEffort = "high"
	if _, err := st.CreateEngineerMessage(ctx, input); err == nil {
		t.Fatal("idempotency accepted changed model selection")
	}
	cached, err := st.EngineerModelCatalog(ctx, control.ProviderCodex)
	if err != nil || len(cached.Models) != 1 || cached.Models[0].Model != "exact-id" {
		t.Fatalf("catalog: %+v %v", cached, err)
	}
}
