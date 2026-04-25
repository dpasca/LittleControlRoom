package codexapp

import (
	"bytes"
	"strings"
)

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
	for i := range out {
		out[i].GeneratedImage = cloneGeneratedImageArtifact(out[i].GeneratedImage)
	}
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
			ItemID:         entry.ItemID,
			Kind:           entry.Kind,
			Text:           text,
			DisplayText:    entry.DisplayText,
			GeneratedImage: cloneGeneratedImageArtifact(entry.GeneratedImage),
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

func cloneGeneratedImageArtifact(image *GeneratedImageArtifact) *GeneratedImageArtifact {
	if image == nil {
		return nil
	}
	out := *image
	if len(image.PreviewData) > 0 {
		out.PreviewData = append([]byte(nil), image.PreviewData...)
	}
	return &out
}

func generatedImageArtifactsEqual(left, right *GeneratedImageArtifact) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ID == right.ID &&
		left.Path == right.Path &&
		left.SourcePath == right.SourcePath &&
		left.Width == right.Width &&
		left.Height == right.Height &&
		left.ByteSize == right.ByteSize &&
		bytes.Equal(left.PreviewData, right.PreviewData)
}
