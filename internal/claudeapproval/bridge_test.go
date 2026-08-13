package claudeapproval

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestBridgeRoundTripsApprovalWithoutChangingToolInput(t *testing.T) {
	server, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	responseCh := make(chan Response, 1)
	errCh := make(chan error, 1)
	go func() {
		response, requestErr := RequestApproval(ctx, server.SocketPath(), Request{
			ID:        "toolu-1",
			ToolName:  "Write",
			ToolUseID: "toolu-1",
			Input:     json.RawMessage(`{"file_path":"/tmp/demo.txt","content":"demo"}`),
		})
		responseCh <- response
		errCh <- requestErr
	}()

	request := <-server.Requests()
	if request.ID != "toolu-1" || request.ToolName != "Write" || request.ToolUseID != "toolu-1" {
		t.Fatalf("bridge request = %#v", request)
	}
	if err := server.Respond(request.ID, Allow(request.Input)); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	response := <-responseCh
	if response.Behavior != "allow" || string(response.UpdatedInput) != string(request.Input) {
		t.Fatalf("approval response = %#v, want unchanged allow input", response)
	}
}

func TestBridgeCloseFailsPendingApprovalClosed(t *testing.T) {
	server, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	socketPath := server.SocketPath()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	responseCh := make(chan Response, 1)
	errCh := make(chan error, 1)
	go func() {
		response, requestErr := RequestApproval(ctx, socketPath, Request{
			ID:        "toolu-close",
			ToolName:  "Bash",
			ToolUseID: "toolu-close",
			Input:     json.RawMessage(`{"command":"make test"}`),
		})
		responseCh <- response
		errCh <- requestErr
	}()

	<-server.Requests()
	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("RequestApproval() error after bridge close = %v", err)
	}
	response := <-responseCh
	if response.Behavior != "deny" || !response.Interrupt {
		t.Fatalf("close response = %#v, want interrupting deny", response)
	}
	if _, err := os.Stat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("approval socket still exists after Close(): %v", err)
	}
}

func TestBridgeRejectsRequestDecodedAfterClose(t *testing.T) {
	server, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	if err := clientConn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set pipe deadline: %v", err)
	}
	go server.handleConnection(serverConn)

	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	request := Request{
		ID:       "toolu-after-close",
		ToolName: "Write",
		Input:    json.RawMessage(`{"file_path":"/tmp/demo.txt"}`),
	}
	if err := json.NewEncoder(clientConn).Encode(request); err != nil {
		t.Fatalf("encode late request: %v", err)
	}
	var response Response
	if err := json.NewDecoder(clientConn).Decode(&response); err != nil {
		t.Fatalf("decode late response: %v", err)
	}
	if response.Behavior != "deny" || !response.Interrupt {
		t.Fatalf("late response = %#v, want interrupting deny", response)
	}
	select {
	case request := <-server.Requests():
		t.Fatalf("closed bridge surfaced late request: %#v", request)
	default:
	}
}
