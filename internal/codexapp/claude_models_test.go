package codexapp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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
		model:        "claude-opus-5-5[1m]",
		modelCatalog: loadClaudeModelCatalog,
	}

	models, err := session.ListModels()
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if models[0].Model != "default" {
		t.Fatalf("first model = %q, want catalog order without a duplicate current row: %#v", models[0].Model, models)
	}
	session.model = "claude-opus-5"
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
		model:            "claude-opus-5",
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
	if snapshot.Model != "claude-opus-5" || snapshot.ReasoningEffort != "high" {
		t.Fatalf("current = %q/%q, want the running turn's model and effort", snapshot.Model, snapshot.ReasoningEffort)
	}
}

func TestClaudeModelStagedBeforeLaunchIsConsumedWhenReported(t *testing.T) {
	session := &claudeCodeSession{
		model:                    "claude-opus-5",
		pendingModel:             "opus[1m]",
		pendingReasoning:         "max",
		launchedPendingModel:     "opus[1m]",
		launchedPendingReasoning: "max",
		assistantBlocks:          make(map[string]map[string]struct{}),
		toolCalls:                make(map[string]claudeToolCall),
		toolResults:              make(map[string]struct{}),
	}

	session.handleClaudeStdoutLine(`{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-5-5[1m]"}`)

	snapshot := session.Snapshot()
	if snapshot.PendingModel != "" || snapshot.PendingReasoning != "" {
		t.Fatalf("pending = %q/%q, want launched choice consumed", snapshot.PendingModel, snapshot.PendingReasoning)
	}
	if snapshot.Model != "claude-opus-5-5[1m]" || snapshot.ReasoningEffort != "max" {
		t.Fatalf("current = %q/%q", snapshot.Model, snapshot.ReasoningEffort)
	}
}

func TestClaudeModelEquivalenceUsesCatalogResolution(t *testing.T) {
	useClaudeModelCatalogHelper(t)
	if ModelNamesEquivalent(ProviderClaudeCode, "claude-opus-5", "opus") != true {
		t.Fatal("without a catalog, family aliases should stay equivalent")
	}
	if _, err := loadClaudeModelCatalog(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if ModelNamesEquivalent(ProviderClaudeCode, "claude-opus-5", "opus") {
		t.Fatal("opus resolves to Opus 5.5, so it must not match a pinned Opus 5")
	}
	if !ModelNamesEquivalent(ProviderClaudeCode, "claude-opus-5-5[1m]", "opus[1m]") {
		t.Fatal("opus[1m] should match the model it resolves to")
	}
}
