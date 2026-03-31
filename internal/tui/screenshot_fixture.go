package tui

import (
	"fmt"
	"time"

	"lcroom/internal/model"
)

const (
	screenshotDemoPrimaryProject  = "LittleControlRoom"
	screenshotDemoBusyProject     = "assistant-lab"
	screenshotDemoFollowupProject = "billing-tool"
)

type screenshotDataSet struct {
	projects []model.ProjectSummary
	details  map[string]model.ProjectDetail
}

func screenshotDemoDataSet() screenshotDataSet {
	loc := time.FixedZone("JST", 9*60*60)
	now := time.Date(2026, time.March, 11, 8, 20, 0, 0, loc)

	type demoProject struct {
		path      string
		name      string
		status    model.ProjectStatus
		attention int
		last      time.Time
		format    string
		category  model.SessionCategory
		summary   string
		reasons   []model.AttentionReason
		repoDirty bool
		repoSync  model.RepoSyncStatus
		pinned    bool
	}

	projects := []demoProject{
		{
			path:      "/workspaces/repos/LittleControlRoom",
			name:      screenshotDemoPrimaryProject,
			status:    model.StatusIdle,
			attention: 58,
			last:      now.Add(-31 * time.Minute),
			format:    "modern",
			category:  model.SessionCategoryCompleted,
			summary:   "Code changes, tests, and smoke checks completed; repo is clean and synced.",
			reasons: []model.AttentionReason{
				{Code: "idle", Text: "Idle for 31m", Weight: 20},
				{Code: "pinned", Text: "Pinned by user", Weight: 38},
			},
			repoSync: model.RepoSyncSynced,
			pinned:   true,
		},
		{
			path:      "/workspaces/repos/assistant-lab",
			name:      screenshotDemoBusyProject,
			status:    model.StatusActive,
			attention: 140,
			last:      now.Add(-14 * 24 * time.Hour),
			format:    "modern",
			category:  model.SessionCategoryInProgress,
			summary:   "Discussion about IPC flakiness and proposed diagnostics is still in progress.",
			reasons: []model.AttentionReason{
				{Code: "stale_active", Text: "Long-running active work needs a check-in", Weight: 140},
			},
			repoSync: model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/render-bench",
			name:      "render-bench",
			status:    model.StatusActive,
			attention: 140,
			last:      now.AddDate(0, -4, 0),
			format:    "modern",
			category:  model.SessionCategoryInProgress,
			summary:   "Developer started running the app, but the build and serve loop still needs cleanup.",
			reasons: []model.AttentionReason{
				{Code: "stale_active", Text: "Long-running active work needs a check-in", Weight: 140},
			},
			repoSync: model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/platform-api",
			name:      "platform-api",
			status:    model.StatusIdle,
			attention: 40,
			last:      now.Add(-81 * time.Minute),
			format:    "modern",
			category:  model.SessionCategoryCompleted,
			summary:   "Adjusted blackout and tint values, then ran a successful smoke check.",
			reasons: []model.AttentionReason{
				{Code: "recent_idle", Text: "Recently completed and worth a quick review", Weight: 40},
			},
			repoSync: model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/task-orbit",
			name:      "task-orbit",
			status:    model.StatusIdle,
			attention: 130,
			last:      now.AddDate(0, -3, 0),
			format:    "modern",
			category:  model.SessionCategoryWaitingForUser,
			summary:   "Assistant made code and config changes, then asked for deployment data.",
			reasons: []model.AttentionReason{
				{Code: "waiting_user", Text: "Waiting on user input for too long", Weight: 130},
			},
			repoSync: model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/billing-tool",
			name:      screenshotDemoFollowupProject,
			status:    model.StatusPossiblyStuck,
			attention: 28,
			last:      now.AddDate(0, 0, -56).Add(-13*time.Hour - 23*time.Minute),
			format:    "modern",
			category:  model.SessionCategoryNeedsFollowUp,
			summary:   "Assistant made code changes and suggested tests, but the follow-up verification still needs to run.",
			reasons: []model.AttentionReason{
				{Code: "needs_follow_up", Text: "Latest session needs follow-up", Weight: 28},
			},
			repoSync: model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/release-notes",
			name:      "release-notes",
			status:    model.StatusIdle,
			attention: 10,
			last:      now.Add(-24 * time.Hour),
			format:    "modern",
			category:  model.SessionCategoryCompleted,
			summary:   "Upload finished and release notes were localized for the handoff.",
			reasons: []model.AttentionReason{
				{Code: "recent_idle", Text: "Recently completed and worth a quick review", Weight: 10},
			},
			repoSync: model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/asset-ops",
			name:      "asset-ops",
			status:    model.StatusIdle,
			attention: 10,
			last:      now.Add(-48 * time.Hour),
			format:    "modern",
			category:  model.SessionCategoryCompleted,
			summary:   "Repo cleanup completed: ignore rules updated and generated files removed.",
			reasons: []model.AttentionReason{
				{Code: "recent_idle", Text: "Recently completed and worth a quick review", Weight: 10},
			},
			repoSync: model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/starlight-web",
			name:      "starlight-web",
			status:    model.StatusIdle,
			attention: 11,
			last:      now.AddDate(0, 0, -30),
			format:    "modern",
			category:  model.SessionCategoryCompleted,
			summary:   "Changes were committed, pushed, and published to staging.",
			reasons: []model.AttentionReason{
				{Code: "repo_dirty", Text: "Repo has local changes that should be reviewed", Weight: 11},
			},
			repoDirty: true,
			repoSync:  model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/portfolio-site-v2",
			name:      "portfolio-site-v2",
			status:    model.StatusIdle,
			attention: 0,
			last:      now.AddDate(0, 0, -85),
			format:    "modern",
			category:  model.SessionCategoryCompleted,
			summary:   "Changes were committed and merge conflicts were resolved.",
			reasons:   nil,
			repoSync:  model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/chat-lab",
			name:      "chat-lab",
			status:    model.StatusIdle,
			attention: 0,
			last:      now.AddDate(0, 0, -7),
			format:    "modern",
			category:  model.SessionCategoryCompleted,
			summary:   "Feature implemented, tests added, and the version was bumped.",
			reasons:   nil,
			repoSync:  model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/portfolio-site",
			name:      "portfolio-site",
			status:    model.StatusIdle,
			attention: 10,
			last:      now.AddDate(0, 0, -7),
			format:    "opencode_db",
			category:  model.SessionCategoryCompleted,
			summary:   "New repo created, README added, and the initial commit pushed.",
			reasons: []model.AttentionReason{
				{Code: "recent_idle", Text: "Recently completed and worth a quick review", Weight: 10},
			},
			repoSync: model.RepoSyncSynced,
		},
		{
			path:      "/workspaces/repos/local-model-lab",
			name:      "local-model-lab",
			status:    model.StatusIdle,
			attention: 0,
			last:      now.AddDate(0, 0, -7),
			format:    "modern",
			category:  model.SessionCategoryCompleted,
			summary:   "Assistant fixed, built, committed, pushed, and documented the patch.",
			reasons:   nil,
			repoSync:  model.RepoSyncSynced,
		},
	}

	summaries := make([]model.ProjectSummary, 0, len(projects))
	details := make(map[string]model.ProjectDetail, len(projects))
	for _, project := range projects {
		summary := model.ProjectSummary{
			Path:                             project.path,
			Name:                             project.name,
			LastActivity:                     project.last,
			Status:                           project.status,
			AttentionScore:                   project.attention,
			PresentOnDisk:                    true,
			RepoDirty:                        project.repoDirty,
			RepoSyncStatus:                   project.repoSync,
			InScope:                          true,
			Pinned:                           project.pinned,
			LatestSessionID:                  fmt.Sprintf("demo-%s", normalizeScreenshotProjectToken(project.name)),
			LatestSessionFormat:              project.format,
			LatestSessionDetectedProjectPath: project.path,
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  project.category,
			LatestSessionSummary:             project.summary,
		}
		summaries = append(summaries, summary)
		detail := model.ProjectDetail{
			Summary: summary,
			Reasons: append([]model.AttentionReason(nil), project.reasons...),
			LatestSessionClassification: &model.SessionClassification{
				SessionID:   summary.LatestSessionID,
				ProjectPath: project.path,
				Status:      model.ClassificationCompleted,
				Category:    project.category,
				Summary:     project.summary,
				UpdatedAt:   project.last,
				CompletedAt: project.last,
			},
		}
		if project.name == screenshotDemoPrimaryProject {
			detail.Todos = []model.TodoItem{
				{ID: 1, ProjectPath: project.path, Text: "Add contextual screenshots to README for each workflow section", Done: true, Position: 0, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-30 * time.Minute), CompletedAt: now.Add(-30 * time.Minute)},
				{ID: 2, ProjectPath: project.path, Text: "Wire /compact slash command to thread/compact/start endpoint", Done: false, Position: 1, CreatedAt: now.Add(-90 * time.Minute), UpdatedAt: now.Add(-90 * time.Minute)},
				{ID: 3, ProjectPath: project.path, Text: "Implement /review command for embedded code review workflow", Done: false, Position: 2, CreatedAt: now.Add(-80 * time.Minute), UpdatedAt: now.Add(-80 * time.Minute)},
				{ID: 4, ProjectPath: project.path, Text: "Add /fork to create a new thread from the current conversation point", Done: false, Position: 3, CreatedAt: now.Add(-70 * time.Minute), UpdatedAt: now.Add(-70 * time.Minute)},
			}
		}
		details[project.path] = detail
	}

	return screenshotDataSet{
		projects: summaries,
		details:  details,
	}
}
