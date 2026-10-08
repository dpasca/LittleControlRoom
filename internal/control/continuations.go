package control

import "time"

// ControlContinuation is an explicit request to end the proposing turn and
// continue its remaining work after success. No prose or idle-state inference
// may create one. Host/session/input identity is captured by the live host.
type ControlContinuation struct {
	OperationID   string
	RequestedAt   time.Time
	State         string
	HostID        string
	SessionID     string
	InputRevision uint64
	Reason        string
}
