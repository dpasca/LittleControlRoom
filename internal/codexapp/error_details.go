package codexapp

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Keep upstream diagnostics visible in both live and resumed transcripts.
// Do not infer the failure category from the human-readable message.
func (e resumedTurnError) diagnosticText() string {
	parts := []string{strings.TrimSpace(e.Message)}
	for _, field := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"codexErrorInfo", e.CodexErrorInfo},
		{"additionalDetails", e.AdditionalDetails},
	} {
		raw := bytes.TrimSpace(field.raw)
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			continue
		}
		var compact bytes.Buffer
		if json.Compact(&compact, raw) != nil {
			continue
		}
		value := compact.String()
		if len(value) > 8000 {
			value = value[:8000] + " [truncated]"
		}
		parts = append(parts, field.name+": "+value)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
