package codexapp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewOpenCodeHTTPClientHasNoGlobalTimeout(t *testing.T) {
	client := newOpenCodeHTTPClient()
	if client == nil {
		t.Fatalf("newOpenCodeHTTPClient() returned nil")
	}
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want 0 so the SSE stream can stay open", client.Timeout)
	}
}

func TestOpenCodePostJSONNilPayloadSendsEmptyJSONObject(t *testing.T) {
	var gotMethod string
	var gotContentType string
	var gotBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("content-type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ses_created"}`))
	}))
	t.Cleanup(server.Close)

	session := &openCodeSession{
		baseURL: server.URL,
		http:    server.Client(),
	}

	var out struct {
		ID string `json:"id"`
	}
	if err := session.postJSON(t.Context(), "/session", nil, &out); err != nil {
		t.Fatalf("postJSON() error = %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want %q", gotMethod, http.MethodPost)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type = %q, want application/json", gotContentType)
	}
	if gotBody != "{}" {
		t.Fatalf("body = %q, want {}", gotBody)
	}
	if out.ID != "ses_created" {
		t.Fatalf("response id = %q, want ses_created", out.ID)
	}
}

func TestOpenCodePostJSONMarshalsPayloadAsJSON(t *testing.T) {
	var got any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal request body: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	session := &openCodeSession{
		baseURL: server.URL,
		http:    server.Client(),
	}

	payload := openCodePermissionReply{Reply: "always", Message: "approved"}
	if err := session.postJSON(t.Context(), "/permission/reply", payload, nil); err != nil {
		t.Fatalf("postJSON() error = %v", err)
	}

	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("decoded payload = %#v, want object", got)
	}
	if gotMap["reply"] != "always" {
		t.Fatalf("reply = %#v, want always", gotMap["reply"])
	}
	if gotMap["message"] != "approved" {
		t.Fatalf("message = %#v, want approved", gotMap["message"])
	}
}

func TestOpenCodeSessionStatusIdleMarksTurnCompleted(t *testing.T) {
	session := newTestOpenCodeSession(t, "")

	session.handleEventData(`{"type":"session.status","properties":{"sessionID":"ses_test","status":{"type":"busy"}}}`)
	session.handleEventData(`{"type":"session.status","properties":{"sessionID":"ses_test","status":{"type":"idle"}}}`)

	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatalf("snapshot.Busy = true, want false")
	}
	if snapshot.BusyExternal {
		t.Fatalf("snapshot.BusyExternal = true, want false")
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("snapshot.ActiveTurnID = %q, want empty", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Turn completed" {
		t.Fatalf("snapshot.Status = %q, want %q", snapshot.Status, "Turn completed")
	}
	if snapshot.LastSystemNotice != "Turn completed" {
		t.Fatalf("snapshot.LastSystemNotice = %q, want %q", snapshot.LastSystemNotice, "Turn completed")
	}
}

func TestOpenCodeSessionIdleEventMarksTurnCompleted(t *testing.T) {
	session := newTestOpenCodeSession(t, "")

	session.handleEventData(`{"type":"session.status","properties":{"sessionID":"ses_test","status":{"type":"busy"}}}`)
	session.handleEventData(`{"type":"session.idle","properties":{"sessionID":"ses_test"}}`)

	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatalf("snapshot.Busy = true, want false")
	}
	if snapshot.Status != "Turn completed" {
		t.Fatalf("snapshot.Status = %q, want %q", snapshot.Status, "Turn completed")
	}
	if snapshot.LastSystemNotice != "Turn completed" {
		t.Fatalf("snapshot.LastSystemNotice = %q, want %q", snapshot.LastSystemNotice, "Turn completed")
	}
}

func TestOpenCodeSessionIdleAfterExternalBusyMarksSessionReady(t *testing.T) {
	session := newTestOpenCodeSession(t, "")
	session.busy = true
	session.busyExternal = true
	session.activeTurnID = "ses_test"
	session.status = "OpenCode is active in another process"

	session.handleEventData(`{"type":"session.status","properties":{"sessionID":"ses_test","status":{"type":"idle"}}}`)

	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatalf("snapshot.Busy = true, want false")
	}
	if snapshot.BusyExternal {
		t.Fatalf("snapshot.BusyExternal = true, want false")
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("snapshot.ActiveTurnID = %q, want empty", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "OpenCode session ready" {
		t.Fatalf("snapshot.Status = %q, want %q", snapshot.Status, "OpenCode session ready")
	}
	if snapshot.LastSystemNotice != "OpenCode session ready" {
		t.Fatalf("snapshot.LastSystemNotice = %q, want %q", snapshot.LastSystemNotice, "OpenCode session ready")
	}
}

func TestOpenCodeSessionIdleRefreshesErrorOnlyMessageIntoTranscript(t *testing.T) {
	session := newTestOpenCodeSession(t, `[
		{
			"info": {
				"id": "msg_err",
				"sessionID": "ses_test",
				"role": "assistant",
				"modelID": "gpt-5.4",
				"providerID": "openai",
				"agent": "build",
				"path": {
					"cwd": "/tmp/demo",
					"root": "/tmp/demo"
				},
				"time": {
					"created": 1,
					"completed": 2
				},
				"error": {
					"name": "APIError",
					"data": {
						"message": "Forbidden",
						"statusCode": 403,
						"metadata": {
							"url": "https://api.openai.com/v1/responses"
						}
					}
				}
			},
			"parts": []
		}
	]`)

	session.handleEventData(`{"type":"session.status","properties":{"sessionID":"ses_test","status":{"type":"busy"}}}`)
	session.handleEventData(`{"type":"session.idle","properties":{"sessionID":"ses_test"}}`)

	snapshot := session.Snapshot()
	if snapshot.Status != "OpenCode OpenAI request rejected (HTTP 403)" {
		t.Fatalf("snapshot.Status = %q, want OpenCode OpenAI request rejected (HTTP 403)", snapshot.Status)
	}
	if snapshot.LastError != "OpenCode OpenAI request rejected (HTTP 403)" {
		t.Fatalf("snapshot.LastError = %q, want OpenCode OpenAI request rejected (HTTP 403)", snapshot.LastError)
	}
	if len(snapshot.Entries) == 0 {
		t.Fatalf("snapshot.Entries should include an error entry")
	}
	last := snapshot.Entries[len(snapshot.Entries)-1]
	if last.Kind != TranscriptError {
		t.Fatalf("last.Kind = %q, want %q", last.Kind, TranscriptError)
	}
	if !strings.Contains(last.Text, "opencode auth list") {
		t.Fatalf("last.Text = %q, want auth guidance", last.Text)
	}
	if !strings.Contains(last.Text, "HTTP 403 Forbidden") {
		t.Fatalf("last.Text = %q, want HTTP 403 detail", last.Text)
	}
}

func TestOpenCodeSessionMessageUpdatedShowsErrorImmediately(t *testing.T) {
	session := newTestOpenCodeSession(t, "")

	session.handleEventData(`{
		"type":"message.updated",
		"properties":{
			"info":{
				"id":"msg_err",
				"sessionID":"ses_test",
				"role":"assistant",
				"modelID":"gpt-5.4",
				"providerID":"openai",
				"agent":"build",
				"error":{
					"name":"APIError",
					"data":{
						"message":"Forbidden",
						"statusCode":403,
						"metadata":{
							"url":"https://api.openai.com/v1/responses"
						}
					}
				}
			}
		}
	}`)

	snapshot := session.Snapshot()
	if snapshot.Status != "OpenCode OpenAI request rejected (HTTP 403)" {
		t.Fatalf("snapshot.Status = %q, want OpenCode OpenAI request rejected (HTTP 403)", snapshot.Status)
	}
	if len(snapshot.Entries) != 1 {
		t.Fatalf("len(snapshot.Entries) = %d, want 1", len(snapshot.Entries))
	}
	if snapshot.Entries[0].Kind != TranscriptError {
		t.Fatalf("snapshot.Entries[0].Kind = %q, want %q", snapshot.Entries[0].Kind, TranscriptError)
	}
}

func newTestOpenCodeSession(t *testing.T, messagesResponse string) *openCodeSession {
	t.Helper()
	if strings.TrimSpace(messagesResponse) == "" {
		messagesResponse = `[]`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case "/session/ses_test/message":
			_, _ = w.Write([]byte(messagesResponse))
		case "/session/status":
			_, _ = w.Write([]byte(`{}`))
		case "/permission", "/question":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	return &openCodeSession{
		sessionID:         "ses_test",
		baseURL:           server.URL,
		http:              server.Client(),
		notify:            func() {},
		entryIndex:        make(map[string]int),
		messageRole:       make(map[string]string),
		partKind:          make(map[string]TranscriptKind),
		partType:          make(map[string]string),
		modelOptionsByKey: make(map[string]ModelOption),
	}
}
