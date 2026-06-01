package codexapp

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxExportedTranscriptEntries          = 1200
	maxExportedTranscriptBytes            = 2 * 1024 * 1024
	maxExportedTranscriptEntryBytes       = 128 * 1024
	maxExportedGeneratedImagePreviewBytes = 4 * 1024 * 1024
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

	reversed := make([]TranscriptEntry, 0, min(len(entries), maxExportedTranscriptEntries))
	totalBytes := 0
	omittedEntries := 0
	retainedGeneratedImagePreview := false
	for i := len(entries) - 1; i >= 0; i-- {
		exported, ok := exportTranscriptEntry(entries[i])
		if !ok {
			continue
		}
		exported = limitGeneratedImagePreviewForTranscriptExport(exported, &retainedGeneratedImagePreview)
		entryBytes := transcriptEntryExportBytes(exported)
		if len(reversed) > 0 && (len(reversed) >= maxExportedTranscriptEntries || totalBytes+entryBytes > maxExportedTranscriptBytes) {
			omittedEntries = countExportableTranscriptEntries(entries[:i+1])
			break
		}
		reversed = append(reversed, exported)
		totalBytes += entryBytes
	}
	if len(reversed) == 0 {
		return nil
	}

	out := make([]TranscriptEntry, 0, len(reversed)+1)
	if omittedEntries > 0 {
		out = append(out, TranscriptEntry{
			Kind: TranscriptSystem,
			Text: fmt.Sprintf("%d older transcript entries omitted from the embedded view; the full session remains in the provider log.", omittedEntries),
		})
	}
	for i := len(reversed) - 1; i >= 0; i-- {
		out = append(out, reversed[i])
	}
	return out
}

func limitGeneratedImagePreviewForTranscriptExport(entry TranscriptEntry, retained *bool) TranscriptEntry {
	if entry.GeneratedImage == nil || len(entry.GeneratedImage.PreviewData) == 0 {
		return entry
	}
	if retained != nil && !*retained && len(entry.GeneratedImage.PreviewData) <= maxExportedGeneratedImagePreviewBytes {
		*retained = true
		return entry
	}
	image := cloneGeneratedImageArtifact(entry.GeneratedImage)
	image.PreviewData = nil
	entry.GeneratedImage = image
	if retained != nil && !*retained {
		*retained = true
	}
	return entry
}

func exportTranscriptEntry(entry transcriptEntry) (TranscriptEntry, bool) {
	text := strings.TrimSpace(entry.Text)
	if text == "" {
		return TranscriptEntry{}, false
	}
	return TranscriptEntry{
		ItemID:         entry.ItemID,
		Kind:           entry.Kind,
		Text:           truncateExportedTranscriptText(text),
		DisplayText:    truncateExportedTranscriptText(strings.TrimSpace(entry.DisplayText)),
		GeneratedImage: cloneGeneratedImageArtifact(entry.GeneratedImage),
	}, true
}

func countExportableTranscriptEntries(entries []transcriptEntry) int {
	count := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.Text) != "" {
			count++
		}
	}
	return count
}

func transcriptEntryExportBytes(entry TranscriptEntry) int {
	size := len(entry.Text) + len(entry.DisplayText)
	return size
}

func truncateExportedTranscriptText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxExportedTranscriptEntryBytes {
		return text
	}
	const marker = "\n\n[... embedded transcript entry shortened; full text remains in the provider log ...]\n\n"
	budget := maxExportedTranscriptEntryBytes - len(marker)
	if budget <= 0 {
		return trimValidUTF8Prefix(text, maxExportedTranscriptEntryBytes)
	}
	headBytes := budget / 3
	tailBytes := budget - headBytes
	head := strings.TrimSpace(trimValidUTF8Prefix(text, headBytes))
	tail := strings.TrimSpace(trimValidUTF8Suffix(text, tailBytes))
	if head == "" {
		return marker + tail
	}
	if tail == "" {
		return head + marker
	}
	return head + marker + tail
}

func trimValidUTF8Prefix(text string, maxBytes int) string {
	if maxBytes >= len(text) {
		return text
	}
	if maxBytes <= 0 {
		return ""
	}
	for maxBytes > 0 && !utf8.ValidString(text[:maxBytes]) {
		maxBytes--
	}
	return text[:maxBytes]
}

func trimValidUTF8Suffix(text string, maxBytes int) string {
	if maxBytes >= len(text) {
		return text
	}
	if maxBytes <= 0 {
		return ""
	}
	start := len(text) - maxBytes
	for start < len(text) && !utf8.ValidString(text[start:]) {
		start++
	}
	return text[start:]
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
