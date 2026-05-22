package script

import (
	"context"
	"strings"
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
