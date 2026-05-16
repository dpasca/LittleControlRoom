package codexapp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func writeLCAgentCompactSummary(dataDir string, trace LCAgentTrace) (string, string, error) {
	sessionID := strings.TrimSpace(trace.SessionID)
	if sessionID == "" {
		return "", "", fmt.Errorf("cannot compact LCAgent trace without a session id")
	}
	path := lcagentCompactSummaryPath(dataDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	summary := formatLCAgentCompactMarkdown(trace)
	if err := os.WriteFile(path, []byte(summary), 0o600); err != nil {
		return "", "", err
	}
	return path, summary, nil
}

func lcagentCompactSummaryPath(dataDir, sessionID string) string {
	return filepath.Join(dataDir, "lcagent", "compact", sanitizeLCAgentCompactName(sessionID)+".md")
}

func sanitizeLCAgentCompactName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "._")
}

func formatLCAgentCompactMarkdown(trace LCAgentTrace) string {
	var b strings.Builder
	b.WriteString("# LCAgent Compact Summary\n\n")
	writeLCAgentCompactField(&b, "Session", trace.SessionID)
	writeLCAgentCompactField(&b, "Project", trace.ProjectPath)
	writeLCAgentCompactField(&b, "Artifact", trace.ArtifactPath)
	if !trace.StartedAt.IsZero() {
		writeLCAgentCompactField(&b, "Started", trace.StartedAt.Format(time.RFC3339))
	}
	if !trace.LastActivityAt.IsZero() {
		writeLCAgentCompactField(&b, "Last activity", trace.LastActivityAt.Format(time.RFC3339))
	}
	writeLCAgentCompactField(&b, "Status", firstNonEmpty(trace.VerificationStatus, "unknown"))
	if trace.ResumeSourceSessionID != "" {
		writeLCAgentCompactField(&b, "Continued from", trace.ResumeSourceSessionID)
	}
	if trace.ContinuationRootSessionID != "" {
		writeLCAgentCompactField(&b, "Continuation root", trace.ContinuationRootSessionID)
	}
	if trace.ContinuationChainDepth > 0 {
		writeLCAgentCompactField(&b, "Continuation depth", fmt.Sprint(trace.ContinuationChainDepth))
	}
	if compact := strings.TrimSpace(trace.CompactSummary()); compact != "" {
		b.WriteString("\n## Handoff\n\n")
		b.WriteString(compact)
		b.WriteString("\n")
	}
	if summary := strings.TrimSpace(trace.Summary); summary != "" {
		b.WriteString("\n## Final Summary\n\n")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	writeLCAgentCompactList(&b, "Files Changed", trace.FilesChanged)
	writeLCAgentCompactList(&b, "Reported Verification", trace.Verification)
	writeLCAgentCompactList(&b, "Pending Files To Verify", trace.PendingFiles)
	writeLCAgentCompactList(&b, "Pending Verification Evidence", trace.PendingVerification)
	writeLCAgentCompactList(&b, "Actual Checks", trace.ActualCheckSummaries())
	writeLCAgentCompactList(&b, "Verification Summaries", trace.VerificationSummaries)
	writeLCAgentCompactList(&b, "Trace Quality", []string{trace.TraceQualitySummary()})
	if len(trace.PermissionDenials) > 0 {
		var denials []string
		for _, denial := range trace.PermissionDenials {
			denials = append(denials, firstNonEmpty(denial.Tool, "tool")+": "+denial.Reason)
		}
		writeLCAgentCompactList(&b, "Permission Denials", denials)
	}
	writeLCAgentCompactList(&b, "Patch Feedback", trace.PatchFeedback)
	writeLCAgentCompactList(&b, "Verification Feedback", trace.VerificationFeedback)
	writeLCAgentCompactList(&b, "Repair Guidance", trace.RepairGuidance)
	b.WriteString("\n## Continuation Note\n\n")
	b.WriteString("This summary is a handoff aid, not source truth for file contents. Re-read exact workspace files before editing.\n")
	return b.String()
}

func writeLCAgentCompactField(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	fmt.Fprintf(b, "- %s: %s\n", label, value)
}

func writeLCAgentCompactList(b *strings.Builder, title string, values []string) {
	values = cleanLCAgentStringList(values)
	if len(values) == 0 {
		return
	}
	b.WriteString("\n## ")
	b.WriteString(title)
	b.WriteString("\n\n")
	for _, value := range values {
		fmt.Fprintf(b, "- %s\n", value)
	}
}
