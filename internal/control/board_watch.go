package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Owner-board click-to-wake.
//
// A project registers one owner board's append-only review outbox against one
// exact engineer session. When the owner makes an explicit review or decision
// click, the board appends one typed event line to that outbox. LCR reads only
// that registered file, persists the event and its cursor, and queues one
// ordinary engineer message for the registered session. Plain note autosaves,
// answer-file rewrites and every other edit never reach LCR because LCR does
// not read owner-answer state at all.

const (
	// BoardReviewEventSchema is the only line format LCR accepts.
	BoardReviewEventSchema = "lcr.board_review_event.v1"

	// BoardReviewMaxLineBytes bounds one outbox line.
	BoardReviewMaxLineBytes = 4096
	// BoardReviewMaxIDLength bounds event, board, item and target ids.
	BoardReviewMaxIDLength = 128
	// BoardReviewClockSkew is how far in the future an event timestamp may be.
	BoardReviewClockSkew = 5 * time.Minute
	// BoardReviewDeliveryDeadline matches the ordinary engineer message
	// deadline and is measured from the event's own creation time.
	BoardReviewDeliveryDeadline = 24 * time.Hour
)

type BoardReviewEventKind string

const (
	// BoardReviewRequested is an explicit owner review or decision click.
	BoardReviewRequested BoardReviewEventKind = "review_requested"
	// BoardReviewCanceled withdraws one earlier request that is not yet delivered.
	BoardReviewCanceled BoardReviewEventKind = "review_canceled"
	// BoardReviewResolved records that the director finished reviewing a request.
	BoardReviewResolved BoardReviewEventKind = "review_resolved"
)

// BoardReviewEvent is one validated outbox line.
type BoardReviewEvent struct {
	Schema        string               `json:"schema"`
	Kind          BoardReviewEventKind `json:"kind"`
	EventID       string               `json:"event_id"`
	BoardID       string               `json:"board_id"`
	ItemID        string               `json:"item_id,omitempty"`
	TargetEventID string               `json:"target_event_id,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
}

// ValidBoardIdentifier reports whether value is a bounded identifier made of
// ASCII letters, digits and the separators . _ : -. Identifiers are inserted
// into prompts and storage keys, so free text is never accepted.
func ValidBoardIdentifier(value string) bool {
	if value == "" || len(value) > BoardReviewMaxIDLength {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case i > 0 && (c == '.' || c == '_' || c == ':' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// ParseBoardReviewEvent strictly decodes one outbox line for a registered
// board. Unknown fields, a different board, malformed identifiers, a missing
// or far-future timestamp, and fields that do not belong to the kind are all
// rejected so that nothing but an explicit, well-formed event can wake anyone.
func ParseBoardReviewEvent(line []byte, boardID string, now time.Time) (BoardReviewEvent, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return BoardReviewEvent{}, errors.New("empty line")
	}
	if len(line) > BoardReviewMaxLineBytes {
		return BoardReviewEvent{}, errors.New("line too long")
	}
	var event BoardReviewEvent
	if err := decodeInvocationArgs(line, &event); err != nil {
		return BoardReviewEvent{}, fmt.Errorf("not a board review event: %w", err)
	}
	if event.Schema != BoardReviewEventSchema {
		return BoardReviewEvent{}, errors.New("unsupported schema")
	}
	if event.BoardID != boardID {
		return BoardReviewEvent{}, errors.New("event is for a different board")
	}
	if !ValidBoardIdentifier(event.EventID) {
		return BoardReviewEvent{}, errors.New("invalid event_id")
	}
	if event.CreatedAt.IsZero() {
		return BoardReviewEvent{}, errors.New("created_at is required")
	}
	if event.CreatedAt.After(now.Add(BoardReviewClockSkew)) {
		return BoardReviewEvent{}, errors.New("created_at is in the future")
	}
	event.CreatedAt = event.CreatedAt.UTC()
	switch event.Kind {
	case BoardReviewRequested:
		if !ValidBoardIdentifier(event.ItemID) {
			return BoardReviewEvent{}, errors.New("review_requested needs a valid item_id")
		}
		if event.TargetEventID != "" {
			return BoardReviewEvent{}, errors.New("review_requested cannot name a target_event_id")
		}
	case BoardReviewCanceled, BoardReviewResolved:
		if !ValidBoardIdentifier(event.TargetEventID) {
			return BoardReviewEvent{}, fmt.Errorf("%s needs a valid target_event_id", event.Kind)
		}
		if event.ItemID != "" && !ValidBoardIdentifier(event.ItemID) {
			return BoardReviewEvent{}, errors.New("invalid item_id")
		}
	default:
		return BoardReviewEvent{}, errors.New("unsupported kind")
	}
	return event, nil
}

// BoardReviewOperationID is the stable idempotency key of the mailbox message
// that wakes a director for one event of one registration.
func BoardReviewOperationID(watchID, eventID string) string {
	return "board-review:" + watchID + ":" + eventID
}

// BoardReviewWakePrompt is the fixed text delivered to the director. It only
// carries validated identifiers; nothing from the board file is free text.
func BoardReviewWakePrompt(watch BoardWatch, event BoardReviewEvent) string {
	return fmt.Sprintf(`LCR owner-board review request %s (board %s, item %s).
The owner made an explicit review or decision click on the registered owner board. This message only asks you to review it.
Reread the actual saved owner state now, including any note conditions, and decide from that; this message is not the answer and not an approval.
It grants no publication, browser or other external-write authority and does not replace your provider's own approval checks.
If you were told to stop or the work was canceled, that still holds; do not resume it because of this message. Skip it if the saved state shows the request is already handled.
When finished, record the outcome through your board's review-resolution path. Do not send an acknowledgment-only reply.`,
		event.EventID, watch.BoardID, event.ItemID)
}

type BoardWatchState string

const (
	// BoardWatchActive is scanned on every poll.
	BoardWatchActive BoardWatchState = "active"
	// BoardWatchAttention stopped scanning after an integrity problem, such as
	// a replaced or truncated outbox. It never guesses; it needs re-registering.
	BoardWatchAttention BoardWatchState = "attention"
	// BoardWatchUnregistered is an explicit stop. Nothing is read or sent again.
	BoardWatchUnregistered BoardWatchState = "unregistered"
)

func (s BoardWatchState) Scanning() bool { return s == BoardWatchActive }

// BoardWatch is one registered board outbox bound to one exact session.
type BoardWatch struct {
	ID                       string          `json:"watch_id"`
	BoardID                  string          `json:"board_id"`
	ProjectPath              string          `json:"project_path"`
	Provider                 Provider        `json:"provider"`
	RequestedTargetSessionID string          `json:"requested_target_session_id,omitempty"`
	TargetSessionID          string          `json:"target_session_id"`
	OutboxPath               string          `json:"outbox_path"`
	State                    BoardWatchState `json:"state"`
	CursorOffset             int64           `json:"cursor_offset"`
	PrefixLen                int             `json:"prefix_len"`
	PrefixHash               string          `json:"prefix_hash,omitempty"`
	MalformedCount           int64           `json:"malformed_count"`
	LastError                string          `json:"last_error,omitempty"`
	LastScanAt               time.Time       `json:"last_scan_at,omitempty"`
	CreatedAt                time.Time       `json:"created_at"`
	UpdatedAt                time.Time       `json:"updated_at"`
	UnregisteredAt           time.Time       `json:"unregistered_at,omitempty"`
}

type BoardWatchEventState string

const (
	// BoardWatchEventPending is stored but not yet queued as a mailbox message.
	BoardWatchEventPending BoardWatchEventState = "pending"
	// BoardWatchEventQueued has a mailbox message; its delivery progress lives there.
	BoardWatchEventQueued BoardWatchEventState = "queued"
	// BoardWatchEventExpired was older than the delivery deadline when read.
	BoardWatchEventExpired BoardWatchEventState = "expired"
	// BoardWatchEventCanceled was withdrawn or stopped by unregistering.
	BoardWatchEventCanceled BoardWatchEventState = "canceled"
	// BoardWatchEventReviewed was marked resolved by the board.
	BoardWatchEventReviewed BoardWatchEventState = "reviewed"
)

type BoardWatchEvent struct {
	WatchID        string               `json:"watch_id"`
	EventID        string               `json:"event_id"`
	ItemID         string               `json:"item_id,omitempty"`
	EventCreatedAt time.Time            `json:"event_created_at"`
	IngestedAt     time.Time            `json:"ingested_at"`
	State          BoardWatchEventState `json:"state"`
	MessageID      string               `json:"message_id,omitempty"`
	Note           string               `json:"note,omitempty"`
	ReviewedAt     time.Time            `json:"reviewed_at,omitempty"`
}

// EngineerBoardWatchRegisterInput opts one board outbox in. Every field is
// explicit: there is no default board, session or path.
type EngineerBoardWatchRegisterInput struct {
	RequestID       string   `json:"request_id,omitempty"`
	BoardID         string   `json:"board_id"`
	ProjectPath     string   `json:"project_path"`
	Provider        Provider `json:"provider"`
	TargetSessionID string   `json:"target_session_id"`
	OutboxPath      string   `json:"outbox_path"`
}

type EngineerBoardWatchUnregisterInput struct {
	RequestID string `json:"request_id,omitempty"`
	WatchID   string `json:"watch_id"`
}

func EngineerBoardWatchRegisterCapability() Capability {
	return Capability{
		Name:         CapabilityEngineerBoardWatchRegister,
		Description:  "Opt one owner board's review outbox in to durable click-to-wake for one exact engineer session. After operator confirmation LCR reads only that regular file inside the project, starting at its current end, and turns each explicit review_requested line (schema lcr.board_review_event.v1) into one durable engineer message for the target session, with a 24-hour deadline from the event time, restart catch-up, /handoff forwarding and deduplication by event_id. Note edits, answer rewrites and unrelated changes never wake anyone because owner-answer files are never read. A wake only requests review: the director must reread the saved owner state, and it grants no publication, browser or other external-write authority. Processing happens only while LCR is running. An embedded session may register only its own project and its own session. Inspect with the engineer.board_watch_status query; stop with engineer.board_watch_unregister.",
		InputSchema:  engineerBoardWatchRegisterInputSchema(),
		OutputSchema: map[string]any{"type": "object"},
		Risk:         RiskWrite,
		Confirmation: ConfirmationRequired,
	}
}

func EngineerBoardWatchUnregisterCapability() Capability {
	return Capability{
		Name:         CapabilityEngineerBoardWatchUnregister,
		Description:  "Stop one registered owner-board watch. Nothing is read or sent for it again: events not yet queued are canceled and queued, undelivered review wakes are withdrawn, so a stale saved click cannot revive canceled work. Re-registering starts at the outbox's then-current end. An embedded session may unregister only a watch bound to its own project and session; the change is recorded immediately without confirmation. Find ids with the engineer.board_watch_status query.",
		InputSchema:  engineerBoardWatchUnregisterInputSchema(),
		OutputSchema: map[string]any{"type": "object"},
		Risk:         RiskWrite,
		Confirmation: ConfirmationNone,
	}
}

func engineerBoardWatchRegisterInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id":        map[string]any{"type": "string", "description": "Optional stable idempotency key for this request."},
			"board_id":          map[string]any{"type": "string", "description": "Board identifier the outbox lines must carry: ASCII letters, digits and . _ : - only, at most 128 characters."},
			"project_path":      map[string]any{"type": "string", "description": "Exact absolute path of the project that owns the board and the director session."},
			"provider":          map[string]any{"type": "string", "description": "Explicit provider of the target session (codex, opencode, claude_code or lcagent); auto is not accepted."},
			"target_session_id": map[string]any{"type": "string", "description": "Exact provider session id of the director to wake. Follows a recorded /handoff successor."},
			"outbox_path":       map[string]any{"type": "string", "description": "Absolute path of the board's append-only JSON Lines review outbox. It must be a regular file (or not exist yet) inside project_path, with no symlink components leaving the project."},
		},
		"required": []string{"board_id", "project_path", "provider", "target_session_id", "outbox_path"},
	}
}

func engineerBoardWatchUnregisterInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id": map[string]any{"type": "string", "description": "Optional stable idempotency key for this request."},
			"watch_id":   map[string]any{"type": "string", "description": "Exact watch id from the registration receipt or engineer.board_watch_status."},
		},
		"required": []string{"watch_id"},
	}
}

func NormalizeEngineerBoardWatchRegisterInput(input EngineerBoardWatchRegisterInput) (EngineerBoardWatchRegisterInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.BoardID = strings.TrimSpace(input.BoardID)
	if !ValidBoardIdentifier(input.BoardID) {
		return input, errors.New("board_id must be 1-128 ASCII letters, digits or . _ : - characters")
	}
	input.ProjectPath = strings.TrimSpace(input.ProjectPath)
	if input.ProjectPath == "" || !filepath.IsAbs(input.ProjectPath) {
		return input, errors.New("project_path must be an absolute path")
	}
	input.ProjectPath = filepath.Clean(input.ProjectPath)
	input.Provider = input.Provider.Normalized()
	if input.Provider == "" || input.Provider == ProviderAuto {
		return input, errors.New("provider must name the target session's provider explicitly")
	}
	input.TargetSessionID = strings.TrimSpace(input.TargetSessionID)
	if input.TargetSessionID == "" {
		return input, errors.New("target_session_id is required: a board watch wakes one exact session")
	}
	input.OutboxPath = strings.TrimSpace(input.OutboxPath)
	if input.OutboxPath == "" || !filepath.IsAbs(input.OutboxPath) {
		return input, errors.New("outbox_path must be an absolute path")
	}
	input.OutboxPath = filepath.Clean(input.OutboxPath)
	return input, nil
}

func validateEngineerBoardWatchRegisterInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityEngineerBoardWatchRegister)
	}
	var input EngineerBoardWatchRegisterInput
	if err := decodeInvocationArgs(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityEngineerBoardWatchRegister, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityEngineerBoardWatchRegister)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeEngineerBoardWatchRegisterInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, err
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func validateEngineerBoardWatchUnregisterInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityEngineerBoardWatchUnregister)
	}
	var input EngineerBoardWatchUnregisterInput
	if err := decodeInvocationArgs(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityEngineerBoardWatchUnregister, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityEngineerBoardWatchUnregister)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	input.WatchID = strings.TrimSpace(input.WatchID)
	if input.WatchID == "" {
		return Invocation{}, errors.New("watch_id is required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Invocation{}, err
	}
	inv.RequestID = input.RequestID
	inv.Args = payload
	return inv, nil
}

// BoardWatchEventStatus joins a stored event with the progress of its
// mailbox message, so delivery is read from the one delivery system.
type BoardWatchEventStatus struct {
	BoardWatchEvent
	MessageState     EngineerMessageState `json:"message_state,omitempty"`
	MessageError     string               `json:"message_error,omitempty"`
	MessageExpiresAt time.Time            `json:"message_expires_at,omitempty"`
	DeliveredAt      time.Time            `json:"delivered_at,omitempty"`
	AttemptCount     int                  `json:"attempt_count,omitempty"`
}

// Delivery names where the wake stands, separately from State, which carries
// the board-reported review outcome. The two are independent facts: a request
// can be canceled or expired while its wake was already claimed (a withdrawal
// loses cleanly to a delivery claim), and a wake that is delivering or
// delivered cannot be recalled. So actual mailbox delivery is reported first,
// and the terminal review states only describe a wake that was not delivered.
func (s BoardWatchEventStatus) Delivery(now time.Time) string {
	switch s.MessageState {
	case EngineerMessageDelivered:
		return "delivered"
	case EngineerMessageDelivering:
		return "delivering"
	}
	switch s.State {
	case BoardWatchEventCanceled:
		return "canceled"
	case BoardWatchEventExpired:
		return "expired"
	case BoardWatchEventPending:
		if s.MessageID == "" {
			return "pending_enqueue"
		}
		// A wake exists that is not linked yet: report its real progress.
	}
	if s.MessageID == "" {
		// Reviewed before any wake was queued, or a tombstone for an id seen
		// only in a later outbox line: nothing was or will be sent.
		return "not_sent"
	}
	switch s.MessageState {
	case EngineerMessageFailed:
		if !s.MessageExpiresAt.IsZero() && !now.Before(s.MessageExpiresAt) {
			return "expired"
		}
		return "failed"
	case EngineerMessageQueued:
		if strings.TrimSpace(s.MessageError) != "" {
			return "queued_waiting"
		}
	}
	return "queued"
}
