package boss

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/procinspect"
)

const bossProcessReportTimeout = 2 * time.Second

type processReportFunc func(context.Context, procinspect.ScanOptions) ([]procinspect.ProjectReport, error)

type bossProcessFinding struct {
	procinspect.Finding
	ProjectName string
}

func defaultProcessReporter(ctx context.Context, opts procinspect.ScanOptions) ([]procinspect.ProjectReport, error) {
	return procinspect.ScanProjects(ctx, opts)
}

func (e *QueryExecutor) processReport(ctx context.Context, action bossAction, view ViewContext) (bossToolResult, error) {
	projects, err := e.store.ListProjects(ctx, action.IncludeHistorical)
	if err != nil {
		return bossToolResult{}, err
	}
	projects = filterProjectSummariesForBossPrivacy(projects, view)

	projectNameByPath := map[string]string{}
	projectPaths := make([]string, 0, len(projects))
	seen := map[string]struct{}{}
	for _, project := range projects {
		path := filepath.Clean(strings.TrimSpace(project.Path))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		projectPaths = append(projectPaths, path)
		projectNameByPath[path] = displayProjectName(project)
	}

	scope := "visible projects"
	note := ""
	if processReportHasTarget(action) {
		path, resolvedNote, err := e.resolveProjectPath(ctx, action, view)
		if err != nil {
			return bossToolResult{}, err
		}
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || path == "." {
			return clippedToolResult(bossActionProcessReport, "Process report needs a project path or visible project name."), nil
		}
		projectPaths = []string{path}
		if _, ok := projectNameByPath[path]; !ok {
			projectNameByPath[path] = filepath.Base(path)
		}
		scope = "project " + projectNameByPath[path]
		note = resolvedNote
	}

	if len(projectPaths) == 0 {
		return clippedToolResult(bossActionProcessReport, "Process report: no visible projects are available to scan."), nil
	}

	reporter := e.processReporter
	if reporter == nil {
		reporter = defaultProcessReporter
	}
	scanCtx, cancel := context.WithTimeout(ctx, bossProcessReportTimeout)
	defer cancel()
	reports, err := reporter(scanCtx, procinspect.ScanOptions{
		ProjectPaths: projectPaths,
		OwnPID:       os.Getpid(),
		Now:          e.now(),
	})
	if err != nil {
		return bossToolResult{}, err
	}

	findings, scannedAt := bossProcessFindings(reports, projectNameByPath)
	limit := clampBossLimit(action.Limit, 10, 30)
	lines := []string{
		fmt.Sprintf("Process report: %d suspicious project-local process%s across %s.", len(findings), pluralSuffix(len(findings)), scope),
		"Scanned at: " + formatBossTimestamp(scannedAt) + ".",
		"Safety note: report-only; Boss Chat cannot stop or kill processes.",
	}
	if note != "" {
		lines = append(lines, "Target note: "+note)
	}
	if action.IncludeHistorical {
		lines = append(lines, "Historical/out-of-scope projects were included.")
	}
	if len(findings) == 0 {
		lines = append(lines, "No suspicious project-local processes found.")
		return clippedToolResult(bossActionProcessReport, strings.Join(lines, "\n")), nil
	}

	lines = append(lines, fmt.Sprintf("Showing %d of %d, sorted by CPU.", minInt(limit, len(findings)), len(findings)))
	for i, finding := range findings {
		if i >= limit {
			break
		}
		lines = append(lines, bossProcessFindingLine(i+1, finding))
	}
	return clippedToolResult(bossActionProcessReport, strings.Join(lines, "\n")), nil
}

func processReportHasTarget(action bossAction) bool {
	return strings.TrimSpace(action.ProjectPath) != "" ||
		strings.TrimSpace(action.ProjectName) != "" ||
		strings.EqualFold(strings.TrimSpace(action.Target), "selected")
}

func bossProcessFindings(reports []procinspect.ProjectReport, projectNameByPath map[string]string) ([]bossProcessFinding, time.Time) {
	findings := []bossProcessFinding{}
	var scannedAt time.Time
	for _, report := range reports {
		if report.ScannedAt.After(scannedAt) {
			scannedAt = report.ScannedAt
		}
		path := filepath.Clean(strings.TrimSpace(report.ProjectPath))
		name := strings.TrimSpace(projectNameByPath[path])
		if name == "" {
			name = filepath.Base(path)
		}
		for _, finding := range report.Findings {
			if len(finding.Reasons) == 0 {
				continue
			}
			findings = append(findings, bossProcessFinding{
				Finding:     finding,
				ProjectName: name,
			})
		}
	}
	if scannedAt.IsZero() {
		scannedAt = time.Now()
	}
	sort.SliceStable(findings, func(i, j int) bool {
		left := findings[i].Finding
		right := findings[j].Finding
		switch {
		case left.CPU != right.CPU:
			return left.CPU > right.CPU
		case findings[i].ProjectName != findings[j].ProjectName:
			return findings[i].ProjectName < findings[j].ProjectName
		default:
			return left.PID < right.PID
		}
	})
	return findings, scannedAt
}

func bossProcessFindingLine(index int, finding bossProcessFinding) string {
	parts := []string{
		fmt.Sprintf("%d. PID %d", index, finding.PID),
		fmt.Sprintf("CPU %.1f%%", finding.CPU),
		"project: " + emptyLabel(finding.ProjectName),
		"reasons: " + strings.Join(finding.Reasons, ", "),
	}
	if len(finding.Ports) > 0 {
		parts = append(parts, "ports: "+joinProcessInts(finding.Ports))
	}
	if finding.PPID > 0 {
		parts = append(parts, fmt.Sprintf("parent: %d", finding.PPID))
	}
	if finding.PGID > 0 {
		parts = append(parts, fmt.Sprintf("group: %d", finding.PGID))
	}
	if cwd := strings.TrimSpace(finding.CWD); cwd != "" {
		parts = append(parts, "cwd: "+cwd)
	}
	if command := strings.TrimSpace(finding.Command); command != "" {
		parts = append(parts, "command: "+clipText(command, 240))
	}
	return "- " + strings.Join(parts, " | ")
}

func joinProcessInts(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ",")
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "es"
}
