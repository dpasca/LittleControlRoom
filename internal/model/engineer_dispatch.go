package model

import "time"

// EngineerDispatchReplyState tracks one request/reply exchange between the
// embedded session that dispatched an engineer and that engineer.
type EngineerDispatchReplyState string

const (
	// EngineerDispatchReplyAwaiting: the dispatcher started a worker turn and
	// has not been told how it ended.
	EngineerDispatchReplyAwaiting EngineerDispatchReplyState = "awaiting"
	// EngineerDispatchReplyReady: the turn ended and the reply is stored but
	// not yet in the mailbox, usually because the caller's identity is unknown.
	EngineerDispatchReplyReady EngineerDispatchReplyState = "ready"
	// EngineerDispatchReplySent: the reply is in the engineer mailbox.
	EngineerDispatchReplySent EngineerDispatchReplyState = "sent"
)

// EngineerDispatch is the return address of an engineer that another embedded
// session launched into a TODO worktree. Only turns the dispatcher requested
// are reported back; turns the operator starts in the worker are not.
type EngineerDispatch struct {
	ID                int64
	OriginOperationID string
	TodoID            int64
	TodoText          string
	WorkerProjectPath string
	WorkerProvider    SessionSource
	WorkerSessionID   string // Empty until the worker's provider identity is known.
	CallerProjectPath string
	CallerProvider    SessionSource
	CallerSessionKey  string // Host control identity; never a provider resume ID.
	CallerSessionID   string
	ReplyState        EngineerDispatchReplyState
	ReplySeq          int64
	ReplyPrompt       string
	ReplyMessageID    string
	ReplyError        string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
