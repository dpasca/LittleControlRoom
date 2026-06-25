package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestPortsDialogShowsExternalAndManagedListeners(t *testing.T) {
	project := model.ProjectSummary{Name: "demo", Path: "/tmp/demo", PresentOnDisk: true}
	scannedAt := time.Date(2026, 6, 25, 10, 15, 0, 0, time.UTC)
	m := Model{
		projects:    []model.ProjectSummary{project},
		allProjects: []model.ProjectSummary{project},
		selected:    0,
		runtimeProcessSnapshots: []projectrun.Snapshot{{
			ID:          "default",
			ProjectPath: project.Path,
			Command:     "npm run dev",
			CWD:         project.Path,
			PID:         2200,
			PGID:        2200,
			Running:     true,
			Ports:       []int{3000},
		}},
		processReports: map[string]procinspect.ProjectReport{
			project.Path: {
				ProjectPath: project.Path,
				ScannedAt:   scannedAt,
				Instances: []procinspect.ProjectInstance{{
					Process: procinspect.Process{
						PID:     4321,
						PPID:    1,
						PGID:    4321,
						Command: "python3 -m http.server 4017",
						CWD:     "/tmp/demo/_site",
						Ports:   []int{4017},
					},
					ProjectPath: project.Path,
				}},
				Findings: []procinspect.Finding{{
					Process: procinspect.Process{
						PID:     4321,
						PPID:    1,
						PGID:    4321,
						Command: "python3 -m http.server 4017",
						CWD:     "/tmp/demo/_site",
						Ports:   []int{4017},
					},
					ProjectPath: project.Path,
					Reasons:     []string{"orphaned under PID 1", "listening on TCP ports"},
				}},
			},
		},
	}

	cmd := m.openPortsDialog()
	if cmd == nil {
		t.Fatalf("/ports should queue a process scan")
	}
	if m.portsDialog == nil {
		t.Fatalf("/ports should open the ports dialog")
	}
	if m.portsDialog.Loading {
		t.Fatalf("cached process report should let /ports render immediately")
	}
	if len(m.portsDialog.Items) != 2 {
		t.Fatalf("ports items len = %d, want 2: %#v", len(m.portsDialog.Items), m.portsDialog.Items)
	}

	rendered := ansi.Strip(m.renderPortsDialogContent(112, 36))
	for _, want := range []string{
		"Ports Inspector",
		"2 TCP listeners",
		"PORT 4017",
		"orphaned external",
		"python3 -m http.server 4017",
		"PORT 3000",
		"managed",
		"npm run dev",
		"Press s to stop this external listener",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("ports dialog missing %q:\n%s", want, rendered)
		}
	}
}

func TestPortsDialogStopSelectedExternalListenerOpensConfirmation(t *testing.T) {
	project := model.ProjectSummary{Name: "demo", Path: "/tmp/demo", PresentOnDisk: true}
	m := Model{
		projects:    []model.ProjectSummary{project},
		allProjects: []model.ProjectSummary{project},
		portsDialog: &portsDialogState{
			Items: []portsDialogItem{{
				ProjectPath: project.Path,
				Port:        4017,
				Snapshot: projectrun.Snapshot{
					ID:          "pid_4321",
					Name:        "local listener",
					External:    true,
					ProjectPath: project.Path,
					Command:     "python3 -m http.server 4017",
					CWD:         "/tmp/demo/_site",
					PID:         4321,
					PGID:        4321,
					Running:     true,
					Ports:       []int{4017},
				},
				ParentPID: 1,
				External:  true,
				Orphaned:  true,
			}},
		},
	}

	updated, cmd := m.updatePortsDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("external stop should wait for confirmation before returning a command")
	}
	if got.portsDialog != nil {
		t.Fatalf("ports dialog should close behind the stop confirmation")
	}
	if got.externalStopConfirm == nil || got.externalStopConfirm.PID != 4321 {
		t.Fatalf("external stop confirmation = %#v, want PID 4321", got.externalStopConfirm)
	}
	if got.externalStopConfirm.Selected != externalProcessStopConfirmFocusKeep {
		t.Fatalf("external stop confirmation should default to keep, got %d", got.externalStopConfirm.Selected)
	}
}

func TestPortsDialogDoesNotStopManagedRuntimeRows(t *testing.T) {
	m := Model{
		portsDialog: &portsDialogState{
			Items: []portsDialogItem{{
				ProjectPath:    "/tmp/demo",
				Port:           3000,
				Snapshot:       projectrun.Snapshot{PID: 2200, ProjectPath: "/tmp/demo", Running: true, Ports: []int{3000}},
				ManagedRuntime: true,
			}},
		},
	}

	updated, cmd := m.updatePortsDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("managed row should not return a stop command from /ports")
	}
	if got.externalStopConfirm != nil {
		t.Fatalf("managed row should not open external stop confirmation")
	}
	if !strings.Contains(got.status, "Only external project-local listeners") {
		t.Fatalf("status = %q, want external-only guidance", got.status)
	}
}
