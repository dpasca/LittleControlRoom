package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (s *appServerSession) initializeHistoryPagination(thread resumedThread) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.historyLoadedTurns = make(map[string]struct{}, len(thread.Turns))
	for _, turn := range thread.Turns {
		if turnID := strings.TrimSpace(turn.ID); turnID != "" {
			s.historyLoadedTurns[turnID] = struct{}{}
		}
	}
	s.historyNextCursor = strings.TrimSpace(thread.HistoryNextCursor)
	s.historyHasMore = s.historyNextCursor != ""
	s.historyLoading = false
	s.historyLoadError = ""
	s.historySummaryOnly = thread.HistorySummaryOnly
	s.historyInitialized = true
}

// LoadOlderHistory fetches one bounded page from the provider. Callers should
// run it outside the Bubble Tea update/render path.
func (s *appServerSession) LoadOlderHistory() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("Codex session is closed")
	}
	if s.historyLoading || !s.historyHasMore {
		s.mu.Unlock()
		return nil
	}
	threadID := strings.TrimSpace(s.threadID)
	cursor := strings.TrimSpace(s.historyNextCursor)
	if threadID == "" || cursor == "" {
		s.historyHasMore = false
		s.mu.Unlock()
		return nil
	}
	s.historyLoading = true
	s.historyLoadError = ""
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	result, err := s.call(ctx, "thread/turns/list", threadTurnsListParams{
		ThreadID:      threadID,
		Cursor:        cursor,
		Limit:         codexResumeInitialTurnLimit,
		SortDirection: "desc",
		ItemsView:     "summary",
	})
	if err != nil {
		return s.finishHistoryLoadError(fmt.Errorf("load older Codex turns: %w", err))
	}

	var page threadTurnsPage
	if err := json.Unmarshal(result, &page); err != nil {
		return s.finishHistoryLoadError(fmt.Errorf("decode older Codex turns: %w", err))
	}
	turns := make([]resumedTurn, len(page.Data))
	for i := range page.Data {
		turns[len(page.Data)-1-i] = page.Data[i]
	}

	s.mu.Lock()
	if s.closed || strings.TrimSpace(s.threadID) != threadID {
		s.historyLoading = false
		s.mu.Unlock()
		return nil
	}
	s.prependHistoryTurnsLocked(turns)
	nextCursor := ""
	if page.NextCursor != nil {
		nextCursor = strings.TrimSpace(*page.NextCursor)
	}
	s.historyNextCursor = nextCursor
	s.historyHasMore = nextCursor != ""
	s.historyLoading = false
	s.historyLoadError = ""
	s.syncHistorySummaryNoticeLocked()
	s.touchLocked()
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) finishHistoryLoadError(err error) error {
	if err == nil {
		return nil
	}
	s.mu.Lock()
	s.historyLoading = false
	s.historyLoadError = err.Error()
	s.mu.Unlock()
	s.notify()
	return err
}

func (s *appServerSession) prependHistoryTurnsLocked(turns []resumedTurn) {
	if len(turns) == 0 {
		return
	}
	if s.historyLoadedTurns == nil {
		s.historyLoadedTurns = make(map[string]struct{})
	}
	existingItems := make(map[string]struct{}, len(s.entryIndex))
	for itemID := range s.entryIndex {
		existingItems[itemID] = struct{}{}
	}
	older := make([]transcriptEntry, 0, len(turns)*2)
	for _, turn := range turns {
		turnID := strings.TrimSpace(turn.ID)
		if turnID != "" {
			if _, loaded := s.historyLoadedTurns[turnID]; loaded {
				continue
			}
			s.historyLoadedTurns[turnID] = struct{}{}
		}
		for _, item := range turn.Items {
			itemID, kind, text, image := s.renderThreadItemForTurn(turn.Status, item)
			itemID = strings.TrimSpace(itemID)
			if strings.TrimSpace(text) == "" && image == nil {
				continue
			}
			if itemID != "" {
				if _, exists := existingItems[itemID]; exists {
					continue
				}
				existingItems[itemID] = struct{}{}
			}
			older = append(older, transcriptEntry{
				ItemID:         itemID,
				TurnID:         turnID,
				Kind:           kind,
				Text:           text,
				GeneratedImage: cloneGeneratedImageArtifact(image),
			})
		}
		if turn.Status == "failed" && turn.Error != nil && strings.TrimSpace(turn.Error.Message) != "" {
			itemID := "history-turn-error:" + turnID
			if _, exists := existingItems[itemID]; !exists {
				existingItems[itemID] = struct{}{}
				older = append(older, transcriptEntry{ItemID: itemID, TurnID: turnID, Kind: TranscriptError, Text: turn.Error.Message})
			}
		}
	}
	if len(older) == 0 {
		return
	}
	s.removeHistorySummaryNoticeLocked()
	entries := make([]transcriptEntry, 0, len(older)+len(s.entries))
	entries = append(entries, older...)
	entries = append(entries, s.entries...)
	s.entries = entries
	s.rebuildEntryIndexLocked()
	s.invalidateTranscriptCacheLocked()
}

func (s *appServerSession) syncHistorySummaryNoticeLocked() {
	notice := "Historical tool details are summarized in the embedded view; the full session remains in the provider log."
	if s.historyHasMore {
		notice = "Older transcript turns are available. Scroll to the top to load them; historical tool details stay summarized for a quick open."
	}
	if index, ok := s.entryIndex[resumedHistorySummaryItemID]; ok && index == 0 && s.historySummaryOnly && s.entries[index].Text == notice {
		return
	}
	removed := s.removeHistorySummaryNoticeLocked()
	if !s.historySummaryOnly {
		if removed {
			s.invalidateTranscriptCacheLocked()
		}
		return
	}
	s.entries = append([]transcriptEntry{{
		ItemID: resumedHistorySummaryItemID,
		Kind:   TranscriptSystem,
		Text:   notice,
	}}, s.entries...)
	s.rebuildEntryIndexLocked()
	s.invalidateTranscriptCacheLocked()
}

func (s *appServerSession) removeHistorySummaryNoticeLocked() bool {
	index, ok := s.entryIndex[resumedHistorySummaryItemID]
	if !ok || index < 0 || index >= len(s.entries) {
		return false
	}
	s.entries = append(s.entries[:index], s.entries[index+1:]...)
	s.rebuildEntryIndexLocked()
	return true
}

func (s *appServerSession) rebuildEntryIndexLocked() {
	s.entryIndex = make(map[string]int, len(s.entries))
	for i := range s.entries {
		if itemID := strings.TrimSpace(s.entries[i].ItemID); itemID != "" {
			s.entryIndex[itemID] = i
		}
	}
}
