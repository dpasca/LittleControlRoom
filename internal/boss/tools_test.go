package boss

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/store"
)

func TestQueryExecutorReportsProjectDetailFromStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Unix(1_800_000_000, 0)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/alpha",
		Name:           "Alpha",
		LastActivity:   now.Add(-2 * time.Hour),
		Status:         model.StatusPossiblyStuck,
		AttentionScore: 42,
		PresentOnDisk:  true,
		InScope:        true,
		RepoBranch:     "feature/boss",
		RepoDirty:      true,
		AttentionReason: []model.AttentionReason{{
			Text:   "Needs a rollout decision",
			Weight: 20,
		}},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if _, err := st.AddTodo(ctx, "/tmp/alpha", "Write boss-mode integration tests"); err != nil {
		t.Fatalf("add todo: %v", err)
	}

	executor := newQueryExecutor(st)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:        bossActionProjectDetail,
		ProjectPath: "/tmp/alpha",
		Limit:       8,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Project detail:",
		"Reference metadata (use only for disambiguation/blockers):",
		"Alpha",
		"/tmp/alpha",
		"Needs a rollout decision",
		"Write boss-mode integration tests",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorReportsOpenAgentTasks(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	store := &fakeBossStore{
		agentTasks: []model.AgentTask{{
			ID:            "agt_cursor",
			Title:         "Revoke Cursor GitHub access",
			Kind:          model.AgentTaskKindAgent,
			Status:        model.AgentTaskStatusActive,
			Summary:       "Waiting on GitHub OAuth settings in the browser.",
			Capabilities:  []string{"browser.inspect", "account.manage"},
			Provider:      model.SessionSourceCodex,
			SessionID:     "019deb93",
			LastTouchedAt: now.Add(-4 * time.Minute),
			Resources: []model.AgentTaskResource{
				{Kind: model.AgentTaskResourceEngineerSession, Provider: model.SessionSourceCodex, SessionID: "019deb93"},
			},
		}, {
			ID:            "agt_diff",
			Title:         "Diff duplicate Codex skills",
			Kind:          model.AgentTaskKindAgent,
			Status:        model.AgentTaskStatusWaiting,
			Summary:       "Needs a decision on whether to remove the stale local copy.",
			Provider:      model.SessionSourceCodex,
			SessionID:     "019deaf3",
			LastTouchedAt: now.Add(-2 * time.Hour),
		}, {
			ID:     "agt_closed",
			Title:  "Already closed",
			Kind:   model.AgentTaskKindAgent,
			Status: model.AgentTaskStatusCompleted,
		}},
	}

	executor := newQueryExecutor(store)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(context.Background(), bossAction{
		Kind:  bossActionAgentTaskReport,
		Limit: 8,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Agent task report: 2 open delegated agent tasks.",
		"separate from project TODOs",
		"Revoke Cursor GitHub access (agt_cursor)",
		"show: agent_task:agt_cursor",
		"kind/status: agent/active",
		"touched 4m ago",
		"engineer: codex 019deb93",
		"Waiting on GitHub OAuth settings",
		"browser.inspect, account.manage",
		"Diff duplicate Codex skills (agt_diff)",
		"kind/status: agent/review",
		"Transcript hint: use ctx show agent_task:<task-id>",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("agent task report missing %q:\n%s", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "Already closed") {
		t.Fatalf("agent task report included completed task:\n%s", result.Text)
	}
}

func TestQueryExecutorAgentTaskReportRespectsPrivacyMode(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		agentTasks: []model.AgentTask{{
			ID:     "agt_public",
			Title:  "Public delegated work",
			Kind:   model.AgentTaskKindAgent,
			Status: model.AgentTaskStatusActive,
		}, {
			ID:     "agt_secret",
			Title:  "Private delegated work",
			Kind:   model.AgentTaskKindAgent,
			Status: model.AgentTaskStatusActive,
		}},
	}

	executor := newQueryExecutor(store)
	result, err := executor.Execute(context.Background(), bossAction{
		Kind: bossActionAgentTaskReport,
	}, StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{
			{ID: "agt_public", Title: "Public delegated work"},
			{ID: "agt_secret", Title: "Private delegated work"},
		},
	}, ViewContext{PrivacyMode: true, PrivacyPatterns: []string{"*private*"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Agent task report: 1 open delegated agent task.",
		"Privacy mode is enabled",
		"Public delegated work",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("privacy-mode agent task report missing %q:\n%s", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "Private delegated work") || strings.Contains(result.Text, "hidden while privacy mode is enabled") {
		t.Fatalf("privacy-mode agent task report = %q", result.Text)
	}
}

func TestQueryExecutorProjectDetailIncludesLinkedWorktreeActivity(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	root := "/tmp/lcr"
	linked := "/tmp/lcr--streaming"
	store := &fakeBossStore{
		projects: []model.ProjectSummary{
			{
				Path:             root,
				Name:             "LittleControlRoom",
				Status:           model.StatusActive,
				LastActivity:     now.Add(-30 * time.Minute),
				PresentOnDisk:    true,
				WorktreeRootPath: root,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Path:                 linked,
				Name:                 "LittleControlRoom--streaming",
				Status:               model.StatusIdle,
				LastActivity:         now.Add(-2 * time.Minute),
				PresentOnDisk:        true,
				WorktreeRootPath:     root,
				WorktreeKind:         model.WorktreeKindLinked,
				LatestSessionSummary: "Added Boss Chat streaming and verified the checks.",
			},
		},
		details: map[string]model.ProjectDetail{
			root: {
				Summary: model.ProjectSummary{
					Path:             root,
					Name:             "LittleControlRoom",
					Status:           model.StatusActive,
					WorktreeRootPath: root,
					WorktreeKind:     model.WorktreeKindMain,
				},
			},
		},
	}

	executor := newQueryExecutor(store)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(context.Background(), bossAction{
		Kind:        bossActionProjectDetail,
		ProjectPath: root,
		Limit:       8,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Worktree family activity:",
		"linked: LittleControlRoom--streaming",
		"Added Boss Chat streaming",
		"worktree=linked",
		"worktree_root=/tmp/lcr",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorKeepsRoutineRepoStateOutOfOperationalDetail(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	store := &fakeBossStore{
		projects: []model.ProjectSummary{{
			Path:           "/tmp/okmain",
			Name:           "okmain",
			Status:         model.StatusActive,
			RepoBranch:     "okmain",
			RepoDirty:      true,
			RepoSyncStatus: model.RepoSyncAhead,
			RepoAheadCount: 3,
		}},
		details: map[string]model.ProjectDetail{
			"/tmp/okmain": {
				Summary: model.ProjectSummary{
					Path:                 "/tmp/okmain",
					Name:                 "okmain",
					Status:               model.StatusActive,
					RepoBranch:           "okmain",
					RepoDirty:            true,
					RepoSyncStatus:       model.RepoSyncAhead,
					RepoAheadCount:       3,
					LatestSessionSummary: "Notification visibility fix around the Other tab is ready for validation.",
				},
			},
		},
	}

	executor := newQueryExecutor(store)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(context.Background(), bossAction{
		Kind:        bossActionProjectDetail,
		ProjectPath: "/tmp/okmain",
		Limit:       8,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	operationalIndex := strings.Index(result.Text, "Operational snapshot:")
	metadataIndex := strings.Index(result.Text, "Reference metadata")
	if operationalIndex < 0 || metadataIndex < 0 || operationalIndex > metadataIndex {
		t.Fatalf("project detail should put operational substance before reference metadata:\n%s", result.Text)
	}
	operational := result.Text[operationalIndex:metadataIndex]
	if !strings.Contains(operational, "Notification visibility fix") {
		t.Fatalf("operational detail missing latest work:\n%s", operational)
	}
	for _, noisy := range []string{"dirty", "ahead +3", "branch=okmain", "name=okmain", "path=/tmp/okmain"} {
		if strings.Contains(operational, noisy) {
			t.Fatalf("operational detail should not include routine repo metadata %q:\n%s", noisy, operational)
		}
	}
	reference := result.Text[metadataIndex:]
	for _, want := range []string{"name=okmain", "path=/tmp/okmain", "branch=okmain", "repo=dirty, ahead +3"} {
		if !strings.Contains(reference, want) {
			t.Fatalf("reference detail missing %q:\n%s", want, reference)
		}
	}
}

func TestQueryExecutorReportsLiveRunningSessionSample(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	sessionFile := filepath.Join(tempDir, "session.jsonl")
	transcript := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Keep going on FCX profile migration."}]}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","output":"tool-output-should-not-leak"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Currently compiling the compressed JSON profile migration path."}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionFile, []byte(transcript), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	now := time.Unix(1_800_000_000, 0)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          "/tmp/live",
		Name:          "Live",
		LastActivity:  now,
		Status:        model.StatusActive,
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     now,
		Sessions: []model.SessionEvidence{{
			Source:               model.SessionSourceCodex,
			SessionID:            "codex:ses_live",
			RawSessionID:         "ses_live",
			ProjectPath:          "/tmp/live",
			SessionFile:          sessionFile,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  false,
		}},
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	executor := newQueryExecutor(st)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:        bossActionProjectDetail,
		ProjectPath: "/tmp/live",
		Limit:       8,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Live engineer work context:",
		"latest turn open",
		"engineer: Currently compiling the compressed JSON profile migration path.",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "tool-output-should-not-leak") {
		t.Fatalf("tool output should not appear in live session sample:\n%s", result.Text)
	}
	if sampleIndex, metadataIndex := strings.Index(result.Text, "Live engineer work context:"), strings.Index(result.Text, "Reference metadata"); sampleIndex < 0 || metadataIndex < 0 || sampleIndex > metadataIndex {
		t.Fatalf("live session sample should precede reference metadata:\n%s", result.Text)
	}
}

func TestQueryExecutorSearchesContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Unix(1_800_000_000, 0)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/okmain",
		Name:           "okmain",
		LastActivity:   now,
		Status:         model.StatusIdle,
		AttentionScore: 18,
		PresentOnDisk:  true,
		InScope:        true,
		AttentionReason: []model.AttentionReason{{
			Text:   "FCX is the current game codename under discussion",
			Weight: 12,
		}},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	executor := newQueryExecutor(st)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:  bossActionSearchContext,
		Query: "FCX",
		Limit: 4,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		`Context search for "FCX"`,
		"Internal routing note:",
		"reference metadata:",
		"Query time:",
		"updated_at=",
		"age_at_query=",
		"/tmp/okmain",
		"FCX",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "okmain | path:") {
		t.Fatalf("search context should not format alias matches as user-facing project mappings:\n%s", result.Text)
	}
}

func TestQueryExecutorContextCommandSearchesEngineerTranscriptsWithHandles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	sessionFile := filepath.Join(tempDir, "engineer-session.jsonl")
	transcript := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"what was the summary flash about?"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The summary flash was about the boss attention row update cue, not an error badge."}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionFile, []byte(transcript), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	now := time.Unix(1_800_000_000, 0)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          "/tmp/lcr",
		Name:          "LittleControlRoom",
		LastActivity:  now,
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     now,
		Sessions: []model.SessionEvidence{{
			Source:      model.SessionSourceCodex,
			SessionID:   "ses_ctx",
			ProjectPath: "/tmp/lcr",
			SessionFile: sessionFile,
			Format:      "modern",
			LastEventAt: now,
		}},
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	executor := newQueryExecutor(st)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:    bossActionContextCommand,
		Command: `ctx search engineer "summary flash" --project LittleControlRoom --limit 4`,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		`ctx search engineer "summary flash": 1 matches.`,
		"Engineer search covers Codex, OpenCode, or Claude Code task/project work logs.",
		"handle: engineer:codex:ses_ctx",
		"summary flash",
		`show: ctx show engineer:codex:ses_ctx --query "summary flash" --before 1 --after 2 --max-chars 6000`,
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorContextCommandShowsEngineerExchange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	sessionFile := filepath.Join(tempDir, "engineer-session.jsonl")
	transcript := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"what was the summary flash about?"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The summary flash was about the boss attention row update cue, not an error badge."}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"are you sure?"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionFile, []byte(transcript), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	now := time.Unix(1_800_000_000, 0)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          "/tmp/lcr",
		Name:          "LittleControlRoom",
		LastActivity:  now,
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     now,
		Sessions: []model.SessionEvidence{{
			Source:      model.SessionSourceCodex,
			SessionID:   "ses_show",
			ProjectPath: "/tmp/lcr",
			SessionFile: sessionFile,
			Format:      "modern",
			LastEventAt: now,
		}},
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	executor := newQueryExecutor(st)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:    bossActionContextCommand,
		Command: `ctx show engineer:codex:ses_show --query "summary flash" --before 0 --after 1 --max-chars 1000`,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Engineer exchange:",
		"handle: engineer:codex:ses_show",
		`role="boss"`,
		`role="engineer"`,
		"The summary flash was about the boss attention row update cue, not an error badge.",
		`anchor: turn 2 matched "summary flash"`,
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
	if strings.Contains(result.Text, `role="assistant"`) || strings.Contains(result.Text, `role="user"`) {
		t.Fatalf("engineer excerpt should use Boss Chat vocabulary for roles:\n%s", result.Text)
	}
}

func TestQueryExecutorContextCommandShowsAgentTaskExchange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	codexHome := filepath.Join(tempDir, ".codex")
	sessionID := "019agenttaskshow"
	writeBossCodexSession(t, codexHome, sessionID, []string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Diff the duplicate skills and summarize the difference."}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The local openai-docs copy is a stale override; keep the system copy and remove the duplicate after checking custom edits."}]}}`,
	})
	workspace := filepath.Join(tempDir, "agent-workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if _, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		ID:            "agt_show",
		Title:         "Diff duplicate Codex skills",
		Kind:          model.AgentTaskKindAgent,
		Provider:      model.SessionSourceCodex,
		SessionID:     sessionID,
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}

	executor := newQueryExecutor(st)
	executor.codexHome = codexHome
	executor.nowFn = func() time.Time { return time.Unix(1_800_000_000, 0) }
	result, err := executor.Execute(ctx, bossAction{
		Kind:    bossActionContextCommand,
		Command: `ctx show agent_task:agt_show --before 1 --after 1 --max-chars 2000`,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Engineer exchange:",
		"handle: engineer:codex:" + sessionID,
		"Diff duplicate Codex skills",
		`role="boss"`,
		`role="engineer"`,
		"stale override",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorContextCommandShowsAgentTaskExchangeFromCodexFallbackHome(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	overlayCodexHome := filepath.Join(tempDir, "overlay-codex-home")
	realCodexHome := filepath.Join(tempDir, "real-codex-home")
	sessionID := "019agenttaskfallbackhome"
	writeBossCodexSession(t, realCodexHome, sessionID, []string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Diff the duplicate skills and summarize the difference."}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The fallback Codex home transcript says the user skill is stale and the system skill should win."}]}}`,
	})
	workspace := filepath.Join(tempDir, "agent-workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if _, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		ID:            "agt_fallback_home",
		Title:         "Diff duplicate Codex skills",
		Kind:          model.AgentTaskKindAgent,
		Provider:      model.SessionSourceCodex,
		SessionID:     sessionID,
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}

	executor := newQueryExecutor(st)
	executor.codexHome = overlayCodexHome
	executor.codexHomeFallbacks = []string{realCodexHome}
	result, err := executor.Execute(ctx, bossAction{
		Kind:    bossActionContextCommand,
		Command: `ctx show agent_task:agt_fallback_home --before 1 --after 1 --max-chars 2000`,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Engineer exchange:",
		"handle: engineer:codex:" + sessionID,
		"Diff duplicate Codex skills",
		"fallback Codex home transcript",
		"system skill should win",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorContextCommandFallsBackFromEngineerSessionToAgentTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	codexHome := filepath.Join(tempDir, ".codex")
	sessionID := "019agenttaskfallback"
	writeBossCodexSession(t, codexHome, sessionID, []string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Explain the stale skill copy."}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The user copy predates the system copy and should be treated as a stale local duplicate."}]}}`,
	})
	workspace := filepath.Join(tempDir, "agent-workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if _, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		ID:            "agt_fallback",
		Title:         "Explain stale skill copy",
		Kind:          model.AgentTaskKindAgent,
		Provider:      model.SessionSourceCodex,
		SessionID:     sessionID,
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}

	executor := newQueryExecutor(st)
	executor.codexHome = codexHome
	result, err := executor.Execute(ctx, bossAction{
		Kind:    bossActionContextCommand,
		Command: `ctx show engineer:019agenttaskfallback --query stale --before 0 --after 1 --max-chars 2000`,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Engineer exchange:",
		"handle: engineer:codex:" + sessionID,
		`anchor: turn 2 matched "stale"`,
		"stale local duplicate",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorSearchesBossSessionsAsXMLSnippets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sessionStore := newBossSessionStore(t.TempDir())
	now := time.Unix(1_800_000_000, 0)
	session, err := sessionStore.createSession(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("createSession() error = %v", err)
	}
	if err := sessionStore.appendMessage(ctx, session.SessionID, ChatMessage{
		Role:    "user",
		Content: "We discussed launch notes with <xml> and \"quotes\".\nKeep this raw for grep.",
		At:      now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("appendMessage() error = %v", err)
	}

	executor := newQueryExecutorWithBossSessions(nil, sessionStore)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:  bossActionSearchBossSessions,
		Query: "launch",
		Limit: 4,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		`<boss_session_search query="launch" matches="1"`,
		`<boss_session id="` + session.SessionID + `"`,
		`<turn index="1" role="user"`,
		"<![CDATA[",
		`<xml>`,
		`"quotes"`,
		"Keep this raw for grep.",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
	if !strings.Contains(result.Text, "with <xml> and \"quotes\".\nKeep this raw") {
		t.Fatalf("turn content should stay raw inside CDATA:\n%s", result.Text)
	}
}

func TestQueryExecutorReportsCodexSkillsInventory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeBossSkill(t, root, filepath.Join("skills", "openai-docs", "SKILL.md"), "openai-docs", "Old docs")
	writeBossSkill(t, root, filepath.Join("skills", ".system", "openai-docs", "SKILL.md"), "openai-docs", "New docs")

	executor := newQueryExecutor(&fakeBossStore{})
	executor.codexHome = root
	result, err := executor.Execute(context.Background(), bossAction{Kind: bossActionSkillsInventory}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute(skills_inventory) error = %v", err)
	}
	for _, want := range []string{
		"Codex skills inventory:",
		"Review suggested",
		"openai-docs",
		"possible stale local duplicate",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("skills inventory missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorResolvesProjectNameThroughContextSearch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Unix(1_800_000_000, 0)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/okmain",
		Name:           "okmain",
		LastActivity:   now.Add(-5 * time.Minute),
		Status:         model.StatusActive,
		AttentionScore: 31,
		PresentOnDisk:  true,
		InScope:        true,
		AttentionReason: []model.AttentionReason{{
			Text:   "FCX flight tuning is active in the latest engineer session",
			Weight: 20,
		}},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	executor := newQueryExecutor(st)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:        bossActionProjectDetail,
		ProjectName: "FCX",
		Limit:       8,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Project detail:",
		"okmain",
		"/tmp/okmain",
		"FCX flight tuning",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
	for _, unwanted := range []string{`resolved "FCX" through context search`, "target note:"} {
		if strings.Contains(result.Text, unwanted) {
			t.Fatalf("tool result should not expose routing note %q:\n%s", unwanted, result.Text)
		}
	}
}

func writeBossSkill(t *testing.T, root, rel, name, description string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	content := "---\nname: \"" + name + "\"\ndescription: \"" + description + "\"\n---\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeBossCodexSession(t *testing.T, codexHome, sessionID string, lines []string) string {
	t.Helper()
	now := time.Now().UTC()
	sessionDir := filepath.Join(codexHome, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sessionDir, err)
	}
	path := filepath.Join(sessionDir, "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestQueryExecutorPrivacyModeFiltersProjectTools(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		projects: []model.ProjectSummary{
			{Path: "/tmp/public", Name: "PublicApp", InScope: true},
			{Path: "/tmp/secret", Name: "SecretClient", InScope: true},
		},
		classifications: []model.SessionClassification{
			{ProjectPath: "/tmp/public", SessionID: "public-session", Summary: "Public summary"},
			{ProjectPath: "/tmp/secret", SessionID: "secret-session", Summary: "Secret summary"},
		},
		searchResults: []model.ContextSearchResult{
			{ProjectPath: "/tmp/public", ProjectName: "PublicApp", Snippet: "Public snippet"},
			{ProjectPath: "/tmp/secret", ProjectName: "SecretClient", Snippet: "Secret snippet"},
		},
	}
	executor := newQueryExecutor(store)
	view := ViewContext{PrivacyMode: true, PrivacyPatterns: []string{"*secret*"}}

	listed, err := executor.Execute(context.Background(), bossAction{Kind: bossActionListProjects}, StateSnapshot{}, view)
	if err != nil {
		t.Fatalf("list Execute() error = %v", err)
	}
	if strings.Contains(listed.Text, "SecretClient") || !strings.Contains(listed.Text, "PublicApp") {
		t.Fatalf("privacy-filtered project list = %q", listed.Text)
	}

	assessments, err := executor.Execute(context.Background(), bossAction{Kind: bossActionSessionClassifications}, StateSnapshot{}, view)
	if err != nil {
		t.Fatalf("assessments Execute() error = %v", err)
	}
	if strings.Contains(assessments.Text, "Secret summary") || strings.Contains(assessments.Text, "/tmp/secret") {
		t.Fatalf("privacy-filtered assessments = %q", assessments.Text)
	}

	search, err := executor.Execute(context.Background(), bossAction{Kind: bossActionSearchContext, Query: "summary"}, StateSnapshot{}, view)
	if err != nil {
		t.Fatalf("search Execute() error = %v", err)
	}
	if strings.Contains(search.Text, "SecretClient") || strings.Contains(search.Text, "Secret snippet") {
		t.Fatalf("privacy-filtered context search = %q", search.Text)
	}

	commandSearch, err := executor.Execute(context.Background(), bossAction{Kind: bossActionContextCommand, Command: `ctx search engineer "summary" --limit 5`}, StateSnapshot{}, view)
	if err != nil {
		t.Fatalf("context command Execute() error = %v", err)
	}
	if strings.Contains(commandSearch.Text, "SecretClient") || strings.Contains(commandSearch.Text, "Secret snippet") {
		t.Fatalf("privacy-filtered context command search = %q", commandSearch.Text)
	}
}

func TestQueryExecutorReportsSuspiciousProcesses(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	store := &fakeBossStore{
		projects: []model.ProjectSummary{
			{Path: "/tmp/alpha", Name: "Alpha", InScope: true},
			{Path: "/tmp/beta", Name: "Beta", InScope: true},
		},
	}
	executor := newQueryExecutor(store)
	executor.nowFn = func() time.Time { return now }
	var gotOpts procinspect.ScanOptions
	executor.processReporter = func(_ context.Context, opts procinspect.ScanOptions) ([]procinspect.ProjectReport, error) {
		gotOpts = opts
		return []procinspect.ProjectReport{{
			ProjectPath: "/tmp/alpha",
			ScannedAt:   now,
			Findings: []procinspect.Finding{{
				Process: procinspect.Process{
					PID:     42,
					PPID:    1,
					PGID:    42,
					CPU:     97.2,
					Mem:     1.3,
					CWD:     "/tmp/alpha/server",
					Ports:   []int{3000},
					Command: "node server.js",
				},
				ProjectPath: "/tmp/alpha",
				Reasons:     []string{"orphaned under PID 1", "high CPU 97.2%"},
			}},
		}, {
			ProjectPath: "/tmp/beta",
			ScannedAt:   now,
		}}, nil
	}

	result, err := executor.Execute(context.Background(), bossAction{
		Kind:  bossActionProcessReport,
		Limit: 5,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotOpts.OwnPID <= 0 {
		t.Fatalf("OwnPID = %d, want current process pid", gotOpts.OwnPID)
	}
	if strings.Join(gotOpts.ProjectPaths, ",") != "/tmp/alpha,/tmp/beta" {
		t.Fatalf("ProjectPaths = %#v, want both visible projects", gotOpts.ProjectPaths)
	}
	for _, want := range []string{
		"Process report: 1 suspicious project-local process across visible projects.",
		"Safety note: report-only",
		"PID 42",
		"CPU 97.2%",
		"project: Alpha",
		"orphaned under PID 1",
		"ports: 3000",
		"node server.js",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("process report missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorProcessReportRespectsPrivacyMode(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		projects: []model.ProjectSummary{
			{Path: "/tmp/public", Name: "PublicApp", InScope: true},
			{Path: "/tmp/secret", Name: "SecretClient", InScope: true},
		},
	}
	executor := newQueryExecutor(store)
	var gotPaths []string
	executor.processReporter = func(_ context.Context, opts procinspect.ScanOptions) ([]procinspect.ProjectReport, error) {
		gotPaths = append([]string(nil), opts.ProjectPaths...)
		return []procinspect.ProjectReport{{
			ProjectPath: "/tmp/public",
			ScannedAt:   time.Unix(1_800_000_000, 0),
		}}, nil
	}

	result, err := executor.Execute(context.Background(), bossAction{
		Kind: bossActionProcessReport,
	}, StateSnapshot{}, ViewContext{PrivacyMode: true, PrivacyPatterns: []string{"*secret*"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Join(gotPaths, ",") != "/tmp/public" {
		t.Fatalf("ProjectPaths = %#v, want only public project", gotPaths)
	}
	if strings.Contains(result.Text, "SecretClient") || strings.Contains(result.Text, "/tmp/secret") {
		t.Fatalf("process report leaked private project:\n%s", result.Text)
	}
}

func TestBuildViewContextBriefIncludesClassicTUIState(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	brief := BuildViewContextBrief(ViewContext{
		Active:              true,
		Embedded:            true,
		AllProjectCount:     4,
		VisibleProjectCount: 2,
		FocusedPane:         "detail",
		SortMode:            "attention",
		Visibility:          "all_folders",
		Filter:              "boss",
		Status:              "Detail focused",
	}, now)

	for _, want := range []string{
		"embedded over classic TUI",
		"2 visible of 4 known",
		"filter=\"boss\"",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "selected project") || strings.Contains(brief, "detail panel") {
		t.Fatalf("brief should not expose hidden project cursor/detail state:\n%s", brief)
	}
}

func TestBuildViewContextBriefIncludesSystemNotices(t *testing.T) {
	t.Parallel()

	brief := BuildViewContextBrief(ViewContext{
		Active:              true,
		Embedded:            true,
		AllProjectCount:     4,
		VisibleProjectCount: 4,
		SystemNotices: []ViewSystemNotice{{
			Code:     "process_suspicious",
			Severity: "warning",
			Summary:  "Processes: 2 suspicious, 1 hot; use process_report or /pids for PID details",
			Count:    2,
		}},
	}, time.Unix(1_800_000_000, 0))

	for _, want := range []string{
		"system notices",
		"warning/process_suspicious count=2",
		"Processes: 2 suspicious, 1 hot",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
}

func TestBuildViewContextBriefIncludesEngineerActivity(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	brief := BuildViewContextBrief(ViewContext{
		Active:              true,
		Embedded:            true,
		AllProjectCount:     4,
		VisibleProjectCount: 4,
		EngineerActivities: []ViewEngineerActivity{{
			Kind:        "agent_task",
			TaskID:      "agt_demo",
			ProjectPath: "/tmp/agent-task",
			Title:       "Revoke Cursor GitHub access",
			Provider:    model.SessionSourceCodex,
			SessionID:   "thread-agent-1",
			Status:      "working",
			Active:      true,
			StartedAt:   now.Add(-37 * time.Second),
		}},
	}, now)

	for _, want := range []string{
		"active engineer work",
		"Revoke Cursor GitHub access",
		"working 00:37 via codex",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
}

func TestQueryExecutorDoesNotUseHiddenSelectedProject(t *testing.T) {
	t.Parallel()

	executor := newQueryExecutor(&fakeBossStore{})
	_, _, err := executor.resolveProjectPath(context.Background(), bossAction{Target: "selected"}, ViewContext{})
	if err == nil {
		t.Fatalf("resolveProjectPath() error = nil, want selected-project rejection")
	}
	if !strings.Contains(err.Error(), "hidden classic TUI selection") {
		t.Fatalf("resolveProjectPath() error = %q, want hidden selection guidance", err)
	}
}
