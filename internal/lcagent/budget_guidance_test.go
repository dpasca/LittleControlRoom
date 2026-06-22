package lcagent

import (
	"strings"
	"testing"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/tools"
)

func TestOpenRouterProgressGuidancePhases(t *testing.T) {
	messages := []modeladapter.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "task"},
		{Role: "tool", Content: `{}`},
	}
	early := openRouterGuidanceForTurn(3, 32, messages, nil)
	if early.Phase != "exploration" || early.ForceSynthesis {
		t.Fatalf("early guidance = %+v, want exploration without forced synthesis", early)
	}
	mid := openRouterGuidanceForTurn(16, 32, messages, nil)
	if mid.Phase != "consolidation" || mid.ForceSynthesis {
		t.Fatalf("mid guidance = %+v, want consolidation without forced synthesis", mid)
	}
	late := openRouterGuidanceForTurn(136, 160, messages, nil)
	if late.Phase != "endgame" || late.ForceSynthesis || late.TurnsRemaining != 24 {
		t.Fatalf("late guidance = %+v, want endgame without forced synthesis near hard cap", late)
	}
	shortRun := openRouterGuidanceForTurn(13, 16, messages, nil)
	if shortRun.ForceSynthesis {
		t.Fatalf("short-run guidance forced synthesis too early: %+v", shortRun)
	}
}

func TestOpenRouterProgressNoteIncludesReadLedgerAndSynthesisInstructions(t *testing.T) {
	ledger := newReadLedger()
	if !ledger.ObserveReadResult(tools.ToolResult{
		Success: true,
		Output:  "file: a.go\ntotal_lines: 200\nhas_more: false\nlines: 1-80\n\n1 | package a\n",
	}) {
		t.Fatal("ledger did not record read result")
	}
	guidance := openRouterProgressGuidance{
		Turn:            24,
		MaxTurns:        128,
		TurnsRemaining:  104,
		Phase:           "synthesis",
		ForceSynthesis:  true,
		SynthesisReason: "stalled",
		NoProgressTurns: openRouterStallSynthesisAfterTurns,
		ToolResults:     1,
		ReadLedgerFiles: 1,
	}
	note := openRouterProgressNote(guidance, ledger)
	for _, want := range []string{
		openRouterProgressNotePrefix,
		"turn: 24 of 128",
		"phase: synthesis",
		"- a.go: lines 1-80 of 200",
		"Tools are unavailable",
		"have not produced new tool evidence",
		"not missing merely because there is no same-named file",
	} {
		if !strings.Contains(note, want) {
			t.Fatalf("progress note missing %q:\n%s", want, note)
		}
	}
}

func TestOpenRouterProgressNoteKeepsConsolidationExecutionAware(t *testing.T) {
	guidance := openRouterGuidanceForTurn(16, 32, nil, nil)
	note := openRouterProgressNote(guidance, nil)
	for _, want := range []string{"phase: consolidation", "for execution requests", "do not skip to final_response before acting"} {
		if !strings.Contains(note, want) {
			t.Fatalf("consolidation progress note missing %q:\n%s", want, note)
		}
	}
}

func TestOpenRouterProgressNoteUsesEndgameGuidance(t *testing.T) {
	guidance := openRouterGuidanceForTurn(136, 160, nil, nil)
	note := openRouterProgressNote(guidance, nil)
	for _, want := range []string{"phase: endgame", "stop adding optional scope", "visual evidence", "requirement evidence"} {
		if !strings.Contains(note, want) {
			t.Fatalf("endgame progress note missing %q:\n%s", want, note)
		}
	}
}

func TestOpenRouterProgressNoteUsesGenerousExplorationGuidance(t *testing.T) {
	guidance := openRouterGuidanceForTurnWithOptions(1, 32, nil, nil, openRouterGuidanceOptions{ToolProfile: "generous"})
	note := openRouterProgressNote(guidance, nil)
	for _, want := range []string{"larger contiguous ranges", "Continue with next_offset", "Do not reread ranges", "read ledger"} {
		if !strings.Contains(note, want) {
			t.Fatalf("generous progress note missing %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "Inspect narrowly") {
		t.Fatalf("generous progress note kept balanced guidance:\n%s", note)
	}
}

func TestAppendOpenRouterProgressNoteDoesNotMutateHistory(t *testing.T) {
	messages := []modeladapter.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "task"},
	}
	guidance := openRouterGuidanceForTurn(1, 32, messages, nil)
	requestMessages := appendOpenRouterProgressNote(messages, guidance, nil)
	if len(messages) != 2 {
		t.Fatalf("history length = %d, want unchanged 2", len(messages))
	}
	if len(requestMessages) != 3 {
		t.Fatalf("request message length = %d, want 3", len(requestMessages))
	}
	if requestMessages[2].Role != "user" || !strings.Contains(requestMessages[2].Content, openRouterProgressNotePrefix) {
		t.Fatalf("request note not appended as user message: %#v", requestMessages[2])
	}
}
