package tui

import (
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/lcagent/sessionmetrics"
)

func TestDevLCAgentReviewTodoTextIncludesActionableTraceContext(t *testing.T) {
	text := devLCAgentReviewTodoText(codexapp.Snapshot{
		ProjectPath: "/tmp/Leaf",
		ThreadID:    "lca_thread",
	}, codexapp.LCAgentTrace{
		SessionID:          "lca_session",
		ProjectPath:        "/tmp/Leaf",
		ArtifactPath:       "/tmp/lca_session.jsonl",
		FilesChanged:       []string{"Assets/Foo.cs"},
		VerificationStatus: "failed",
		TraceQuality: sessionmetrics.TraceQuality{
			Score: 30,
			Grade: "needs_attention",
		},
	}, nil)

	for _, want := range []string{
		"Review LCAgent trace-quality issue: needs_attention in Leaf",
		"Source project: /tmp/Leaf",
		"LCAgent session: lca_session",
		"Trace artifact: /tmp/lca_session.jsonl",
		"trace quality: 30/needs_attention",
		"Files changed: Assets/Foo.cs",
		"Review actions:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("devLCAgentReviewTodoText missing %q:\n%s", want, text)
		}
	}
}
