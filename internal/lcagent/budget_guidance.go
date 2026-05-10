package lcagent

import (
	"fmt"
	"strings"

	"lcroom/internal/lcagent/modeladapter"
)

const (
	openRouterProgressNotePrefix          = "Harness progress note for this model request."
	openRouterProgressLedgerChars         = 3000
	openRouterSynthesisRemainingTurns     = 8
	openRouterMinimumTurnBeforeSynthesis  = 20
	openRouterConsolidationTurnMultiplier = 2
)

type openRouterProgressGuidance struct {
	Turn             int    `json:"turn"`
	MaxTurns         int    `json:"max_turns"`
	TurnsRemaining   int    `json:"turns_remaining"`
	Phase            string `json:"phase"`
	ForceSynthesis   bool   `json:"force_synthesis"`
	ToolResults      int    `json:"tool_results"`
	ToolProfile      string `json:"tool_profile,omitempty"`
	ReadLedgerFiles  int    `json:"read_ledger_files,omitempty"`
	ReadLedgerRanges int    `json:"read_ledger_ranges,omitempty"`
}

type openRouterGuidanceOptions struct {
	ToolProfile string
}

func openRouterGuidanceForTurn(turn, maxTurns int, messages []modeladapter.Message, ledger *readLedger) openRouterProgressGuidance {
	return openRouterGuidanceForTurnWithOptions(turn, maxTurns, messages, ledger, openRouterGuidanceOptions{})
}

func openRouterGuidanceForTurnWithOptions(turn, maxTurns int, messages []modeladapter.Message, ledger *readLedger, opts openRouterGuidanceOptions) openRouterProgressGuidance {
	if turn < 1 {
		turn = 1
	}
	if maxTurns < turn {
		maxTurns = turn
	}
	remaining := maxTurns - turn
	files, ranges := ledger.Stats()
	guidance := openRouterProgressGuidance{
		Turn:             turn,
		MaxTurns:         maxTurns,
		TurnsRemaining:   remaining,
		Phase:            "exploration",
		ToolResults:      countToolResultMessages(messages),
		ToolProfile:      strings.TrimSpace(opts.ToolProfile),
		ReadLedgerFiles:  files,
		ReadLedgerRanges: ranges,
	}
	if turn*openRouterConsolidationTurnMultiplier >= maxTurns {
		guidance.Phase = "consolidation"
	}
	if turn >= openRouterMinimumTurnBeforeSynthesis && remaining <= openRouterSynthesisRemainingTurns {
		guidance.Phase = "synthesis"
		guidance.ForceSynthesis = true
	}
	return guidance
}

func appendOpenRouterProgressNote(messages []modeladapter.Message, guidance openRouterProgressGuidance, ledger *readLedger) []modeladapter.Message {
	note := openRouterProgressNote(guidance, ledger)
	if strings.TrimSpace(note) == "" {
		return messages
	}
	out := append([]modeladapter.Message(nil), messages...)
	out = append(out, modeladapter.Message{Role: "user", Content: note})
	return out
}

func openRouterProgressNote(guidance openRouterProgressGuidance, ledger *readLedger) string {
	var b strings.Builder
	b.WriteString(openRouterProgressNotePrefix)
	b.WriteString("\n\nThis is not a new user request. It is transient harness guidance; do not quote it as user intent.")
	fmt.Fprintf(&b, "\n\nBudget:\n- turn: %d of %d\n- turns remaining after this response: %d\n- phase: %s\n- tool results observed: %d",
		guidance.Turn,
		guidance.MaxTurns,
		guidance.TurnsRemaining,
		guidance.Phase,
		guidance.ToolResults,
	)
	if guidance.ReadLedgerFiles > 0 {
		fmt.Fprintf(&b, "\n- read ledger: %d files, %d ranges", guidance.ReadLedgerFiles, guidance.ReadLedgerRanges)
	}
	if ledgerText := ledger.Format(openRouterProgressLedgerChars); ledgerText != "" {
		b.WriteString("\n\nRead ledger summary:\n")
		b.WriteString(indentBlock(ledgerText))
	}
	b.WriteString("\n\nGuidance:\n")
	switch guidance.Phase {
	case "synthesis":
		b.WriteString("- Tools are unavailable for this request. Produce the final user-facing answer now from gathered evidence.\n")
		b.WriteString("- Do not say the turn budget was reached; this is a planned synthesis checkpoint before the hard cap.\n")
		b.WriteString("- Distinguish confirmed gaps from unverified items. A feature is not missing merely because there is no same-named file; it may be implemented inline in CLI, script, or orchestration code.\n")
		b.WriteString("- Include enough uncertainty to be honest, but avoid asking the user to continue unless a concrete blocker remains.")
	case "consolidation":
		b.WriteString("- Start converging on the answer. Each new tool call should resolve a named blocker for the final response.\n")
		b.WriteString("- Prefer final_response once the main question can be answered with the evidence already collected.\n")
		b.WriteString("- Do not audit every file for completeness; prioritize the user-visible conclusion.")
	default:
		if strings.EqualFold(guidance.ToolProfile, "generous") {
			b.WriteString("- Be evidence-complete rather than overly terse. Use outline/search to choose files, then read larger contiguous ranges for central files.\n")
			b.WriteString("- Continue with next_offset for relevant files instead of sampling only first chunks. Do not reread ranges already listed in the read ledger.\n")
		} else {
			b.WriteString("- Inspect narrowly. Prefer outline/search before broad reads, and do not reread ranges already listed in the read ledger.\n")
		}
		b.WriteString("- Keep track of what answer the user needs, not just what can be inspected next.")
	}
	return b.String()
}
