package agentcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"lcroom/internal/control"
	"lcroom/internal/store"
)

type Options struct {
	Store             *store.Store
	OriginProjectPath string
	Scope             control.AuthorityScope
	Source            string
	Provider          string
	SessionKey        string
}

type Executor struct {
	store             *store.Store
	originProjectPath string
	scope             control.AuthorityScope
	source            string
	provider          string
	sessionKey        string
}

func NewExecutor(opts Options) (*Executor, error) {
	if opts.Store == nil {
		return nil, errors.New("control store is required")
	}
	scope := control.NormalizeAuthorityScope(string(opts.Scope))
	if scope == "" {
		return nil, errors.New("control authority scope is required")
	}
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		return nil, errors.New("control source is required")
	}
	sessionKey := strings.TrimSpace(opts.SessionKey)
	return &Executor{
		store:             opts.Store,
		originProjectPath: strings.TrimSpace(opts.OriginProjectPath),
		scope:             scope,
		source:            source,
		provider:          strings.TrimSpace(opts.Provider),
		sessionKey:        sessionKey,
	}, nil
}

func (e *Executor) Scope() control.AuthorityScope {
	if e == nil {
		return ""
	}
	return e.scope
}

func (e *Executor) List(domain string) (map[string]any, error) {
	if e == nil {
		return nil, errors.New("LCR controls are unavailable")
	}
	return control.ListReport(domain, e.scope, e.store != nil)
}

func (e *Executor) Describe(name string) (map[string]any, error) {
	if e == nil {
		return nil, errors.New("LCR controls are unavailable")
	}
	return control.DescribeReport(name, e.scope, "propose_control_operation")
}

func (e *Executor) Propose(ctx context.Context, capabilityName string, arguments json.RawMessage, clientRequestID string) (map[string]any, error) {
	if e == nil || e.store == nil {
		return nil, errors.New("LCR control proposals are unavailable")
	}
	capability, ok := control.CapabilityByName(control.CapabilityName(strings.TrimSpace(capabilityName)))
	if !ok {
		return nil, errors.New("unknown control capability; call list_control_capabilities first")
	}
	if !control.AuthorityAllows(e.scope, capability.Scope) {
		return nil, fmt.Errorf("control capability %q requires %s scope; this agent has %s scope", capability.Name, capability.Scope, e.scope)
	}
	operationID, err := control.NewOperationID()
	if err != nil {
		return nil, err
	}
	invocation, err := control.BuildProposedInvocation(operationID, capability.Name, arguments)
	if err != nil {
		return nil, err
	}
	created, err := e.store.CreateControlOperation(ctx, control.Operation{
		ID:              operationID,
		ClientRequestID: strings.TrimSpace(clientRequestID),
		Capability:      capability.Name,
		Status:          control.OperationProposed,
		Invocation:      invocation,
		Source:          e.source,
		Provider:        e.provider,
		SessionKey:      e.sessionKey,
		ProjectPath:     e.originProjectPath,
		RequestedBy:     firstNonEmpty(e.provider, "embedded_agent"),
	})
	if err != nil {
		return nil, err
	}
	return OperationReport(created, created.ID != operationID), nil
}

func (e *Executor) Get(ctx context.Context, operationID string) (map[string]any, error) {
	if e == nil || e.store == nil {
		return nil, errors.New("LCR control operations are unavailable")
	}
	operation, err := e.store.GetControlOperation(ctx, operationID)
	if err != nil {
		return nil, err
	}
	if operation.Source != e.source || operation.SessionKey != e.sessionKey {
		return nil, errors.New("control operation belongs to a different embedded session")
	}
	return OperationReport(operation, false), nil
}

func OperationReport(operation control.Operation, idempotentReplay bool) map[string]any {
	message := "The proposal was queued for Little Control Room. Stop this turn and ask the user to confirm it in LCR. On a later user turn, call get_control_operation."
	switch operation.Status {
	case control.OperationWaitingForConfirmation:
		message = "The proposal is waiting for explicit operator confirmation in Little Control Room. Stop this turn and wait for a new user message."
	case control.OperationRunning:
		message = "The operator confirmed the proposal. Little Control Room is executing it or waiting to deliver its durable engineer message."
	case control.OperationCompleted:
		message = "Little Control Room completed the confirmed operation."
	case control.OperationFailed:
		message = "Little Control Room could not complete the operation. Stop this turn and report the failure. Do not retry or continue later write or external-action steps from the same requested workflow through other tools."
	case control.OperationCanceled:
		message = "The operator canceled the proposal. Stop this turn and report the cancellation. Do not retry or continue later write or external-action steps from the same requested workflow through other tools."
	}
	return map[string]any{
		"success":           true,
		"operation":         operation,
		"idempotent_replay": idempotentReplay,
		"message":           message,
		"requires_new_user_turn": operation.Status == control.OperationProposed ||
			operation.Status == control.OperationWaitingForConfirmation ||
			operation.Status == control.OperationFailed ||
			operation.Status == control.OperationCanceled,
		"operator_confirmation": operation.Status == control.OperationWaitingForConfirmation,
		"terminal":              operation.Status.Terminal(),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
