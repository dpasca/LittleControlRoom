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

func TestStdioApprovalBrokerEmitsProcessRequestAndReceivesResult(t *testing.T) {
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
		strings.NewReader(`{"type":"process_response","id":"lca_process_1","result":{"success":true,"output":"started","command":"pnpm dev","cwd":"/repo/frontend"}}`+"\n"),
	)
	result, err := broker.RequestProcess(context.Background(), script.ProcessRequest{
		Action:  script.ProcessActionStart,
		Name:    "frontend",
		Command: "pnpm dev",
		CWD:     "frontend",
	})
	if err != nil {
		t.Fatalf("RequestProcess() error = %v", err)
	}
	if !result.Success || result.Output != "started" || result.Command != "pnpm dev" || result.CWD != "/repo/frontend" {
		t.Fatalf("result = %#v", result)
	}
	text := stream.String()
	for _, want := range []string{
		`"type":"process_request"`,
		`"id":"lca_process_1"`,
		`"action":"start"`,
		`"name":"frontend"`,
		`"command":"pnpm dev"`,
		`"cwd":"frontend"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestStdioApprovalBrokerEmitsProcessProjectPathWithoutDefaultCWD(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	broker := newStdioApprovalBroker(
		writer,
		sessionID,
		"/repo/site",
		strings.NewReader(`{"type":"process_response","id":"lca_process_1","result":{"success":true,"output":"started","command":"pnpm run export","cwd":"/repo/game"}}`+"\n"),
	)
	result, err := broker.RequestProcess(context.Background(), script.ProcessRequest{
		Action:      script.ProcessActionStart,
		ProjectPath: "../game",
		Name:        "promo-export",
		Command:     "pnpm run export",
	})
	if err != nil {
		t.Fatalf("RequestProcess() error = %v", err)
	}
	if !result.Success || result.CWD != "/repo/game" {
		t.Fatalf("result = %#v", result)
	}
	text := stream.String()
	for _, want := range []string{
		`"type":"process_request"`,
		`"id":"lca_process_1"`,
		`"project_path":"../game"`,
		`"command":"pnpm run export"`,
		`"name":"promo-export"`,
		`"cwd":""`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"cwd":"/repo/site"`) {
		t.Fatalf("process request defaulted cwd to session project despite project_path:\n%s", text)
	}
}
