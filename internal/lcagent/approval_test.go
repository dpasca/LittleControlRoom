package lcagent

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
)

func TestStdioApprovalBrokerEmitsRequestAndResolvedEvents(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	broker := newStdioApprovalBroker(
		writer,
		sessionID,
		"/repo",
		strings.NewReader(`{"type":"approval_response","id":"lca_approval_1","decision":"acceptForSession"}`+"\n"),
	)
	decision, err := broker.RequestCommandApproval(context.Background(), script.CommandApprovalRequest{
		Command: "corepack enable",
		Reason:  "requires medium autonomy",
		Scope:   "this exact command in /repo",
	})
	if err != nil {
		t.Fatalf("RequestCommandApproval() error = %v", err)
	}
	if decision != script.DecisionAcceptForSession {
		t.Fatalf("decision = %q, want acceptForSession", decision)
	}
	text := stream.String()
	for _, want := range []string{
		`"type":"approval_request"`,
		`"id":"lca_approval_1"`,
		`"command":"corepack enable"`,
		`"scope":"this exact command in /repo"`,
		`"type":"approval_resolved"`,
		`"decision":"acceptForSession"`,
		`"status":"approved"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}
