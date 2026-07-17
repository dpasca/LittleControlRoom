package script

import (
	"context"
	"strings"

	"lcroom/internal/lcagent/tools"
	"lcroom/internal/todocapture"
)

type ApprovalDecision string

const (
	DecisionAccept           ApprovalDecision = "accept"
	DecisionAcceptForSession ApprovalDecision = "acceptForSession"
	DecisionDecline          ApprovalDecision = "decline"
	DecisionCancel           ApprovalDecision = "cancel"
)

type CommandApprovalRequest struct {
	ID        string
	SessionID string
	Tool      string
	Command   string
	CWD       string
	Reason    string
	Scope     string
}

type ApprovalBroker interface {
	RequestCommandApproval(context.Context, CommandApprovalRequest) (ApprovalDecision, error)
}

type ProcessAction string

const (
	ProcessActionStart ProcessAction = "start"
	ProcessActionList  ProcessAction = "list"
	ProcessActionStop  ProcessAction = "stop"
)

type ProcessRequest struct {
	ID              string
	SessionID       string
	Action          ProcessAction
	ProjectPath     string
	ProcessID       string
	Name            string
	Command         string
	CWD             string
	Purpose         string
	CreateNew       bool
	ReplaceExisting bool
}

type ProcessBroker interface {
	RequestProcess(context.Context, ProcessRequest) (tools.ToolResult, error)
}

type ProjectTodoRequest struct {
	ID             string
	SessionID      string
	Action         todocapture.Action
	Text           string
	CaptureKind    todocapture.CaptureKind
	ReviewRevision string
}

// ProjectTodoBroker forwards repository-scoped TODO actions to the embedding
// Little Control Room host. The model-facing request deliberately carries no
// project path, provider, or session-origin override.
type ProjectTodoBroker interface {
	RequestProjectTodo(context.Context, ProjectTodoRequest) (tools.ToolResult, error)
}

func NormalizeApprovalDecision(raw string) ApprovalDecision {
	switch ApprovalDecision(strings.TrimSpace(raw)) {
	case DecisionAccept:
		return DecisionAccept
	case DecisionAcceptForSession:
		return DecisionAcceptForSession
	case DecisionDecline:
		return DecisionDecline
	case DecisionCancel:
		return DecisionCancel
	default:
		return DecisionCancel
	}
}
