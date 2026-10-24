package codexapp

import "strings"

const reportContractContextSource = "little-control-room/report-contract"

var reportContractLines = []string{
	"- Answer the user's exact request directly and include enough concrete detail to verify the outcome.",
	"- Preserve source, metric, timeframe, scope, negations, and explicit exclusions from the user request; if evidence answers a different question, report that mismatch instead of substituting it.",
	"- For comparison, diff, cleanup, or review work, name what was compared, what was kept, what was discarded, and the substantive differences.",
	"- For retry, sync, export, file, or document work, say whether content changed and summarize the meaningful changes; if nothing changed, name the file or document and say there were no content changes.",
	"- Do not final-report only success/failure plus artifact links; include the substantive outcome, or say exactly which requested outcome evidence is still missing.",
	"- Avoid vague wrap-ups like only saying the entries differ, the state is clean, canonical copies were kept, or the retry succeeded.",
}

// ReportContractLines returns the engineer report contract bullets. Providers
// that cannot carry session-level instructions get them inline in the prompt.
func ReportContractLines() []string {
	return append([]string(nil), reportContractLines...)
}

// ReportContractInstructions is the contract as session-level instructions.
// Hosts attach it to every turn (Codex) or the system prompt (Claude Code), so
// it survives context compaction and follow-up prompts need not repeat it.
func ReportContractInstructions() string {
	return "Little Control Room report contract for every report in this session:\n" + strings.Join(reportContractLines, "\n")
}

// ProviderCarriesReportContract reports whether sessions launched with
// LaunchRequest.ReportContract keep the contract in session-level
// instructions instead of needing it inline in each prompt.
func ProviderCarriesReportContract(provider Provider) bool {
	switch provider.Normalized() {
	case ProviderCodex, ProviderClaudeCode:
		return true
	default:
		return false
	}
}

func reportContractInstructionsFor(req LaunchRequest) string {
	if !req.ReportContract {
		return ""
	}
	return ReportContractInstructions()
}
