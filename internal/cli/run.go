package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/buildinfo"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/detectors/claudecode"
	"lcroom/internal/detectors/codex"
	lcagentdetector "lcroom/internal/detectors/lcagent"
	"lcroom/internal/detectors/opencode"
	"lcroom/internal/events"
	"lcroom/internal/helpmeta"
	"lcroom/internal/model"
	"lcroom/internal/modeleval"
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
	modelEvalOpts := modelEvalCLIOptions{}
	if subcmd == "version" || subcmd == "--version" || subcmd == "-v" {
		fmt.Println(buildinfo.Summary(programName))
		return 0
	}
	if subcmd == "playwright-mcp" {
		return runPlaywrightMCP(args[1:])
	}
	if subcmd == "runtime-mcp" {
		return runRuntimeMCP(args[1:])
	}
	if subcmd == "browser" {
		return runBrowser(args[1:])
	}
	if subcmd == "mockups" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return runMockups(ctx, args[1:])
	}
	if subcmd == "help-meta" {
		return runHelpMeta(args[1:])
	}
	commonArgs := append([]string(nil), args[1:]...)
	if subcmd == "model-eval" {
		var err error
		commonArgs, modelEvalOpts, err = splitModelEvalArgs(commonArgs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "model-eval flag error: %v\n", err)
			return 2
		}
	}
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
	listenOverride := ""
	if subcmd == "serve" || subcmd == "tui" {
		var err error
		commonArgs, listenOverride, err = stripPathFlagArg(commonArgs, "--listen")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s flag error: %v\n", subcmd, err)
			return 2
		}
	}

	cfg, err := config.Parse(subcmd, commonArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 2
	}
	serveAddr := server.DefaultListenAddress
	mobileEnabled := cfg.MobileEnabled
	if subcmd == "serve" || subcmd == "tui" {
		serveAddr, mobileEnabled, err = resolveMobileRuntimeOptions(cfg, listenOverride)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s flag error: %v\n", subcmd, err)
			return 2
		}
	}
	if subcmd == "scope" {
		runScope(cfg)
		return 0
	}
	if subcmd == "tui" || subcmd == "boss" {
		defer recoverInteractivePanic(cfg, subcmd)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if subcmd == "model-eval" {
		return runModelEval(ctx, cfg, modelEvalOpts)
	}

	var runtimeLease *runtimeguard.Lease
	if guardedRuntimeMode(subcmd) {
		lease, owner, err := runtimeguard.Acquire(cfg.DBPath, subcmd)
		if err != nil {
			if errors.Is(err, runtimeguard.ErrBusy) {
				message := formatRuntimeConflictMessage(owner, cfg.DBPath, subcmd)
				if cfg.AllowMultipleInstances {
					printPlainRuntimeError("warning: " + message)
				} else {
					printPlainRuntimeError(message)
					return 1
				}
			} else {
				printPlainRuntimeError(fmt.Sprintf("runtime lease failed: %v", err))
				return 1
			}
		} else {
			runtimeLease = lease
			if peer, err := runtimeguard.FindPeerProcess(cfg.DBPath); err != nil {
				printPlainRuntimeError(fmt.Sprintf("runtime peer check failed: %v", err))
				return 1
			} else if peer != nil {
				message := formatRuntimeConflictMessage(peer, cfg.DBPath, subcmd)
				if cfg.AllowMultipleInstances {
					printPlainRuntimeError("warning: " + message)
				} else {
					_ = runtimeLease.Close()
					printPlainRuntimeError(message)
					return 1
				}
			}
			defer func() {
				if runtimeLease != nil {
					_ = runtimeLease.Close()
				}
			}()
		}
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		return 1
	}
	defer func() {
		if st != nil {
			_ = st.Close()
		}
	}()

	bus := events.NewBus()
	detectorList := []detectors.Detector{
		codex.New(cfg.CodexHome),
		opencode.New(cfg.OpenCodeHome),
		claudecode.New(cfg.ClaudeCodeHome),
		lcagentdetector.New(cfg.DataDir),
	}
	svc := service.New(cfg, st, bus, detectorList)
	if subcmd == "tui" || subcmd == "serve" {
		if err := svc.InitializeTodoCapturePolicy(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "TODO capture policy error: %v\n", err)
			return 1
		}
	}

	switch subcmd {
	case "scan":
		return runScan(ctx, svc)
	case "classify":
		return runClassify(ctx, svc)
	case "sanitize-summaries":
		return runSanitizeSummaries(ctx, svc.Store(), cfg)
	case "doctor":
		return runDoctor(ctx, svc, cfg)
	case "snapshot":
		return runSnapshot(ctx, svc, cfg)
	case "screenshots":
		return runScreenshots(ctx, svc, args[1:])
	case "tui":
		code, relaunch := runTUI(ctx, svc, serveAddr, mobileEnabled)
		if code != 0 || !relaunch {
			return code
		}
		cancel()
		if err := st.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close store before update restart: %v\n", err)
			return 1
		}
		st = nil
		if runtimeLease != nil {
			if err := runtimeLease.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "release runtime lease before update restart: %v\n", err)
				return 1
			}
			runtimeLease = nil
		}
		return execUpdatedProcess(programName, args)
	case "serve":
		return runServe(ctx, svc, serveAddr)
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

func runHelpMeta(args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "help-meta does not accept arguments: %s\n", strings.Join(args, " "))
		return 2
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(helpmeta.Topics()); err != nil {
		fmt.Fprintf(os.Stderr, "help-meta failed: %v\n", err)
		return 1
	}
	return 0
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
	if report.GitMetadataTimeoutCount > 0 {
		fmt.Printf("git metadata timeouts: %d\n", report.GitMetadataTimeoutCount)
		for _, path := range report.GitMetadataTimeoutPathSamples {
			fmt.Printf("  - %s\n", path)
		}
		if remaining := report.GitMetadataTimeoutCount - len(report.GitMetadataTimeoutPathSamples); remaining > 0 {
			fmt.Printf("  ... and %d more\n", remaining)
		}
	}
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

type modelEvalCLIOptions struct {
	Backend    config.AIBackend
	BackendSet bool
	Model      string
	BaseURL    string
	APIKey     string
	Timeout    time.Duration
	JSON       bool
}

func splitModelEvalArgs(args []string) ([]string, modelEvalCLIOptions, error) {
	common := make([]string, 0, len(args))
	opts := modelEvalCLIOptions{}
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--json":
			opts.JSON = true
		case arg == "--backend" || strings.HasPrefix(arg, "--backend="):
			value, next, err := flagValue(args, i, "--backend")
			if err != nil {
				return nil, opts, err
			}
			backend, err := config.ParseAIBackend(value)
			if err != nil {
				return nil, opts, err
			}
			opts.Backend = backend
			opts.BackendSet = true
			i = next
		case arg == "--model" || strings.HasPrefix(arg, "--model="):
			value, next, err := flagValue(args, i, "--model")
			if err != nil {
				return nil, opts, err
			}
			opts.Model = strings.TrimSpace(value)
			i = next
		case arg == "--base-url" || strings.HasPrefix(arg, "--base-url="):
			value, next, err := flagValue(args, i, "--base-url")
			if err != nil {
				return nil, opts, err
			}
			opts.BaseURL = strings.TrimSpace(value)
			i = next
		case arg == "--api-key" || strings.HasPrefix(arg, "--api-key="):
			value, next, err := flagValue(args, i, "--api-key")
			if err != nil {
				return nil, opts, err
			}
			opts.APIKey = strings.TrimSpace(value)
			i = next
		case arg == "--timeout" || strings.HasPrefix(arg, "--timeout="):
			value, next, err := flagValue(args, i, "--timeout")
			if err != nil {
				return nil, opts, err
			}
			timeout, err := time.ParseDuration(strings.TrimSpace(value))
			if err != nil {
				return nil, opts, fmt.Errorf("--timeout: %w", err)
			}
			opts.Timeout = timeout
			i = next
		default:
			common = append(common, args[i])
		}
	}
	return common, opts, nil
}

func flagValue(args []string, index int, name string) (string, int, error) {
	arg := strings.TrimSpace(args[index])
	prefix := name + "="
	if strings.HasPrefix(arg, prefix) {
		value := strings.TrimSpace(strings.TrimPrefix(arg, prefix))
		if value == "" {
			return "", index, fmt.Errorf("%s requires a value", name)
		}
		return value, index, nil
	}
	if arg != name {
		return "", index, fmt.Errorf("internal flag parser mismatch for %s", name)
	}
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("%s requires a value", name)
	}
	value := strings.TrimSpace(args[index+1])
	if value == "" {
		return "", index, fmt.Errorf("%s requires a value", name)
	}
	return value, index + 1, nil
}

func runModelEval(ctx context.Context, cfg config.AppConfig, cliOpts modelEvalCLIOptions) int {
	backend := cfg.EffectiveAIBackend()
	if cliOpts.BackendSet {
		backend = cliOpts.Backend
	}
	if backend == config.AIBackendUnset || backend == config.AIBackendDisabled {
		fmt.Fprintln(os.Stderr, "model-eval requires a model backend; pass --backend ollama or configure ai_backend")
		return 2
	}

	opts := modeleval.Options{
		Backend: backend,
		BaseURL: cfg.OpenAICompatibleBaseURL(backend),
		APIKey:  cfg.OpenAICompatibleAPIKey(backend),
		Model:   cfg.OpenAICompatibleModel(backend),
		Timeout: cliOpts.Timeout,
	}
	if backend == config.AIBackendOpenAIAPI {
		opts.APIKey = strings.TrimSpace(cfg.OpenAIAPIKey)
	}
	if cliOpts.BaseURL != "" {
		opts.BaseURL = cliOpts.BaseURL
	}
	if cliOpts.APIKey != "" {
		opts.APIKey = cliOpts.APIKey
	}
	if cliOpts.Model != "" {
		opts.Model = cliOpts.Model
	}

	report, err := modeleval.Run(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "model-eval failed: %v\n", err)
		return 1
	}
	if cliOpts.JSON {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "model-eval JSON failed: %v\n", err)
			return 1
		}
		fmt.Println(string(raw))
	} else {
		printModelEvalReport(report)
	}
	if !report.Passed() {
		return 1
	}
	return 0
}

func printModelEvalReport(report modeleval.Report) {
	status := "FAIL"
	if report.Passed() {
		status = "PASS"
	}
	fmt.Printf("model eval: %s\n", status)
	fmt.Printf("backend: %s\n", report.Backend)
	fmt.Printf("model: %s\n", report.Model)
	if strings.TrimSpace(report.BaseURL) != "" {
		fmt.Printf("base url: %s\n", report.BaseURL)
	}
	if report.ContextWindow > 0 {
		fmt.Printf("context: %s tokens", formatPlainTokenCount(report.ContextWindow))
		if strings.TrimSpace(report.ContextDetail) != "" {
			fmt.Printf(" (%s)", report.ContextDetail)
		}
		fmt.Println()
	} else if strings.TrimSpace(report.ContextDetail) != "" {
		fmt.Printf("context: %s\n", report.ContextDetail)
	}
	fmt.Printf("duration: %s\n", report.Duration.Round(time.Millisecond))
	fmt.Println()
	for _, c := range report.Cases {
		caseStatus := "FAIL"
		if c.Passed {
			caseStatus = "PASS"
		}
		fmt.Printf("- %s %s", caseStatus, c.Name)
		if c.Duration > 0 {
			fmt.Printf(" %s", c.Duration.Round(time.Millisecond))
		}
		if c.TokensPerSecond > 0 {
			fmt.Printf(" %.1f tok/s", c.TokensPerSecond)
		}
		fmt.Println()
		if c.Error != "" {
			fmt.Printf("  error: %s\n", c.Error)
		}
		if c.OutputPreview != "" {
			fmt.Printf("  output: %s\n", c.OutputPreview)
		}
	}
}

func formatPlainTokenCount(tokens int64) string {
	if tokens >= 1000 && tokens%1000 == 0 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
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
	ignored, err := svc.Store().ListIgnoredProjects(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctor ignored-project lookup failed: %v\n", err)
		return 1
	}
	states = filterProjectStatesByIgnores(states, ignored)
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
				fmt.Printf("    - id=%s format=%s last=%s errors=%d file=%s\n", session.ExternalID(), session.Format, session.LastEventAt.Format(time.RFC3339), session.ErrorCount, session.SessionFile)
			}
			classification, err := svc.Store().GetSessionClassification(ctx, s.Sessions[0].SessionID)
			if err == nil {
				effective := sessionclassify.DeriveEffectiveAssessment(sessionclassify.EffectiveAssessmentInput{
					Status:               classification.Status,
					Category:             classification.Category,
					Summary:              classification.Summary,
					LastEventAt:          s.Sessions[0].LastEventAt,
					LatestTurnStateKnown: s.Sessions[0].LatestTurnStateKnown,
					LatestTurnCompleted:  s.Sessions[0].LatestTurnCompleted,
					Now:                  report.At,
					StuckThreshold:       sessionclassify.EffectiveAssessmentStallThreshold(svc.Config().ActiveThreshold, svc.Config().StuckThreshold),
				})
				fmt.Printf("  latest_session_assessment: status=%s", classification.Status)
				if classification.Stage != "" {
					fmt.Printf(" stage=%s", classificationStageLabel(classification.Status, classification.Stage))
				}
				if elapsed := classificationStageElapsed(classification, report.At); elapsed != "" {
					fmt.Printf(" elapsed=%s", elapsed)
				}
				if effective.Category != "" {
					fmt.Printf(" category=%s", effective.Category)
				}
				fmt.Println()
				if effective.Summary != "" {
					fmt.Printf("    %s\n", effective.Summary)
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
	ignored, err := svc.Store().ListIgnoredProjects(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot ignored-project lookup failed: %v\n", err)
		return 1
	}
	states = filterProjectStatesByIgnores(states, ignored)
	selected := selectSnapshotSessions(states, cfg.SnapshotProject, cfg.SnapshotSessionID, cfg.SnapshotLimit)
	dumps := make([]snapshotDumpEntry, 0, len(selected))
	for _, choice := range selected {
		entry := snapshotDumpEntry{
			ProjectPath:   choice.State.Path,
			SessionID:     choice.Session.ExternalID(),
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
			Source:          choice.Session.Source,
			SessionID:       choice.Session.SessionID,
			RawSessionID:    choice.Session.RawSessionID,
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

func selectSnapshotSessions(states []model.ProjectState, projectPath, sessionID string, limit int) []snapshotDumpSelection {
	projectPath = strings.TrimSpace(projectPath)
	sessionID = strings.TrimSpace(sessionID)

	selected := make([]snapshotDumpSelection, 0, limit)
	for _, state := range states {
		if projectPath != "" && filepath.Clean(state.Path) != filepath.Clean(projectPath) {
			continue
		}
		for _, session := range state.Sessions {
			if sessionID != "" && session.SessionID != sessionID && session.ExternalID() != sessionID {
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
				return selected[i].Session.ExternalID() < selected[j].Session.ExternalID()
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

func filterProjectStatesByIgnores(states []model.ProjectState, ignored []model.IgnoredProject) []model.ProjectState {
	if len(states) == 0 || len(ignored) == 0 {
		return states
	}
	ignoredNames := make(map[string]struct{}, len(ignored))
	ignoredPaths := make(map[string]struct{}, len(ignored))
	for _, entry := range ignored {
		switch entry.Scope {
		case model.ProjectIgnoreScopePath:
			path := filepath.Clean(strings.TrimSpace(entry.Path))
			if path != "." {
				ignoredPaths[path] = struct{}{}
			}
		default:
			name := strings.ToLower(strings.TrimSpace(entry.Name))
			if name != "" {
				ignoredNames[name] = struct{}{}
			}
		}
	}
	if len(ignoredNames) == 0 && len(ignoredPaths) == 0 {
		return states
	}
	filtered := make([]model.ProjectState, 0, len(states))
	for _, state := range states {
		if _, hidden := ignoredPaths[filepath.Clean(strings.TrimSpace(state.Path))]; hidden {
			continue
		}
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

func resolveMobileRuntimeOptions(cfg config.AppConfig, listenOverride string) (string, bool, error) {
	listenAddress := strings.TrimSpace(cfg.MobileListenAddress)
	if listenAddress == "" {
		listenAddress = server.DefaultListenAddress
	}
	enabled := cfg.MobileEnabled
	if override := strings.TrimSpace(listenOverride); override != "" {
		listenAddress = override
		enabled = true
	}
	if err := server.ValidateListenAddress(listenAddress); err != nil {
		return "", false, err
	}
	return listenAddress, enabled, nil
}

func runTUI(ctx context.Context, svc *service.Service, mobileListenAddress string, mobileEnabled bool) (int, bool) {
	runCtx, cancel := context.WithCancel(ctx)
	codexManager := codexapp.NewManager()
	var mobileServer *server.RunningServer
	mobileStatus := tui.MobileServerStatus{ListenAddress: mobileListenAddress, Disabled: !mobileEnabled}
	if mobileEnabled {
		mobileServer, mobileStatus = startTUIMobileServer(runCtx, svc, codexManager, mobileListenAddress)
	}
	mobileStatus.LANAddresses = localPrivateLANIPv4Addresses()
	defer func() {
		cancel()
		if err := stopRunningServer(mobileServer); err != nil {
			fmt.Fprintf(os.Stderr, "mobile server shutdown failed: %v\n", err)
		}
	}()
	defer func() {
		if _, err := codexManager.CloseAllForRestart(svc.Config().DataDir); err != nil {
			fmt.Fprintf(os.Stderr, "embedded session shutdown failed: %v\n", err)
			_ = codexManager.CloseAll()
		}
	}()

	go svc.StartScheduler(runCtx)
	go svc.StartSessionClassifier(runCtx)
	go svc.StartTodoWorktreeSuggester(runCtx)
	go svc.StartTodoCaptureRelay(runCtx)
	go svc.StartCommitTodoChecker(runCtx)
	svc.StartBackgroundDiscovery(runCtx)

	m := tui.NewWithCodexManager(runCtx, svc, codexManager)
	m.SetMobileServerStatus(mobileStatus)
	m.EnableUIStallWatchdog()
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui failed: %v\n", err)
		return 1, false
	}
	relaunch := false
	switch final := finalModel.(type) {
	case tui.Model:
		_, relaunch = final.RelaunchAfterUpdate()
	case *tui.Model:
		if final != nil {
			_, relaunch = final.RelaunchAfterUpdate()
		}
	}
	return 0, relaunch
}

func execUpdatedProcess(programName string, args []string) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve updated executable for restart: %v\n", err)
		return 1
	}
	argv := append([]string{programName}, args...)
	if err := syscall.Exec(executable, argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "restart updated Little Control Room: %v\n", err)
		return 1
	}
	return 0
}

type lanAddressCandidate struct {
	address        string
	interfaceIndex int
	interfaceRank  int
}

func localPrivateLANIPv4Addresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	candidates := []lanAddressCandidate{}
	seen := map[string]struct{}{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip := interfaceAddressIP(address)
			if ip == nil || ip.To4() == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			value := ip.To4().String()
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			candidates = append(candidates, lanAddressCandidate{
				address:        value,
				interfaceIndex: iface.Index,
				interfaceRank:  lanInterfaceRank(iface.Name),
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].interfaceRank != candidates[j].interfaceRank {
			return candidates[i].interfaceRank < candidates[j].interfaceRank
		}
		if candidates[i].interfaceIndex != candidates[j].interfaceIndex {
			return candidates[i].interfaceIndex < candidates[j].interfaceIndex
		}
		return candidates[i].address < candidates[j].address
	})
	addresses := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		addresses = append(addresses, candidate.address)
	}
	return addresses
}

func interfaceAddressIP(address net.Addr) net.IP {
	switch value := address.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		ip, _, err := net.ParseCIDR(strings.TrimSpace(address.String()))
		if err != nil {
			return nil
		}
		return ip
	}
}

func lanInterfaceRank(name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(name, "en"),
		strings.HasPrefix(name, "eth"),
		strings.HasPrefix(name, "wlan"),
		strings.HasPrefix(name, "wl"):
		return 0
	case strings.HasPrefix(name, "bridge"):
		return 1
	default:
		return 2
	}
}

func startTUIMobileServer(ctx context.Context, svc *service.Service, liveSessions server.LiveSessionSource, listenAddress string) (*server.RunningServer, tui.MobileServerStatus) {
	listenAddress = strings.TrimSpace(listenAddress)
	if listenAddress == "" {
		listenAddress = server.DefaultListenAddress
	}
	status := tui.MobileServerStatus{ListenAddress: listenAddress}
	mobileServer := server.New(svc).WithLiveSessions(liveSessions)
	auth, err := configureMobileServerAuth(mobileServer, svc, listenAddress)
	if err != nil {
		status.Error = err.Error()
		return nil, status
	}
	if auth != nil {
		status.AuthRequired = true
		status.PairingCode = auth.PairingCode()
	}
	running, err := mobileServer.Start(ctx, listenAddress)
	if err != nil {
		status.Error = err.Error()
		return nil, status
	}
	status.URL = running.URL()
	status.BoundAddress = running.Address()
	return running, status
}

func configureMobileServerAuth(mobileServer *server.Server, svc *service.Service, listenAddress string) (*server.MobileAuth, error) {
	if server.ListenAddressIsLoopback(listenAddress) {
		return nil, nil
	}
	auth, err := server.NewMobileAuth(svc.Config().DataDir)
	if err != nil {
		return nil, fmt.Errorf("initialize LAN mobile authentication: %w", err)
	}
	mobileServer.WithMobileAuth(auth)
	return auth, nil
}

func stopRunningServer(running *server.RunningServer) error {
	if running == nil {
		return nil
	}
	select {
	case <-running.Done():
		return running.Wait()
	default:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := running.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = running.Close()
	}
	return errors.Join(shutdownErr, running.Wait())
}

func recoverInteractivePanic(cfg config.AppConfig, mode string) {
	recovered := recover()
	if recovered == nil {
		return
	}
	path, err := writeInteractivePanicDump(cfg, mode, recovered)
	if err != nil {
		printPlainRuntimeError(fmt.Sprintf("%s panic dump failed: %v", brand.CLIName, err))
	} else {
		printPlainRuntimeError(fmt.Sprintf("%s panic dump written to %s", brand.CLIName, path))
	}
	panic(recovered)
}

func writeInteractivePanicDump(cfg config.AppConfig, mode string, recovered any) (string, error) {
	dataDir := strings.TrimSpace(cfg.DataDir)
	if dataDir == "" {
		dataDir = filepath.Dir(strings.TrimSpace(cfg.DBPath))
	}
	if dataDir == "" || dataDir == "." {
		dataDir = filepath.Join(".", brand.DataDirName)
	}
	now := time.Now()
	dumpDir := filepath.Join(dataDir, "crash-dumps", now.Format("20060102-150405.000")+"-"+mode)
	if err := os.MkdirAll(dumpDir, 0o755); err != nil {
		return "", fmt.Errorf("create crash dump directory: %w", err)
	}
	stack := allGoroutineStack()
	cwd, _ := os.Getwd()
	payload := strings.Join([]string{
		fmt.Sprintf("mode: %s", strings.TrimSpace(mode)),
		fmt.Sprintf("time: %s", now.Format(time.RFC3339Nano)),
		fmt.Sprintf("pid: %d", os.Getpid()),
		fmt.Sprintf("cwd: %s", cwd),
		fmt.Sprintf("db_path: %s", cfg.DBPath),
		fmt.Sprintf("config_path: %s", cfg.ConfigPath),
		fmt.Sprintf("panic: %v", recovered),
		"",
		string(stack),
	}, "\n")
	panicPath := filepath.Join(dumpDir, "panic.txt")
	if err := os.WriteFile(panicPath, []byte(payload), 0o600); err != nil {
		return "", fmt.Errorf("write panic dump: %w", err)
	}
	return panicPath, nil
}

func allGoroutineStack() []byte {
	size := 1 << 20
	for {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return buf[:n]
		}
		size *= 2
	}
}

func runServe(ctx context.Context, svc *service.Service, addr string) int {
	if bus := svc.Bus(); bus != nil {
		scanEvents, unsubscribe := bus.Subscribe(256)
		defer unsubscribe()
		go logServeScanEvents(ctx, scanEvents, os.Stderr)
	}
	go func() {
		if _, err := svc.ScanOnce(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "initial serve scan failed: %v\n", err)
		}
	}()
	go svc.StartScheduler(ctx)
	go svc.StartSessionClassifier(ctx)
	go svc.StartTodoWorktreeSuggester(ctx)
	go svc.StartTodoCaptureRelay(ctx)
	go svc.StartCommitTodoChecker(ctx)
	svc.StartBackgroundDiscovery(ctx)

	s := server.New(svc)
	auth, err := configureMobileServerAuth(s, svc, addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve failed: %v\n", err)
		return 1
	}
	running, err := s.Start(ctx, addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve failed: %v\n", err)
		return 1
	}
	fmt.Printf("serving %s at %s\n", brand.Name, running.URL())
	if auth != nil {
		fmt.Printf("mobile pairing code: %s\n", auth.PairingCode())
		fmt.Println("warning: LAN authentication does not encrypt HTTP traffic; use a trusted network")
	}
	if err := running.Wait(); err != nil {
		fmt.Fprintf(os.Stderr, "serve failed: %v\n", err)
		return 1
	}
	return 0
}

func runSanitizeSummaries(ctx context.Context, st *store.Store, cfg config.AppConfig) int {
	dryRun := cfg.SanitizeDryRun
	if cfg.SanitizeApply {
		dryRun = false
	}
	projectPath := strings.TrimSpace(cfg.SanitizeProject)
	sessionID := strings.TrimSpace(cfg.SanitizeSessionID)

	classifications, err := st.ListSessionClassifications(ctx, projectPath, sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list session classifications failed: %v\n", err)
		return 1
	}

	if len(classifications) == 0 {
		fmt.Println("no matching session classifications found")
		return 0
	}

	details := make(map[string]model.ProjectDetail)
	changedCount := 0
	skippedCount := 0
	failedCount := 0
	for _, classification := range classifications {
		project := strings.TrimSpace(classification.ProjectPath)
		detail, ok := details[project]
		if !ok {
			detail, err = st.GetProjectDetail(ctx, project, 1)
			if err != nil {
				fmt.Fprintf(os.Stderr, "load project detail failed: project=%s err=%v\n", project, err)
				failedCount++
				continue
			}
			details[project] = detail
		}
		session := model.SessionEvidence{
			Source:       classification.Source,
			SessionID:    strings.TrimSpace(classification.SessionID),
			RawSessionID: strings.TrimSpace(classification.RawSessionID),
			ProjectPath:  strings.TrimSpace(classification.ProjectPath),
			SessionFile:  strings.TrimSpace(classification.SessionFile),
			Format:       strings.TrimSpace(classification.SessionFormat),
			SnapshotHash: strings.TrimSpace(classification.SnapshotHash),
		}

		gitStatus := sessionclassify.NewGitStatusSnapshot(
			detail.Summary.RepoDirty,
			detail.Summary.RepoSyncStatus,
			detail.Summary.RepoAheadCount,
			detail.Summary.RepoBehindCount,
		)
		snapshot, err := sessionclassify.ExtractSnapshot(ctx, model.SessionClassification{
			Source:          classification.Source,
			SessionID:       classification.SessionID,
			RawSessionID:    classification.RawSessionID,
			ProjectPath:     classification.ProjectPath,
			SessionFile:     classification.SessionFile,
			SessionFormat:   classification.SessionFormat,
			SourceUpdatedAt: classification.SourceUpdatedAt,
		}, session, gitStatus)
		if err != nil {
			fmt.Fprintf(os.Stderr, "extract snapshot failed: project=%s session=%s err=%v\n", project, classification.ExternalID(), err)
			failedCount++
			continue
		}

		sanitized := sessionclassify.SanitizeClassificationSummary(classification.Summary, snapshot)
		if strings.TrimSpace(sanitized) == strings.TrimSpace(classification.Summary) {
			skippedCount++
			continue
		}

		if dryRun {
			fmt.Printf("dry-run: would update %s summary\n  old: %q\n  new: %q\n", classification.ExternalID(), classification.Summary, sanitized)
			skippedCount++
			continue
		}

		updated, err := st.UpdateSessionClassificationSummary(ctx, classification.SessionID, sanitized)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update summary failed: project=%s session=%s err=%v\n", classification.ProjectPath, classification.ExternalID(), err)
			failedCount++
			continue
		}
		if updated {
			fmt.Printf("updated %s summary\n", classification.ExternalID())
			changedCount++
		} else {
			skippedCount++
		}
	}

	fmt.Printf("sanitize-summaries: matched=%d changed=%d skipped=%d failed=%d\n", len(classifications), changedCount, skippedCount, failedCount)
	if failedCount > 0 {
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
	fmt.Printf("Usage: %s <version|scope|scan|classify|model-eval|doctor|snapshot|sanitize-summaries|screenshots|mockups|browser|help-meta|tui|serve> [flags]\n", name)
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
	fmt.Println("Sanitize summaries flags:")
	fmt.Println("  --project <path>")
	fmt.Println("  --session-id <id>")
	fmt.Println("  --apply")
	fmt.Println("  --dry-run")
	fmt.Println("Model eval flags:")
	fmt.Println("  --backend <openai_api|ollama|mlx|openrouter|deepseek|moonshot|xiaomi>")
	fmt.Println("  --model <model-id>")
	fmt.Println("  --base-url <url>")
	fmt.Println("  --api-key <key>")
	fmt.Println("  --timeout <duration>")
	fmt.Println("  --json")
	fmt.Println("Snapshot flags:")
	fmt.Println("  --limit <count>")
	fmt.Println("  --project <path>")
	fmt.Println("  --session-id <id>")
	fmt.Println("Screenshots flags:")
	fmt.Println("  --screenshot-config <path>")
	fmt.Println("  --output-dir <path>")
	fmt.Println("Mockups flags:")
	fmt.Println("  --screenshot-config <path>")
	fmt.Println("  --output-dir <path>")
	fmt.Println("TUI and serve flags:")
	fmt.Printf("  --listen <host:port> (override saved mobile address; default %s)\n", server.DefaultListenAddress)
	fmt.Println("Browser flags:")
	fmt.Println("  browser <status|reveal> --session-key <id> [--data-dir <path>]")
	fmt.Println("Help metadata:")
	fmt.Println("  help-meta writes the generated command, capability, workflow, and keybinding corpus as JSON")
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
	cleanDB := strings.TrimSpace(dbPath)
	if cleanDB != "" {
		cleanDB = filepath.Clean(cleanDB)
	} else {
		cleanDB = "(unknown database)"
	}
	cleanMode := strings.TrimSpace(mode)
	if cleanMode == "" {
		cleanMode = "runtime"
	}

	lines := []string{
		fmt.Sprintf("%s is already running a %s runtime for this database.", brand.Name, cleanMode),
		"This launch stopped before starting the runtime so the database is not shared by two long-lived processes.",
		"",
		fmt.Sprintf("database: %s", cleanDB),
	}
	appendRuntimeConflictRecovery(&lines, owner)
	appendRuntimeConflictOwner(&lines, owner)
	lines = append(lines,
		"",
		"For intentional short-lived dev/debug overlap only, re-run with --allow-multiple-instances.",
	)
	return strings.Join(lines, "\n")
}

func appendRuntimeConflictRecovery(lines *[]string, owner *runtimeguard.Owner) {
	*lines = append(*lines, "", "To recover:")
	if owner != nil && owner.PID > 0 {
		*lines = append(*lines,
			fmt.Sprintf("  kill %d", owner.PID),
			fmt.Sprintf("  # if it does not exit: kill -9 %d", owner.PID),
		)
	} else {
		*lines = append(*lines, "  stop the older lcroom process, then run this command again")
	}
	*lines = append(*lines, "  # if your prompt still looks stair-stepped: stty sane")
}

func appendRuntimeConflictOwner(lines *[]string, owner *runtimeguard.Owner) {
	if owner == nil {
		return
	}
	*lines = append(*lines, "", "Active runtime:")
	if owner.PID > 0 {
		*lines = append(*lines, fmt.Sprintf("  pid: %d", owner.PID))
	}
	if trimmed := strings.TrimSpace(owner.Mode); trimmed != "" {
		*lines = append(*lines, fmt.Sprintf("  mode: %s", trimmed))
	}
	if !owner.StartedAt.IsZero() {
		*lines = append(*lines, fmt.Sprintf("  started: %s", owner.StartedAt.Format(time.RFC3339)))
	}
	if trimmed := strings.TrimSpace(owner.CWD); trimmed != "" {
		*lines = append(*lines, fmt.Sprintf("  cwd: %s", trimmed))
	}
	if trimmed := strings.TrimSpace(owner.Hostname); trimmed != "" {
		*lines = append(*lines, fmt.Sprintf("  host: %s", trimmed))
	}
	if trimmed := strings.TrimSpace(owner.Command); trimmed != "" {
		*lines = append(*lines, fmt.Sprintf("  command: %s", trimRuntimeConflictField(trimmed, 220)))
	}
}

func trimRuntimeConflictField(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func printPlainRuntimeError(message string) {
	restoreTerminalForPlainText()
	message = strings.TrimRight(message, "\r\n")
	if message == "" {
		return
	}
	fmt.Fprint(os.Stderr, strings.ReplaceAll(message, "\n", "\r\n")+"\r\n")
}

func restoreTerminalForPlainText() {
	const reset = "\r\x1b[0m\x1b[?25h\x1b[?1049l\x1b[?2004l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\r"
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer tty.Close()

	_, _ = tty.WriteString(reset)
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "stty", "sane")
	cmd.Stdin = tty
	_ = cmd.Run()
	_, _ = tty.WriteString(reset)
}
