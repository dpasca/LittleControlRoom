package uisurface

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

const (
	engineerSessionEntryLimit     = 120
	engineerSessionTextRuneLimit  = 48000
	engineerSessionEntryRuneLimit = 6000
)

type EngineerSessionItem struct {
	ID                 string    `json:"id"`
	DisplayID          string    `json:"display_id"`
	ProjectPath        string    `json:"project_path"`
	ProjectName        string    `json:"project_name,omitempty"`
	Provider           string    `json:"provider"`
	ProviderLabel      string    `json:"provider_label"`
	Live               bool      `json:"live"`
	Status             Status    `json:"status"`
	Phase              string    `json:"phase,omitempty"`
	Summary            string    `json:"summary"`
	LastActivityAt     time.Time `json:"last_activity_at,omitempty"`
	LastActivityLabel  string    `json:"last_activity_label"`
	Model              string    `json:"model,omitempty"`
	ReasoningEffort    string    `json:"reasoning_effort,omitempty"`
	TranscriptRevision uint64    `json:"transcript_revision,omitempty"`
}

type EngineerSessionListSurface struct {
	ProjectPath string                `json:"project_path"`
	Sessions    []EngineerSessionItem `json:"sessions"`
}

type EngineerTranscriptEntry struct {
	ItemID string `json:"item_id,omitempty"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Text   string `json:"text"`
	Tone   Tone   `json:"tone"`
}

type EngineerSessionDetailSurface struct {
	Session      EngineerSessionItem       `json:"session"`
	Entries      []EngineerTranscriptEntry `json:"entries"`
	Instruments  []DetailFieldValue        `json:"instruments"`
	Input        EngineerSessionInput      `json:"input"`
	Truncated    bool                      `json:"truncated,omitempty"`
	EmptyMessage string                    `json:"empty_message,omitempty"`
}

type EngineerSessionInput struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Mode      string `json:"mode,omitempty"`
	Label     string `json:"label,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func BuildEngineerSessionInput(snapshot codexapp.Snapshot, enabled bool) EngineerSessionInput {
	if !enabled {
		return EngineerSessionInput{Reason: "Session messages are disabled in Mobile settings."}
	}
	availability := codexapp.DescribeSessionInput(snapshot)
	input := EngineerSessionInput{
		Enabled:   true,
		Available: availability.Available,
		Mode:      string(availability.Mode),
		Reason:    availability.Reason,
	}
	switch availability.Mode {
	case codexapp.SessionInputSteer:
		input.Label = "Steer"
	case codexapp.SessionInputQueue:
		input.Label = "Queue"
	default:
		input.Label = "Send"
	}
	return input
}

func BuildLiveEngineerSession(snapshot codexapp.Snapshot, now time.Time) EngineerSessionItem {
	if now.IsZero() {
		now = time.Now()
	}
	source := sessionSourceForProvider(snapshot.Provider)
	rawID := strings.TrimSpace(snapshot.ThreadID)
	id := model.BuildCanonicalSessionID(source, rawID)
	if id == "" {
		id = "live"
	}
	lastActivity := snapshot.LastActivityAt
	if lastActivity.IsZero() {
		lastActivity = snapshot.BusySince
	}

	status := liveEngineerSessionStatus(snapshot)
	summary := liveEngineerSessionSummary(snapshot)
	if summary == "" {
		summary = status.Label
	}
	return EngineerSessionItem{
		ID:                 id,
		DisplayID:          shortSessionID(rawID),
		ProjectPath:        strings.TrimSpace(snapshot.ProjectPath),
		Provider:           string(source),
		ProviderLabel:      snapshot.Provider.Label(),
		Live:               true,
		Status:             status,
		Phase:              string(snapshot.Phase),
		Summary:            clipSessionText(summary, 240),
		LastActivityAt:     lastActivity,
		LastActivityLabel:  formatLastActivity(now, lastActivity),
		Model:              strings.TrimSpace(snapshot.Model),
		ReasoningEffort:    strings.TrimSpace(snapshot.ReasoningEffort),
		TranscriptRevision: snapshot.TranscriptRevision,
	}
}

func BuildRecordedEngineerSession(evidence model.SessionEvidence, classification model.SessionClassification, now time.Time) EngineerSessionItem {
	if now.IsZero() {
		now = time.Now()
	}
	evidence = model.NormalizeSessionEvidenceIdentity(evidence)
	status := recordedEngineerSessionStatus(evidence, classification)
	summary := strings.TrimSpace(classification.Summary)
	if summary == "" {
		summary = recordedEngineerSessionSummary(evidence)
	}
	return EngineerSessionItem{
		ID:                evidence.SessionID,
		DisplayID:         shortSessionID(evidence.RawSessionID),
		ProjectPath:       strings.TrimSpace(evidence.ProjectPath),
		Provider:          string(evidence.Source),
		ProviderLabel:     sessionSourceLabel(evidence.Source),
		Status:            status,
		Phase:             "recorded",
		Summary:           clipSessionText(summary, 240),
		LastActivityAt:    evidence.LastEventAt,
		LastActivityLabel: formatLastActivity(now, evidence.LastEventAt),
	}
}

func BuildLiveEngineerSessionDetail(snapshot codexapp.Snapshot, now time.Time) EngineerSessionDetailSurface {
	item := BuildLiveEngineerSession(snapshot, now)
	entries, truncated := liveEngineerTranscriptEntries(snapshot.Entries)
	instruments := []DetailFieldValue{
		FieldValue("Link", "Live from the TUI", TonePositive),
		FieldValue("Provider", item.ProviderLabel, ToneValue),
		FieldValue("Status", item.Status.Label, item.Status.Tone),
	}
	if phase := sessionPhaseLabel(snapshot.Phase); phase != "" {
		instruments = append(instruments, FieldValue("Phase", phase, sessionPhaseTone(snapshot.Phase)))
	}
	if item.Model != "" {
		instruments = append(instruments, FieldValue("Model", item.Model, ToneValue))
	}
	if item.ReasoningEffort != "" {
		instruments = append(instruments, FieldValue("Reasoning", item.ReasoningEffort, ToneValue))
	}
	if permission := strings.TrimSpace(snapshot.PermissionLevel); permission != "" {
		instruments = append(instruments, FieldValue("Permission", permission, ToneWarning))
	}
	if threadID := strings.TrimSpace(snapshot.ThreadID); threadID != "" {
		instruments = append(instruments, FieldValue("Thread", threadID, ToneMuted))
	}
	if snapshot.Goal != nil && strings.TrimSpace(snapshot.Goal.Objective) != "" {
		instruments = append(instruments, FieldValue("Goal", clipSessionText(snapshot.Goal.Objective, 320), ToneInfo))
	}

	emptyMessage := ""
	if len(entries) == 0 {
		emptyMessage = "The live session is connected but has no transcript yet."
	}
	return EngineerSessionDetailSurface{
		Session:      item,
		Entries:      entries,
		Instruments:  instruments,
		Truncated:    truncated,
		EmptyMessage: emptyMessage,
	}
}

func BuildRecordedEngineerSessionDetail(evidence model.SessionEvidence, classification model.SessionClassification, excerpt model.SessionContextExcerpt, now time.Time) EngineerSessionDetailSurface {
	item := BuildRecordedEngineerSession(evidence, classification, now)
	entries := make([]EngineerTranscriptEntry, 0, len(excerpt.Turns))
	for _, turn := range excerpt.Turns {
		text := strings.TrimSpace(turn.Text)
		if text == "" {
			continue
		}
		kind := "agent"
		label := item.ProviderLabel
		tone := ToneInfo
		if strings.EqualFold(strings.TrimSpace(turn.Role), "user") {
			kind = "user"
			label = "You"
			tone = ToneWarning
		}
		entries = append(entries, EngineerTranscriptEntry{
			ItemID: fmt.Sprintf("turn-%d", turn.Index),
			Kind:   kind,
			Label:  label,
			Text:   clipSessionText(text, engineerSessionEntryRuneLimit),
			Tone:   tone,
		})
	}

	instruments := []DetailFieldValue{
		FieldValue("Link", "Recorded transcript", ToneMuted),
		FieldValue("Provider", item.ProviderLabel, ToneValue),
		FieldValue("Status", item.Status.Label, item.Status.Tone),
	}
	if item.LastActivityLabel != "Never" {
		instruments = append(instruments, FieldValue("Activity", item.LastActivityLabel, ToneValue))
	}
	if item.DisplayID != "" {
		instruments = append(instruments, FieldValue("Session", item.DisplayID, ToneMuted))
	}
	if format := strings.TrimSpace(evidence.Format); format != "" {
		instruments = append(instruments, FieldValue("Format", format, ToneMuted))
	}

	emptyMessage := ""
	if len(entries) == 0 {
		emptyMessage = "No readable transcript was found for this recorded session."
	}
	return EngineerSessionDetailSurface{
		Session:      item,
		Entries:      entries,
		Instruments:  instruments,
		Truncated:    excerpt.Truncated,
		EmptyMessage: emptyMessage,
	}
}

func liveEngineerSessionStatus(snapshot codexapp.Snapshot) Status {
	switch {
	case snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil:
		return Status{Label: "Input needed", Tone: ToneWarning}
	case strings.TrimSpace(snapshot.LastError) != "":
		return Status{Label: "Error", Tone: ToneDanger}
	case snapshot.Closed || snapshot.Phase == codexapp.SessionPhaseClosed:
		return Status{Label: "Closed", Tone: ToneMuted}
	case snapshot.Phase == codexapp.SessionPhaseStalled:
		return Status{Label: "Stalled", Tone: ToneDanger}
	case snapshot.BusyExternal:
		return Status{Label: "External work", Tone: ToneInfo}
	case snapshot.Busy:
		return Status{Label: "Working", Tone: TonePositive}
	case snapshot.Started:
		return Status{Label: "Ready", Tone: ToneInfo}
	default:
		return Status{Label: "Starting", Tone: ToneInfo}
	}
}

func recordedEngineerSessionStatus(evidence model.SessionEvidence, classification model.SessionClassification) Status {
	if evidence.ErrorCount > 0 {
		return Status{Label: "Errors", Tone: ToneDanger}
	}
	if evidence.LatestTurnStateKnown && !evidence.LatestTurnCompleted {
		return Status{Label: "In progress", Tone: TonePositive}
	}
	if classification.Status == model.ClassificationRunning || classification.Status == model.ClassificationPending {
		return Status{Label: "Assessing", Tone: ToneInfo}
	}
	switch classification.Category {
	case model.SessionCategoryBlocked:
		return Status{Label: "Blocked", Tone: ToneDanger}
	case model.SessionCategoryWaitingForUser:
		return Status{Label: "Waiting", Tone: ToneWarning}
	case model.SessionCategoryNeedsFollowUp:
		return Status{Label: "Follow-up", Tone: ToneWarning}
	case model.SessionCategoryInProgress:
		return Status{Label: "In progress", Tone: TonePositive}
	case model.SessionCategoryCompleted:
		return Status{Label: "Complete", Tone: ToneMuted}
	default:
		return Status{Label: "Recorded", Tone: ToneMuted}
	}
}

func recordedEngineerSessionSummary(evidence model.SessionEvidence) string {
	if evidence.LatestTurnStateKnown && !evidence.LatestTurnCompleted {
		return "The latest recorded turn is still in progress."
	}
	if evidence.LatestTurnCompleted {
		return "Recorded engineer exchange."
	}
	return "Recorded engineer session."
}

func liveEngineerSessionSummary(snapshot codexapp.Snapshot) string {
	for i := len(snapshot.Entries) - 1; i >= 0; i-- {
		entry := snapshot.Entries[i]
		if entry.Kind == codexapp.TranscriptReasoning {
			continue
		}
		text := strings.TrimSpace(entry.DisplayText)
		if text == "" {
			text = strings.TrimSpace(entry.Text)
		}
		if text != "" {
			return text
		}
	}
	return strings.TrimSpace(snapshot.Status)
}

func liveEngineerTranscriptEntries(entries []codexapp.TranscriptEntry) ([]EngineerTranscriptEntry, bool) {
	reversed := make([]EngineerTranscriptEntry, 0, min(len(entries), engineerSessionEntryLimit))
	usedRunes := 0
	truncated := false
	for i := len(entries) - 1; i >= 0; i-- {
		if len(reversed) >= engineerSessionEntryLimit || usedRunes >= engineerSessionTextRuneLimit {
			truncated = true
			break
		}
		entry := entries[i]
		text := strings.TrimSpace(entry.DisplayText)
		if text == "" {
			text = strings.TrimSpace(entry.Text)
		}
		if text == "" && entry.GeneratedImage != nil {
			text = "Generated image"
		}
		if text == "" {
			continue
		}
		remaining := engineerSessionTextRuneLimit - usedRunes
		entryLimit := min(engineerSessionEntryRuneLimit, remaining)
		clipped := clipSessionText(text, entryLimit)
		if clipped != text {
			truncated = true
		}
		usedRunes += len([]rune(clipped))
		kind, label, tone := engineerTranscriptPresentation(entry.Kind)
		reversed = append(reversed, EngineerTranscriptEntry{
			ItemID: strings.TrimSpace(entry.ItemID),
			Kind:   kind,
			Label:  label,
			Text:   clipped,
			Tone:   tone,
		})
	}

	out := make([]EngineerTranscriptEntry, len(reversed))
	for i := range reversed {
		out[len(reversed)-1-i] = reversed[i]
	}
	return out, truncated
}

func engineerTranscriptPresentation(kind codexapp.TranscriptKind) (string, string, Tone) {
	switch kind {
	case codexapp.TranscriptUser:
		return "user", "You", ToneWarning
	case codexapp.TranscriptAgent:
		return "agent", "Engineer", ToneInfo
	case codexapp.TranscriptTool:
		return "tool", "Tool activity", TonePositive
	case codexapp.TranscriptCommand:
		return "command", "Command", TonePositive
	case codexapp.TranscriptFileChange:
		return "file_change", "File change", TonePositive
	case codexapp.TranscriptPlan:
		return "plan", "Plan", ToneInfo
	case codexapp.TranscriptReasoning:
		return "reasoning", "Reasoning", ToneMuted
	case codexapp.TranscriptError:
		return "error", "Error", ToneDanger
	case codexapp.TranscriptSystem:
		return "system", "System", ToneMuted
	case codexapp.TranscriptStatus:
		return "status", "Status", ToneMuted
	default:
		return "other", "Activity", ToneMuted
	}
}

func sessionSourceForProvider(provider codexapp.Provider) model.SessionSource {
	switch provider.Normalized() {
	case codexapp.ProviderOpenCode:
		return model.SessionSourceOpenCode
	case codexapp.ProviderClaudeCode:
		return model.SessionSourceClaudeCode
	case codexapp.ProviderLCAgent:
		return model.SessionSourceLCAgent
	default:
		return model.SessionSourceCodex
	}
}

func sessionSourceLabel(source model.SessionSource) string {
	switch model.NormalizeSessionSource(source) {
	case model.SessionSourceOpenCode:
		return "OpenCode"
	case model.SessionSourceClaudeCode:
		return "Claude Code"
	case model.SessionSourceLCAgent:
		return "LCAgent"
	default:
		return "Codex"
	}
}

func sessionPhaseLabel(phase codexapp.SessionPhase) string {
	switch phase {
	case codexapp.SessionPhaseRunning:
		return "Running"
	case codexapp.SessionPhaseFinishing:
		return "Finishing"
	case codexapp.SessionPhaseReconciling:
		return "Reconciling"
	case codexapp.SessionPhaseStalled:
		return "Stalled"
	case codexapp.SessionPhaseExternal:
		return "External"
	case codexapp.SessionPhaseClosed:
		return "Closed"
	case codexapp.SessionPhaseIdle:
		return "Idle"
	default:
		return ""
	}
}

func sessionPhaseTone(phase codexapp.SessionPhase) Tone {
	switch phase {
	case codexapp.SessionPhaseStalled:
		return ToneDanger
	case codexapp.SessionPhaseRunning, codexapp.SessionPhaseFinishing, codexapp.SessionPhaseReconciling:
		return TonePositive
	case codexapp.SessionPhaseExternal:
		return ToneInfo
	default:
		return ToneMuted
	}
}

func shortSessionID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "Pending"
	}
	runes := []rune(id)
	if len(runes) <= 10 {
		return id
	}
	return string(runes[:10])
}

func clipSessionText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	marker := "..."
	if limit <= len(marker) {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-len(marker)])) + marker
}
