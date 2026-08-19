package demorecord

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestControllerStartsCapturesAndStopsRecording(t *testing.T) {
	controller := NewController()
	path := filepath.Join(t.TempDir(), "dynamic.lcrdemo")

	startedPath, err := controller.Start(path)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if startedPath != path || controller.Path() != path || !controller.Active() {
		t.Fatalf("active controller = path %q, reported %q, active %t", startedPath, controller.Path(), controller.Active())
	}

	controller.MarkInteraction()
	controller.Capture(80, 24, "recorded view")

	stoppedPath, stopped, err := controller.Stop()
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !stopped || stoppedPath != path {
		t.Fatalf("Stop() = path %q, stopped %t", stoppedPath, stopped)
	}
	if controller.Active() || controller.Path() != "" {
		t.Fatalf("controller remained active at %q", controller.Path())
	}

	reader, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	frame, err := reader.FrameAt(0)
	if err != nil {
		t.Fatalf("FrameAt() error = %v", err)
	}
	if frame.View != "recorded view" {
		t.Fatalf("recorded view = %q", frame.View)
	}
}

func TestControllerRejectsSecondActiveRecording(t *testing.T) {
	controller := NewController()
	firstPath := filepath.Join(t.TempDir(), "first.lcrdemo")
	if _, err := controller.Start(firstPath); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	t.Cleanup(func() {
		_ = controller.Close()
	})

	secondPath := filepath.Join(t.TempDir(), "second.lcrdemo")
	gotPath, err := controller.Start(secondPath)
	if err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("second Start() = path %q, error %v", gotPath, err)
	}
	if gotPath != firstPath || controller.Path() != firstPath {
		t.Fatalf("active path changed: Start() %q, controller %q", gotPath, controller.Path())
	}
}

func TestControllerStopIsNoOpWhenInactive(t *testing.T) {
	path, stopped, err := NewController().Stop()
	if err != nil || stopped || path != "" {
		t.Fatalf("Stop() = path %q, stopped %t, error %v", path, stopped, err)
	}
}

func TestControllerCanStartAnotherRecordingAfterStop(t *testing.T) {
	controller := NewController()
	dir := t.TempDir()
	for _, name := range []string{"first.lcrdemo", "second.lcrdemo"} {
		path := filepath.Join(dir, name)
		if _, err := controller.Start(path); err != nil {
			t.Fatalf("Start(%q) error = %v", name, err)
		}
		controller.Capture(80, 24, name)
		if _, stopped, err := controller.Stop(); err != nil || !stopped {
			t.Fatalf("Stop(%q) = stopped %t, error %v", name, stopped, err)
		}
	}
}
