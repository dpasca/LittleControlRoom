package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestCPUDialogShowsListedAndCapacityAdjustedTotals(t *testing.T) {
	m := Model{
		cpuDialog: &cpuDialogState{
			Processes: []procinspect.CPUProcess{
				{Process: procinspect.Process{PID: 1110, CPU: 76, Command: "fileproviderd"}},
				{Process: procinspect.Process{PID: 34857, CPU: 41, Command: "python worker.py"}},
			},
			TotalCPU:    280,
			LogicalCPUs: 8,
			ScannedAt:   time.Date(2026, 5, 5, 14, 46, 39, 0, time.UTC),
		},
	}

	rendered := ansi.Strip(m.renderCPUDialogContent(100, 40))
	for _, want := range []string{
		"Shown:",
		"15% (117% raw)",
		"Total:",
		"35% (280% raw)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("CPU dialog missing %q:\n%s", want, rendered)
		}
	}
}

func TestCPUProcessRowsStayWithinCompactLimit(t *testing.T) {
	processes := make([]procinspect.CPUProcess, 0, 18)
	for i := 0; i < 18; i++ {
		processes = append(processes, procinspect.CPUProcess{
			Process: procinspect.Process{
				PID:     1000 + i,
				CPU:     float64(80 - i),
				Mem:     0.1,
				Command: "worker",
			},
		})
	}
	m := Model{cpuDialog: &cpuDialogState{Processes: processes, Selected: 9}}

	rows := m.renderCPUProcessRows(100, cpuDialogVisibleProcessRows)
	if len(rows) > cpuDialogVisibleProcessRows {
		t.Fatalf("rows len = %d, want <= %d:\n%s", len(rows), cpuDialogVisibleProcessRows, strings.Join(rows, "\n"))
	}
	rendered := ansi.Strip(strings.Join(rows, "\n"))
	if !strings.Contains(rendered, "↑") || !strings.Contains(rendered, "↓") || !strings.Contains(rendered, "PID 1009") {
		t.Fatalf("compact rows should show both scroll hints and selected PID, got:\n%s", rendered)
	}
}

func TestCPUDialogActionsRenderOnOneLine(t *testing.T) {
	rendered := ansi.Strip(renderCPUDialogActions())
	compact := strings.Join(strings.Fields(rendered), " ")
	for _, want := range []string{"a ask engineer", "r refresh", "Esc close"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("actions missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "\n") {
		t.Fatalf("actions should render on one line: %q", rendered)
	}
}

func TestCPUDialogAskEngineerCreatesScratchTask(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "thread-cpu-task",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		ctx:          ctx,
		svc:          svc,
		codexManager: manager,
		cpuDialog: &cpuDialogState{
			Selected:    1,
			ScannedAt:   time.Date(2026, 5, 5, 14, 46, 39, 0, time.UTC),
			TotalCPU:    280,
			LogicalCPUs: 8,
			Processes: []procinspect.CPUProcess{
				{
					Process: procinspect.Process{
						PID:     1110,
						PPID:    1,
						PGID:    1110,
						Stat:    "R",
						CPU:     75.7,
						Mem:     0.2,
						Elapsed: "23-00:13:13",
						Command: "/System/Library/PrivateFrameworks/FileProvider.framework/Support/fileproviderd",
					},
					Reasons: []string{"high CPU 75.7%", "orphaned under PID 1"},
				},
				{
					Process: procinspect.Process{
						PID:     34857,
						PPID:    1,
						PGID:    34857,
						Stat:    "R",
						CPU:     41,
						Mem:     0.1,
						Elapsed: "02:11:09",
						Command: "python runaway.py",
						CWD:     "/tmp/demo",
					},
					ProjectPath: "/tmp/demo",
					Reasons:     []string{"orphaned under PID 1"},
				},
			},
		},
	}

	updated, cmd := m.updateCPUDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got := updated.(Model)
	if got.cpuDialog == nil {
		t.Fatalf("CPU dialog should stay open while reviewing the engineer prompt")
	}
	if got.cpuRemediationEditor == nil {
		t.Fatalf("a should open the CPU engineer prompt editor")
	}
	if cmd == nil {
		t.Fatalf("a should focus the CPU engineer prompt editor")
	}
	if len(requests) != 0 {
		t.Fatalf("launch requests before prompt confirmation = %d, want 0", len(requests))
	}
	if !strings.Contains(got.cpuRemediationEditor.Input.Value(), "PID 34857") || !strings.Contains(got.cpuRemediationEditor.Input.Value(), "PID 1110") {
		t.Fatalf("CPU engineer prompt editor missing process details:\n%s", got.cpuRemediationEditor.Input.Value())
	}
	got.cpuRemediationEditor.Input.SetValue(got.cpuRemediationEditor.Input.Value() + "\nExtra operator note: leave my current foreground apps alone.")
	updated, cmd = got.updateCPURemediationEditorMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+s should create a CPU remediation task")
	}
	if got.cpuRemediationEditor != nil {
		t.Fatalf("CPU engineer prompt editor should close after dispatch")
	}
	if got.status != "Creating CPU task..." {
		t.Fatalf("status after dispatch = %q, want task creation", got.status)
	}

	createMsgs := collectCmdMsgs(cmd)
	var created cpuRemediationTaskCreatedMsg
	for _, msg := range createMsgs {
		if candidate, ok := msg.(cpuRemediationTaskCreatedMsg); ok {
			created = candidate
			break
		}
	}
	if created.err != nil {
		t.Fatalf("scratch task create err = %v", created.err)
	}
	if created.result.TaskPath == "" {
		t.Fatalf("create messages = %#v, want cpuRemediationTaskCreatedMsg", createMsgs)
	}

	updated, cmd = got.Update(created)
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("cpuRemediationTaskCreatedMsg should launch the task engineer")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != created.result.TaskPath || got.codexPendingOpen.showWhilePending {
		t.Fatalf("pending open = %#v, want hidden open in scratch task", got.codexPendingOpen)
	}
	project, ok := got.projectSummaryByPathAllProjects(created.result.TaskPath)
	if !ok {
		t.Fatalf("scratch task %q should be in the local project list", created.result.TaskPath)
	}
	if project.Kind != model.ProjectKindScratchTask || project.Name != cpuRemediationTaskTitle {
		t.Fatalf("project = %#v, want CPU scratch task", project)
	}

	var opened codexSessionOpenedMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			break
		}
	}
	if opened.projectPath == "" {
		t.Fatalf("launch command did not return codexSessionOpenedMsg")
	}
	if opened.err != nil {
		t.Fatalf("engineer launch err = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if !requests[0].ForceNew || requests[0].ProjectPath == "" {
		t.Fatalf("launch request = %#v, want fresh task workspace", requests[0])
	}
	if requests[0].ProjectPath != created.result.TaskPath {
		t.Fatalf("launch request path = %q, want scratch task path %q", requests[0].ProjectPath, created.result.TaskPath)
	}
	for _, want := range []string{
		"Little Control Room task:",
		"Title: Investigate and reduce CPU usage",
		"Allowed capabilities: process.inspect, process.terminate",
		"PID 34857",
		"PID 1110",
		"Do not terminate macOS system services",
		"If uncertain, do not kill the process",
		"Extra operator note: leave my current foreground apps alone.",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("launch prompt missing %q:\n%s", want, requests[0].Prompt)
		}
	}
	if strings.Contains(requests[0].Prompt, "Little Control Room agent task:") {
		t.Fatalf("CPU launch prompt should not use agent task framing:\n%s", requests[0].Prompt)
	}

	tasks, err := svc.ListOpenAgentTasks(ctx, 5)
	if err != nil {
		t.Fatalf("ListOpenAgentTasks() error = %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("open agent tasks = %#v, want none for /cpu remediation", tasks)
	}
	summaries, err := svc.Store().GetProjectSummaryMap(ctx)
	if err != nil {
		t.Fatalf("GetProjectSummaryMap() error = %v", err)
	}
	stored := summaries[created.result.TaskPath]
	if stored.Kind != model.ProjectKindScratchTask || !stored.PresentOnDisk {
		t.Fatalf("stored project = %#v, want present scratch task", stored)
	}
}

func TestCPUSnapshotHotTotalIsCapacityAdjusted(t *testing.T) {
	now := time.Date(2026, 5, 5, 14, 46, 39, 0, time.UTC)
	warmMulticore := procinspect.CPUSnapshot{
		TotalCPU:    200,
		LogicalCPUs: 8,
		ScannedAt:   now,
		Processes: []procinspect.CPUProcess{{
			Process: procinspect.Process{PID: 42, CPU: 20, Command: "worker"},
		}},
	}
	if cpuSnapshotIsHot(warmMulticore) {
		t.Fatalf("200%% raw on 8 CPUs should not be hot without a hot individual process")
	}

	hotMulticore := warmMulticore
	hotMulticore.TotalCPU = 700
	if !cpuSnapshotIsHot(hotMulticore) {
		t.Fatalf("700%% raw on 8 CPUs should be hot")
	}
}

func agentTaskHasProcessResource(task model.AgentTask, pid int) bool {
	for _, resource := range task.Resources {
		if model.NormalizeAgentTaskResourceKind(resource.Kind) == model.AgentTaskResourceProcess && resource.PID == pid {
			return true
		}
	}
	return false
}
