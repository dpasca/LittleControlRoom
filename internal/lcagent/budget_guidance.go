package lcagent

import (
	"fmt"
	"strings"

	"lcroom/internal/lcagent/modeladapter"
)

const (
	openRouterProgressNotePrefix          = "Harness progress note for this model request."
	openRouterProgressLedgerChars         = 3000
	openRouterStallSynthesisAfterTurns    = 6
	openRouterMinimumTurnBeforeStallCheck = 12
	openRouterConsolidationTurnMultiplier = 2
	openRouterEndgameTurnPercent          = 85
	openRouterSearchConsolidationCalls    = 12
)

type openRouterProgressGuidance struct {
	Turn             int    `json:"turn"`
	MaxTurns         int    `json:"max_turns"`
	TurnsRemaining   int    `json:"turns_remaining"`
	Phase            string `json:"phase"`
	ForceSynthesis   bool   `json:"force_synthesis"`
	SynthesisReason  string `json:"synthesis_reason,omitempty"`
	NoProgressTurns  int    `json:"no_progress_turns,omitempty"`
	ToolResults      int    `json:"tool_results"`
	WebSearchCalls   int    `json:"web_search_calls,omitempty"`
	ToolProfile      string `json:"tool_profile,omitempty"`
	ReadLedgerFiles  int    `json:"read_ledger_files,omitempty"`
	ReadLedgerRanges int    `json:"read_ledger_ranges,omitempty"`
}

type openRouterGuidanceOptions struct {
	ToolProfile    string
	WebSearchCalls int
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
		WebSearchCalls:   opts.WebSearchCalls,
		ToolProfile:      strings.TrimSpace(opts.ToolProfile),
		ReadLedgerFiles:  files,
		ReadLedgerRanges: ranges,
	}
	if turn*100 >= maxTurns*openRouterEndgameTurnPercent {
		guidance.Phase = "endgame"
	} else if turn*openRouterConsolidationTurnMultiplier >= maxTurns || opts.WebSearchCalls >= openRouterSearchConsolidationCalls {
		guidance.Phase = "consolidation"
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
	if guidance.WebSearchCalls > 0 {
		fmt.Fprintf(&b, "\n- web search calls for the current request: %d", guidance.WebSearchCalls)
	}
	if ledgerText := ledger.Format(openRouterProgressLedgerChars); ledgerText != "" {
		b.WriteString("\n\nRead ledger summary:\n")
		b.WriteString(indentBlock(ledgerText))
	}
	b.WriteString("\n\nGuidance:\n")
	if guidance.WebSearchCalls >= openRouterSearchConsolidationCalls && !guidance.ForceSynthesis {
		// This is a work-budget checkpoint, not an intent or relevance classifier.
		// The model must assess the evidence; search count alone never stops work.
		b.WriteString("- Before another search batch, briefly update the user with what is established, what remains unknown, and which specific gap the next action will resolve.\n")
		b.WriteString("- Assess whether recent searches added useful evidence. Different query wording or snippets alone do not establish progress. Inspect promising sources, correct unsuitable filters, or finish with an honest partial answer when no productive next step remains.\n")
		b.WriteString("- The search count is a checkpoint, not a hard limit. Continue justified investigation or execution when a concrete completion blocker remains.\n")
	}
	switch guidance.Phase {
	case "synthesis":
		b.WriteString("- Tools are unavailable for this request. Produce the final user-facing answer now from gathered evidence.\n")
		if strings.EqualFold(guidance.SynthesisReason, "stalled") {
			fmt.Fprintf(&b, "- Recent turns have not produced new tool evidence, file changes, or verification for %d turn%s. Finish honestly instead of continuing to churn.\n", guidance.NoProgressTurns, pluralSuffix(guidance.NoProgressTurns))
		} else {
			b.WriteString("- This is a planned synthesis checkpoint before the hard cap; do not say the turn budget was reached.\n")
		}
		b.WriteString("- Distinguish confirmed gaps from unverified items. A feature is not missing merely because there is no same-named file; it may be implemented inline in CLI, script, or orchestration code.\n")
		b.WriteString("- Include enough uncertainty to be honest, but avoid asking the user to continue unless a concrete blocker remains.")
	case "consolidation":
		b.WriteString("- Start converging on the answer. Each new tool call should resolve a named blocker for the final response.\n")
		b.WriteString("- Prefer final_response once the main question can be answered with the evidence already collected; for execution requests, do not skip to final_response before acting or reporting a blocker.\n")
		b.WriteString("- Do not audit every file for completeness; prioritize the user-visible conclusion.")
	case "endgame":
		b.WriteString("- This is the endgame phase: stop adding optional scope and converge on evidence.\n")
		b.WriteString("- Each tool call should resolve a concrete completion blocker: build/run/test, visual evidence, requirement evidence, or final_response.\n")
		b.WriteString("- Prefer repairing failed evidence over adding new features; finish partial/blocked/failed honestly when remaining gaps cannot be closed with the available tools.")
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

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
