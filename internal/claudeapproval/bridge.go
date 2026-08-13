package claudeapproval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const PermissionToolName = "request_tool_approval"

type Request struct {
	ID        string          `json:"id"`
	ToolName  string          `json:"toolName"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"toolUseID,omitempty"`
}

type Response struct {
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
	Interrupt    bool            `json:"interrupt,omitempty"`
}

func Allow(input json.RawMessage) Response {
	return Response{
		Behavior:     "allow",
		UpdatedInput: cloneRawMessage(input),
	}
}

func Deny(message string, interrupt bool) Response {
	return Response{
		Behavior:  "deny",
		Message:   strings.TrimSpace(message),
		Interrupt: interrupt,
	}
}

type Server struct {
	listener   net.Listener
	tempDir    string
	socketPath string
	requests   chan Request
	done       chan struct{}
	closeOnce  sync.Once

	mu      sync.Mutex
	closed  bool
	pending map[string]chan Response
}

func NewServer() (*Server, error) {
	tempDir, err := os.MkdirTemp("", "lcroom-claude-approval-*")
	if err != nil {
		return nil, fmt.Errorf("create Claude approval socket directory: %w", err)
	}
	socketPath := filepath.Join(tempDir, "approval.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.Remove(tempDir)
		return nil, fmt.Errorf("listen for Claude approvals: %w", err)
	}
	server := &Server{
		listener:   listener,
		tempDir:    tempDir,
		socketPath: socketPath,
		requests:   make(chan Request, 8),
		done:       make(chan struct{}),
		pending:    make(map[string]chan Response),
	}
	go server.accept()
	return server, nil
}

func (s *Server) SocketPath() string {
	if s == nil {
		return ""
	}
	return s.socketPath
}

func (s *Server) Requests() <-chan Request {
	if s == nil {
		return nil
	}
	return s.requests
}

func (s *Server) Done() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.done
}

func (s *Server) Respond(id string, response Response) error {
	if s == nil {
		return errors.New("Claude approval bridge unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("Claude approval request id is required")
	}
	s.mu.Lock()
	responseCh, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("Claude approval request %q is no longer pending", id)
	}
	select {
	case responseCh <- response:
		return nil
	case <-s.done:
		return errors.New("Claude approval bridge is closed")
	}
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
		if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = err
		}
		s.mu.Lock()
		for id, responseCh := range s.pending {
			delete(s.pending, id)
			select {
			case responseCh <- Deny("Little Control Room closed the approval bridge", true):
			default:
			}
		}
		s.mu.Unlock()
		_ = os.Remove(s.socketPath)
		_ = os.Remove(s.tempDir)
	})
	return closeErr
}

func (s *Server) accept() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	var request Request
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		return
	}
	request.ID = strings.TrimSpace(request.ID)
	request.ToolName = strings.TrimSpace(request.ToolName)
	request.ToolUseID = strings.TrimSpace(request.ToolUseID)
	if request.ID == "" || request.ToolName == "" {
		_ = json.NewEncoder(conn).Encode(Deny("Malformed Claude approval request", false))
		return
	}
	if len(strings.TrimSpace(string(request.Input))) == 0 {
		request.Input = json.RawMessage(`{}`)
	}

	responseCh := make(chan Response, 1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(Deny("Little Control Room closed the approval bridge", true))
		return
	}
	if _, exists := s.pending[request.ID]; exists {
		s.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(Deny("Duplicate Claude approval request", false))
		return
	}
	s.pending[request.ID] = responseCh
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, request.ID)
		s.mu.Unlock()
	}()

	select {
	case s.requests <- cloneRequest(request):
	case <-s.done:
		return
	}

	var response Response
	select {
	case response = <-responseCh:
	case <-s.done:
		response = Deny("Little Control Room closed the approval bridge", true)
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func RequestApproval(ctx context.Context, socketPath string, request Request) (Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return Response{}, errors.New("Claude approval socket path is required")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return Response{}, fmt.Errorf("connect to Little Control Room approval bridge: %w", err)
	}
	defer conn.Close()

	stopClose := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopClose:
		}
	}()
	defer close(stopClose)

	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return Response{}, fmt.Errorf("send Claude approval request: %w", err)
	}
	var response Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, fmt.Errorf("read Claude approval response: %w", err)
	}
	if response.Behavior != "allow" && response.Behavior != "deny" {
		return Response{}, fmt.Errorf("invalid Claude approval behavior %q", response.Behavior)
	}
	return response, nil
}

func cloneRequest(request Request) Request {
	request.Input = cloneRawMessage(request.Input)
	return request
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}
