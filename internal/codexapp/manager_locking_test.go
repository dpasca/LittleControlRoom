package codexapp

import (
	"fmt"
	"testing"
	"time"
)

type blockedManagerSession struct {
	*fakeSession
	readSnapshot func()
	readIdentity func()
}

func (s *blockedManagerSession) Snapshot() Snapshot {
	if s.readSnapshot != nil {
		s.readSnapshot()
	}
	return s.fakeSession.Snapshot()
}

func (s *blockedManagerSession) StateSnapshot() Snapshot {
	return s.Snapshot()
}

func (s *blockedManagerSession) MatchesResumeID(id string) bool {
	if s.readIdentity != nil {
		s.readIdentity()
	}
	return s.fakeSession.MatchesResumeID(id)
}

// A provider may notify the manager while holding its state lock. A manager
// read waiting for that state must leave notifications and UI lookups available.
func TestManagerSessionReadsDoNotBlockNotificationsOrLookups(t *testing.T) {
	for _, operation := range []string{"snapshots", "parallel snapshots", "open", "open parallel", "resume identity"} {
		t.Run(operation, func(t *testing.T) {
			const path = "/tmp/manager-locking"
			parallel := operation == "parallel snapshots" || operation == "open parallel"
			session := &blockedManagerSession{fakeSession: &fakeSession{
				projectPath: path,
				snapshot: Snapshot{
					Provider: ProviderCodex,
					ThreadID: "thread",
					Started:  true,
					Busy:     true,
				},
			}}
			manager := NewManagerWithFactory(func(LaunchRequest, func()) (Session, error) {
				return session, nil
			})
			t.Cleanup(func() { _ = manager.CloseAll() })
			req := LaunchRequest{ProjectPath: path, Provider: ProviderCodex}
			open := manager.Open
			if parallel {
				open = manager.OpenParallel
			}
			if _, _, err := open(req); err != nil {
				t.Fatal(err)
			}
			manager.AckUpdate(path)
			manager.AckParallelUpdate(path)

			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			block := func() {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-release
			}
			if operation == "resume identity" {
				session.readIdentity = block
				req.ResumeID = "thread"
				req.RequireResumeID = true
			} else {
				session.readSnapshot = block
			}
			done := make(chan error, 1)
			go func() {
				switch operation {
				case "snapshots", "parallel snapshots":
					read := manager.Snapshots
					if parallel {
						read = manager.ParallelSnapshots
					}
					got := read()
					if len(got) != 1 || got[0].ThreadID != "thread" {
						done <- fmt.Errorf("snapshots = %#v, want original session", got)
						return
					}
				default:
					got, reused, err := open(req)
					if err != nil || !reused || got != session {
						done <- fmt.Errorf("open: reused=%t err=%v, want original session", reused, err)
						return
					}
				}
				done <- nil
			}()
			defer func() {
				close(release)
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(2 * time.Second):
					t.Error("session read did not finish after release")
				}
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("manager did not begin session read")
			}

			available := make(chan bool, 1)
			go func() {
				if parallel {
					manager.notifyParallel(path)
					got, ok := manager.ParallelSession(path)
					available <- ok && got == session
				} else {
					manager.notify(path)
					got, ok := manager.Session(path)
					provider, known := manager.SessionProvider(path)
					available <- ok && got == session && known && provider == ProviderCodex
				}
			}()
			select {
			case ok := <-available:
				if !ok {
					t.Fatal("registered session was unavailable during its state read")
				}
			case <-time.After(time.Second):
				t.Fatal("provider notification and UI lookup blocked behind session read")
			}
		})
	}
}
