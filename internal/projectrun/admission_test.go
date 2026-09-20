package projectrun

import (
	"errors"
	"testing"
)

func TestRepositoryAdmissionRejectsProcessBeforeStarting(t *testing.T) {
	manager := NewManager()
	blocked := errors.New("repository owned by worker")
	manager.SetStartAdmission(func(project, cwd string) (func(), error) { return nil, blocked })
	if _, err := manager.Start(StartRequest{ProjectPath: t.TempDir(), Command: "exit 77"}); !errors.Is(err, blocked) {
		t.Fatalf("process admission=%v", err)
	}
	if len(manager.Snapshots()) != 0 {
		t.Fatal("rejected process started")
	}
}
