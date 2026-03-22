package codexapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"lcroom/internal/codexcli"
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

func TestOpenCodePermissionOverrideForPreset(t *testing.T) {
	tests := []struct {
		name   string
		preset codexcli.Preset
		want   openCodePermissionOverride
	}{
		{
			name:   "yolo",
			preset: codexcli.PresetYolo,
			want: openCodePermissionOverride{
				Edit:              "allow",
				Bash:              "allow",
				WebFetch:          "allow",
				DoomLoop:          "allow",
				ExternalDirectory: "allow",
			},
		},
		{
			name:   "full-auto",
			preset: codexcli.PresetFullAuto,
			want: openCodePermissionOverride{
				Edit:              "allow",
				Bash:              "allow",
				WebFetch:          "allow",
				DoomLoop:          "ask",
				ExternalDirectory: "ask",
			},
		},
		{
			name:   "safe",
			preset: codexcli.PresetSafe,
			want: openCodePermissionOverride{
				Edit:              "ask",
				Bash:              "ask",
				WebFetch:          "ask",
				DoomLoop:          "ask",
				ExternalDirectory: "ask",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openCodePermissionOverrideForPreset(tt.preset); got != tt.want {
				t.Fatalf("openCodePermissionOverrideForPreset(%q) = %#v, want %#v", tt.preset, got, tt.want)
			}
		})
	}
}

func TestBuildOpenCodeServerCommandInjectsPresetConfig(t *testing.T) {
	cmd, err := buildOpenCodeServerCommand("/tmp/demo", codexcli.PresetSafe)
	if err != nil {
		t.Fatalf("buildOpenCodeServerCommand() error = %v", err)
	}
	if cmd.Dir != "/tmp/demo" {
		t.Fatalf("cmd.Dir = %q, want /tmp/demo", cmd.Dir)
	}
	if got := strings.Join(cmd.Args, " "); !strings.Contains(got, "opencode serve") {
		t.Fatalf("cmd.Args = %q, want opencode serve", got)
	}

	var configContent string
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "OPENCODE_CONFIG_CONTENT=") {
			configContent = strings.TrimPrefix(entry, "OPENCODE_CONFIG_CONTENT=")
			break
		}
	}
	if configContent == "" {
		t.Fatalf("cmd.Env missing OPENCODE_CONFIG_CONTENT: %#v", cmd.Env)
	}

	var cfg openCodeConfigOverride
	if err := json.Unmarshal([]byte(configContent), &cfg); err != nil {
		t.Fatalf("unmarshal injected config: %v", err)
	}
	if got := cfg.Permission; got != (openCodePermissionOverride{
		Edit:              "ask",
		Bash:              "ask",
		WebFetch:          "ask",
		DoomLoop:          "ask",
		ExternalDirectory: "ask",
	}) {
		t.Fatalf("cfg.Permission = %#v, want safe preset ask-everything override", got)
	}

	foundPath := false
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "PATH=") {
			foundPath = true
			break
		}
	}
	if !foundPath && os.Getenv("PATH") != "" {
		t.Fatalf("cmd.Env should preserve PATH from the parent environment")
	}
}

func TestBuildOpenCodeServerCommandOverridesPreExistingConfigEnv(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"permission":{"edit":"old"}}`)

	cmd, err := buildOpenCodeServerCommand("/tmp/demo", codexcli.PresetSafe)
	if err != nil {
		t.Fatalf("buildOpenCodeServerCommand() error = %v", err)
	}

	values := 0
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "OPENCODE_CONFIG_CONTENT=") {
			values++
		}
	}
	if values != 1 {
		t.Fatalf("expected exactly one OPENCODE_CONFIG_CONTENT entry, got %d", values)
	}

	var configContent string
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "OPENCODE_CONFIG_CONTENT=") {
			configContent = strings.TrimPrefix(entry, "OPENCODE_CONFIG_CONTENT=")
			break
		}
	}
	if configContent == "" {
		t.Fatal("cmd.Env missing OPENCODE_CONFIG_CONTENT")
	}
	if configContent == `{"permission":{"edit":"old"}}` {
		t.Fatalf("config env was not overridden: %q", configContent)
	}

	var cfg openCodeConfigOverride
	if err := json.Unmarshal([]byte(configContent), &cfg); err != nil {
		t.Fatalf("unmarshal injected config: %v", err)
	}
	if got := cfg.Permission; got != (openCodePermissionOverride{
		Edit:              "ask",
		Bash:              "ask",
		WebFetch:          "ask",
		DoomLoop:          "ask",
		ExternalDirectory: "ask",
	}) {
		t.Fatalf("cfg.Permission = %#v, want safe preset ask-everything override", got)
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

func TestOpenCodeSessionRefreshReconcilesStalePendingModelFromReplayedModel(t *testing.T) {
	session := newTestOpenCodeSession(t, `[
		{
			"info": {
				"id": "msg_assistant_01",
				"sessionID": "ses_test",
				"role": "assistant",
				"modelID": "gpt-5",
				"providerID": "openai"
			},
			"parts": []
		}
	]`)
	session.model = "openai/gpt-5.4"
	session.pendingFromLaunch = true
	session.pendingModel = "openai/gpt-5.4"
	session.pendingReasoning = "high"

	if err := session.refreshSessionState(context.Background(), false); err != nil {
		t.Fatalf("session.refreshSessionState() error = %v", err)
	}

	snapshot := session.Snapshot()
	if got := snapshot.Model; got != "openai/gpt-5" {
		t.Fatalf("snapshot.Model = %q, want %q", got, "openai/gpt-5")
	}
	if snapshot.PendingModel != "" || snapshot.PendingReasoning != "" {
		t.Fatalf("snapshot.PendingModel = %q, PendingReasoning = %q, want both empty", snapshot.PendingModel, snapshot.PendingReasoning)
	}

	if session.pendingFromLaunch {
		t.Fatalf("pendingFromLaunch should be cleared after reconcile")
	}
}

func TestOpenCodeSessionRefreshKeepsLaunchPendingWhenNoReplayedModel(t *testing.T) {
	session := newTestOpenCodeSession(t, `[]`)
	session.model = "openai/gpt-5"
	session.pendingFromLaunch = true
	session.pendingModel = "openai/gpt-5.4"
	session.pendingReasoning = "high"

	if err := session.refreshSessionState(context.Background(), false); err != nil {
		t.Fatalf("session.refreshSessionState() error = %v", err)
	}

	snapshot := session.Snapshot()
	if snapshot.Model != "openai/gpt-5" {
		t.Fatalf("snapshot.Model = %q, want %q", snapshot.Model, "openai/gpt-5")
	}
	if snapshot.PendingModel != "openai/gpt-5.4" {
		t.Fatalf("snapshot.PendingModel = %q, want %q", snapshot.PendingModel, "openai/gpt-5.4")
	}
	if snapshot.PendingReasoning != "high" {
		t.Fatalf("snapshot.PendingReasoning = %q, want %q", snapshot.PendingReasoning, "high")
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
