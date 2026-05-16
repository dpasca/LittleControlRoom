package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"

	"github.com/charmbracelet/x/ansi"
)

func TestBuildProjectLCAgentTraceQualityAggregatesRecentSessions(t *testing.T) {
	dir := t.TempDir()
	latest := writeTUILCAgentQualityFixture(t, dir, "lca_latest", "/tmp/demo", strings.Join([]string{
		`{"type":"session_meta","id":"lca_latest","cwd":"/tmp/demo","started_at":"2026-05-12T10:00:00Z"}`,
		`{"type":"provider_failure","provider":"openrouter","kind":"rate_limited","message":"HTTP 429: slow down","retryable":true,"retrying":true}`,
		`{"type":"provider_retry","provider":"openrouter","attempt":2,"delay_ms":250}`,
		`{"type":"verification_check","command":"go test ./...","argv":["go","test","./..."],"purpose":"verify","status":"passed","success":true}`,
		`{"type":"verification_summary","status":"verified","message":"Verification checks passed: go test ./..."}`,
		`{"type":"turn_complete","session_id":"lca_latest","summary":"fixed tests","files_changed":["main.go"],"verification_status":"verified","actual_checks":[{"command":"go test ./...","status":"passed","success":true}]}`,
	}, "\n")+"\n")
	older := writeTUILCAgentQualityFixture(t, dir, "lca_old", "/tmp/demo", strings.Join([]string{
		`{"type":"session_meta","id":"lca_old","cwd":"/tmp/demo","started_at":"2026-05-12T09:00:00Z"}`,
		`{"type":"continuation","parent_session_id":"lca_parent","root_session_id":"lca_parent","chain_depth":1,"pending_status":"missing_after_changes","pending_files":["main.go"]}`,
		`{"type":"verification_summary","status":"missing_after_changes"}`,
		`{"type":"turn_complete","session_id":"lca_old","summary":"left pending verification","files_changed":["main.go"],"verification_status":"missing_after_changes"}`,
	}, "\n")+"\n")

	quality, err := buildProjectLCAgentTraceQuality("/tmp/demo", []model.SessionEvidence{
		{SessionID: "lca_old", Format: "lcagent_jsonl", SessionFile: older, LastEventAt: time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)},
		{SessionID: "lca_latest", Format: "lcagent_jsonl", SessionFile: latest, LastEventAt: time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("buildProjectLCAgentTraceQuality() error = %v", err)
	}
	if quality.ParsedSessions != 2 || quality.VerifiedSessions != 1 || quality.ProviderFailures != 1 || quality.ProviderRetries != 1 || quality.PendingSessions != 1 || quality.ContinuationCount != 1 {
		t.Fatalf("quality aggregate = %#v", quality)
	}
	summary := quality.summary()
	for _, want := range []string{"latest trace quality:", "checks: go test ./...", "last 2 sessions", "verified 1/2", "provider failures 1", "provider retries 1", "pending 1", "continuations 1"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %q", want, summary)
		}
	}
}

func TestRenderDetailContentShowsLCAgentTraceQuality(t *testing.T) {
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionFormat: "lcagent_jsonl",
	}
	m := Model{
		allProjects: []model.ProjectSummary{project},
		projects:    []model.ProjectSummary{project},
		detail:      model.ProjectDetail{Summary: project},
		lcagentTraceQuality: map[string]projectLCAgentTraceQuality{
			"/tmp/demo": {
				ProjectPath:       "/tmp/demo",
				ParsedSessions:    1,
				LatestQuality:     "trace quality: 91/good",
				LatestStatus:      "verified",
				LatestChecks:      []string{"go test ./... passed"},
				ScoredSessions:    1,
				ScoreTotal:        91,
				VerifiedSessions:  1,
				ProviderRetries:   1,
				AvailableSessions: 1,
			},
		},
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	for _, want := range []string{"LCAgent trace", "latest trace quality: 91/good", "checks: go test ./... passed", "provider retries 1"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderDetailContent() missing %q: %q", want, rendered)
		}
	}
}

func writeTUILCAgentQualityFixture(t *testing.T, dir, sessionID, projectPath, body string) string {
	t.Helper()
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write LCAgent fixture for %s in %s: %v", sessionID, projectPath, err)
	}
	return path
}
