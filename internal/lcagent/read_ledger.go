package lcagent

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"lcroom/internal/lcagent/tools"
)

const (
	readLedgerMaxFiles         = 40
	readLedgerMaxRangesPerFile = 6
	readLedgerMaxChars         = 8000
)

type readLedger struct {
	files map[string]readLedgerFile
}

type readLedgerFile struct {
	TotalLines int
	Ranges     []readSpan
}

type readSpan struct {
	Start int
	End   int
}

func newReadLedger() *readLedger {
	return &readLedger{files: map[string]readLedgerFile{}}
}

func (l *readLedger) ObserveReadResult(result tools.ToolResult) bool {
	if l == nil || !result.Success {
		return false
	}
	path, totalLines, span, ok := parseReadFileOutputRange(result.Output)
	if !ok {
		return false
	}
	if l.files == nil {
		l.files = map[string]readLedgerFile{}
	}
	entry := l.files[path]
	if totalLines > 0 {
		entry.TotalLines = totalLines
	}
	entry.Ranges = mergeReadSpans(entry.Ranges, span)
	l.files[path] = entry
	return true
}

func (l *readLedger) Stats() (int, int) {
	if l == nil {
		return 0, 0
	}
	ranges := 0
	for _, entry := range l.files {
		ranges += len(entry.Ranges)
	}
	return len(l.files), ranges
}

func (l *readLedger) Format(maxChars int) string {
	if l == nil || len(l.files) == 0 {
		return ""
	}
	if maxChars <= 0 {
		maxChars = readLedgerMaxChars
	}
	paths := make([]string, 0, len(l.files))
	for path := range l.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) > readLedgerMaxFiles {
		paths = paths[:readLedgerMaxFiles]
	}

	var b strings.Builder
	for _, path := range paths {
		entry := l.files[path]
		if len(entry.Ranges) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "- %s: %s", path, formatReadSpans(entry.Ranges, readLedgerMaxRangesPerFile))
		if entry.TotalLines > 0 {
			fmt.Fprintf(&b, " of %d", entry.TotalLines)
		}
	}
	if omitted := len(l.files) - len(paths); omitted > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "- ... %d more files omitted from ledger", omitted)
	}
	return truncateMiddle(strings.TrimSpace(b.String()), maxChars)
}

func parseReadFileOutputRange(output string) (string, int, readSpan, bool) {
	header := output
	if before, _, ok := strings.Cut(output, "\n\n"); ok {
		header = before
	}
	var path string
	var totalLines int
	var span readSpan
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "file:"):
			path = strings.TrimSpace(strings.TrimPrefix(line, "file:"))
		case strings.HasPrefix(line, "total_lines:"):
			value := strings.TrimSpace(strings.TrimPrefix(line, "total_lines:"))
			totalLines, _ = strconv.Atoi(value)
		case strings.HasPrefix(line, "lines:"):
			value := strings.TrimSpace(strings.TrimPrefix(line, "lines:"))
			if parsed, ok := parseReadSpan(value); ok {
				span = parsed
			}
		}
	}
	return path, totalLines, span, path != "" && span.Start > 0 && span.End >= span.Start
}

func parseReadSpan(value string) (readSpan, bool) {
	startRaw, endRaw, ok := strings.Cut(strings.TrimSpace(value), "-")
	if !ok {
		return readSpan{}, false
	}
	start, err := strconv.Atoi(strings.TrimSpace(startRaw))
	if err != nil {
		return readSpan{}, false
	}
	end, err := strconv.Atoi(strings.TrimSpace(endRaw))
	if err != nil {
		return readSpan{}, false
	}
	if start <= 0 || end < start {
		return readSpan{}, false
	}
	return readSpan{Start: start, End: end}, true
}

func mergeReadSpans(spans []readSpan, next readSpan) []readSpan {
	spans = append(append([]readSpan(nil), spans...), next)
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].Start == spans[j].Start {
			return spans[i].End < spans[j].End
		}
		return spans[i].Start < spans[j].Start
	})
	merged := spans[:0]
	for _, span := range spans {
		if len(merged) == 0 || span.Start > merged[len(merged)-1].End+1 {
			merged = append(merged, span)
			continue
		}
		if span.End > merged[len(merged)-1].End {
			merged[len(merged)-1].End = span.End
		}
	}
	return merged
}

func formatReadSpans(spans []readSpan, maxRanges int) string {
	if maxRanges <= 0 {
		maxRanges = readLedgerMaxRangesPerFile
	}
	parts := []string{}
	for i, span := range spans {
		if i >= maxRanges {
			parts = append(parts, fmt.Sprintf("... %d more ranges", len(spans)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%d-%d", span.Start, span.End))
	}
	return "lines " + strings.Join(parts, ", ")
}
