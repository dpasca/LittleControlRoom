package browserctl

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/brand"
)

type ManagedLaunchMode string

const (
	ManagedLaunchModeHeadless   ManagedLaunchMode = "headless"
	ManagedLaunchModeHeaded     ManagedLaunchMode = "headed"
	ManagedLaunchModeBackground ManagedLaunchMode = "background"
)

type ManagedPlaywrightPaths struct {
	DataDir      string
	RootDir      string
	SessionDir   string
	StatePath    string
	OutputDir    string
	ProfileDir   string
	ProfileKey   string
	SessionKey   string
	ProjectPath  string
	Provider     string
	LaunchMode   ManagedLaunchMode
	CreatedAtUTC time.Time
}

type ManagedPlaywrightState struct {
	SessionKey        string            `json:"session_key"`
	ProfileKey        string            `json:"profile_key"`
	Provider          string            `json:"provider"`
	ProjectPath       string            `json:"project_path"`
	LaunchMode        ManagedLaunchMode `json:"launch_mode"`
	Policy            Policy            `json:"policy"`
	MCPPID            int               `json:"mcp_pid"`
	BrowserPID        int               `json:"browser_pid"`
	BrowserAppPath    string            `json:"browser_app_path"`
	BrowserAppName    string            `json:"browser_app_name"`
	BrowserExecutable string            `json:"browser_executable"`
	Hidden            bool              `json:"hidden"`
	RevealSupported   bool              `json:"reveal_supported"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

type ManagedBrowserProcess struct {
	PID            int
	PPID           int
	Command        string
	Args           string
	AppPath        string
	AppName        string
	ExecutablePath string
}

type osProcessSnapshot struct {
	PID     int
	PPID    int
	Command string
	Args    string
}

func (m ManagedLaunchMode) Normalize() ManagedLaunchMode {
	switch strings.TrimSpace(strings.ToLower(string(m))) {
	case string(ManagedLaunchModeHeaded):
		return ManagedLaunchModeHeaded
	case string(ManagedLaunchModeBackground):
		return ManagedLaunchModeBackground
	default:
		return ManagedLaunchModeHeadless
	}
}

func DefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".", brand.DataDirName)
	}
	return filepath.Join(home, brand.DataDirName)
}

func EffectiveDataDir(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return DefaultDataDir()
	}
	return filepath.Clean(clean)
}

func NewManagedSessionKey() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + hex.EncodeToString(raw[:])
}

func ManagedProfileKey(policy Policy, provider, projectPath, resumeID, sessionKey string) string {
	policy = policy.Normalize()
	provider = strings.TrimSpace(strings.ToLower(provider))
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	resumeID = strings.TrimSpace(resumeID)
	sessionKey = strings.TrimSpace(sessionKey)

	scopeSeed := sessionKey
	if policy.IsolationScope == IsolationScopeProject {
		scopeSeed = projectPath
	} else if resumeID != "" {
		scopeSeed = resumeID
	}
	sum := sha256.Sum256([]byte(provider + "\x00" + projectPath + "\x00" + scopeSeed))
	return hex.EncodeToString(sum[:12])
}

func ManagedLaunchModeForPolicy(policy Policy) ManagedLaunchMode {
	normalized := policy.Normalize()
	if normalized.ManagementMode != ManagementModeManaged {
		return ManagedLaunchModeHeadless
	}
	if normalized.DefaultBrowserMode == BrowserModeHeaded {
		return ManagedLaunchModeHeaded
	}
	if runtime.GOOS == "darwin" && normalized.LoginMode == LoginModePromote {
		return ManagedLaunchModeBackground
	}
	return ManagedLaunchModeHeadless
}

func ManagedPlaywrightPathsFor(dataDir, provider, projectPath, sessionKey, profileKey string, mode ManagedLaunchMode) (ManagedPlaywrightPaths, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return ManagedPlaywrightPaths{}, fmt.Errorf("managed Playwright session key required")
	}
	profileKey = strings.TrimSpace(profileKey)
	if profileKey == "" {
		return ManagedPlaywrightPaths{}, fmt.Errorf("managed Playwright profile key required")
	}
	rootDir := filepath.Join(EffectiveDataDir(dataDir), "browser", "playwright")
	paths := ManagedPlaywrightPaths{
		DataDir:      EffectiveDataDir(dataDir),
		RootDir:      rootDir,
		SessionDir:   filepath.Join(rootDir, "sessions", sessionKey),
		StatePath:    filepath.Join(rootDir, "sessions", sessionKey, "state.json"),
		OutputDir:    filepath.Join(rootDir, "sessions", sessionKey, "output"),
		ProfileDir:   filepath.Join(rootDir, "profiles", profileKey),
		ProfileKey:   profileKey,
		SessionKey:   sessionKey,
		ProjectPath:  filepath.Clean(strings.TrimSpace(projectPath)),
		Provider:     strings.TrimSpace(strings.ToLower(provider)),
		LaunchMode:   mode.Normalize(),
		CreatedAtUTC: time.Now().UTC(),
	}
	for _, dir := range []string{paths.SessionDir, paths.OutputDir, paths.ProfileDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return ManagedPlaywrightPaths{}, err
		}
	}
	return paths, nil
}

func ReadManagedPlaywrightState(dataDir, sessionKey string) (ManagedPlaywrightState, error) {
	statePath := filepath.Join(EffectiveDataDir(dataDir), "browser", "playwright", "sessions", strings.TrimSpace(sessionKey), "state.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return ManagedPlaywrightState{}, err
	}
	var state ManagedPlaywrightState
	if err := json.Unmarshal(raw, &state); err != nil {
		return ManagedPlaywrightState{}, err
	}
	return state.Normalize(), nil
}

func WriteManagedPlaywrightState(paths ManagedPlaywrightPaths, state ManagedPlaywrightState) error {
	normalized := state.Normalize()
	if normalized.UpdatedAt.IsZero() {
		normalized.UpdatedAt = time.Now().UTC()
	}
	raw, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(paths.StatePath, raw, 0o644)
}

func (s ManagedPlaywrightState) Normalize() ManagedPlaywrightState {
	normalized := s
	normalized.SessionKey = strings.TrimSpace(normalized.SessionKey)
	normalized.ProfileKey = strings.TrimSpace(normalized.ProfileKey)
	normalized.Provider = strings.TrimSpace(strings.ToLower(normalized.Provider))
	normalized.ProjectPath = filepath.Clean(strings.TrimSpace(normalized.ProjectPath))
	normalized.BrowserAppPath = strings.TrimSpace(normalized.BrowserAppPath)
	normalized.BrowserAppName = strings.TrimSpace(normalized.BrowserAppName)
	normalized.BrowserExecutable = strings.TrimSpace(normalized.BrowserExecutable)
	normalized.LaunchMode = normalized.LaunchMode.Normalize()
	normalized.Policy = normalized.Policy.Normalize()
	normalized.RevealSupported = normalized.BrowserPID > 0 || normalized.BrowserAppPath != "" || normalized.BrowserAppName != ""
	return normalized
}

func RevealManagedPlaywrightState(state ManagedPlaywrightState) error {
	normalized := state.Normalize()
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("managed browser reveal is currently only supported on macOS")
	}
	switch {
	case normalized.BrowserPID > 0:
		return setMacApplicationProcessVisible(normalized.BrowserPID, true, true)
	case normalized.BrowserAppPath != "":
		return exec.Command("open", "-a", normalized.BrowserAppPath).Run()
	case normalized.BrowserAppName != "":
		return exec.Command("open", "-a", normalized.BrowserAppName).Run()
	default:
		return fmt.Errorf("managed browser window is not available for this session yet")
	}
}

func HideManagedBrowserProcess(pid int) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if pid <= 0 {
		return fmt.Errorf("managed browser pid required")
	}
	return setMacApplicationProcessVisible(pid, false, false)
}

func setMacApplicationProcessVisible(pid int, visible, frontmost bool) error {
	if pid <= 0 {
		return fmt.Errorf("managed browser pid required")
	}
	pidLiteral := strconv.Itoa(pid)
	visibleLiteral := "false"
	if visible {
		visibleLiteral = "true"
	}
	lines := []string{
		`tell application "System Events"`,
		`set targetProcess to first application process whose unix id is ` + pidLiteral,
		`set visible of targetProcess to ` + visibleLiteral,
	}
	if frontmost {
		lines = append(lines, `set frontmost of targetProcess to true`)
	}
	lines = append(lines, `end tell`)
	args := make([]string, 0, len(lines)*2)
	for _, line := range lines {
		args = append(args, "-e", line)
	}
	return exec.Command("osascript", args...).Run()
}

func DetectManagedBrowserProcess(rootPID int) (ManagedBrowserProcess, bool, error) {
	processes, err := processSnapshot()
	if err != nil {
		return ManagedBrowserProcess{}, false, err
	}
	descendants := descendantProcesses(processes, rootPID)
	if len(descendants) == 0 {
		return ManagedBrowserProcess{}, false, nil
	}

	bestScore := -1
	best := ManagedBrowserProcess{}
	for _, process := range descendants {
		candidate, ok := managedBrowserCandidate(process)
		if !ok {
			continue
		}
		score := managedBrowserCandidateScore(process, candidate)
		if score > bestScore {
			bestScore = score
			best = candidate
		}
	}
	if bestScore < 0 {
		return ManagedBrowserProcess{}, false, nil
	}
	return best, true, nil
}

func processSnapshot() ([]osProcessSnapshot, error) {
	cmd := exec.Command("ps", "-axo", "pid=,ppid=,comm=,args=")
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	out := make([]osProcessSnapshot, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 4 {
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
		command := fields[2]
		args := strings.Join(fields[3:], " ")
		out = append(out, osProcessSnapshot{
			PID:     pid,
			PPID:    ppid,
			Command: command,
			Args:    args,
		})
	}
	return out, nil
}

func descendantProcesses(processes []osProcessSnapshot, rootPID int) []osProcessSnapshot {
	if rootPID <= 0 || len(processes) == 0 {
		return nil
	}
	childrenByParent := make(map[int][]osProcessSnapshot)
	for _, process := range processes {
		childrenByParent[process.PPID] = append(childrenByParent[process.PPID], process)
	}
	var out []osProcessSnapshot
	queue := []int{rootPID}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range childrenByParent[parent] {
			out = append(out, child)
			queue = append(queue, child.PID)
		}
	}
	return out
}

func managedBrowserCandidate(process osProcessSnapshot) (ManagedBrowserProcess, bool) {
	args := strings.TrimSpace(process.Args)
	appPath := extractMacAppPath(args)
	processName := filepath.Base(strings.TrimSpace(process.Command))
	if appPath == "" && !strings.Contains(strings.ToLower(processName), "chrome") && !strings.Contains(strings.ToLower(processName), "chromium") {
		return ManagedBrowserProcess{}, false
	}
	return ManagedBrowserProcess{
		PID:            process.PID,
		PPID:           process.PPID,
		Command:        strings.TrimSpace(process.Command),
		Args:           args,
		AppPath:        appPath,
		AppName:        macAppName(appPath, processName),
		ExecutablePath: strings.TrimSpace(process.Command),
	}, true
}

func managedBrowserCandidateScore(process osProcessSnapshot, candidate ManagedBrowserProcess) int {
	score := 0
	argsLower := strings.ToLower(process.Args)
	nameLower := strings.ToLower(filepath.Base(process.Command))
	if candidate.AppPath != "" {
		score += 10
	}
	if !strings.Contains(argsLower, "--type=") {
		score += 5
	}
	if strings.Contains(nameLower, "helper") || strings.Contains(argsLower, "helper") {
		score -= 10
	}
	if strings.Contains(argsLower, "--remote-debugging-port=") {
		score += 4
	}
	return score
}

func extractMacAppPath(args string) string {
	start := strings.Index(args, ".app/Contents/MacOS/")
	if start < 0 {
		return ""
	}
	appEnd := start + len(".app")
	prefix := args[:appEnd]
	firstSlash := strings.Index(prefix, "/")
	if firstSlash < 0 {
		return ""
	}
	return prefix[firstSlash:]
}

func macAppName(appPath, fallback string) string {
	if strings.TrimSpace(appPath) == "" {
		return strings.TrimSpace(fallback)
	}
	base := filepath.Base(appPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
