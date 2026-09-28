package sessionclassify

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestControlOutcomeRequeuesUnchangedTranscript(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	artifact := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(artifact, []byte(`{"type":"message","role":"user","content":[{"type":"text","text":"Move the queued copy and implement the remaining tasks."}]}
{"type":"message","role":"assistant","content":[{"type":"text","text":"Awaiting Ctrl+G confirmation; implementation still remains."}]}
`), 0600); err != nil {
		t.Fatal(err)
	}
	state := model.ProjectState{Path: root, Name: "test", InScope: true, PresentOnDisk: true, UpdatedAt: time.Now(), Sessions: []model.SessionEvidence{{
		Source: model.SessionSourceCodex, SessionID: "thread", ProjectPath: root, SessionFile: artifact, Format: "modern",
		LastEventAt: time.Now(), LatestTurnStateKnown: true, LatestTurnCompleted: true,
	}}}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := st.BindEngineerSession(ctx, root, model.SessionSourceCodex, "channel", "thread"); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(control.TodoAddInput{ProjectPath: root, Text: "queued copy"})
	if _, err := st.CreateControlOperation(ctx, control.Operation{ID: "op", Provider: "codex", ProjectPath: root, SessionKey: "channel",
		Invocation: control.Invocation{Capability: control.CapabilityTodoAdd, Args: args},
	}); err != nil {
		t.Fatal(err)
	}
	var seen SessionSnapshot
	manager := NewManager(st, nil, Options{Client: &fakeClassifier{seen: &seen, result: Result{
		Category: model.SessionCategoryNeedsFollowUp, Summary: "Implementation remains.", Confidence: 0.9,
	}}})
	previousHash := ""
	for _, status := range []control.OperationStatus{control.OperationWaitingForConfirmation, control.OperationRunning, control.OperationCompleted} {
		var result json.RawMessage
		if status == control.OperationCompleted {
			result = json.RawMessage(`{"status":"copy moved"}`)
		}
		if _, err := st.UpdateControlOperationStatus(ctx, "op", status, result, nil); err != nil {
			t.Fatal(err)
		}
		// Reuse the previous hash exactly as a cached, unchanged artifact would.
		state.Sessions[0].SnapshotHash = previousHash
		if queued, err := manager.QueueProject(ctx, state); err != nil || !queued {
			t.Fatalf("queue %s: %t, %v", status, queued, err)
		}
		queued, err := st.GetSessionClassification(ctx, "thread")
		if err != nil {
			t.Fatal(err)
		}
		if queued.SnapshotHash == previousHash {
			t.Fatal("control transition did not invalidate cached assessment")
		}
		if processed, err := manager.processOne(ctx); err != nil || !processed {
			t.Fatalf("process: %t, %v", processed, err)
		}
		if len(seen.ControlOperations) != 1 || seen.ControlOperations[0].Status != status {
			t.Fatalf("model evidence: %+v", seen.ControlOperations)
		}
		if seen.ControlOperations[0].Confirmed != (status != control.OperationWaitingForConfirmation) {
			t.Fatalf("confirmation evidence: %+v", seen.ControlOperations[0])
		}
		completed, err := st.GetSessionClassification(ctx, "thread")
		if err != nil {
			t.Fatal(err)
		}
		if completed.SnapshotHash != queued.SnapshotHash || completed.SnapshotHash != SnapshotHashForSnapshot(seen) {
			t.Fatal("queue and worker hashes disagree")
		}
		previousHash = completed.SnapshotHash
		state.Sessions[0].SnapshotHash = previousHash
		if queued, err := manager.QueueProject(ctx, state); err != nil || queued {
			t.Fatalf("unchanged evidence requeued: %t, %v", queued, err)
		}
	}
}

func TestControlEvidenceUsesExactBindingAndBounds(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	project := t.TempDir()
	for _, tc := range []struct{ id, path, provider, key, thread string }{
		{"a", project, "codex", "first-channel", "thread"},
		{"b", project, "codex", "resumed-channel", "thread"},
		{"c", project, "codex", "other-channel", "other-thread"},
		{"d", project, "claude_code", "first-channel", "thread"},
		{"e", t.TempDir(), "codex", "first-channel", "thread"},
		{"f", project, "codex", "thread", ""},
	} {
		if tc.thread != "" {
			if err := st.BindEngineerSession(ctx, tc.path, model.SessionSource(tc.provider), tc.key, tc.thread); err != nil {
				t.Fatal(err)
			}
		}
		args, _ := json.Marshal(control.TodoAddInput{ProjectPath: tc.path, Text: strings.Repeat("content ", 200)})
		if _, err := st.CreateControlOperation(ctx, control.Operation{ID: tc.id, Provider: tc.provider, ProjectPath: tc.path, SessionKey: tc.key,
			Invocation: control.Invocation{Capability: control.CapabilityTodoAdd, Args: args},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.UpdateControlOperationStatus(ctx, "a", control.OperationFailed, nil, errors.New(strings.Repeat("error ", 200))); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateControlOperationStatus(ctx, "b", control.OperationCanceled, nil, nil); err != nil {
		t.Fatal(err)
	}
	evidence, err := ControlOperationsForSession(ctx, st, project, model.SessionEvidence{SessionID: model.BuildCanonicalSessionID(model.SessionSourceCodex, "thread")})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 {
		t.Fatalf("cross-session evidence: %+v", evidence)
	}
	for _, op := range evidence {
		if op.ID != "a" && op.ID != "b" {
			t.Fatalf("unrelated operation: %+v", op)
		}
		if len(op.Arguments) > 800 || len(op.Error) > 400 {
			t.Fatalf("unbounded evidence: %+v", op)
		}
		if op.ID == "a" && (op.Status != control.OperationFailed || op.Error == "") {
			t.Fatalf("missing failure: %+v", op)
		}
		if op.ID == "b" && op.Status != control.OperationCanceled {
			t.Fatalf("missing cancellation: %+v", op)
		}
	}
	limited, err := st.ListSessionControlOperations(ctx, project, model.SessionSourceCodex, "thread", 1)
	if err != nil || len(limited) != 1 {
		t.Fatalf("bounded lookup: %+v, %v", limited, err)
	}
}
