package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/demorecord"

	"github.com/charmbracelet/x/ansi"
)

type fakeDemoRecordingController struct {
	active      bool
	path        string
	startPath   string
	startErr    error
	stopErr     error
	association demorecord.Association
}

func (c *fakeDemoRecordingController) Active() bool {
	return c.active
}

func (c *fakeDemoRecordingController) Path() string {
	return c.path
}

func (c *fakeDemoRecordingController) Start(path string) (string, error) {
	c.startPath = path
	if c.startErr != nil {
		return "", c.startErr
	}
	c.active = true
	c.path = path
	return path, nil
}

func (c *fakeDemoRecordingController) StartWithAssociation(path string, association demorecord.Association) (string, error) {
	c.association = association.Normalize()
	return c.Start(path)
}

func (c *fakeDemoRecordingController) Stop() (string, bool, error) {
	if !c.active {
		return "", false, c.stopErr
	}
	path := c.path
	c.active = false
	c.path = ""
	return path, true, c.stopErr
}

func TestRecordSlashCommandStartsAndStopsWithoutRestart(t *testing.T) {
	now := time.Date(2026, 8, 19, 14, 30, 45, 0, time.UTC)
	controller := &fakeDemoRecordingController{}
	m := Model{
		status:                  "Ready",
		appDataDirPath:          "/tmp/lcr-data",
		demoRecordingController: controller,
		nowFn: func() time.Time {
			return now
		},
	}

	started, startCmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRecord, Record: commands.RecordToggle})
	starting := normalizeUpdateModel(started)
	if startCmd == nil || !starting.demoRecordingBusy || starting.status != "Starting demo recording..." {
		t.Fatalf("starting state = busy %t, status %q, cmd %v", starting.demoRecordingBusy, starting.status, startCmd)
	}

	startMsg, ok := startCmd().(demoRecordingStartedMsg)
	if !ok {
		t.Fatalf("start command returned %T", startCmd())
	}
	wantPath := filepath.Join("/tmp/lcr-data", "demo-recordings", "lcr-demo-20260819-143045.lcrdemo")
	if controller.startPath != wantPath || startMsg.path != wantPath || startMsg.err != nil {
		t.Fatalf("start = requested %q, message %#v, want %q", controller.startPath, startMsg, wantPath)
	}

	activeModel, _ := starting.applyDemoRecordingStartedMsg(startMsg)
	active := normalizeUpdateModel(activeModel)
	if active.demoRecordingBusy || !controller.Active() || !strings.Contains(active.status, "Recording started:") {
		t.Fatalf("active state = busy %t, active %t, status %q", active.demoRecordingBusy, controller.Active(), active.status)
	}

	stopped, stopCmd := active.dispatchCommand(commands.Invocation{Kind: commands.KindRecord, Record: commands.RecordToggle})
	stopping := normalizeUpdateModel(stopped)
	if stopCmd == nil || !stopping.demoRecordingBusy || stopping.status != "Stopping demo recording..." {
		t.Fatalf("stopping state = busy %t, status %q, cmd %v", stopping.demoRecordingBusy, stopping.status, stopCmd)
	}
	stopMsg, ok := stopCmd().(demoRecordingStoppedMsg)
	if !ok {
		t.Fatalf("stop command returned %T", stopCmd())
	}
	finalModel, _ := stopping.applyDemoRecordingStoppedMsg(stopMsg)
	final := normalizeUpdateModel(finalModel)
	if final.demoRecordingBusy || controller.Active() || final.status != "Recording saved: "+wantPath {
		t.Fatalf("final state = busy %t, active %t, status %q", final.demoRecordingBusy, controller.Active(), final.status)
	}
}

func TestRecordSlashCommandSupportsExplicitPathAndStatus(t *testing.T) {
	controller := &fakeDemoRecordingController{}
	m := Model{demoRecordingController: controller}
	explicitPath := "/tmp/custom recording.lcrdemo"

	started, cmd := m.dispatchCommand(commands.Invocation{
		Kind:          commands.KindRecord,
		Record:        commands.RecordStart,
		RecordingPath: explicitPath,
	})
	if cmd == nil {
		t.Fatal("explicit record start returned nil command")
	}
	msg := cmd().(demoRecordingStartedMsg)
	activeModel, _ := normalizeUpdateModel(started).applyDemoRecordingStartedMsg(msg)
	active := normalizeUpdateModel(activeModel)
	if controller.startPath != explicitPath {
		t.Fatalf("Start() path = %q, want %q", controller.startPath, explicitPath)
	}

	statusModel, statusCmd := active.dispatchCommand(commands.Invocation{Kind: commands.KindRecord, Record: commands.RecordStatus})
	status := normalizeUpdateModel(statusModel)
	if statusCmd != nil || status.status != "Recording active: "+explicitPath {
		t.Fatalf("status = %q, cmd %v", status.status, statusCmd)
	}
}

func TestRecordSlashCommandAssociatesVisibleEmbeddedSession(t *testing.T) {
	projectPath := "/tmp/demo-project"
	controller := &fakeDemoRecordingController{}
	m := Model{
		demoRecordingController: controller,
		codexVisibleProject:     projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: {
				Provider:    codexapp.ProviderCodex,
				ProjectPath: projectPath,
				ThreadID:    "thread-demo",
				Started:     true,
			},
		},
	}
	_, cmd := m.dispatchCommand(commands.Invocation{
		Kind:          commands.KindRecord,
		Record:        commands.RecordStart,
		RecordingPath: "/tmp/associated.lcrdemo",
	})
	if cmd == nil {
		t.Fatal("record start returned nil command")
	}
	_ = cmd()
	if got := controller.association; got.ProjectPath != projectPath || got.Provider != "codex" || got.SessionID != "thread-demo" {
		t.Fatalf("recording association = %#v", got)
	}
}

func TestRecordSlashCommandSurfacesStartFailure(t *testing.T) {
	controller := &fakeDemoRecordingController{startErr: errors.New("destination exists")}
	m := Model{demoRecordingController: controller, appDataDirPath: t.TempDir()}

	started, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRecord, Record: commands.RecordStart})
	msg := cmd().(demoRecordingStartedMsg)
	failedModel, _ := normalizeUpdateModel(started).applyDemoRecordingStartedMsg(msg)
	failed := normalizeUpdateModel(failedModel)
	if failed.demoRecordingBusy || !strings.Contains(failed.status, "Demo recording start failed") {
		t.Fatalf("failed state = busy %t, status %q", failed.demoRecordingBusy, failed.status)
	}
	if len(failed.errorLogEntries) != 1 || !strings.Contains(failed.errorLogEntries[0].Message, "destination exists") {
		t.Fatalf("error log entries = %#v", failed.errorLogEntries)
	}
}

func TestRecordSlashCommandReportsBusyOperation(t *testing.T) {
	controller := &fakeDemoRecordingController{}
	m := Model{demoRecordingController: controller, demoRecordingBusy: true}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRecord, Record: commands.RecordStatus})
	got := normalizeUpdateModel(updated)
	if cmd != nil || got.status != "A demo recording operation is already in progress" {
		t.Fatalf("busy status = %q, cmd %v", got.status, cmd)
	}
}

func TestRenderTopStatusLineShowsRecordingBadge(t *testing.T) {
	controller := &fakeDemoRecordingController{active: true, path: "/tmp/demo.lcrdemo"}
	m := Model{status: "Ready", demoRecordingController: controller}

	rendered := ansi.Strip(m.renderTopStatusLine(160))
	if !strings.Contains(rendered, "REC") {
		t.Fatalf("top status line missing recording badge: %q", rendered)
	}
}

func TestEmbeddedFooterShowsRecordingStateAndFailure(t *testing.T) {
	controller := &fakeDemoRecordingController{active: true, path: "/tmp/demo.lcrdemo"}
	m := Model{status: "Recording started: /tmp/demo.lcrdemo", demoRecordingController: controller}
	if rendered := ansi.Strip(m.renderDemoRecordingFooterNotice()); !strings.Contains(rendered, "REC") {
		t.Fatalf("active embedded footer notice = %q", rendered)
	}

	controller.active = false
	m.status = "Demo recording start failed (use /errors)"
	if rendered := ansi.Strip(m.renderDemoRecordingFooterNotice()); !strings.Contains(rendered, "Demo recording start failed") {
		t.Fatalf("failed embedded footer notice = %q", rendered)
	}
}
