package service

import (
	"strings"
	"testing"

	"lcroom/internal/scanner"
)

func TestDiffStatSummary(t *testing.T) {
	t.Parallel()

	diffStat := " README.md | 3 ++-\n notes.txt | 1 +\n 2 files changed, 3 insertions(+), 1 deletion(-)"
	if got := diffStatSummary(diffStat); got != "2 files changed, 3 insertions(+), 1 deletion(-)" {
		t.Fatalf("diffStatSummary() = %q, want final summary line", got)
	}
}

func TestPushAvailability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  scanner.GitRepoStatus
		wantYes bool
		wantMsg string
	}{
		{
			name:    "no remote warns",
			wantYes: false,
			wantMsg: "no remote is configured",
		},
		{
			name:    "remote and upstream can push",
			status:  scanner.GitRepoStatus{HasRemote: true, HasUpstream: true},
			wantYes: true,
		},
		{
			name:    "behind upstream warns",
			status:  scanner.GitRepoStatus{HasRemote: true, HasUpstream: true, Behind: 2},
			wantYes: false,
			wantMsg: "behind upstream by 2 commit(s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotYes, gotMsg := pushAvailability(tt.status)
			if gotYes != tt.wantYes {
				t.Fatalf("pushAvailability() = (%v, %q), want (%v, ...)", gotYes, gotMsg, tt.wantYes)
			}
			if tt.wantMsg == "" {
				if gotMsg != "" {
					t.Fatalf("pushAvailability() warning = %q, want empty", gotMsg)
				}
				return
			}
			if !strings.Contains(gotMsg, tt.wantMsg) {
				t.Fatalf("pushAvailability() warning = %q, want substring %q", gotMsg, tt.wantMsg)
			}
		})
	}
}
