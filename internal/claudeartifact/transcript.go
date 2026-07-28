package claudeartifact

import "strings"

// TranscriptEntry carries the structured fields needed to distinguish
// conversational user input from Claude Code's generated user-role records.
type TranscriptEntry struct {
	Type         string
	UUID         string
	ParentUUID   string
	IsMeta       bool
	PromptSource string
	OriginKind   string
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
	case entry.IsMeta:
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
