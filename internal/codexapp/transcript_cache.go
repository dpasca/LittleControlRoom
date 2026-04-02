package codexapp

import "strings"

type transcriptExportCache struct {
	ready      bool
	revision   uint64
	entries    []TranscriptEntry
	transcript string
}

func (c *transcriptExportCache) invalidate(nextRevision *uint64) {
	if nextRevision != nil {
		*nextRevision = *nextRevision + 1
	}
	c.ready = false
}

func cloneTranscriptEntries(entries []TranscriptEntry) []TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]TranscriptEntry, len(entries))
	copy(out, entries)
	return out
}

func exportTranscriptEntries(entries []transcriptEntry) []TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		out = append(out, TranscriptEntry{
			ItemID:      entry.ItemID,
			Kind:        entry.Kind,
			Text:        text,
			DisplayText: entry.DisplayText,
		})
	}
	return out
}

func buildTranscriptText(provider Provider, entries []TranscriptEntry, separator string, preferDisplayText bool) string {
	if len(entries) == 0 {
		return ""
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		text := strings.TrimSpace(entry.Text)
		if preferDisplayText {
			if displayText := strings.TrimSpace(entry.DisplayText); displayText != "" {
				text = displayText
			}
		}
		if text == "" {
			continue
		}
		lines = append(lines, formatTranscriptEntryForProvider(provider, entry.Kind, text))
	}
	return strings.Join(lines, separator)
}
