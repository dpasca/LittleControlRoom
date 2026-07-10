package uisurface

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/sessionclassify"
)

// Tone describes semantic emphasis without prescribing terminal or web styles.
type Tone string

const (
	ToneValue    Tone = "value"
	ToneMuted    Tone = "muted"
	ToneInfo     Tone = "info"
	TonePositive Tone = "positive"
	ToneWarning  Tone = "warning"
	ToneDanger   Tone = "danger"
	ToneConflict Tone = "conflict"
)

type Status struct {
	Label string `json:"label"`
	Tone  Tone   `json:"tone"`
}

type Badge struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Tone  Tone   `json:"tone"`
}

type ProjectBucket string

const (
	ProjectBucketAttention ProjectBucket = "attention"
	ProjectBucketActive    ProjectBucket = "active"
	ProjectBucketQuiet     ProjectBucket = "quiet"
)

type ProjectItem struct {
	Path              string        `json:"path"`
	Name              string        `json:"name"`
	Kind              string        `json:"kind"`
	CategoryID        string        `json:"category_id"`
	CategoryName      string        `json:"category_name,omitempty"`
	Summary           string        `json:"summary"`
	Assessment        Status        `json:"assessment"`
	Activity          Status        `json:"activity"`
	Bucket            ProjectBucket `json:"bucket"`
	AttentionScore    int           `json:"-"`
	LastActivityAt    time.Time     `json:"last_activity_at,omitempty"`
	LastActivityLabel string        `json:"last_activity_label"`
	SourceLabel       string        `json:"source_label,omitempty"`
	OpenTODOCount     int           `json:"open_todo_count"`
	TotalTODOCount    int           `json:"total_todo_count"`
	Badges            []Badge       `json:"badges"`
}

type Category struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Count          int    `json:"count"`
	AttentionCount int    `json:"attention_count"`
	Private        bool   `json:"private,omitempty"`
}

type DashboardCounts struct {
	Attention int `json:"attention"`
	Active    int `json:"active"`
	Quiet     int `json:"quiet"`
	All       int `json:"all"`
}

type DashboardSurface struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Counts      DashboardCounts `json:"counts"`
	Categories  []Category      `json:"categories"`
	Projects    []ProjectItem   `json:"projects"`
}

type DetailBlockKind string

const (
	DetailBlockField        DetailBlockKind = "field"
	DetailBlockWrappedField DetailBlockKind = "wrapped_field"
	DetailBlockFieldGroup   DetailBlockKind = "field_group"
	DetailBlockText         DetailBlockKind = "text"
	DetailBlockSection      DetailBlockKind = "section"
	DetailBlockBullet       DetailBlockKind = "bullet"
)

type DetailFieldValue struct {
	Label        string `json:"label"`
	Text         string `json:"text"`
	Tone         Tone   `json:"tone"`
	RenderedText string `json:"-"`
}

type DetailBlock struct {
	Kind         DetailBlockKind    `json:"kind"`
	Label        string             `json:"label,omitempty"`
	Text         string             `json:"text,omitempty"`
	Tone         Tone               `json:"tone,omitempty"`
	RenderedText string             `json:"-"`
	Fields       []DetailFieldValue `json:"fields,omitempty"`
}

type ProjectDetailSurface struct {
	Project ProjectItem   `json:"project"`
	Blocks  []DetailBlock `json:"blocks"`
}

type BuildOptions struct {
	Now            time.Time
	StuckThreshold time.Duration
	HidePrivate    bool
}

func (s *ProjectDetailSurface) Field(label, text string, tone Tone) {
	s.Blocks = append(s.Blocks, DetailBlock{Kind: DetailBlockField, Label: label, Text: text, Tone: tone})
}

func (s *ProjectDetailSurface) RenderedField(label, text string, tone Tone, renderedText string) {
	s.Blocks = append(s.Blocks, DetailBlock{Kind: DetailBlockField, Label: label, Text: text, Tone: tone, RenderedText: renderedText})
}

func (s *ProjectDetailSurface) WrappedField(label, text string, tone Tone) {
	s.Blocks = append(s.Blocks, DetailBlock{Kind: DetailBlockWrappedField, Label: label, Text: text, Tone: tone})
}

func (s *ProjectDetailSurface) FieldGroup(fields ...DetailFieldValue) {
	s.Blocks = append(s.Blocks, DetailBlock{Kind: DetailBlockFieldGroup, Fields: fields})
}

func (s *ProjectDetailSurface) Text(text string, tone Tone) {
	s.Blocks = append(s.Blocks, DetailBlock{Kind: DetailBlockText, Text: text, Tone: tone})
}

func (s *ProjectDetailSurface) RenderedText(text string, tone Tone, renderedText string) {
	s.Blocks = append(s.Blocks, DetailBlock{Kind: DetailBlockText, Text: text, Tone: tone, RenderedText: renderedText})
}

func (s *ProjectDetailSurface) Section(text string) {
	s.Blocks = append(s.Blocks, DetailBlock{Kind: DetailBlockSection, Text: text})
}

func (s *ProjectDetailSurface) Bullet(text string, tone Tone) {
	s.Blocks = append(s.Blocks, DetailBlock{Kind: DetailBlockBullet, Text: text, Tone: tone})
}

func FieldValue(label, text string, tone Tone) DetailFieldValue {
	return DetailFieldValue{Label: label, Text: text, Tone: tone}
}

func RenderedFieldValue(label, text string, tone Tone, renderedText string) DetailFieldValue {
	return DetailFieldValue{Label: label, Text: text, Tone: tone, RenderedText: renderedText}
}

func BuildDashboard(projects []model.ProjectSummary, categories []model.ProjectCategory, options BuildOptions) DashboardSurface {
	options = normalizedOptions(options)
	items := make([]ProjectItem, 0, len(projects))
	for _, project := range projects {
		if project.Archived || (options.HidePrivate && project.CategoryPrivate) {
			continue
		}
		items = append(items, BuildProjectItem(project, options))
	}

	sort.SliceStable(items, func(i, j int) bool {
		leftRank := projectBucketRank(items[i].Bucket)
		rightRank := projectBucketRank(items[j].Bucket)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if items[i].AttentionScore != items[j].AttentionScore {
			return items[i].AttentionScore > items[j].AttentionScore
		}
		if !items[i].LastActivityAt.Equal(items[j].LastActivityAt) {
			return items[i].LastActivityAt.After(items[j].LastActivityAt)
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	surface := DashboardSurface{
		GeneratedAt: options.Now,
		Projects:    items,
		Categories:  buildCategories(items, categories, options.HidePrivate),
	}
	for _, item := range items {
		surface.Counts.All++
		switch item.Bucket {
		case ProjectBucketAttention:
			surface.Counts.Attention++
		case ProjectBucketActive:
			surface.Counts.Active++
		default:
			surface.Counts.Quiet++
		}
	}
	return surface
}

func BuildProjectItem(project model.ProjectSummary, options BuildOptions) ProjectItem {
	options = normalizedOptions(options)
	assessment, category, hasAssessment := projectAssessment(project, options)
	activity := projectActivity(project, options.Now)
	summary := projectSummary(project, options, category, hasAssessment)
	bucket := projectBucket(project, category, hasAssessment, options.Now)
	source := SourceLabel(project.LatestSessionFormat)

	item := ProjectItem{
		Path:              project.Path,
		Name:              project.Name,
		Kind:              string(model.NormalizeProjectKind(project.Kind)),
		CategoryID:        strings.TrimSpace(project.CategoryID),
		CategoryName:      strings.TrimSpace(project.CategoryName),
		Summary:           summary,
		Assessment:        assessment,
		Activity:          activity,
		Bucket:            bucket,
		AttentionScore:    project.AttentionScore,
		LastActivityAt:    project.LastActivity,
		LastActivityLabel: formatLastActivity(options.Now, project.LastActivity),
		SourceLabel:       source,
		OpenTODOCount:     project.OpenTODOCount,
		TotalTODOCount:    project.TotalTODOCount,
		Badges:            projectBadges(project, assessment, bucket, source),
	}
	if strings.TrimSpace(item.Name) == "" {
		item.Name = project.Path
	}
	return item
}

func BuildProjectDetail(detail model.ProjectDetail, options BuildOptions) ProjectDetailSurface {
	options = normalizedOptions(options)
	project := detail.Summary
	item := BuildProjectItem(project, options)
	item.Badges = badgesWithoutKind(item.Badges, "todos")
	surface := ProjectDetailSurface{Project: item}

	statusFields := []DetailFieldValue{
		FieldValue("Assessment", item.Assessment.Label, item.Assessment.Tone),
	}
	if item.Activity.Label != "Idle" {
		statusFields = append(statusFields, FieldValue("Activity", item.Activity.Label, item.Activity.Tone))
	}
	surface.FieldGroup(statusFields...)
	if item.CategoryName != "" {
		surface.Field("Category", item.CategoryName, ToneValue)
	}
	surface.WrappedField("Path", item.Path, ToneValue)

	lastActivity := "Never"
	lastActivityTone := ToneMuted
	if !project.LastActivity.IsZero() {
		lastActivity = project.LastActivity.Format(time.RFC3339)
		lastActivityTone = ToneValue
	}
	if item.SourceLabel != "" {
		lastActivity += " - " + item.SourceLabel
	}
	surface.Field("Last activity", lastActivity, lastActivityTone)

	if repository := repositoryDescription(project); repository != "" {
		surface.WrappedField("Repository", repository, repositoryTone(project))
	}
	if worktree := worktreeDescription(project); worktree != "" {
		surface.WrappedField("Worktree", worktree, worktreeTone(project))
	}

	todoText := "None"
	todoTone := ToneMuted
	if project.OpenTODOCount > 0 {
		todoText = fmt.Sprintf("%d open", project.OpenTODOCount)
		todoTone = ToneValue
	}
	surface.Field("TODOs", todoText, todoTone)

	surface.Section("Attention reasons")
	reasons := DetailAttentionReasons(detail.Reasons)
	if len(reasons) == 0 {
		surface.Text("None", ToneMuted)
	} else {
		for _, reason := range reasons {
			tone := ToneValue
			if reason.Weight > 0 {
				tone = ToneWarning
			}
			surface.Bullet(reason.Text, tone)
		}
	}
	return surface
}

func DetailAttentionReasons(reasons []model.AttentionReason) []model.AttentionReason {
	filtered := make([]model.AttentionReason, 0, len(reasons))
	for _, reason := range reasons {
		if reason.Code == "has_open_todos" {
			continue
		}
		filtered = append(filtered, reason)
	}
	return filtered
}

func badgesWithoutKind(badges []Badge, kind string) []Badge {
	filtered := make([]Badge, 0, len(badges))
	for _, badge := range badges {
		if badge.Kind == kind {
			continue
		}
		filtered = append(filtered, badge)
	}
	return filtered
}

func normalizedOptions(options BuildOptions) BuildOptions {
	if options.Now.IsZero() {
		options.Now = time.Now()
	}
	return options
}

func buildCategories(items []ProjectItem, categories []model.ProjectCategory, hidePrivate bool) []Category {
	counts := map[string]int{}
	attentionCounts := map[string]int{}
	for _, item := range items {
		counts[item.CategoryID]++
		if item.Bucket == ProjectBucketAttention {
			attentionCounts[item.CategoryID]++
		}
	}

	out := []Category{{
		ID:             "main",
		Label:          "Main",
		Count:          counts[""],
		AttentionCount: attentionCounts[""],
	}}
	ordered := append([]model.ProjectCategory(nil), categories...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Position != ordered[j].Position {
			return ordered[i].Position < ordered[j].Position
		}
		return strings.ToLower(ordered[i].Name) < strings.ToLower(ordered[j].Name)
	})
	for _, category := range ordered {
		if hidePrivate && category.Private {
			continue
		}
		out = append(out, Category{
			ID:             category.ID,
			Label:          category.Name,
			Count:          counts[category.ID],
			AttentionCount: attentionCounts[category.ID],
			Private:        category.Private,
		})
	}
	all := Category{ID: "all", Label: "All", Count: len(items)}
	for _, item := range items {
		if item.Bucket == ProjectBucketAttention {
			all.AttentionCount++
		}
	}
	return append(out, all)
}

func projectAssessment(project model.ProjectSummary, options BuildOptions) (Status, model.SessionCategory, bool) {
	effective := effectiveAssessment(project, options)
	category := model.SessionCategoryUnknown
	hasAssessment := false

	if effective.Status == model.ClassificationCompleted && assessmentCategoryKnown(effective.Category) {
		category = effective.Category
		hasAssessment = true
	} else if project.LatestSessionClassification == model.ClassificationFailed && strings.TrimSpace(project.LatestSessionSummary) != "" && assessmentCategoryKnown(project.LatestSessionClassificationType) {
		category = project.LatestSessionClassificationType
		hasAssessment = true
	} else if assessmentRefreshing(project) && strings.TrimSpace(project.LatestSessionSummary) != "" && assessmentCategoryKnown(project.LatestSessionClassificationType) {
		category = project.LatestSessionClassificationType
		hasAssessment = true
	} else if !assessmentRefreshing(project) && project.LatestSessionClassification != model.ClassificationFailed && assessmentCategoryKnown(project.LatestCompletedSessionClassificationType) {
		category = project.LatestCompletedSessionClassificationType
		hasAssessment = true
	}

	if hasAssessment {
		return Status{Label: assessmentCategoryLabel(category), Tone: assessmentCategoryTone(category)}, category, true
	}
	switch project.LatestSessionClassification {
	case model.ClassificationPending:
		return Status{Label: "Queued", Tone: ToneInfo}, category, false
	case model.ClassificationRunning:
		return Status{Label: classificationStageLabel(project.LatestSessionClassificationStage), Tone: ToneInfo}, category, false
	case model.ClassificationFailed:
		return Status{Label: "Failed", Tone: ToneDanger}, category, false
	default:
		if project.LatestSessionFormat != "" {
			return Status{Label: "Not assessed", Tone: ToneMuted}, category, false
		}
		return Status{Label: "No assessment", Tone: ToneMuted}, category, false
	}
}

func effectiveAssessment(project model.ProjectSummary, options BuildOptions) sessionclassify.EffectiveAssessment {
	return EffectiveAssessmentForProject(project, options.Now, options.StuckThreshold)
}

func EffectiveAssessmentForProject(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) sessionclassify.EffectiveAssessment {
	return sessionclassify.DeriveEffectiveAssessment(sessionclassify.EffectiveAssessmentInput{
		Status:               project.LatestSessionClassification,
		Category:             project.LatestSessionClassificationType,
		Summary:              project.LatestSessionSummary,
		LastEventAt:          project.LatestSessionLastEventAt,
		LatestTurnStateKnown: project.LatestTurnStateKnown,
		LatestTurnCompleted:  project.LatestTurnCompleted,
		Now:                  now,
		StuckThreshold:       stuckThreshold,
	})
}

func projectSummary(project model.ProjectSummary, options BuildOptions, visibleCategory model.SessionCategory, hasAssessment bool) string {
	effective := effectiveAssessment(project, options)
	if strings.TrimSpace(effective.Summary) != "" && assessmentUsesLatestSummary(project) {
		return strings.TrimSpace(effective.Summary)
	}
	if assessmentRefreshing(project) {
		switch project.LatestSessionClassificationStage {
		case model.ClassificationStagePreparingSnapshot:
			return "Preparing the latest session snapshot"
		case model.ClassificationStageWaitingForModel:
			return "Waiting for the assessment model"
		default:
			return "Assessment is in progress"
		}
	}
	if project.LatestSessionClassification == model.ClassificationFailed {
		return "Assessment failed"
	}
	if strings.TrimSpace(project.LatestCompletedSessionSummary) != "" {
		return strings.TrimSpace(project.LatestCompletedSessionSummary)
	}
	if hasAssessment {
		return assessmentCategoryDescription(visibleCategory)
	}
	if fallback := repositorySummary(project); fallback != "" {
		return fallback
	}
	if project.LatestSessionFormat != "" {
		return "Not assessed yet"
	}
	return "No recent AI activity"
}

func projectActivity(project model.ProjectSummary, now time.Time) Status {
	switch {
	case !project.PresentOnDisk:
		return Status{Label: "Missing", Tone: ToneWarning}
	case moveStatusActive(project, now):
		return Status{Label: "Moved", Tone: ToneWarning}
	case project.Status == model.StatusActive:
		return Status{Label: "Active", Tone: TonePositive}
	case project.Status == model.StatusPossiblyStuck:
		return Status{Label: "Possibly stuck", Tone: ToneDanger}
	default:
		return Status{Label: "Idle", Tone: ToneMuted}
	}
}

func projectBucket(project model.ProjectSummary, category model.SessionCategory, hasAssessment bool, now time.Time) ProjectBucket {
	if !project.PresentOnDisk || moveStatusActive(project, now) || project.RepoConflict || project.RepoSyncStatus == model.RepoSyncDiverged || project.Status == model.StatusPossiblyStuck {
		return ProjectBucketAttention
	}
	if hasAssessment {
		switch category {
		case model.SessionCategoryBlocked, model.SessionCategoryWaitingForUser, model.SessionCategoryNeedsFollowUp:
			return ProjectBucketAttention
		case model.SessionCategoryInProgress:
			return ProjectBucketActive
		}
	}
	if project.Status == model.StatusActive || assessmentRefreshing(project) || (project.LatestTurnStateKnown && !project.LatestTurnCompleted) {
		return ProjectBucketActive
	}
	return ProjectBucketQuiet
}

func projectBadges(project model.ProjectSummary, assessment Status, bucket ProjectBucket, source string) []Badge {
	badges := []Badge{{Kind: "status", Label: assessment.Label, Tone: assessment.Tone}}
	if project.RepoConflict {
		badges = append(badges, Badge{Kind: "repository", Label: "Conflict", Tone: ToneConflict})
	} else if project.RepoDirty {
		badges = append(badges, Badge{Kind: "repository", Label: "Dirty", Tone: ToneWarning})
	} else if project.RepoSyncStatus == model.RepoSyncDiverged {
		badges = append(badges, Badge{Kind: "repository", Label: "Diverged", Tone: ToneDanger})
	} else if project.RepoSyncStatus == model.RepoSyncBehind {
		badges = append(badges, Badge{Kind: "repository", Label: fmt.Sprintf("Behind %d", project.RepoBehindCount), Tone: ToneWarning})
	} else if project.RepoSyncStatus == model.RepoSyncAhead {
		badges = append(badges, Badge{Kind: "repository", Label: fmt.Sprintf("Ahead %d", project.RepoAheadCount), Tone: ToneInfo})
	}
	if source != "" {
		badges = append(badges, Badge{Kind: "source", Label: source, Tone: ToneInfo})
	}
	if project.OpenTODOCount > 0 {
		badges = append(badges, Badge{Kind: "todos", Label: fmt.Sprintf("%d TODO", project.OpenTODOCount), Tone: ToneValue})
	}
	if len(badges) == 0 && bucket == ProjectBucketQuiet {
		badges = append(badges, Badge{Kind: "status", Label: "Quiet", Tone: ToneMuted})
	}
	return badges
}

func repositorySummary(project model.ProjectSummary) string {
	switch {
	case project.RepoConflict:
		return "Unmerged files need attention"
	case project.RepoDirty:
		return "Working tree has local changes"
	case project.RepoSubmoduleDirtyCount > 0 || project.RepoSubmoduleUnpushedCount > 0:
		return "Submodules need attention"
	case project.RepoSyncStatus == model.RepoSyncDiverged:
		return "Local and remote branches have diverged"
	case project.RepoSyncStatus == model.RepoSyncBehind:
		return fmt.Sprintf("Branch is behind by %d commit(s)", project.RepoBehindCount)
	case project.RepoSyncStatus == model.RepoSyncAhead:
		return fmt.Sprintf("Branch is ahead by %d commit(s)", project.RepoAheadCount)
	default:
		return ""
	}
}

func repositoryDescription(project model.ProjectSummary) string {
	hasRepository := project.RepoBranch != "" || project.RepoDirty || project.RepoConflict || project.RepoSyncStatus != "" || project.WorktreeKind != model.WorktreeKindNone
	if !hasRepository {
		return ""
	}
	parts := []string{}
	switch {
	case project.RepoConflict:
		parts = append(parts, "conflict")
	case project.RepoDirty:
		parts = append(parts, "dirty")
	default:
		parts = append(parts, "clean")
	}
	switch project.RepoSyncStatus {
	case model.RepoSyncNoRemote:
		parts = append(parts, "no remote")
	case model.RepoSyncNoUpstream:
		parts = append(parts, "no upstream")
	case model.RepoSyncSynced:
		parts = append(parts, "synced")
	case model.RepoSyncAhead:
		parts = append(parts, fmt.Sprintf("ahead %d", project.RepoAheadCount))
	case model.RepoSyncBehind:
		parts = append(parts, fmt.Sprintf("behind %d", project.RepoBehindCount))
	case model.RepoSyncDiverged:
		parts = append(parts, fmt.Sprintf("diverged +%d/-%d", project.RepoAheadCount, project.RepoBehindCount))
	}
	if branch := strings.TrimSpace(project.RepoBranch); branch != "" {
		parts = append(parts, branch)
	}
	return strings.Join(parts, " - ")
}

func repositoryTone(project model.ProjectSummary) Tone {
	if project.RepoConflict {
		return ToneConflict
	}
	if project.RepoSyncStatus == model.RepoSyncDiverged {
		return ToneDanger
	}
	if project.RepoDirty || project.RepoSyncStatus == model.RepoSyncAhead || project.RepoSyncStatus == model.RepoSyncBehind || project.RepoSyncStatus == model.RepoSyncNoUpstream || project.RepoSubmoduleDirtyCount > 0 || project.RepoSubmoduleUnpushedCount > 0 {
		return ToneWarning
	}
	return ToneMuted
}

func worktreeDescription(project model.ProjectSummary) string {
	if project.WorktreeKind != model.WorktreeKindLinked {
		return ""
	}
	target := strings.TrimSpace(project.WorktreeParentBranch)
	if target == "" {
		target = "parent branch unavailable"
	}
	switch project.WorktreeMergeStatus {
	case model.WorktreeMergeStatusMerged:
		return "Merged into " + target
	case model.WorktreeMergeStatusMergeInProgress:
		return "Merge in progress into " + target
	case model.WorktreeMergeStatusNotMerged:
		return "Ready to merge into " + target
	default:
		return "Linked worktree - target " + target
	}
}

func worktreeTone(project model.ProjectSummary) Tone {
	if project.RepoDirty || project.WorktreeMergeStatus == model.WorktreeMergeStatusMergeInProgress || project.WorktreeMergeStatus == model.WorktreeMergeStatusNotMerged {
		return ToneWarning
	}
	if project.WorktreeMergeStatus == model.WorktreeMergeStatusUnknown {
		return ToneMuted
	}
	return ToneValue
}

func assessmentRefreshing(project model.ProjectSummary) bool {
	return project.LatestSessionClassification == model.ClassificationPending || project.LatestSessionClassification == model.ClassificationRunning
}

func assessmentUsesLatestSummary(project model.ProjectSummary) bool {
	switch project.LatestSessionClassification {
	case model.ClassificationCompleted, model.ClassificationFailed, model.ClassificationPending, model.ClassificationRunning:
		return true
	default:
		return false
	}
}

func assessmentCategoryKnown(category model.SessionCategory) bool {
	switch category {
	case model.SessionCategoryCompleted, model.SessionCategoryBlocked, model.SessionCategoryWaitingForUser, model.SessionCategoryNeedsFollowUp, model.SessionCategoryInProgress:
		return true
	default:
		return false
	}
}

func assessmentCategoryLabel(category model.SessionCategory) string {
	switch category {
	case model.SessionCategoryCompleted:
		return "Done"
	case model.SessionCategoryBlocked:
		return "Blocked"
	case model.SessionCategoryWaitingForUser:
		return "Waiting"
	case model.SessionCategoryNeedsFollowUp:
		return "Follow up"
	case model.SessionCategoryInProgress:
		return "Working"
	default:
		return "Not assessed"
	}
}

func assessmentCategoryDescription(category model.SessionCategory) string {
	switch category {
	case model.SessionCategoryCompleted:
		return "Latest engineer work is complete"
	case model.SessionCategoryBlocked:
		return "Latest engineer work is blocked"
	case model.SessionCategoryWaitingForUser:
		return "Waiting for your input"
	case model.SessionCategoryNeedsFollowUp:
		return "Follow-up is recommended"
	case model.SessionCategoryInProgress:
		return "Engineer work is in progress"
	default:
		return "Not assessed yet"
	}
}

func assessmentCategoryTone(category model.SessionCategory) Tone {
	switch category {
	case model.SessionCategoryCompleted:
		return TonePositive
	case model.SessionCategoryBlocked:
		return ToneDanger
	case model.SessionCategoryWaitingForUser, model.SessionCategoryNeedsFollowUp:
		return ToneWarning
	case model.SessionCategoryInProgress:
		return ToneInfo
	default:
		return ToneMuted
	}
}

func classificationStageLabel(stage model.SessionClassificationStage) string {
	switch stage {
	case model.ClassificationStagePreparingSnapshot:
		return "Preparing"
	case model.ClassificationStageWaitingForModel:
		return "Assessing"
	default:
		return "Assessing"
	}
}

func AssessmentCompactLabel(category model.SessionCategory) (string, bool) {
	switch category {
	case model.SessionCategoryCompleted:
		return "done", true
	case model.SessionCategoryBlocked:
		return "blocked", true
	case model.SessionCategoryWaitingForUser:
		return "waiting", true
	case model.SessionCategoryNeedsFollowUp:
		return "followup", true
	case model.SessionCategoryInProgress:
		return "working", true
	default:
		return "", false
	}
}

func SourceLabel(format string) string {
	switch format {
	case "modern", "legacy":
		return "Codex"
	case "opencode_db":
		return "OpenCode"
	case "claude_code":
		return "Claude Code"
	case "lcagent_jsonl":
		return "LCAgent"
	default:
		return ""
	}
}

func RepoSyncWarning(status model.RepoSyncStatus) bool {
	switch status {
	case model.RepoSyncAhead, model.RepoSyncBehind, model.RepoSyncDiverged, model.RepoSyncNoUpstream:
		return true
	default:
		return false
	}
}

func ProjectMissing(project model.ProjectSummary) bool {
	return !project.PresentOnDisk
}

func moveStatusActive(project model.ProjectSummary, now time.Time) bool {
	if project.MovedAt.IsZero() {
		return false
	}
	age := now.Sub(project.MovedAt)
	if age < -time.Minute || age > 24*time.Hour {
		return false
	}
	latestPath := strings.TrimSpace(project.LatestSessionDetectedProjectPath)
	return latestPath == "" || filepath.Clean(latestPath) != filepath.Clean(strings.TrimSpace(project.Path))
}

func formatLastActivity(now, activity time.Time) string {
	if activity.IsZero() {
		return "Never"
	}
	age := now.Sub(activity)
	if age < 0 {
		return activity.Format("Jan 2, 15:04")
	}
	switch {
	case age < time.Minute:
		return "Now"
	case age < time.Hour:
		return fmt.Sprintf("%dm", max(1, int(age/time.Minute)))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh", max(1, int(age/time.Hour)))
	case age < 7*24*time.Hour:
		return fmt.Sprintf("%dd", max(1, int(age/(24*time.Hour))))
	default:
		return activity.Format("Jan 2")
	}
}

func projectBucketRank(bucket ProjectBucket) int {
	switch bucket {
	case ProjectBucketAttention:
		return 0
	case ProjectBucketActive:
		return 1
	default:
		return 2
	}
}
