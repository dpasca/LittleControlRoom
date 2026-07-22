package service

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"

	_ "modernc.org/sqlite"
)

func TestRestoreWorktreeSessionRecreatesCheckoutAndTracking(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "demo")
	worktreePath := filepath.Join(parent, "demo--recover-water")
	branch := "feature/recover-water"
	initGitRepo(t, root)
	runGit(t, root, "git", "branch", branch)
	head := strings.TrimSpace(gitOutput(t, root, "git", "rev-parse", "HEAD"))

	codexHome := t.TempDir()
	writeCodexRecoveryThread(t, codexHome, codexRecoveryThreadFixture{
		ID:         "thread-recover-water",
		CWD:        worktreePath,
		Title:      "Recover the water rendering task",
		GitSHA:     head,
		GitBranch:  branch,
		LastActive: time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC),
	})

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.CodexHome = codexHome
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: parent, Name: "demo"}); err != nil {
		t.Fatalf("track root: %v", err)
	}

	candidates, err := svc.ListRestorableWorktreeSessions(ctx, root)
	if err != nil {
		t.Fatalf("ListRestorableWorktreeSessions() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1 (%#v)", len(candidates), candidates)
	}
	candidate := candidates[0]
	if !candidate.Ready || !candidate.BranchExists || candidate.RecreateBranch {
		t.Fatalf("candidate readiness = %#v", candidate)
	}
	if candidate.SessionID != "thread-recover-water" || candidate.WorktreePath != worktreePath || candidate.BranchName != branch {
		t.Fatalf("candidate identity = %#v", candidate)
	}

	result, err := svc.RestoreWorktreeSession(ctx, RestoreWorktreeSessionRequest{
		ProjectPath: root,
		SessionID:   candidate.SessionID,
	})
	if err != nil {
		t.Fatalf("RestoreWorktreeSession() error = %v", err)
	}
	if !result.WorktreeCreated || !result.ProjectTracked || result.WorktreePath != worktreePath || result.SessionID != candidate.SessionID {
		t.Fatalf("restore result = %#v", result)
	}
	if info, err := os.Stat(worktreePath); err != nil || !info.IsDir() {
		t.Fatalf("restored worktree stat = (%v, %v)", info, err)
	}
	if got := strings.TrimSpace(gitOutput(t, worktreePath, "git", "branch", "--show-current")); got != branch {
		t.Fatalf("restored branch = %q, want %q", got, branch)
	}
	detail, err := st.GetProjectDetail(ctx, worktreePath, 1)
	if err != nil {
		t.Fatalf("get restored project: %v", err)
	}
	if !detail.Summary.PresentOnDisk || detail.Summary.Forgotten || detail.Summary.WorktreeKind != model.WorktreeKindLinked {
		t.Fatalf("restored project summary = %#v", detail.Summary)
	}
	if detail.Summary.WorktreeRootPath != root || detail.Summary.WorktreeInitialBranch != branch {
		t.Fatalf("restored worktree metadata = %#v", detail.Summary)
	}
}

func TestRestoreWorktreeSessionCanRecreateDeletedBranchFromRecordedCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "demo")
	worktreePath := filepath.Join(parent, "demo--lost-branch")
	branch := "feature/lost-branch"
	initGitRepo(t, root)
	head := strings.TrimSpace(gitOutput(t, root, "git", "rev-parse", "HEAD"))

	codexHome := t.TempDir()
	writeCodexRecoveryThread(t, codexHome, codexRecoveryThreadFixture{
		ID:         "thread-lost-branch",
		CWD:        worktreePath,
		Title:      "Continue the lost branch",
		GitSHA:     head,
		GitBranch:  branch,
		LastActive: time.Date(2026, 7, 22, 3, 4, 5, 0, time.UTC),
	})

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.CodexHome = codexHome
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: parent, Name: "demo"}); err != nil {
		t.Fatalf("track root: %v", err)
	}

	candidates, err := svc.ListRestorableWorktreeSessions(ctx, root)
	if err != nil {
		t.Fatalf("ListRestorableWorktreeSessions() error = %v", err)
	}
	if len(candidates) != 1 || !candidates[0].Ready || !candidates[0].RecreateBranch || candidates[0].BranchExists {
		t.Fatalf("fallback candidate = %#v", candidates)
	}
	if _, err := svc.RestoreWorktreeSession(ctx, RestoreWorktreeSessionRequest{ProjectPath: root, SessionID: candidates[0].SessionID}); err != nil {
		t.Fatalf("RestoreWorktreeSession() error = %v", err)
	}
	if got := strings.TrimSpace(gitOutput(t, worktreePath, "git", "branch", "--show-current")); got != branch {
		t.Fatalf("recreated branch = %q, want %q", got, branch)
	}
	if got := strings.TrimSpace(gitOutput(t, worktreePath, "git", "rev-parse", "HEAD")); got != head {
		t.Fatalf("recreated branch HEAD = %q, want %q", got, head)
	}
}

func TestRestoreWorktreeSessionReusesPrunableRegistrationAtSamePath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "demo")
	worktreePath := filepath.Join(parent, "demo--physically-deleted")
	branch := "feature/physically-deleted"
	initGitRepo(t, root)
	runGit(t, root, "git", "worktree", "add", "-b", branch, worktreePath)
	head := strings.TrimSpace(gitOutput(t, root, "git", "rev-parse", "HEAD"))
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("remove test worktree directory: %v", err)
	}

	codexHome := t.TempDir()
	writeCodexRecoveryThread(t, codexHome, codexRecoveryThreadFixture{
		ID:         "thread-physically-deleted",
		CWD:        worktreePath,
		Title:      "Continue after accidental directory deletion",
		GitSHA:     head,
		GitBranch:  branch,
		LastActive: time.Now(),
	})
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.CodexHome = codexHome
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: parent, Name: "demo"}); err != nil {
		t.Fatalf("track root: %v", err)
	}

	candidates, err := svc.ListRestorableWorktreeSessions(ctx, root)
	if err != nil {
		t.Fatalf("ListRestorableWorktreeSessions() error = %v", err)
	}
	if len(candidates) != 1 || !candidates[0].Ready || !candidates[0].StaleRegistration {
		t.Fatalf("stale-registration candidate = %#v", candidates)
	}
	if _, err := svc.RestoreWorktreeSession(ctx, RestoreWorktreeSessionRequest{ProjectPath: root, SessionID: candidates[0].SessionID}); err != nil {
		t.Fatalf("RestoreWorktreeSession() error = %v", err)
	}
	if got := strings.TrimSpace(gitOutput(t, worktreePath, "git", "branch", "--show-current")); got != branch {
		t.Fatalf("restored stale-registration branch = %q, want %q", got, branch)
	}
}

func TestListRestorableWorktreeSessionsBlocksBranchCheckedOutElsewhere(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "demo")
	occupiedPath := filepath.Join(parent, "demo--occupied")
	deletedPath := filepath.Join(parent, "demo--deleted")
	branch := "feature/occupied"
	initGitRepo(t, root)
	runGit(t, root, "git", "worktree", "add", "-b", branch, occupiedPath)
	head := strings.TrimSpace(gitOutput(t, root, "git", "rev-parse", "HEAD"))

	codexHome := t.TempDir()
	writeCodexRecoveryThread(t, codexHome, codexRecoveryThreadFixture{
		ID:         "thread-occupied",
		CWD:        deletedPath,
		Title:      "Session on a branch now used elsewhere",
		GitSHA:     head,
		GitBranch:  branch,
		LastActive: time.Now(),
	})
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.CodexHome = codexHome
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: parent, Name: "demo"}); err != nil {
		t.Fatalf("track root: %v", err)
	}

	candidates, err := svc.ListRestorableWorktreeSessions(ctx, root)
	if err != nil {
		t.Fatalf("ListRestorableWorktreeSessions() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].Ready || !strings.Contains(candidates[0].BlockReason, occupiedPath) {
		t.Fatalf("blocked candidate = %#v", candidates)
	}
}

func TestListRestorableWorktreeSessionsUsesRetainedHistoryWithoutCodexIndex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "demo")
	worktreePath := filepath.Join(parent, "demo--retained-history")
	branch := "feature/retained-history"
	initGitRepo(t, root)
	runGit(t, root, "git", "branch", branch)

	codexHome := t.TempDir()
	rolloutPath := filepath.Join(codexHome, "sessions", "rollout-thread-retained.jsonl")
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolloutPath, []byte(`{"type":"session_meta","payload":{"id":"thread-retained"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.CodexHome = codexHome
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: parent, Name: "demo"}); err != nil {
		t.Fatalf("track root: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:                  worktreePath,
		Name:                  filepath.Base(worktreePath),
		Kind:                  model.ProjectKindProject,
		Status:                model.StatusIdle,
		PresentOnDisk:         false,
		WorktreeRootPath:      root,
		WorktreeKind:          model.WorktreeKindLinked,
		WorktreeParentBranch:  "master",
		WorktreeInitialBranch: branch,
		RepoBranch:            branch,
		InScope:               true,
		UpdatedAt:             now,
		Sessions: []model.SessionEvidence{{
			Source:              model.SessionSourceCodex,
			SessionID:           "thread-retained",
			RawSessionID:        "thread-retained",
			ProjectPath:         worktreePath,
			DetectedProjectPath: worktreePath,
			SessionFile:         rolloutPath,
			Format:              "modern",
			StartedAt:           now.Add(-time.Hour),
			LastEventAt:         now,
		}},
	}); err != nil {
		t.Fatalf("seed retained worktree history: %v", err)
	}

	candidates, err := svc.ListRestorableWorktreeSessions(ctx, root)
	if err != nil {
		t.Fatalf("ListRestorableWorktreeSessions() error = %v", err)
	}
	if len(candidates) != 1 || !candidates[0].Ready || !candidates[0].StoredMetadata {
		t.Fatalf("retained-history candidates = %#v", candidates)
	}
	if candidates[0].SessionID != "thread-retained" || candidates[0].BranchName != branch || candidates[0].WorktreePath != worktreePath {
		t.Fatalf("retained-history candidate identity = %#v", candidates[0])
	}
}

func TestListRestorableWorktreeSessionsDoesNotOfferStaleHistoryAfterThreadMoves(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "demo")
	oldWorktreePath := filepath.Join(parent, "demo--old-path")
	branch := "feature/moved-session"
	initGitRepo(t, root)
	runGit(t, root, "git", "branch", branch)
	head := strings.TrimSpace(gitOutput(t, root, "git", "rev-parse", "HEAD"))

	codexHome := t.TempDir()
	writeCodexRecoveryThread(t, codexHome, codexRecoveryThreadFixture{
		ID:         "thread-moved",
		CWD:        root,
		Title:      "Session now attached to the repository root",
		GitSHA:     head,
		GitBranch:  branch,
		LastActive: time.Now(),
	})
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.CodexHome = codexHome
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: parent, Name: "demo"}); err != nil {
		t.Fatalf("track root: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:                  oldWorktreePath,
		Name:                  filepath.Base(oldWorktreePath),
		Kind:                  model.ProjectKindProject,
		Status:                model.StatusIdle,
		PresentOnDisk:         false,
		WorktreeRootPath:      root,
		WorktreeKind:          model.WorktreeKindLinked,
		WorktreeInitialBranch: branch,
		RepoBranch:            branch,
		InScope:               true,
		UpdatedAt:             now,
		Sessions: []model.SessionEvidence{{
			Source:              model.SessionSourceCodex,
			SessionID:           "thread-moved",
			RawSessionID:        "thread-moved",
			ProjectPath:         oldWorktreePath,
			DetectedProjectPath: oldWorktreePath,
			Format:              "modern",
			StartedAt:           now.Add(-time.Hour),
			LastEventAt:         now,
		}},
	}); err != nil {
		t.Fatalf("seed stale worktree history: %v", err)
	}

	candidates, err := svc.ListRestorableWorktreeSessions(ctx, root)
	if err != nil {
		t.Fatalf("ListRestorableWorktreeSessions() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("stale historical session should not be offered after its indexed cwd moved: %#v", candidates)
	}
}

type codexRecoveryThreadFixture struct {
	ID         string
	CWD        string
	Title      string
	GitSHA     string
	GitBranch  string
	LastActive time.Time
}

func writeCodexRecoveryThread(t *testing.T, codexHome string, fixture codexRecoveryThreadFixture) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(codexHome, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open Codex state db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE threads (
			id TEXT PRIMARY KEY,
			rollout_path TEXT,
			cwd TEXT,
			title TEXT,
			git_sha TEXT,
			git_branch TEXT,
			git_origin_url TEXT,
			source TEXT,
			agent_role TEXT,
			archived INTEGER,
			has_user_event INTEGER,
			created_at INTEGER,
			updated_at INTEGER,
			recency_at_ms INTEGER
		)
	`); err != nil {
		t.Fatalf("create Codex threads table: %v", err)
	}
	rolloutPath := filepath.Join(codexHome, "sessions", "rollout-"+fixture.ID+".jsonl")
	if _, err := db.Exec(`
		INSERT INTO threads(
			id, rollout_path, cwd, title, git_sha, git_branch, git_origin_url,
			source, agent_role, archived, has_user_event, created_at, updated_at, recency_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, '', 'cli', '', 0, 1, ?, ?, ?)
	`, fixture.ID, rolloutPath, fixture.CWD, fixture.Title, fixture.GitSHA, fixture.GitBranch,
		fixture.LastActive.Add(-time.Hour).Unix(), fixture.LastActive.Unix(), fixture.LastActive.UnixMilli(),
	); err != nil {
		t.Fatalf("insert Codex recovery thread: %v", err)
	}
}
