package procinspect

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultHighCPUThreshold   = 50.0
	DefaultOrphanCPUThreshold = 1.0
	DefaultCPUTopLimit        = 15
	DefaultCPUCWDLimit        = 25
	DefaultHotTotalCPU        = 100.0
)

type Process struct {
	PID     int
	PPID    int
	PGID    int
	Stat    string
	CPU     float64
	Mem     float64
	Elapsed string
	Command string
	CWD     string
	Ports   []int
}

type Finding struct {
	Process
	ProjectPath       string
	Reasons           []string
	ManagedRuntime    bool
	OwnedByCurrentApp bool
}

type ProjectReport struct {
	ProjectPath string
	Findings    []Finding
	ScannedAt   time.Time
}

type CPUProcess struct {
	Process
	ProjectPath       string
	Reasons           []string
	ManagedRuntime    bool
	OwnedByCurrentApp bool
}

type CPUSnapshot struct {
	Processes    []CPUProcess
	TotalCPU     float64
	ProcessCount int
	LogicalCPUs  int
	ScannedAt    time.Time
}

type ScanOptions struct {
	ProjectPaths       []string
	ManagedPIDs        map[int]struct{}
	ManagedPGIDs       map[int]struct{}
	OwnPID             int
	HighCPUThreshold   float64
	OrphanCPUThreshold float64
	Now                time.Time
}

type CPUScanOptions struct {
	ProjectPaths       []string
	ManagedPIDs        map[int]struct{}
	ManagedPGIDs       map[int]struct{}
	OwnPID             int
	Limit              int
	CWDLimit           int
	LogicalCPUs        int
	HighCPUThreshold   float64
	OrphanCPUThreshold float64
	Now                time.Time
}

func ScanProjects(ctx context.Context, opts ScanOptions) ([]ProjectReport, error) {
	projectPaths := cleanProjectPaths(opts.ProjectPaths)
	if len(projectPaths) == 0 {
		return nil, nil
	}

	processes, err := currentProcesses(ctx)
	if err != nil {
		return nil, err
	}
	cwds, err := currentProcessCWDs(ctx)
	if err != nil {
		return nil, err
	}
	ports, _ := currentListeningPorts(ctx)

	ownPID := opts.OwnPID
	if ownPID <= 0 {
		ownPID = os.Getpid()
	}
	highCPUThreshold := opts.HighCPUThreshold
	if highCPUThreshold <= 0 {
		highCPUThreshold = DefaultHighCPUThreshold
	}
	orphanCPUThreshold := opts.OrphanCPUThreshold
	if orphanCPUThreshold <= 0 {
		orphanCPUThreshold = DefaultOrphanCPUThreshold
	}
	scannedAt := opts.Now
	if scannedAt.IsZero() {
		scannedAt = time.Now()
	}

	byPID := make(map[int]Process, len(processes))
	ppids := make(map[int]int, len(processes))
	for _, process := range processes {
		if cwd := strings.TrimSpace(cwds[process.PID]); cwd != "" {
			process.CWD = filepath.Clean(cwd)
		}
		if len(ports[process.PID]) > 0 {
			process.Ports = append([]int(nil), ports[process.PID]...)
		}
		byPID[process.PID] = process
		ppids[process.PID] = process.PPID
	}

	reportsByProject := make(map[string]*ProjectReport, len(projectPaths))
	for _, projectPath := range projectPaths {
		reportsByProject[projectPath] = &ProjectReport{ProjectPath: projectPath, ScannedAt: scannedAt}
	}

	for _, process := range byPID {
		if process.PID <= 0 || process.CWD == "" {
			continue
		}
		projectPath, ok := deepestProjectForPath(process.CWD, projectPaths)
		if !ok {
			continue
		}
		finding := classifyProcess(process, projectPath, ppids, ownPID, opts.ManagedPIDs, opts.ManagedPGIDs, highCPUThreshold, orphanCPUThreshold)
		if len(finding.Reasons) == 0 {
			continue
		}
		reportsByProject[projectPath].Findings = append(reportsByProject[projectPath].Findings, finding)
	}

	reports := make([]ProjectReport, 0, len(reportsByProject))
	for _, projectPath := range projectPaths {
		report := reportsByProject[projectPath]
		sort.SliceStable(report.Findings, func(i, j int) bool {
			left, right := report.Findings[i], report.Findings[j]
			if left.CPU != right.CPU {
				return left.CPU > right.CPU
			}
			if left.PPID == 1 && right.PPID != 1 {
				return true
			}
			if left.PPID != 1 && right.PPID == 1 {
				return false
			}
			return left.PID < right.PID
		})
		reports = append(reports, *report)
	}
	return reports, nil
}

func ScanCPU(ctx context.Context, opts CPUScanOptions) (CPUSnapshot, error) {
	processes, err := currentProcesses(ctx)
	if err != nil {
		return CPUSnapshot{}, err
	}
	pids := topProcessPIDs(processes, opts.cwdLimit())
	cwds := map[int]string{}
	if len(pids) > 0 {
		if pidCWDs, err := currentProcessCWDsForPIDs(ctx, pids); err == nil {
			cwds = pidCWDs
		}
	}
	return buildCPUSnapshot(processes, cwds, opts), nil
}

func buildCPUSnapshot(processes []Process, cwds map[int]string, opts CPUScanOptions) CPUSnapshot {
	projectPaths := cleanProjectPaths(opts.ProjectPaths)
	ownPID := opts.OwnPID
	if ownPID <= 0 {
		ownPID = os.Getpid()
	}
	highCPUThreshold := opts.HighCPUThreshold
	if highCPUThreshold <= 0 {
		highCPUThreshold = DefaultHighCPUThreshold
	}
	orphanCPUThreshold := opts.OrphanCPUThreshold
	if orphanCPUThreshold <= 0 {
		orphanCPUThreshold = DefaultOrphanCPUThreshold
	}
	scannedAt := opts.Now
	if scannedAt.IsZero() {
		scannedAt = time.Now()
	}

	ppids := make(map[int]int, len(processes))
	for _, process := range processes {
		ppids[process.PID] = process.PPID
	}

	sorted := append([]Process(nil), processes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := sorted[i], sorted[j]
		if left.CPU != right.CPU {
			return left.CPU > right.CPU
		}
		return left.PID < right.PID
	})

	limit := opts.limit()
	out := make([]CPUProcess, 0, minInt(limit, len(sorted)))
	totalCPU := 0.0
	for _, process := range sorted {
		if process.CPU > 0 {
			totalCPU += process.CPU
		}
		if len(out) >= limit {
			continue
		}
		if cwd := strings.TrimSpace(cwds[process.PID]); cwd != "" {
			process.CWD = filepath.Clean(cwd)
		}
		out = append(out, classifyCPUProcess(process, projectPaths, ppids, ownPID, opts.ManagedPIDs, opts.ManagedPGIDs, highCPUThreshold, orphanCPUThreshold))
	}
	return CPUSnapshot{
		Processes:    out,
		TotalCPU:     totalCPU,
		ProcessCount: len(processes),
		LogicalCPUs:  opts.logicalCPUs(),
		ScannedAt:    scannedAt,
	}
}

func classifyCPUProcess(process Process, projectPaths []string, ppids map[int]int, ownPID int, managedPIDs, managedPGIDs map[int]struct{}, highCPUThreshold, orphanCPUThreshold float64) CPUProcess {
	result := CPUProcess{Process: process}
	if process.CWD != "" {
		if projectPath, ok := deepestProjectForPath(process.CWD, projectPaths); ok {
			result.ProjectPath = projectPath
		}
	}
	if _, ok := managedPIDs[process.PID]; ok {
		result.ManagedRuntime = true
	}
	if _, ok := managedPGIDs[process.PGID]; ok && process.PGID > 0 {
		result.ManagedRuntime = true
	}
	result.OwnedByCurrentApp = processDescendsFrom(process.PID, ownPID, ppids)

	if process.CPU >= highCPUThreshold {
		result.Reasons = append(result.Reasons, fmt.Sprintf("high CPU %.1f%%", process.CPU))
	}
	if result.OwnedByCurrentApp {
		result.Reasons = append(result.Reasons, "spawned by LCR")
	}
	if result.ManagedRuntime {
		result.Reasons = append(result.Reasons, "managed runtime")
	}
	if result.ProjectPath != "" {
		result.Reasons = append(result.Reasons, "project-local")
	}
	if process.PPID == 1 && process.CPU >= orphanCPUThreshold {
		result.Reasons = append(result.Reasons, "orphaned under PID 1")
	}
	return result
}

func classifyProcess(process Process, projectPath string, ppids map[int]int, ownPID int, managedPIDs, managedPGIDs map[int]struct{}, highCPUThreshold, orphanCPUThreshold float64) Finding {
	finding := Finding{
		Process:     process,
		ProjectPath: projectPath,
	}
	if _, ok := managedPIDs[process.PID]; ok {
		finding.ManagedRuntime = true
	}
	if _, ok := managedPGIDs[process.PGID]; ok && process.PGID > 0 {
		finding.ManagedRuntime = true
	}
	finding.OwnedByCurrentApp = processDescendsFrom(process.PID, ownPID, ppids)
	if finding.ManagedRuntime || finding.OwnedByCurrentApp {
		return finding
	}

	if process.PPID == 1 && (process.CPU >= orphanCPUThreshold || len(process.Ports) > 0) {
		finding.Reasons = append(finding.Reasons, "orphaned under PID 1")
	}
	if process.CPU >= highCPUThreshold {
		finding.Reasons = append(finding.Reasons, fmt.Sprintf("high CPU %.1f%%", process.CPU))
	}
	if len(process.Ports) > 0 && process.PPID == 1 {
		finding.Reasons = append(finding.Reasons, "listening on TCP ports")
	}
	return finding
}

func processDescendsFrom(pid, ancestor int, ppids map[int]int) bool {
	if pid <= 0 || ancestor <= 0 {
		return false
	}
	seen := map[int]struct{}{}
	for current := pid; current > 0; current = ppids[current] {
		if current == ancestor {
			return true
		}
		if _, ok := seen[current]; ok {
			return false
		}
		seen[current] = struct{}{}
		next, ok := ppids[current]
		if !ok || next == current {
			return false
		}
	}
	return false
}

func topProcessPIDs(processes []Process, limit int) []int {
	if limit <= 0 {
		limit = DefaultCPUCWDLimit
	}
	sorted := append([]Process(nil), processes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := sorted[i], sorted[j]
		if left.CPU != right.CPU {
			return left.CPU > right.CPU
		}
		return left.PID < right.PID
	})
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	out := make([]int, 0, len(sorted))
	seen := map[int]struct{}{}
	for _, process := range sorted {
		if process.PID <= 0 {
			continue
		}
		if _, ok := seen[process.PID]; ok {
			continue
		}
		seen[process.PID] = struct{}{}
		out = append(out, process.PID)
	}
	return out
}

func (opts CPUScanOptions) limit() int {
	if opts.Limit <= 0 {
		return DefaultCPUTopLimit
	}
	return opts.Limit
}

func (opts CPUScanOptions) cwdLimit() int {
	if opts.CWDLimit <= 0 {
		return DefaultCPUCWDLimit
	}
	return opts.CWDLimit
}

func (opts CPUScanOptions) logicalCPUs() int {
	if opts.LogicalCPUs > 0 {
		return opts.LogicalCPUs
	}
	return runtime.NumCPU()
}

func cleanProjectPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

func deepestProjectForPath(path string, projects []string) (string, bool) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return "", false
	}
	for _, project := range projects {
		if pathWithin(path, project) {
			return project, true
		}
	}
	return "", false
}

func pathWithin(path, root string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))
	if path == "" || root == "" || path == "." || root == "." {
		return false
	}
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func currentProcesses(ctx context.Context) ([]Process, error) {
	out, err := exec.CommandContext(ctx, "ps", "-Ao", "pid=,ppid=,pgid=,stat=,pcpu=,pmem=,etime=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	return parsePSOutput(string(out)), nil
}

func parsePSOutput(output string) []Process {
	processes := []Process{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		pgid, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		cpu, err := strconv.ParseFloat(fields[4], 64)
		if err != nil {
			continue
		}
		mem, err := strconv.ParseFloat(fields[5], 64)
		if err != nil {
			continue
		}
		command := ""
		if len(fields) > 7 {
			command = strings.Join(fields[7:], " ")
		}
		processes = append(processes, Process{
			PID:     pid,
			PPID:    ppid,
			PGID:    pgid,
			Stat:    fields[3],
			CPU:     cpu,
			Mem:     mem,
			Elapsed: fields[6],
			Command: command,
		})
	}
	return processes
}

func currentProcessCWDs(ctx context.Context) (map[int]string, error) {
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-Fpcn", "-d", "cwd").Output()
	if err != nil {
		return nil, fmt.Errorf("list process cwd: %w", err)
	}
	return parseLsofCWDOutput(string(out)), nil
}

func currentProcessCWDsForPIDs(ctx context.Context, pids []int) (map[int]string, error) {
	pidList := joinPositiveInts(pids)
	if pidList == "" {
		return map[int]string{}, nil
	}
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-Fpcn", "-a", "-d", "cwd", "-p", pidList).Output()
	if err != nil {
		if len(out) > 0 {
			return parseLsofCWDOutput(string(out)), nil
		}
		return nil, fmt.Errorf("list process cwd for pids: %w", err)
	}
	return parseLsofCWDOutput(string(out)), nil
}

func parseLsofCWDOutput(output string) map[int]string {
	cwds := map[int]string{}
	currentPID := 0
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(strings.TrimSpace(line[1:]))
			if err != nil {
				currentPID = 0
				continue
			}
			currentPID = pid
		case 'n':
			if currentPID > 0 {
				cwds[currentPID] = strings.TrimSpace(line[1:])
			}
		}
	}
	return cwds
}

func joinPositiveInts(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	seen := map[int]struct{}{}
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
}

func currentListeningPorts(ctx context.Context) (map[int][]int, error) {
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn").Output()
	if err != nil {
		return nil, fmt.Errorf("list listening tcp ports: %w", err)
	}
	return parseLsofPortsOutput(string(out)), nil
}

func parseLsofPortsOutput(output string) map[int][]int {
	portsByPID := map[int][]int{}
	currentPID := 0
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(strings.TrimSpace(line[1:]))
			if err != nil {
				currentPID = 0
				continue
			}
			currentPID = pid
		case 'n':
			if currentPID == 0 {
				continue
			}
			port, ok := parseLsofPort(line[1:])
			if !ok {
				continue
			}
			portsByPID[currentPID] = append(portsByPID[currentPID], port)
		}
	}
	for pid, ports := range portsByPID {
		portsByPID[pid] = dedupeSortedInts(ports)
	}
	return portsByPID
}

func parseLsofPort(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	lastColon := strings.LastIndex(value, ":")
	if lastColon < 0 || lastColon == len(value)-1 {
		return 0, false
	}
	portPart := value[lastColon+1:]
	for i, r := range portPart {
		if r < '0' || r > '9' {
			portPart = portPart[:i]
			break
		}
	}
	if portPart == "" {
		return 0, false
	}
	port, err := strconv.Atoi(portPart)
	if err != nil || port <= 0 {
		return 0, false
	}
	return port, true
}

func dedupeSortedInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	sort.Ints(values)
	out := values[:0]
	last := -1
	for _, value := range values {
		if len(out) > 0 && value == last {
			continue
		}
		out = append(out, value)
		last = value
	}
	return append([]int(nil), out...)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
