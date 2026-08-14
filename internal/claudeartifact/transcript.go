package claudeartifact

import (
	"strings"
	"time"
)

// TranscriptEntry carries the structured fields needed to distinguish
// conversational user input from Claude Code's generated user-role records.
type TranscriptEntry struct {
	Type             string
	UUID             string
	ParentUUID       string
	IsMeta           bool
	IsCompactSummary bool
	PromptSource     string
	OriginKind       string
}

// ConversationTracker follows Claude Code's ordered JSONL event stream.
//
// Claude Code records local slash commands as an isMeta user event followed by
// non-meta user events linked through uuid/parentUuid. Those descendants are
// provider-generated records even though their role is "user". Submitted
// prompts carry origin.kind == "human" or a promptSource such as "typed" or
// "sdk" and start a conversational chain regardless of their parent.
//
// This tracker deliberately uses event metadata rather than inspecting message
// text, so XML-looking content explicitly submitted by the user remains visible.
type ConversationTracker struct {
	previousUUID          string
	previousGeneratedUser bool
}

// TurnObservation is the structured lifecycle portion of one Claude Code
// JSONL record. Callers retain responsibility for parsing the provider record;
// TurnTracker only interprets explicit provider fields and never displayed
// message text.
type TurnObservation struct {
	Type                string
	Subtype             string
	At                  time.Time
	ConversationalUser  bool
	AssistantStopReason string
	AsyncEvents         []AsyncTaskEvent
}

// TurnState describes the latest top-level Claude Code turn observed in a
// transcript. UpdatedAt is retained even when a completed turn clears its
// start time, allowing restart recovery to compare the terminal record with
// the turn that was active when LCR shut down.
type TurnState struct {
	StartedAt time.Time
	UpdatedAt time.Time
	Known     bool
	Completed bool
	Verified  bool
}

// TurnTracker follows Claude Code's structured turn and background-task
// lifecycle. A terminal assistant stop or turn_duration record completes the
// turn only while no provider-declared async task remains pending.
type TurnTracker struct {
	state                 TurnState
	pendingAsync          map[string]struct{}
	pendingAsyncStartedAt time.Time
}

func (t *TurnTracker) Observe(observation TurnObservation) {
	if t == nil {
		return
	}
	asyncUserEvent := false
	for _, event := range observation.AsyncEvents {
		eventAt := event.At
		if eventAt.IsZero() {
			eventAt = observation.At
		}
		switch event.Kind {
		case AsyncTaskLaunched:
			if t.pendingAsync == nil {
				t.pendingAsync = make(map[string]struct{})
			}
			if len(t.pendingAsync) == 0 && !eventAt.IsZero() {
				t.pendingAsyncStartedAt = eventAt
			}
			if taskID := strings.TrimSpace(event.TaskID); taskID != "" {
				t.pendingAsync[taskID] = struct{}{}
			}
			if observation.Type == "user" {
				asyncUserEvent = true
				t.set(eventAt, false, true)
			}
		case AsyncTaskUpdated:
			if observation.Type == "user" {
				asyncUserEvent = true
				t.set(eventAt, false, true)
			}
			if IsTerminalTaskStatus(event.Status) {
				delete(t.pendingAsync, strings.TrimSpace(event.TaskID))
				if len(t.pendingAsync) == 0 {
					t.pendingAsyncStartedAt = time.Time{}
				}
			}
		}
	}

	switch strings.TrimSpace(observation.Type) {
	case "assistant":
		stopReason := strings.TrimSpace(observation.AssistantStopReason)
		t.set(observation.At, AssistantTurnCompleted(stopReason), stopReason != "")
	case "progress":
		t.set(observation.At, false, true)
	case "system":
		if strings.TrimSpace(observation.Subtype) == "turn_duration" {
			t.set(observation.At, true, true)
		}
	case "user":
		if !asyncUserEvent && observation.ConversationalUser {
			t.set(observation.At, false, true)
		}
	}
}

func (t *TurnTracker) State() TurnState {
	if t == nil {
		return TurnState{}
	}
	state := t.state
	if len(t.pendingAsync) > 0 {
		state.Known = true
		state.Completed = false
		state.Verified = true
		state.StartedAt = t.pendingAsyncStartedAt
		if state.StartedAt.IsZero() {
			state.StartedAt = t.state.StartedAt
		}
	}
	return state
}

func (t *TurnTracker) set(at time.Time, completed, verified bool) {
	t.state.Known = true
	if !at.IsZero() {
		t.state.UpdatedAt = at
	}
	if completed {
		t.state.StartedAt = time.Time{}
	} else if t.state.Completed || t.state.StartedAt.IsZero() {
		t.state.StartedAt = at
	}
	t.state.Completed = completed
	t.state.Verified = verified
}

// AssistantTurnCompleted interprets Claude's structured stop reason. tool_use
// is an intermediate boundary; every other explicit stop reason is terminal.
func AssistantTurnCompleted(stopReason string) bool {
	stopReason = strings.ToLower(strings.TrimSpace(stopReason))
	return stopReason != "" && stopReason != "tool_use"
}

// Observe records one event and reports whether it is conversational user
// input. Callers should call Observe for every ordered JSONL event, not only
// user events, so non-user records terminate a generated user-event chain.
func (t *ConversationTracker) Observe(entry TranscriptEntry) bool {
	entryType := strings.TrimSpace(entry.Type)
	uuid := strings.TrimSpace(entry.UUID)
	parentUUID := strings.TrimSpace(entry.ParentUUID)
	promptSource := strings.TrimSpace(entry.PromptSource)
	originKind := strings.TrimSpace(entry.OriginKind)

	if entryType != "user" {
		t.previousUUID = uuid
		t.previousGeneratedUser = false
		return false
	}

	generated := false
	conversational := false
	switch {
	case entry.IsMeta, entry.IsCompactSummary:
		generated = true
	case originKind != "" && !strings.EqualFold(originKind, "human"):
		generated = true
	case strings.EqualFold(originKind, "human"), promptSource != "":
		conversational = true
	case t.previousGeneratedUser && parentUUID != "" && parentUUID == t.previousUUID:
		generated = true
	default:
		// Preserve legacy Claude Code prompts that predate origin and
		// promptSource fields.
		conversational = true
	}

	t.previousUUID = uuid
	t.previousGeneratedUser = generated && uuid != ""
	return conversational
}
