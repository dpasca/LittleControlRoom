package codexapp

import (
	"errors"
	"testing"
)

func TestRepositoryAdmissionRejectsAllProviderTurnsBeforeIO(t *testing.T) {
	blocked := errors.New("repository owned by visible worker")
	for _, provider := range []Provider{ProviderCodex, ProviderClaudeCode, ProviderOpenCode, ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			calls := 0
			admission := func() (func(), error) { calls++; return nil, blocked }
			var session Session
			switch provider {
			case ProviderCodex:
				session = &appServerSession{turnAdmission: admission}
			case ProviderClaudeCode:
				session = &claudeCodeSession{turnAdmission: admission}
			case ProviderOpenCode:
				session = &openCodeSession{turnAdmission: admission}
			case ProviderLCAgent:
				session = &lcagentSession{turnAdmission: admission}
			}
			if err := session.SubmitInput(Submission{Text: "edit the repository"}); !errors.Is(err, blocked) {
				t.Fatalf("got %v", err)
			}
			if calls != 1 {
				t.Fatalf("admission calls=%d", calls)
			}
			if err := session.SubmitInput(Submission{}); err != nil || calls != 1 {
				t.Fatal("empty input acquired ownership")
			}
		})
	}
}

func TestRepositoryAdmissionGuardsCodexAutonomousGoalsAndReview(t *testing.T) {
	blocked := errors.New("repository owned")
	session := &appServerSession{turnAdmission: func() (func(), error) { return nil, blocked }}
	for _, start := range []func() error{func() error { return session.SetGoal("edit", nil) }, session.ResumeGoal, session.Review} {
		if err := start(); !errors.Is(err, blocked) {
			t.Fatalf("unguarded operation: %v", err)
		}
	}
}

func TestRepositoryAdmissionCoversLaunchUntilSessionRegistration(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		name := "interactive"
		if parallel {
			name = "parallel"
		}
		t.Run(name, func(t *testing.T) {
			calls, releases := 0, 0
			var later TurnAdmission
			var manager *Manager
			manager = NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
				later = req.TurnAdmission
				unlock, err := req.TurnAdmission(req.ProjectPath, req.TodoCaptureSessionKey)
				if err != nil {
					return nil, err
				}
				unlock()
				if calls != 1 || releases != 0 {
					t.Fatal("initial submission released admission before registration")
				}
				return &fakeSession{projectPath: req.ProjectPath, snapshot: Snapshot{Busy: true}}, nil
			})
			path := t.TempDir()
			manager.SetTurnAdmission(func(project, key string) (func(), error) {
				calls++
				return func() {
					releases++
					var registered bool
					if parallel {
						_, registered = manager.ParallelSession(project)
					} else {
						_, registered = manager.Session(project)
					}
					if !registered {
						t.Error("admission released before session became visible")
					}
				}, nil
			})
			req := LaunchRequest{ProjectPath: path, Prompt: "work"}
			var err error
			if parallel {
				_, _, err = manager.OpenParallel(req)
			} else {
				_, _, err = manager.Open(req)
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || releases != 1 {
				t.Fatalf("admission %d/%d", calls, releases)
			}
			unlock, err := later(path, "owner")
			if err != nil {
				t.Fatal(err)
			}
			unlock()
			if calls != 2 || releases != 2 {
				t.Fatal("later turn bypassed admission")
			}
			manager.CloseProject(path)
		})
	}
}

func TestRepositoryAdmissionDoesNotSkipFutureInputAfterReplayOnlyLaunch(t *testing.T) {
	calls := 0
	var later TurnAdmission
	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		// A resumed provider may discover that an interrupted turn already completed.
		later = req.TurnAdmission
		return &fakeSession{projectPath: req.ProjectPath}, nil
	})
	manager.SetTurnAdmission(func(path, key string) (func(), error) { calls++; return func() {}, nil })
	path := t.TempDir()
	if _, _, err := manager.Open(LaunchRequest{ProjectPath: path, Prompt: "continue saved turn"}); err != nil {
		t.Fatal(err)
	}
	unlock, err := later(path, "owner")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if calls != 2 {
		t.Fatalf("future submission bypassed admission: %d", calls)
	}
	manager.CloseProject(path)
}

func TestRepositoryAdmissionBindsExistingHostedSessions(t *testing.T) {
	var later TurnAdmission
	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		later = req.TurnAdmission
		return &fakeSession{projectPath: req.ProjectPath}, nil
	})
	path := t.TempDir()
	if _, _, err := manager.Open(LaunchRequest{ProjectPath: path}); err != nil {
		t.Fatal(err)
	}
	blocked := errors.New("owned by task")
	manager.SetTurnAdmission(func(path, key string) (func(), error) { return nil, blocked })
	if _, err := later(path, "caller"); !errors.Is(err, blocked) {
		t.Fatalf("preexisting session bypassed ownership: %v", err)
	}
	manager.CloseProject(path)
}

func TestStructuredReviewSubmissionRechecksCancellationBeforeProviderIO(t *testing.T) {
	for _, provider := range []Provider{ProviderCodex, ProviderClaudeCode, ProviderOpenCode, ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			held, released := false, false
			admission := func() (func(), error) { held = true; return func() { held = false; released = true }, nil }
			var session Session
			switch provider {
			case ProviderCodex:
				session = &appServerSession{turnAdmission: admission}
			case ProviderClaudeCode:
				session = &claudeCodeSession{turnAdmission: admission}
			case ProviderOpenCode:
				session = &openCodeSession{turnAdmission: admission}
			case ProviderLCAgent:
				session = &lcagentSession{turnAdmission: admission}
			}
			canceled := errors.New("result superseded")
			err := session.SubmitInput(Submission{Text: "review", RequireIdle: true, BeforeStart: func() error {
				if !held {
					t.Fatal("delivery check outside admission")
				}
				return canceled
			}})
			if !errors.Is(err, canceled) || !released {
				t.Fatalf("submission escaped cancellation: %v, released %t", err, released)
			}
		})
	}
}

func TestStructuredReviewRequiresIdleNativeSession(t *testing.T) {
	for _, session := range []Session{&appServerSession{busy: true}, &claudeCodeSession{busy: true}, &openCodeSession{busy: true}, &lcagentSession{busy: true}} {
		if err := session.SubmitInput(Submission{Text: "review", RequireIdle: true}); err == nil {
			t.Fatalf("steered busy %T", session)
		}
	}
}
