package sessionclassify

import (
	"context"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

// ControlOperationSnapshot is bounded host evidence, separate from the agent's
// transcript. Approval and execution may happen after the last agent message.
type ControlOperationSnapshot struct {
	ID         string                  `json:"id"`
	Capability control.CapabilityName  `json:"capability"`
	Status     control.OperationStatus `json:"status"`
	Confirmed  bool                    `json:"confirmed"`
	UpdatedAt  string                  `json:"updated_at"`
	Arguments  string                  `json:"arguments,omitempty"`
	Result     string                  `json:"result,omitempty"`
	Error      string                  `json:"error,omitempty"`
}

func ControlOperationsForSession(ctx context.Context, st *store.Store, projectPath string, session model.SessionEvidence) ([]ControlOperationSnapshot, error) {
	if st == nil {
		return nil, nil
	}
	session = model.NormalizeSessionEvidenceIdentity(session)
	operations, err := st.ListSessionControlOperations(ctx, projectPath, session.Source, session.ExternalID(), 16)
	if err != nil {
		return nil, err
	}
	var snapshots []ControlOperationSnapshot
	for _, operation := range operations {
		snapshots = append(snapshots, ControlOperationSnapshot{
			ID: operation.ID, Capability: operation.Capability, Status: operation.Status,
			Confirmed: operation.Confirmed, UpdatedAt: operation.UpdatedAt.UTC().Format(time.RFC3339),
			Arguments: clipForStorage(string(operation.Invocation.Args), 800),
			Result:    clipForStorage(string(operation.Result), 800),
			Error:     clipForStorage(operation.Error, 400),
		})
	}
	return snapshots, nil
}
