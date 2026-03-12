package codexapp

import "testing"

func TestNewOpenCodeHTTPClientHasNoGlobalTimeout(t *testing.T) {
	client := newOpenCodeHTTPClient()
	if client == nil {
		t.Fatalf("newOpenCodeHTTPClient() returned nil")
	}
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want 0 so the SSE stream can stay open", client.Timeout)
	}
}
