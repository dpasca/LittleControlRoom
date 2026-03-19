package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/detectors/codex"
	"lcroom/internal/detectors/opencode"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/runtimeguard"
	"lcroom/internal/server"
	"lcroom/internal/service"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/store"
	"lcroom/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

const recentMoveWindow = 24 * time.Hour

func Run(programName string, args []string) int {
	if len(args) < 1 {
		printUsage(programName)
		return 2
	}

	subcmd := args[0]
	commonArgs := append([]string(nil), args[1:]...)
	if subcmd == "screenshots" {
		var err error
		commonArgs, _, err = stripPathFlagArg(commonArgs, "--screenshot-config")
		if err != nil {
			fmt.Fprintf(os.Stderr, "screenshots flag error: %v\n", err)
			return 2
		}
		commonArgs, _, err = stripPathFlagArg(commonArgs, "--output-dir")
		if err != nil {
			fmt.Fprintf(os.Stderr, "screenshots flag error: %v\n", err)
			return 2
		}
	}

	cfg, err := config.Parse(subcmd, commonArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 2
	}
	if subcmd == "scope" {
		runScope(cfg)
		return 0
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var runtimeLease *runtimeguard.Lease
	if guardedRuntimeMode(subcmd) {
		lease, owner, err := runtimeguard.Acquire(cfg.DBPath, subcmd)
		if err != nil {
			if errors.Is(err, runtimeguard.ErrBusy) {
				message := formatRuntimeConflictMessage(owner, cfg.DBPath, subcmd)
				if cfg.AllowMultipleInstances {
					fmt.Fprintf(os.Stderr, "warning: %s\n", message)
				} else {
					fmt.Fprintln(os.Stderr, message)
					fmt.Fprintln(os.Stderr, "Re-run with --allow-multiple-instances only for intentional short-lived dev/debug overlap.")
					return 1
				}
			} else {
				fmt.Fprintf(os.Stderr, "runtime lease failed: %v\n", err)
				return 1
			}
		} else {
			runtimeLease = lease
			if peer, err := runtimeguard.FindPeerProcess(cfg.DBPath); err != nil {
				fmt.Fprintf(os.Stderr, "runtime peer check failed: %v\n", err)
				return 1
			} else if peer != nil {
				message := formatRuntimeConflictMessage(peer, cfg.DBPath, subcmd)
				if cfg.AllowMultipleInstances {
					fmt.Fprintf(os.Stderr, "warning: %s\n", message)
				} else {
					_ = runtimeLease.Close()
					fmt.Fprintln(os.Stderr, message)
					fmt.Fprintln(os.Stderr, "Stop the older lcroom process first, or re-run with --allow-multiple-instances only for intentional short-lived dev/debug overlap.")
					return 1
				}
			}
			defer runtimeLease.Close()
		}
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		return 1
	}
	defer st.Close()

	bus := events.NewBus()
	detectorList := []detectors.Detector{
		codex.New(cfg.CodexHome),
		opencode.New(cfg.OpenCodeHome),
	}
	svc := service.New(cfg, st, bus, detectorList)

	switch subcmd {
	case "scan":
		return runScan(ctx, svc)
	case "classify":
		return runClassify(ctx, svc)
	case "doctor":
		return runDoctor(ctx, svc, cfg)
	case "snapshot":
		return runSnapshot(ctx, svc, cfg)
	case "screenshots":
		return runScreenshots(ctx, svc, args[1:])
	case "tui":
		return runTUI(ctx, svc)
	case "serve":
		return runServe(ctx, svc)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcmd)
		printUsage(programName)
		return 2
	}
}

func runScope(cfg config.AppConfig) {
	fmt.Printf("config file: %s\n", cfg.ConfigPath)
	if cfg.ConfigLoaded {
		fmt.Println("config loaded: yes")
	} else {
		fmt.Println("config loaded: no (using defaults and/or flags)")
	}
	fmt.Printf("include paths: %d\n", len(cfg.IncludePaths))
	for i, path := range cfg.IncludePaths {
		fmt.Printf("  %d. %s\n", i+1, path)
	}
	fmt.Printf("exclude paths: %d\n", len(cfg.ExcludePaths))
	for i, path := range cfg.ExcludePaths {
		fmt.Printf("  %d. %s\n", i+1, path)
	}
	fmt.Printf("exclude project patterns: %d\n", len(cfg.ExcludeProjectPatterns))
	for i, pattern := range cfg.ExcludeProjectPatterns {
		fmt.Printf("  %d. %s\n", i+1, pattern)
	}
}

func runScan(ctx context.Context, svc *service.Service) int {
	report, err := svc.ScanOnce(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", err)
		return 1
	}

	fmt.Printf("scan complete at %s\n", report.At.Format(time.RFC3339))
	fmt.Printf("activity projects: %d\n", report.ActivityProjectCount)
	fmt.Printf("tracked projects: %d\n", report.TrackedProjectCount)
	fmt.Printf("updated projects: %d\n", len(report.UpdatedProjects))
	fmt.Printf("queued classifications: %d\n", report.QueuedClassifications)
	return 0
}

func runClassify(ctx context.Context, svc *service.Service) int {
	if !svc.HasSessionClassifier() {
		fmt.Fprintln(os.Stderr, "classify requires a configured AI backend; run /setup in the TUI or save ai_backend in the config first")
		return 1
	}

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "classify scan failed: %v\n", err)
		return 1
	}

	go svc.StartSessionClassifier(ctx)
	fmt.Printf("classification run started at %s\n", report.At.Format(time.RFC3339))
	fmt.Printf("queued classifications: %d\n", report.QueuedClassifications)

	prevPending, prevRunning := -1, -1
	for {
		counts, err := svc.Store().GetSessionClassificationCounts(ctx, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "classification counts failed: %v\n", err)
			return 1
		}

		pending := counts[model.ClassificationPending]
		running := counts[model.ClassificationRunning]
		if pending != prevPending || running != prevRunning {
			fmt.Printf("queue status: pending=%d running=%d completed=%d failed=%d\n",
				pending,
				running,
				counts[model.ClassificationCompleted],
				counts[model.ClassificationFailed],
			)
			prevPending, prevRunning = pending, running
		}

		if pending == 0 && running == 0 {
			fmt.Println("classification queue drained")
			return 0
		}

		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "classification interrupted")
			return 1
		case <-time.After(750 * time.Millisecond):
		}
	}
}

func runDoctor(ctx context.Context, svc *service.Service, cfg config.AppConfig) int {
	report, err := loadDoctorReport(ctx, svc, cfg.DoctorScan)
	if err != nil {
		if cfg.DoctorScan {
			fmt.Fprintf(os.Stderr, "doctor scan failed: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "doctor read failed: %v\n", err)
		}
		return 1
	}

	states := append([]model.ProjectState(nil), report.States...)
	ignored, err := svc.Store().ListIgnoredProjectNames(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctor ignored-project lookup failed: %v\n", err)
		return 1
	}
	states = filterProjectStatesByIgnoredName(states, ignored)
	states = filterProjectStatesByName(states, cfg.ExcludeProjectPatterns)
	sort.Slice(states, func(i, j int) bool {
		if states[i].AttentionScore == states[j].AttentionScore {
			return states[i].Path < states[j].Path
		}
		return states[i].AttentionScore > states[j].AttentionScore
	})

	mode := "cached"
	if cfg.DoctorScan {
		mode = "fresh scan"
	}
	fmt.Printf("doctor report (%s, %s)\n", mode, report.At.Format(time.RFC3339))
	fmt.Printf("projects: %d\n\n", len(states))
	if !cfg.DoctorScan && len(states) == 0 {
		fmt.Println("No cached project state yet. Run `lcroom scan` or `lcroom doctor --scan` to populate the store.")
		fmt.Println()
	}
	for _, s := range states {
		last := "-"
		if !s.LastActivity.IsZero() {
			last = s.LastActivity.Format(time.RFC3339)
		}
		displayStatus := string(s.Status)
		if !s.PresentOnDisk {
			displayStatus = "missing"
		} else if stateMoveStatusActive(s) {
			displayStatus = "moved"
		}
		fmt.Printf("Project: %s\n", s.Path)
		fmt.Printf("  status=%s attention=%d last_activity=%s\n", displayStatus, s.AttentionScore, last)
		if s.RepoDirty {
			fmt.Println("  repo_dirty=true")
		}
		if remoteLine := formatRepoSyncLine(s.RepoSyncStatus, s.RepoAheadCount, s.RepoBehindCount); remoteLine != "" {
			fmt.Printf("  repo_remote=%s\n", remoteLine)
		}
		if !s.PresentOnDisk {
			fmt.Printf("  attention_status=%s\n", s.Status)
			fmt.Println("  path_status=missing_on_disk")
		} else if stateMoveStatusActive(s) {
			fmt.Printf("  attention_status=%s\n", s.Status)
		}
		if s.MovedFromPath != "" {
			movedAt := "-"
			if !s.MovedAt.IsZero() {
				movedAt = s.MovedAt.Format(time.RFC3339)
			}
			fmt.Printf("  moved_from=%s moved_at=%s\n", s.MovedFromPath, movedAt)
		}
		if len(s.AttentionReason) > 0 {
			fmt.Println("  reasons:")
			for _, r := range s.AttentionReason {
				fmt.Printf("    - [%+d] %s (%s)\n", r.Weight, r.Text, r.Code)
			}
		}
		fmt.Printf("  sessions=%d artifacts=%d\n", len(s.Sessions), len(s.Artifacts))
		if len(s.Sessions) > 0 {
			fmt.Println("  session samples:")
			for i := 0; i < min(3, len(s.Sessions)); i++ {
				session := s.Sessions[i]
				fmt.Printf("    - id=%s format=%s last=%s errors=%d file=%s\n", session.SessionID, session.Format, session.LastEventAt.Format(time.RFC3339), session.ErrorCount, session.SessionFile)
			}
			classification, err := svc.Store().GetSessionClassification(ctx, s.Sessions[0].SessionID)
			if err == nil {
				fmt.Printf("  latest_session_assessment: status=%s", classification.Status)
				if classification.Stage != "" {
					fmt.Printf(" stage=%s", classificationStageLabel(classification.Status, classification.Stage))
				}
				if elapsed := classificationStageElapsed(classification, report.At); elapsed != "" {
					fmt.Printf(" elapsed=%s", elapsed)
				}
				if classification.Category != "" {
					fmt.Printf(" category=%s", classification.Category)
				}
				fmt.Println()
				if classification.Summary != "" {
					fmt.Printf("    %s\n", classification.Summary)
				}
			} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
				fmt.Printf("  latest_session_assessment: error=%v\n", err)
			}
		}
		if len(s.Artifacts) > 0 {
			fmt.Println("  artifacts:")
			for i := 0; i < min(4, len(s.Artifacts)); i++ {
				a := s.Artifacts[i]
				fmt.Printf("    - %s | %s | %s\n", a.Kind, a.Path, strings.TrimSpace(a.Note))
			}
		}
		fmt.Println()
	}
	return 0
}

type snapshotDumpSelection struct {
	State   model.ProjectState
	Session model.SessionEvidence
}

type snapshotDumpEntry struct {
	ProjectPath   string                           `json:"project_path"`
	SessionID     string                           `json:"session_id"`
	SessionFile   string                           `json:"session_file"`
	SessionFormat string                           `json:"session_format"`
	LastEventAt   string                           `json:"last_event_at"`
	Snapshot      *sessionclassify.SessionSnapshot `json:"snapshot,omitempty"`
	ExtractError  string                           `json:"extract_error,omitempty"`
}

func runSnapshot(ctx context.Context, svc *service.Service, cfg config.AppConfig) int {
	report, err := svc.ScanOnce(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot scan failed: %v\n", err)
		return 1
	}

	states := filterProjectStatesByName(report.States, cfg.ExcludeProjectPatterns)
	ignored, err := svc.Store().ListIgnoredProjectNames(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot ignored-project lookup failed: %v\n", err)
		return 1
	}
	states = filterProjectStatesByIgnoredName(states, ignored)
	selected := selectOpenCodeSnapshotSessions(states, cfg.SnapshotProject, cfg.SnapshotSessionID, cfg.SnapshotLimit)
	dumps := make([]snapshotDumpEntry, 0, len(selected))
	for _, choice := range selected {
		entry := snapshotDumpEntry{
			ProjectPath:   choice.State.Path,
			SessionID:     choice.Session.SessionID,
			SessionFile:   choice.Session.SessionFile,
			SessionFormat: choice.Session.Format,
		}
		if !choice.Session.LastEventAt.IsZero() {
			entry.LastEventAt = choice.Session.LastEventAt.UTC().Format(time.RFC3339)
		}
		gitStatus := sessionclassify.NewGitStatusSnapshot(
			choice.State.RepoDirty,
			choice.State.RepoSyncStatus,
			choice.State.RepoAheadCount,
			choice.State.RepoBehindCount,
		)
		snapshot, err := sessionclassify.ExtractSnapshot(ctx, model.SessionClassification{
			SessionID:       choice.Session.SessionID,
			ProjectPath:     choice.State.Path,
			SessionFile:     choice.Session.SessionFile,
			SessionFormat:   choice.Session.Format,
			SourceUpdatedAt: choice.Session.LastEventAt,
		}, choice.Session, gitStatus)
		if err != nil {
			entry.ExtractError = err.Error()
		} else {
			entry.Snapshot = &snapshot
		}
		dumps = append(dumps, entry)
	}

	encoded, err := json.MarshalIndent(dumps, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot encode failed: %v\n", err)
		return 1
	}
	fmt.Println(string(encoded))
	return 0
}

func selectOpenCodeSnapshotSessions(states []model.ProjectState, projectPath, sessionID string, limit int) []snapshotDumpSelection {
	projectPath = strings.TrimSpace(projectPath)
	sessionID = strings.TrimSpace(sessionID)

	selected := make([]snapshotDumpSelection, 0, limit)
	for _, state := range states {
		if projectPath != "" && filepath.Clean(state.Path) != filepath.Clean(projectPath) {
			continue
		}
		for _, session := range state.Sessions {
			if session.Format != "opencode_db" {
				continue
			}
			if sessionID != "" && session.SessionID != sessionID {
				continue
			}
			selected = append(selected, snapshotDumpSelection{
				State:   state,
				Session: session,
			})
		}
	}

	sort.Slice(selected, func(i, j int) bool {
		if selected[i].Session.LastEventAt.Equal(selected[j].Session.LastEventAt) {
			if selected[i].State.Path == selected[j].State.Path {
				return selected[i].Session.SessionID < selected[j].Session.SessionID
			}
			return selected[i].State.Path < selected[j].State.Path
		}
		return selected[i].Session.LastEventAt.After(selected[j].Session.LastEventAt)
	})

	if limit > 0 && len(selected) > limit {
		selected = selected[:limit]
	}
	return selected
}

func loadDoctorReport(ctx context.Context, svc *service.Service, refresh bool) (service.ScanReport, error) {
	if refresh {
		return svc.ScanOnce(ctx)
	}

	states, err := loadStoredProjectStates(ctx, svc.Store())
	if err != nil {
		return service.ScanReport{}, err
	}

	return service.ScanReport{
		At:     time.Now(),
		States: states,
	}, nil
}

func classificationStageLabel(status model.SessionClassificationStatus, stage model.SessionClassificationStage) string {
	switch status {
	case model.ClassificationPending:
		return "queued"
	case model.ClassificationRunning:
		switch stage {
		case model.ClassificationStagePreparingSnapshot:
			return "preparing_snapshot"
		case model.ClassificationStageWaitingForModel:
			return "waiting_for_model"
		default:
			return "running"
		}
	default:
		return string(stage)
	}
}

func classificationStageElapsed(classification model.SessionClassification, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	startedAt := classification.StageStartedAt
	if startedAt.IsZero() {
		startedAt = classification.UpdatedAt
	}
	if startedAt.IsZero() {
		return ""
	}
	switch classification.Status {
	case model.ClassificationPending, model.ClassificationRunning:
		return formatAssessmentElapsed(now.Sub(startedAt))
	default:
		return ""
	}
}

func formatAssessmentElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSeconds := int64(d / time.Second)
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

func loadStoredProjectStates(ctx context.Context, st *store.Store) ([]model.ProjectState, error) {
	summaries, err := st.ListProjects(ctx, false)
	if err != nil {
		return nil, err
	}

	states := make([]model.ProjectState, 0, len(summaries))
	for _, summary := range summaries {
		detail, err := st.GetProjectDetail(ctx, summary.Path, 1)
		if err != nil {
			return nil, err
		}
		states = append(states, projectStateFromDetail(detail))
	}
	return states, nil
}

func projectStateFromDetail(detail model.ProjectDetail) model.ProjectState {
	summary := detail.Summary
	return model.ProjectState{
		Path:            summary.Path,
		Name:            summary.Name,
		LastActivity:    summary.LastActivity,
		Status:          summary.Status,
		AttentionScore:  summary.AttentionScore,
		PresentOnDisk:   summary.PresentOnDisk,
		RepoDirty:       summary.RepoDirty,
		RepoSyncStatus:  summary.RepoSyncStatus,
		RepoAheadCount:  summary.RepoAheadCount,
		RepoBehindCount: summary.RepoBehindCount,
		Forgotten:       summary.Forgotten,
		InScope:         summary.InScope,
		Pinned:          summary.Pinned,
		SnoozedUntil:    cloneOptionalTime(summary.SnoozedUntil),
		Note:            summary.Note,
		MovedFromPath:   summary.MovedFromPath,
		MovedAt:         summary.MovedAt,
		AttentionReason: append([]model.AttentionReason(nil), detail.Reasons...),
		Sessions:        append([]model.SessionEvidence(nil), detail.Sessions...),
		Artifacts:       append([]model.ArtifactEvidence(nil), detail.Artifacts...),
	}
}

func cloneOptionalTime(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	t := *src
	return &t
}

func filterProjectStatesByName(states []model.ProjectState, excludeProjectPatterns []string) []model.ProjectState {
	if len(states) == 0 || len(excludeProjectPatterns) == 0 {
		return states
	}
	filtered := make([]model.ProjectState, 0, len(states))
	for _, state := range states {
		if config.ProjectNameExcluded(state.Name, excludeProjectPatterns) {
			continue
		}
		base := filepath.Base(filepath.Clean(state.Path))
		if strings.EqualFold(strings.TrimSpace(base), strings.TrimSpace(state.Name)) || !config.ProjectNameExcluded(base, excludeProjectPatterns) {
			filtered = append(filtered, state)
		}
	}
	return filtered
}

func filterProjectStatesByIgnoredName(states []model.ProjectState, ignored []model.IgnoredProjectName) []model.ProjectState {
	if len(states) == 0 || len(ignored) == 0 {
		return states
	}
	ignoredNames := make(map[string]struct{}, len(ignored))
	for _, entry := range ignored {
		name := strings.ToLower(strings.TrimSpace(entry.Name))
		if name == "" {
			continue
		}
		ignoredNames[name] = struct{}{}
	}
	if len(ignoredNames) == 0 {
		return states
	}
	filtered := make([]model.ProjectState, 0, len(states))
	for _, state := range states {
		if _, hidden := ignoredNames[strings.ToLower(strings.TrimSpace(state.Name))]; hidden {
			continue
		}
		filtered = append(filtered, state)
	}
	return filtered
}

func movedRecently(movedAt time.Time) bool {
	if movedAt.IsZero() {
		return false
	}
	age := time.Since(movedAt)
	return age >= -time.Minute && age <= recentMoveWindow
}

func stateMoveStatusActive(state model.ProjectState) bool {
	if !movedRecently(state.MovedAt) {
		return false
	}
	if len(state.Sessions) == 0 || state.Sessions[0].DetectedProjectPath == "" {
		return true
	}
	return filepath.Clean(state.Sessions[0].DetectedProjectPath) != filepath.Clean(state.Path)
}

func formatRepoSyncLine(status model.RepoSyncStatus, ahead, behind int) string {
	switch status {
	case model.RepoSyncNoRemote:
		return "none"
	case model.RepoSyncNoUpstream:
		return "no_upstream"
	case model.RepoSyncSynced:
		return "synced"
	case model.RepoSyncAhead:
		return fmt.Sprintf("ahead +%d", ahead)
	case model.RepoSyncBehind:
		return fmt.Sprintf("behind -%d", behind)
	case model.RepoSyncDiverged:
		return fmt.Sprintf("diverged +%d/-%d", ahead, behind)
	default:
		return ""
	}
}

func runTUI(ctx context.Context, svc *service.Service) int {
	_, _ = svc.ScanOnce(ctx)
	go svc.StartScheduler(ctx)
	go svc.StartSessionClassifier(ctx)

	m := tui.New(ctx, svc)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui failed: %v\n", err)
		return 1
	}
	return 0
}

func runServe(ctx context.Context, svc *service.Service) int {
	_, _ = svc.ScanOnce(ctx)
	go svc.StartScheduler(ctx)
	go svc.StartSessionClassifier(ctx)

	s := server.New(svc)
	fmt.Printf("serving %s on :7777\n", brand.Name)
	if err := s.Run(ctx, ":7777"); err != nil {
		fmt.Fprintf(os.Stderr, "serve failed: %v\n", err)
		return 1
	}
	return 0
}

func printUsage(programName string) {
	name := strings.TrimSpace(programName)
	if name == "" {
		name = brand.CLIName
	}
	fmt.Println(brand.Name)
	fmt.Println(brand.Subtitle)
	fmt.Printf("Usage: %s <scope|scan|classify|doctor|snapshot|screenshots|tui|serve> [flags]\n", name)
	fmt.Println("Common flags:")
	fmt.Println("  --config <path>")
	fmt.Println("  --include-paths <comma-separated-paths>")
	fmt.Println("  --exclude-paths <comma-separated-paths>")
	fmt.Println("  --exclude-project-patterns <comma-separated-patterns>")
	fmt.Println("  --codex-home <path>")
	fmt.Println("  --opencode-home <path>")
	fmt.Println("  --db <path>")
	fmt.Println("  --interval <duration>")
	fmt.Println("  --active-threshold <duration>")
	fmt.Println("  --stuck-threshold <duration>")
	fmt.Println("  --allow-multiple-instances")
	fmt.Println("Snapshot flags:")
	fmt.Println("  --limit <count>")
	fmt.Println("  --project <path>")
	fmt.Println("  --session-id <id>")
	fmt.Println("Screenshots flags:")
	fmt.Println("  --screenshot-config <path>")
	fmt.Println("  --output-dir <path>")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func guardedRuntimeMode(subcmd string) bool {
	switch subcmd {
	case "classify", "tui", "serve":
		return true
	default:
		return false
	}
}

func formatRuntimeConflictMessage(owner *runtimeguard.Owner, dbPath, mode string) string {
	lines := []string{
		fmt.Sprintf("another %s %s runtime is already active for %s", brand.Name, strings.TrimSpace(mode), filepath.Clean(strings.TrimSpace(dbPath))),
	}
	if owner == nil {
		return strings.Join(lines, "\n")
	}
	if owner.PID > 0 {
		lines = append(lines, fmt.Sprintf("  pid: %d", owner.PID))
	}
	if trimmed := strings.TrimSpace(owner.Mode); trimmed != "" {
		lines = append(lines, fmt.Sprintf("  active mode: %s", trimmed))
	}
	if !owner.StartedAt.IsZero() {
		lines = append(lines, fmt.Sprintf("  started: %s", owner.StartedAt.Format(time.RFC3339)))
	}
	if trimmed := strings.TrimSpace(owner.CWD); trimmed != "" {
		lines = append(lines, fmt.Sprintf("  cwd: %s", trimmed))
	}
	if trimmed := strings.TrimSpace(owner.Hostname); trimmed != "" {
		lines = append(lines, fmt.Sprintf("  host: %s", trimmed))
	}
	if trimmed := strings.TrimSpace(owner.Command); trimmed != "" {
		lines = append(lines, fmt.Sprintf("  command: %s", trimmed))
	}
	return strings.Join(lines, "\n")
}
