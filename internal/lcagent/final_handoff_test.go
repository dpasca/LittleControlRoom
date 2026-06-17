package lcagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/tools"
)

func TestCompactOpenRouterFinalMessagesSummarizesToolOutput(t *testing.T) {
	var output strings.Builder
	output.WriteString("file: big.go\ntotal_lines: 1000\nhas_more: false\nlines: 1-1000\n\n")
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&output, "%d | line %04d middle-marker\n", i, i)
	}
	result, err := json.Marshal(tools.ToolResult{Success: true, Output: output.String()})
	if err != nil {
		t.Fatal(err)
	}
	messages := []modeladapter.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "review the module"},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
			ID: "call_read",
			Function: modeladapter.FunctionCall{
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"big.go","limit":1000}`),
			},
		}}},
		{Role: "tool", ToolCallID: "call_read", Content: string(result)},
	}

	compacted, stats := compactOpenRouterFinalMessages(messages, "Do not call more tools.")
	if len(compacted) != 2 {
		t.Fatalf("compacted messages = %d, want 2", len(compacted))
	}
	if stats.ToolResults != 1 || stats.CompactedChars >= stats.OriginalChars {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	finalContent := compacted[len(compacted)-1].Content
	for _, want := range []string{"Current user request", "review the module", "tool_result: read_file", "file: big.go", "omitted", "Do not call more tools."} {
		if !strings.Contains(finalContent, want) {
			t.Fatalf("compacted content missing %q:\n%s", want, finalContent)
		}
	}
	if strings.Contains(finalContent, "line 0500 middle-marker") {
		t.Fatalf("compacted content kept middle of large tool output:\n%s", finalContent)
	}
}

func TestOpenRouterContextProfileBudgets(t *testing.T) {
	balanced := openRouterContextOptionsForProfile(openRouterContextProfileBalanced)
	if balanced.LoopCompactionCharThreshold != 200000 || balanced.LoopCompactionTranscriptChars != 50000 {
		t.Fatalf("balanced loop budget = threshold %d transcript %d, want 200000/50000", balanced.LoopCompactionCharThreshold, balanced.LoopCompactionTranscriptChars)
	}
	if balanced.FinalHandoffTranscriptMaxChars != 80000 {
		t.Fatalf("balanced final handoff transcript budget = %d, want 80000", balanced.FinalHandoffTranscriptMaxChars)
	}
	large := openRouterContextOptionsForProfile(openRouterContextProfileLarge)
	if large.LoopCompactionCharThreshold != 600000 || large.LoopCompactionTranscriptChars != 240000 {
		t.Fatalf("large loop budget = threshold %d transcript %d, want 600000/240000", large.LoopCompactionCharThreshold, large.LoopCompactionTranscriptChars)
	}
	if large.FinalHandoffTranscriptMaxChars != 240000 {
		t.Fatalf("large final handoff transcript budget = %d, want 240000", large.FinalHandoffTranscriptMaxChars)
	}
	if large.LoopCompactionCharThreshold <= balanced.LoopCompactionCharThreshold {
		t.Fatalf("large threshold = %d, want > balanced threshold %d", large.LoopCompactionCharThreshold, balanced.LoopCompactionCharThreshold)
	}
	if got := ContextCompactionApproxTokenBudget("balanced"); got != 50_000 {
		t.Fatalf("balanced approx token budget = %d, want 50000", got)
	}
	if got := ContextCompactionApproxTokenBudget("large"); got != 150_000 {
		t.Fatalf("large approx token budget = %d, want 150000", got)
	}
}

func TestOpenRouterContextModelAwareBudgets(t *testing.T) {
	deepseek := openRouterContextOptionsForProfileAndModel(openRouterContextProfileBalanced, "deepseek", "deepseek-v4-pro")
	if deepseek.ModelContextWindowTokens != 1_000_000 || deepseek.LoopCompactionTokenBudget != 500_000 || deepseek.LoopCompactionUtilizationPercent != 50 {
		t.Fatalf("deepseek context budget = %+v, want 1M window with 50%% / 500000 token threshold", deepseek)
	}
	if deepseek.LoopCompactionCharThreshold != 2_000_000 || deepseek.LoopCompactionTranscriptChars != 600_000 {
		t.Fatalf("deepseek char budgets = threshold %d transcript %d, want 2000000/600000", deepseek.LoopCompactionCharThreshold, deepseek.LoopCompactionTranscriptChars)
	}
	compat := openRouterContextOptionsForProfileAndModel(openRouterContextProfileBalanced, "deepseek", "deepseek-chat")
	if compat.ModelContextWindowTokens != 1_000_000 || compat.LoopCompactionTokenBudget != 500_000 {
		t.Fatalf("deepseek compatibility context budget = %+v, want 1M window with 500000 token threshold", compat)
	}

	mimo := openRouterContextOptionsForProfileAndModel(openRouterContextProfileLarge, "xiaomi", "mimo-v2.5-pro")
	if mimo.ModelContextWindowTokens != 1_000_000 || mimo.LoopCompactionTokenBudget != 500_000 || mimo.LoopCompactionUtilizationPercent != 50 {
		t.Fatalf("mimo context budget = %+v, want 1M window with 50%% / 500000 token threshold", mimo)
	}
	if mimo.LoopCompactionCharThreshold != 2_000_000 || mimo.LoopCompactionTranscriptChars != 600_000 {
		t.Fatalf("mimo char budgets = threshold %d transcript %d, want 2000000/600000", mimo.LoopCompactionCharThreshold, mimo.LoopCompactionTranscriptChars)
	}
	if got := ContextCompactionApproxTokenBudgetForModel("large", "xiaomi", "mimo-v2.5-pro"); got != 500_000 {
		t.Fatalf("mimo approx token budget = %d, want 500000", got)
	}

	unknown := openRouterContextOptionsForProfileAndModel(openRouterContextProfileLarge, "openrouter", "custom-model")
	if unknown.ModelContextWindowTokens != 0 || unknown.LoopCompactionCharThreshold != 600_000 {
		t.Fatalf("unknown model budget = %+v, want large profile fallback", unknown)
	}
}

func TestCompactOpenRouterLoopMessagesPreservesRequestAndDropsToolRoles(t *testing.T) {
	var output strings.Builder
	output.WriteString("file: big.go\ntotal_lines: 1000\nhas_more: false\nlines: 1-1000\n\n")
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&output, "%d | line %04d %s\n", i, i, strings.Repeat("compact-me ", 24))
	}
	result, err := json.Marshal(tools.ToolResult{Success: true, Output: output.String()})
	if err != nil {
		t.Fatal(err)
	}
	messages := []modeladapter.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "review the module"},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
			ID: "call_read",
			Function: modeladapter.FunctionCall{
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"big.go","limit":1000}`),
			},
		}}},
		{Role: "tool", ToolCallID: "call_read", Content: string(result)},
	}

	compacted, stats, ok := compactOpenRouterLoopMessages(messages)
	if !ok {
		t.Fatal("compactOpenRouterLoopMessages() ok = false, want true")
	}
	if len(compacted) != 3 {
		t.Fatalf("compacted messages = %d, want 3", len(compacted))
	}
	if compacted[0].Role != "system" || compacted[1].Role != "user" || compacted[1].Content != "review the module" || compacted[2].Role != "user" {
		t.Fatalf("unexpected compacted shape: %#v", compacted)
	}
	for _, msg := range compacted {
		if msg.Role == "tool" || len(msg.ToolCalls) > 0 {
			t.Fatalf("compacted loop history kept tool protocol message: %#v", msg)
		}
	}
	if stats.ToolResults != 1 || stats.CompactedChars >= stats.OriginalChars {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	context := compacted[2].Content
	for _, want := range []string{loopCompactedContextPrefix, "tool_result: read_file", "file: big.go", "omitted", "call final_response"} {
		if !strings.Contains(context, want) {
			t.Fatalf("compacted loop context missing %q:\n%s", want, context)
		}
	}
}

func TestCompactOpenRouterLoopMessagesCanPackTextOnlyHistory(t *testing.T) {
	messages := []modeladapter.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "old request " + strings.Repeat("context ", 80)},
		{Role: "assistant", Content: "old answer " + strings.Repeat("details ", 80)},
		{Role: "user", Content: "new request"},
	}
	opts := defaultOpenRouterContextOptions()
	opts.LoopCompactionCharThreshold = 1000
	opts.LoopCompactionTranscriptChars = 600

	compacted, stats, ok := compactOpenRouterLoopMessagesWithOptions(messages, nil, opts)
	if !ok {
		t.Fatal("text-only oversized history did not compact")
	}
	if stats.ToolResults != 0 || stats.CompactedChars >= stats.OriginalChars {
		t.Fatalf("unexpected text-only compaction stats: %+v", stats)
	}
	if len(compacted) != 3 || compacted[1].Role != "user" || compacted[1].Content != "new request" {
		t.Fatalf("compacted text-only shape = %#v, want system + active request + compact context", compacted)
	}
	context := compacted[2].Content
	for _, want := range []string{loopCompactedContextPrefix, "old request", "details", "latest real user request above"} {
		if !strings.Contains(context, want) {
			t.Fatalf("compacted text-only context missing %q:\n%s", want, context)
		}
	}
}

func TestCompactOpenRouterLoopMessagesUsesLatestRealUserRequest(t *testing.T) {
	var output strings.Builder
	output.WriteString("file: big.go\ntotal_lines: 1000\nhas_more: false\nlines: 1-1000\n\n")
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&output, "%d | line %04d %s\n", i, i, strings.Repeat("compact-me ", 24))
	}
	result, err := json.Marshal(tools.ToolResult{Success: true, Output: output.String()})
	if err != nil {
		t.Fatal(err)
	}
	latestRequest := "build and run original FF, then FF with the new sprites"
	messages := []modeladapter.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "please check the handoff.md"},
		{Role: "assistant", Content: "handoff summary"},
		{Role: "user", Content: latestRequest},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
			ID: "call_read",
			Function: modeladapter.FunctionCall{
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"big.go","limit":1000}`),
			},
		}}},
		{Role: "tool", ToolCallID: "call_read", Content: string(result)},
	}

	compacted, _, ok := compactOpenRouterLoopMessages(messages)
	if !ok {
		t.Fatal("compactOpenRouterLoopMessages() ok = false, want true")
	}
	if compacted[1].Role != "user" || compacted[1].Content != latestRequest {
		t.Fatalf("compacted active request = %#v, want latest request %q", compacted[1], latestRequest)
	}
	context := compacted[2].Content
	for _, want := range []string{"latest real user request above", "user_note:", "please check the handoff.md", "tool_result: read_file"} {
		if !strings.Contains(context, want) {
			t.Fatalf("compacted context missing %q:\n%s", want, context)
		}
	}
}

func TestCompactOpenRouterLoopMessagesIgnoresHarnessFeedbackAsActiveRequest(t *testing.T) {
	var output strings.Builder
	output.WriteString("file: big.go\ntotal_lines: 1000\nhas_more: false\nlines: 1-1000\n\n")
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&output, "%d | line %04d %s\n", i, i, strings.Repeat("compact-me ", 24))
	}
	result, err := json.Marshal(tools.ToolResult{Success: true, Output: output.String()})
	if err != nil {
		t.Fatal(err)
	}
	latestRequest := "build and run original FF"
	messages := []modeladapter.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: latestRequest},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
			ID: "call_read",
			Function: modeladapter.FunctionCall{
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"big.go","limit":1000}`),
			},
		}}},
		{Role: "tool", ToolCallID: "call_read", Content: string(result)},
		{Role: "user", Content: "Verification feedback: ./build.sh -b failed. Rerun a purpose=verify check after fixing it."},
		{Role: "user", Content: "Patch feedback: README.md failed during apply: hunk context not found."},
	}

	compacted, _, ok := compactOpenRouterLoopMessages(messages)
	if !ok {
		t.Fatal("compactOpenRouterLoopMessages() ok = false, want true")
	}
	if compacted[1].Role != "user" || compacted[1].Content != latestRequest {
		t.Fatalf("compacted active request = %#v, want latest request %q", compacted[1], latestRequest)
	}
	context := compacted[2].Content
	for _, want := range []string{"Verification feedback:", "Patch feedback:", "tool_result: read_file"} {
		if !strings.Contains(context, want) {
			t.Fatalf("compacted context missing %q:\n%s", want, context)
		}
	}
}

func TestCompactOpenRouterLoopMessagesIncludesReadLedger(t *testing.T) {
	var output strings.Builder
	output.WriteString("file: big.go\ntotal_lines: 1000\nhas_more: false\nlines: 1-1000\n\n")
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&output, "%d | line %04d %s\n", i, i, strings.Repeat("compact-me ", 24))
	}
	result := tools.ToolResult{Success: true, Output: output.String()}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	ledger := newReadLedger()
	if !ledger.ObserveReadResult(result) {
		t.Fatal("ledger did not record read result")
	}
	messages := []modeladapter.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "review the module"},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
			ID: "call_read",
			Function: modeladapter.FunctionCall{
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"big.go","limit":1000}`),
			},
		}}},
		{Role: "tool", ToolCallID: "call_read", Content: string(resultJSON)},
	}

	compacted, stats, ok := compactOpenRouterLoopMessagesWithReadLedger(messages, ledger)
	if !ok {
		t.Fatal("compactOpenRouterLoopMessagesWithReadLedger() ok = false, want true")
	}
	if stats.ReadLedgerFiles != 1 || stats.ReadLedgerRanges != 1 {
		t.Fatalf("unexpected ledger stats: %+v", stats)
	}
	context := compacted[len(compacted)-1].Content
	for _, want := range []string{"Read ledger", "- big.go: lines 1-1000 of 1000", "Check the read ledger before calling read_file"} {
		if !strings.Contains(context, want) {
			t.Fatalf("compacted loop context missing %q:\n%s", want, context)
		}
	}
}

func TestCompactRunCommandGitStatusOmitsNoisyUntrackedSubtrees(t *testing.T) {
	output := strings.Join([]string{
		"On branch master",
		"Your branch is up to date with 'origin/master'.",
		"",
		"Untracked files:",
		"  (use \"git add <file>...\" to include in what will be committed)",
		"\tTools/render_sprites/README.md",
		"\tTools/render_sprites/node_modules/chromium-bidi/lib/cjs/bidiMapper/modules/cdp/CdpTarget.d.ts",
		"\tTools/render_sprites/node_modules/chromium-bidi/lib/cjs/bidiMapper/modules/cdp/CdpTarget.js",
		"\t_inspect_sprites/frame.png",
		"",
		"--- output truncated (296820 bytes) ---",
		"Full output: /tmp/command-output.txt",
		"Explore: tail -100 /tmp/command-output.txt",
		"[exit:0 | 199ms]",
	}, "\n")
	resultJSON, err := json.Marshal(tools.ToolResult{
		Success:      true,
		Output:       output,
		Command:      "git status --untracked-files=all",
		Truncated:    true,
		ArtifactPath: "/tmp/command-output.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	compacted := compactToolResultEntry("run_command", `{"argv":["git","status","--untracked-files=all"]}`, string(resultJSON), 1400)
	for _, want := range []string{"git status output compacted", "changed_or_untracked_roots:", "- Tools/render_sprites/", "- _inspect_sprites/", "noisy_subtrees_omitted:", "- Tools/render_sprites/node_modules/", "full_output: /tmp/command-output.txt"} {
		if !strings.Contains(compacted, want) {
			t.Fatalf("compacted status missing %q:\n%s", want, compacted)
		}
	}
	if strings.Contains(compacted, "chromium-bidi") || strings.Contains(compacted, "CdpTarget") {
		t.Fatalf("compacted status kept noisy node_modules detail:\n%s", compacted)
	}
}

func TestLargeContextProfileDefersLoopCompaction(t *testing.T) {
	var output strings.Builder
	output.WriteString("file: big.go\ntotal_lines: 1000\nhas_more: false\nlines: 1-1000\n\n")
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&output, "%d | line %04d %s\n", i, i, strings.Repeat("compact-me ", 24))
	}
	result, err := json.Marshal(tools.ToolResult{Success: true, Output: output.String()})
	if err != nil {
		t.Fatal(err)
	}
	messages := []modeladapter.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "review the module"},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
			ID: "call_read",
			Function: modeladapter.FunctionCall{
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"big.go","limit":1000}`),
			},
		}}},
		{Role: "tool", ToolCallID: "call_read", Content: string(result)},
	}

	if _, _, ok := compactOpenRouterLoopMessages(messages); !ok {
		t.Fatal("default loop compaction did not trigger")
	}
	largeOpts := openRouterContextOptionsForProfile(openRouterContextProfileLarge)
	if _, _, ok := compactOpenRouterLoopMessagesWithOptions(messages, nil, largeOpts); ok {
		t.Fatal("large context profile compacted before its larger threshold")
	}
	if largeOpts.LoopCompactionCharThreshold <= loopCompactionCharThreshold {
		t.Fatalf("large threshold = %d, want > %d", largeOpts.LoopCompactionCharThreshold, loopCompactionCharThreshold)
	}
}

func TestParseOpenRouterContextProfile(t *testing.T) {
	profile, err := parseOpenRouterContextProfile(" large ")
	if err != nil {
		t.Fatal(err)
	}
	if profile != openRouterContextProfileLarge {
		t.Fatalf("profile = %q, want large", profile)
	}
	if _, err := parseOpenRouterContextProfile("huge"); err == nil {
		t.Fatal("invalid context profile parsed without error")
	}
}
