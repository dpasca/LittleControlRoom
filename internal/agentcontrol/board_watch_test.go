package agentcontrol

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func boardWatchExecutor(t *testing.T, st *store.Store, project, key string) *Executor {
	t.Helper()
	e, err := NewExecutor(Options{Store: st, OriginProjectPath: project, Scope: control.AuthorityScopeProject, Source: "claude_code", Provider: "claude_code", SessionKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestBoardWatchRegistrationIsBoundToCallingProjectAndSession(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "board-watch.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	project, other := t.TempDir(), t.TempDir()
	for _, p := range []string{project, other} {
		if err := st.UpsertProjectState(ctx, model.ProjectState{Path: p, Name: filepath.Base(p), PresentOnDisk: true, InScope: true}); err != nil {
			t.Fatal(err)
		}
	}
	outbox := filepath.Join(project, "review-events.jsonl")
	register := func(e *Executor, mutate func(*control.EngineerBoardWatchRegisterInput)) (map[string]any, error) {
		in := control.EngineerBoardWatchRegisterInput{BoardID: "board-1", ProjectPath: project, Provider: control.ProviderClaudeCode, TargetSessionID: "director-1", OutboxPath: outbox}
		if mutate != nil {
			mutate(&in)
		}
		args, _ := json.Marshal(in)
		return e.Propose(ctx, string(control.CapabilityEngineerBoardWatchRegister), args, "")
	}

	director := boardWatchExecutor(t, st, project, "director-key")
	if _, err := register(director, nil); err == nil || !strings.Contains(err.Error(), "has not observed") {
		t.Fatalf("registration before the host observed the session identity: err=%v", err)
	}
	if err := st.BindEngineerSession(ctx, project, model.SessionSourceClaudeCode, "director-key", "director-1"); err != nil {
		t.Fatal(err)
	}

	cases := map[string]func(*control.EngineerBoardWatchRegisterInput){
		"another project": func(in *control.EngineerBoardWatchRegisterInput) {
			in.ProjectPath = other
			in.OutboxPath = filepath.Join(other, "e.jsonl")
		},
		"another session":  func(in *control.EngineerBoardWatchRegisterInput) { in.TargetSessionID = "someone-else" },
		"another provider": func(in *control.EngineerBoardWatchRegisterInput) { in.Provider = control.ProviderCodex },
		"outbox outside":   func(in *control.EngineerBoardWatchRegisterInput) { in.OutboxPath = filepath.Join(other, "e.jsonl") },
		"outbox not a jsonl": func(in *control.EngineerBoardWatchRegisterInput) {
			in.OutboxPath = filepath.Join(project, "answers.json")
		},
	}
	for name, mutate := range cases {
		if _, err := register(director, mutate); err == nil {
			t.Errorf("%s: proposal was accepted", name)
		}
	}

	report, err := register(director, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Registration is never silent: it waits for the operator's confirmation.
	if report["requires_new_user_turn"] != true || report["terminal"] != false {
		t.Fatalf("registration report = %#v", report)
	}
	if watches, _ := st.ListBoardWatches(ctx, project, false); len(watches) != 0 {
		t.Fatalf("watch registered before confirmation: %#v", watches)
	}
}

func TestBoardWatchUnregisterByOwnerSessionOnly(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "board-watch-stop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	project := t.TempDir()
	if err := st.UpsertProjectState(ctx, model.ProjectState{Path: project, Name: "p", PresentOnDisk: true, InScope: true}); err != nil {
		t.Fatal(err)
	}
	for key, id := range map[string]string{"director-key": "director-1", "bystander-key": "bystander-1"} {
		if err := st.BindEngineerSession(ctx, project, model.SessionSourceClaudeCode, key, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "events.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	watch, _, err := st.RegisterBoardWatch(ctx, control.BoardWatch{
		BoardID: "board-1", ProjectPath: project, Provider: control.ProviderClaudeCode, TargetSessionID: "director-1", OutboxPath: filepath.Join(project, "events.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(control.EngineerBoardWatchUnregisterInput{WatchID: watch.ID})
	if _, err := boardWatchExecutor(t, st, project, "bystander-key").Propose(ctx, string(control.CapabilityEngineerBoardWatchUnregister), args, ""); err == nil || !strings.Contains(err.Error(), "only the session this watch wakes") {
		t.Fatalf("bystander unregister err = %v", err)
	}
	if got, _ := st.GetBoardWatch(ctx, watch.ID); got.State != control.BoardWatchActive {
		t.Fatalf("watch = %#v", got)
	}
	result, err := boardWatchExecutor(t, st, project, "director-key").Propose(ctx, string(control.CapabilityEngineerBoardWatchUnregister), args, "")
	if err != nil {
		t.Fatal(err)
	}
	if result["operator_confirmation"] != false || result["state"] != control.BoardWatchUnregistered {
		t.Fatalf("unregister receipt = %#v", result)
	}
	if got, _ := st.GetBoardWatch(ctx, watch.ID); got.State != control.BoardWatchUnregistered {
		t.Fatalf("watch = %#v", got)
	}
}
