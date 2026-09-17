package codexapp

import "testing"

func TestStoppedSessionErrorRequiresStoppedWork(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot Snapshot
		want     bool
	}{
		{"idle failure", Snapshot{LastError: "Bad Request\ncodexErrorInfo: other"}, true},
		{"transport failure", Snapshot{LastError: "transport closed", Closed: true}, true},
		{"retrying", Snapshot{LastError: "retrying", Busy: true}, false},
		{"active turn", Snapshot{LastError: "retrying", ActiveTurnID: "turn"}, false},
		{"finishing", Snapshot{LastError: "retrying", Phase: SessionPhaseFinishing}, false},
		{"success", Snapshot{LatestTurnCompleted: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StoppedSessionError(tc.snapshot); (got != "") != tc.want {
				t.Fatalf("StoppedSessionError() = %q, want failure=%v", got, tc.want)
			}
		})
	}
}
