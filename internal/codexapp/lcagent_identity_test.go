package codexapp

import (
	"errors"
	"testing"
)

func TestLCAgentHostIdentityStaysBoundToRepositoryAdmission(t *testing.T) {
	for _, key := range []string{"", "host-launch-key"} {
		t.Run("key="+key, func(t *testing.T) {
			var admitted string
			session, err := newLCAgentSession(LaunchRequest{
				Provider: ProviderLCAgent, ProjectPath: t.TempDir(), AppDataDir: t.TempDir(),
				LCAgentProvider: "deepseek", PendingModel: "deepseek-flash", TodoCaptureSessionKey: key,
				TurnAdmission: func(path, key string) (func(), error) {
					admitted = key
					return func() {}, nil
				},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			before := session.Snapshot().ControlSessionKey
			if before == "" || (key != "" && before != key) {
				t.Fatalf("host identity missing before thread announcement: %q", before)
			}
			s := session.(*lcagentSession)
			s.handleEvent([]byte(`{"type":"session_meta","id":"lca_run_one","thread_id":"lct_worker"}`))
			stop := errors.New("stop before provider IO")
			for _, run := range []string{"lca_run_one", "lca_run_two"} {
				s.handleEvent([]byte(`{"type":"session_meta","id":"` + run + `","thread_id":"lct_worker"}`))
				snapshot := s.Snapshot()
				if snapshot.ThreadID != "lct_worker" || snapshot.ControlSessionKey != before {
					t.Fatalf("thread/run changed host ownership: %+v", snapshot)
				}
				if err := s.SubmitInput(Submission{Text: "continue", BeforeStart: func() error { return stop }}); !errors.Is(err, stop) {
					t.Fatal(err)
				}
				if admitted != snapshot.ControlSessionKey {
					t.Fatalf("snapshot key %q differs from admitted key %q", snapshot.ControlSessionKey, admitted)
				}
			}
		})
	}
}
