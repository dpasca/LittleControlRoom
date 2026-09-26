package codexapp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexcli"
)

const claudeModelCatalogHelperEnv = "LCROOM_CLAUDE_MODEL_CATALOG_HELPER"

// claudeInitializeModelsFixture mirrors the models section Claude Code 2.1
// returns from its stream-json initialize handshake.
const claudeInitializeModelsFixture = `[
{"value":"default","resolvedModel":"claude-opus-5-5[1m]","displayName":"Default (recommended)","description":"Opus 5.5 with 1M context · Best for everyday, complex tasks","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]},
{"value":"opus[1m]","resolvedModel":"claude-opus-5-5[1m]","displayName":"Opus (1M context)","description":"Opus 5.5 with 1M context · Best for everyday, complex tasks","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]},
{"value":"claude-fable-5-1[1m]","resolvedModel":"claude-fable-5-1","displayName":"Fable","description":"Fable 5.1 · Most capable for your hardest and longest-running tasks","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]},
{"value":"sonnet","resolvedModel":"claude-sonnet-5","displayName":"Sonnet","description":"Sonnet 5 · Efficient for routine tasks","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]},
{"value":"haiku","resolvedModel":"claude-haiku-4-5-20251001","displayName":"Haiku","description":"Haiku 4.5 · Fastest for quick answers"}
]`

func TestClaudeModelCatalogHelperProcess(t *testing.T) {
	if os.Getenv(claudeModelCatalogHelperEnv) != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, `"subtype":"initialize"`) || !strings.Contains(line, claudeModelCatalogRequestID) {
			continue
		}
		fmt.Println(`{"type":"system","subtype":"status"}`)
		models := strings.ReplaceAll(claudeInitializeModelsFixture, "\n", "")
		fmt.Printf(`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"commands":[{"name":%q}],"models":%s}}}`+"\n", claudeModelCatalogRequestID, strings.Repeat("x", 128*1024), models)
	}
	os.Exit(0)
}

func useClaudeModelCatalogHelper(t *testing.T) {
	t.Helper()
	original := newClaudeModelCatalogCommand
	newClaudeModelCatalogCommand = func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestClaudeModelCatalogHelperProcess$")
		cmd.Env = append(os.Environ(), claudeModelCatalogHelperEnv+"=1")
		return cmd
	}
	t.Cleanup(func() { newClaudeModelCatalogCommand = original })
	resetClaudeModelCatalogCache(t)
}

func resetClaudeModelCatalogCache(t *testing.T) {
	t.Helper()
	reset := func() {
		claudeModelCatalog.mu.Lock()
		claudeModelCatalog.entries = nil
		claudeModelCatalog.latest = nil
		claudeModelCatalog.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func TestLoadClaudeModelCatalogReadsInitializeModels(t *testing.T) {
	useClaudeModelCatalogHelper(t)

	options, err := loadClaudeModelCatalog(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("loadClaudeModelCatalog() error = %v", err)
	}
	var got []string
	for _, option := range options {
		got = append(got, option.Model+"="+option.DisplayName)
	}
	want := []string{
		"default=Opus 5.5 (1M)",
		"opus[1m]=Opus 5.5 (1M)",
		"claude-fable-5-1[1m]=Fable 5.1",
		"sonnet=Sonnet 5",
		"haiku=Haiku 4.5",
		"fable=Fable 5.1",
		"opus=Opus 5.5",
		"sonnet[1m]=Sonnet 5 (1M)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("catalog options =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !options[0].IsDefault || options[3].IsDefault {
		t.Fatalf("only Claude Code's default choice should be the default: %#v", options)
	}
	if len(options[4].SupportedReasoningEfforts) != 0 {
		t.Fatalf("haiku efforts = %#v, want none because the CLI reports no effort support", options[4].SupportedReasoningEfforts)
	}
	if options[3].DefaultReasoningEffort != claudeDefaultReasoningEffort || len(options[3].SupportedReasoningEfforts) != 5 {
		t.Fatalf("sonnet efforts = %#v default %q", options[3].SupportedReasoningEfforts, options[3].DefaultReasoningEffort)
	}
	if got := ModelDisplayName(ProviderClaudeCode, "opus"); got != "Opus 5.5" {
		t.Fatalf("ModelDisplayName(opus) = %q, want the version the catalog resolves", got)
	}
	if got := ModelDisplayName(ProviderClaudeCode, "default"); got != "Opus 5.5 (1M)" {
		t.Fatalf("ModelDisplayName(default) = %q", got)
	}
}

func TestClaudeCatalogPreservesNativeContextAliasesWhenPickerOmitsThem(t *testing.T) {
	// New CLI versions can show only the base alias for a model with native
	// 1M context. That does not invalidate an earlier opus[1m] launch choice.
	models := []claudeCLIModel{
		{Value: "default", ResolvedModel: "claude-opus-5-5", SupportedEffortLevels: []string{"medium", "xhigh"}},
		{Value: "opus", ResolvedModel: "claude-opus-5-5", SupportedEffortLevels: []string{"medium", "xhigh"}},
		{Value: "sonnet", ResolvedModel: "claude-sonnet-5", SupportedEffortLevels: []string{"medium", "high"}},
	}
	options := claudeModelOptionsFromCLI(models)
	for _, tc := range []struct {
		id       string
		resolved string
		efforts  string
	}{
		{"opus[1m]", "claude-opus-5-5[1m]", "medium,xhigh"},
		{"sonnet[1m]", "claude-sonnet-5[1m]", "medium,high"},
	} {
		found := false
		for _, option := range options {
			if option.Model != tc.id {
				continue
			}
			found = true
			var efforts []string
			for _, effort := range option.SupportedReasoningEfforts {
				efforts = append(efforts, effort.ReasoningEffort)
			}
			if option.ResolvedModel != tc.resolved || strings.Join(efforts, ",") != tc.efforts || option.IsDefault {
				t.Fatalf("native alias metadata = %#v", option)
			}
		}
		if !found {
			t.Fatalf("native alias %q disappeared with its picker row", tc.id)
		}
	}
}

func TestLoadClaudeModelCatalogKeepsStaleChoicesWhenRefreshFails(t *testing.T) {
	useClaudeModelCatalogHelper(t)
	dir := t.TempDir()
	if _, err := loadClaudeModelCatalog(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	claudeModelCatalog.mu.Lock()
	entry := claudeModelCatalog.entries[dir]
	entry.fetchedAt = time.Now().Add(-2 * claudeModelCatalogTTL)
	claudeModelCatalog.entries[dir] = entry
	claudeModelCatalog.mu.Unlock()
	newClaudeModelCatalogCommand = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "lcroom-missing-claude-for-test")
	}

	options, err := loadClaudeModelCatalog(context.Background(), dir)
	if err != nil || len(options) == 0 || options[0].Model != "default" {
		t.Fatalf("stale refresh = %#v, %v; want previous choices", options, err)
	}
}

func TestClaudeListModelsUsesCatalogWithoutDuplicatingResolvedCurrentModel(t *testing.T) {
	useClaudeModelCatalogHelper(t)
	session := &claudeCodeSession{
		projectPath:  t.TempDir(),
		modelChoice:  "default",
		model:        "claude-opus-5-5",
		modelCatalog: loadClaudeModelCatalog,
	}

	models, err := session.ListModels()
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if models[0].Model != "default" {
		t.Fatalf("first model = %q, want catalog order without a duplicate current row: %#v", models[0].Model, models)
	}
	session.modelChoice = "claude-opus-5"
	models, err = session.ListModels()
	if err != nil {
		t.Fatal(err)
	}
	if models[0].Model != "claude-opus-5" || models[0].DisplayName != "Opus 5" {
		t.Fatalf("pinned older model row = %#v, want it listed as current", models[0])
	}
}

func TestClaudeModelDisplayName(t *testing.T) {
	for model, want := range map[string]string{
		"claude-opus-5-5":                            "Opus 5.5",
		"claude-opus-5-5[1m]":                        "Opus 5.5 (1M)",
		"claude-haiku-4-5-20251001":                  "Haiku 4.5",
		"claude-sonnet-4-20250514":                   "Sonnet 4",
		"claude-3-5-sonnet-20241022":                 "Sonnet 3.5",
		"us.anthropic.claude-opus-4-1-20250805-v1:0": "Opus 4.1",
		"claude-sonnet-4-5@20250929":                 "Sonnet 4.5",
		"claude-fable-5-1":                           "Fable 5.1",
		"opus":                                       "Opus",
		"sonnet[1m]":                                 "Sonnet (1M)",
		"default":                                    "Default",
		"gpt-5.5":                                    "gpt-5.5",
		"<synthetic>":                                "",
	} {
		if got := claudeModelDisplayName(model); got != want {
			t.Errorf("claudeModelDisplayName(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestModelDisplayNameLeavesOtherProvidersUnchanged(t *testing.T) {
	if got := ModelDisplayName(ProviderCodex, "claude-opus-5"); got != "claude-opus-5" {
		t.Fatalf("Codex model label = %q, want raw ID", got)
	}
}

func TestParseClaudeModelCatalogLineReportsInitializeErrors(t *testing.T) {
	_, ok, err := parseClaudeModelCatalogLine([]byte(`{"type":"control_response","response":{"subtype":"error","request_id":"` + claudeModelCatalogRequestID + `","error":"not logged in"}}`))
	if !ok || err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("parse error response = ok %t err %v", ok, err)
	}
}

func TestClaudeModelStagedMidTurnSurvivesRunningTurnReports(t *testing.T) {
	session := &claudeCodeSession{
		modelChoice:      "claude-opus-5",
		modelChoiceSet:   true,
		reasoningEffort:  "high",
		pendingModel:     "opus[1m]",
		pendingReasoning: "max",
		assistantBlocks:  make(map[string]map[string]struct{}),
		toolCalls:        make(map[string]claudeToolCall),
		toolResults:      make(map[string]struct{}),
	}

	session.handleClaudeStdoutLine(`{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-5"}`)
	session.handleClaudeStdoutLine(`{"type":"assistant","message":{"id":"msg_1","model":"claude-opus-5","role":"assistant","content":[{"type":"text","text":"Still working."}]}}`)

	snapshot := session.Snapshot()
	if snapshot.PendingModel != "opus[1m]" || snapshot.PendingReasoning != "max" {
		t.Fatalf("pending = %q/%q, want the choice staged after launch kept for the next turn", snapshot.PendingModel, snapshot.PendingReasoning)
	}
	if snapshot.Model != "claude-opus-5" || snapshot.ReportedModel != "claude-opus-5" || snapshot.ReasoningEffort != "high" {
		t.Fatalf("current = %q (reported %q) / %q, want the running turn's choice and effort", snapshot.Model, snapshot.ReportedModel, snapshot.ReasoningEffort)
	}
}

func TestClaudeLaunchUsesChoiceAndConsumesStagedChoice(t *testing.T) {
	binDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args.log")
	script := `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ] && [ "$3" = "--json" ]; then
	printf '%s\n' '{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}'
	exit 0
fi
printf '%s\n' "$*" >> "$CLAUDE_TEST_ARGS"
IFS= read -r payload || exit 2
printf '%s\n' '{"type":"system","subtype":"init","session_id":"ses-choice","model":"claude-opus-5-5[1m]"}'
printf '%s\n' '{"type":"assistant","message":{"id":"msg-final","model":"claude-opus-5-5","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done."}]}}'
while IFS= read -r trailing; do :; done
`
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("CLAUDE_TEST_ARGS", argsPath)

	session := &claudeCodeSession{
		projectPath:      t.TempDir(),
		claudeHome:       t.TempDir(),
		preset:           codexcli.PresetSafe,
		safetySettings:   `{"hooks":{"PreToolUse":[]}}`,
		status:           claudeFreshReadyStatus,
		closedCh:         make(chan struct{}),
		modelChoice:      "claude-opus-5",
		modelChoiceSet:   true,
		pendingModel:     "opus[1m]",
		pendingReasoning: "max",
		assistantBlocks:  make(map[string]map[string]struct{}),
		toolCalls:        make(map[string]claudeToolCall),
		toolResults:      make(map[string]struct{}),
	}
	t.Cleanup(func() { _ = session.Close() })

	for _, prompt := range []string{"first", "second"} {
		if err := session.Submit(prompt); err != nil {
			t.Fatalf("Submit(%q) error = %v", prompt, err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for session.Snapshot().Busy && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		snapshot := session.Snapshot()
		if snapshot.Busy {
			t.Fatalf("turn %q did not settle", prompt)
		}
		if snapshot.Model != "opus[1m]" || snapshot.PendingModel != "" || snapshot.ReasoningEffort != "max" || snapshot.PendingReasoning != "" {
			t.Fatalf("after %q: model %q pending %q effort %q pending effort %q, want the staged choice in use", prompt, snapshot.Model, snapshot.PendingModel, snapshot.ReasoningEffort, snapshot.PendingReasoning)
		}
		if snapshot.ReportedModel != "claude-opus-5-5" {
			t.Fatalf("reported model = %q, want what Claude last reported", snapshot.ReportedModel)
		}
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	launches := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(launches) != 2 {
		t.Fatalf("launches = %q, want two turns", launches)
	}
	for _, launch := range launches {
		// Later turns keep the choice rather than pinning the reported ID.
		if !strings.Contains(launch, "--model opus[1m]") {
			t.Fatalf("launch args = %q, want --model opus[1m]", launch)
		}
	}
}

func TestClaudeModelEquivalenceComparesCatalogResolution(t *testing.T) {
	useClaudeModelCatalogHelper(t)
	if ModelNamesEquivalent(ProviderClaudeCode, "claude-opus-5", "opus") {
		t.Fatal("without a catalog an alias cannot be matched to a pinned version")
	}
	if _, err := loadClaudeModelCatalog(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !ModelNamesEquivalent(ProviderClaudeCode, "default", "opus[1m]") {
		t.Fatal("default and opus[1m] both resolve to claude-opus-5-5[1m]")
	}
	if ModelNamesEquivalent(ProviderClaudeCode, "claude-opus-5", "opus") {
		t.Fatal("opus resolves to Opus 5.5, so it must not match a pinned Opus 5")
	}
}
