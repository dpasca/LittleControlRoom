package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	GitHubDistribution        = "github"
	DefaultRepository         = "dpasca/LittleControlRoom"
	defaultAPIBaseURL         = "https://api.github.com"
	defaultCheckInterval      = 24 * time.Hour
	defaultFailedCheckBackoff = time.Hour
	stateVersion              = 1
	maxReleaseResponseBytes   = 4 << 20
	maxReleaseNotesBytes      = 64 << 10
	updateStateDirName        = "updates"
	updateStateFileName       = "state.json"
)

// Asset is the release metadata needed to download and authenticate one file.
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
	Digest      string `json:"digest,omitempty"`
}

// Release describes a stable GitHub release that can update this platform.
type Release struct {
	Tag         string    `json:"tag"`
	Version     string    `json:"version"`
	Name        string    `json:"name,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	HTMLURL     string    `json:"html_url,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
	Archive     Asset     `json:"archive"`
	Checksums   Asset     `json:"checksums"`
}

// CheckResult reports updater eligibility and any newer stable release. A
// throttled automatic check can return a cached release with Checked false.
type CheckResult struct {
	Supported      bool
	Reason         string
	CurrentVersion string
	InstallPath    string
	Checked        bool
	Release        *Release
}

// InstallResult describes an update committed to the executable directory.
type InstallResult struct {
	Version    string
	InstallDir string
	Warnings   []string
}

type Options struct {
	Repository         string
	APIBaseURL         string
	DataDir            string
	CurrentVersion     string
	Distribution       string
	ExecutablePath     string
	GOOS               string
	GOARCH             string
	HTTPClient         *http.Client
	Now                func() time.Time
	CheckInterval      time.Duration
	FailedCheckBackoff time.Duration
	AllowInsecureHTTP  bool
	AutomaticDisabled  bool
	VerifyBinaries     func(context.Context, map[string]string) error
}

// Manager owns the UI-neutral GitHub release check, staging, verification, and
// rollback-protected binary replacement lifecycle.
type Manager struct {
	repository         string
	apiBaseURL         string
	dataDir            string
	currentVersion     string
	distribution       string
	executablePath     string
	executablePathErr  error
	goos               string
	goarch             string
	httpClient         *http.Client
	now                func() time.Time
	checkInterval      time.Duration
	failedCheckBackoff time.Duration
	allowInsecureHTTP  bool
	automaticDisabled  bool
	verifyBinaries     func(context.Context, map[string]string) error
}

type persistedState struct {
	Version       int       `json:"version"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt time.Time `json:"last_success_at,omitempty"`
	NextCheckAt   time.Time `json:"next_check_at,omitempty"`
	ETag          string    `json:"etag,omitempty"`
	Release       *Release  `json:"release,omitempty"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

func New(options Options) *Manager {
	repository := strings.TrimSpace(options.Repository)
	if repository == "" {
		repository = DefaultRepository
	}
	apiBaseURL := strings.TrimRight(strings.TrimSpace(options.APIBaseURL), "/")
	if apiBaseURL == "" {
		apiBaseURL = defaultAPIBaseURL
	}
	goos := strings.TrimSpace(options.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := strings.TrimSpace(options.GOARCH)
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	executablePath := strings.TrimSpace(options.ExecutablePath)
	var executablePathErr error
	if executablePath == "" {
		executablePath, executablePathErr = os.Executable()
	}
	if executablePathErr == nil && executablePath != "" {
		executablePath, executablePathErr = filepath.Abs(executablePath)
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	priorRedirectCheck := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateTransportURL(req.URL.String(), options.AllowInsecureHTTP); err != nil {
			return err
		}
		if priorRedirectCheck != nil {
			return priorRedirectCheck(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	client = &clientCopy
	now := options.Now
	if now == nil {
		now = time.Now
	}
	checkInterval := options.CheckInterval
	if checkInterval <= 0 {
		checkInterval = defaultCheckInterval
	}
	failedCheckBackoff := options.FailedCheckBackoff
	if failedCheckBackoff <= 0 {
		failedCheckBackoff = defaultFailedCheckBackoff
	}
	verifyBinaries := options.VerifyBinaries
	if verifyBinaries == nil {
		verifyBinaries = verifyPlatformBinaries
	}
	automaticDisabled := options.AutomaticDisabled
	if !automaticDisabled {
		automaticDisabled = envBool("LCR_DISABLE_UPDATE_CHECKS")
	}
	return &Manager{
		repository:         repository,
		apiBaseURL:         apiBaseURL,
		dataDir:            filepath.Clean(strings.TrimSpace(options.DataDir)),
		currentVersion:     strings.TrimSpace(options.CurrentVersion),
		distribution:       strings.ToLower(strings.TrimSpace(options.Distribution)),
		executablePath:     executablePath,
		executablePathErr:  executablePathErr,
		goos:               goos,
		goarch:             goarch,
		httpClient:         client,
		now:                now,
		checkInterval:      checkInterval,
		failedCheckBackoff: failedCheckBackoff,
		allowInsecureHTTP:  options.AllowInsecureHTTP,
		automaticDisabled:  automaticDisabled,
		verifyBinaries:     verifyBinaries,
	}
}

func envBool(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

// Check looks up GitHub's latest stable release. Automatic checks are cached
// and throttled; force bypasses the interval and any automatic-check opt-out.
func (m *Manager) Check(ctx context.Context, force bool) (CheckResult, error) {
	result := m.eligibility()
	if !result.Supported {
		return result, nil
	}
	if !force && m.automaticDisabled {
		result.Reason = "Automatic update checks are disabled by LCR_DISABLE_UPDATE_CHECKS."
		return result, nil
	}

	state, _ := m.loadState()
	now := m.now().UTC()
	if !force && !state.NextCheckAt.IsZero() && now.Before(state.NextCheckAt) {
		result.Release = m.newerRelease(state.Release)
		return result, nil
	}

	state.Version = stateVersion
	state.LastAttemptAt = now
	state.NextCheckAt = now.Add(m.failedCheckBackoff)
	release, etag, notModified, err := m.fetchLatestRelease(ctx, state.ETag)
	result.Checked = true
	if err != nil {
		result.Release = m.newerRelease(state.Release)
		return result, errors.Join(err, m.saveState(state))
	}
	if notModified {
		if state.Release == nil {
			return result, fmt.Errorf("GitHub returned not modified without a cached release")
		}
	} else {
		state.Release = release
	}
	state.ETag = etag
	state.LastSuccessAt = now
	state.NextCheckAt = now.Add(m.checkInterval)
	result.Release = m.newerRelease(state.Release)
	if err := m.saveState(state); err != nil {
		return result, err
	}
	return result, nil
}

func (m *Manager) eligibility() CheckResult {
	result := CheckResult{
		CurrentVersion: m.currentVersion,
		InstallPath:    m.executablePath,
	}
	if m.distribution != GitHubDistribution {
		if m.distribution == "" || m.distribution == "source" {
			result.Reason = "This source/development build is not managed by the GitHub updater."
		} else {
			result.Reason = fmt.Sprintf("This %s build is managed by its package distributor, not the GitHub updater.", m.distribution)
		}
		return result
	}
	current := canonicalVersion(m.currentVersion)
	if current == "" {
		result.Reason = fmt.Sprintf("The current build version %q is not a release version.", m.currentVersion)
		return result
	}
	result.CurrentVersion = current
	if m.goos != "darwin" && m.goos != "linux" {
		result.Reason = fmt.Sprintf("Automatic updates are not available on %s.", m.goos)
		return result
	}
	if m.goarch != "amd64" && m.goarch != "arm64" {
		result.Reason = fmt.Sprintf("Automatic updates are not available for %s.", m.goarch)
		return result
	}
	if strings.TrimSpace(m.dataDir) == "" || m.dataDir == "." {
		result.Reason = "The application data directory is not configured."
		return result
	}
	if m.executablePathErr != nil {
		result.Reason = "The running executable path could not be resolved: " + m.executablePathErr.Error()
		return result
	}
	if filepath.Base(m.executablePath) != "lcroom" {
		result.Reason = fmt.Sprintf("The running executable is named %q; self-update only replaces an lcroom binary.", filepath.Base(m.executablePath))
		return result
	}
	if info, err := os.Lstat(m.executablePath); err != nil {
		result.Reason = "The running executable could not be inspected: " + err.Error()
		return result
	} else if info.Mode()&os.ModeSymlink != 0 {
		result.Reason = "The running lcroom executable is a symbolic link; update its installation through the link owner."
		return result
	} else if !info.Mode().IsRegular() {
		result.Reason = "The running lcroom path is not a regular file."
		return result
	}
	result.Supported = true
	return result
}

func canonicalVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "v") {
		value = "v" + value
	}
	if !semver.IsValid(value) {
		return ""
	}
	return semver.Canonical(value)
}

func (m *Manager) newerRelease(release *Release) *Release {
	if release == nil {
		return nil
	}
	current := canonicalVersion(m.currentVersion)
	latest := canonicalVersion(release.Version)
	if current == "" || latest == "" || semver.Compare(latest, current) <= 0 {
		return nil
	}
	copy := *release
	return &copy
}

func (m *Manager) fetchLatestRelease(ctx context.Context, etag string) (*Release, string, bool, error) {
	owner, repo, err := splitRepository(m.repository)
	if err != nil {
		return nil, "", false, err
	}
	endpoint := m.apiBaseURL + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/latest"
	if err := m.validateURL(endpoint); err != nil {
		return nil, "", false, fmt.Errorf("GitHub release endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("create GitHub release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Little-Control-Room/"+strings.TrimPrefix(canonicalVersion(m.currentVersion), "v"))
	if strings.TrimSpace(etag) != "" {
		req.Header.Set("If-None-Match", strings.TrimSpace(etag))
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("check GitHub release: %w", err)
	}
	defer resp.Body.Close()
	if err := m.validateURL(resp.Request.URL.String()); err != nil {
		return nil, "", false, fmt.Errorf("GitHub release redirect: %w", err)
	}
	responseETag := strings.TrimSpace(resp.Header.Get("ETag"))
	if resp.StatusCode == http.StatusNotModified {
		if responseETag == "" {
			responseETag = strings.TrimSpace(etag)
		}
		return nil, responseETag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, "", false, fmt.Errorf("check GitHub release: HTTP %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseResponseBytes+1))
	var raw githubRelease
	if err := decoder.Decode(&raw); err != nil {
		return nil, "", false, fmt.Errorf("decode GitHub release: %w", err)
	}
	release, err := m.releaseFromGitHub(raw)
	if err != nil {
		return nil, "", false, err
	}
	return release, responseETag, false, nil
}

func splitRepository(repository string) (string, string, error) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(repository), "/"), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("GitHub repository must use owner/name form")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

func (m *Manager) releaseFromGitHub(raw githubRelease) (*Release, error) {
	if raw.Draft || raw.Prerelease {
		return nil, fmt.Errorf("GitHub latest release is not a published stable release")
	}
	version := canonicalVersion(raw.TagName)
	if version == "" {
		return nil, fmt.Errorf("GitHub release tag %q is not semantic versioning", raw.TagName)
	}
	archiveName, err := platformArchiveName(m.goos, m.goarch)
	if err != nil {
		return nil, err
	}
	archive, ok := findGitHubAsset(raw.Assets, archiveName)
	if !ok {
		return nil, fmt.Errorf("GitHub release %s does not contain %s", raw.TagName, archiveName)
	}
	checksums, ok := findGitHubAsset(raw.Assets, "checksums.txt")
	if !ok {
		return nil, fmt.Errorf("GitHub release %s does not contain checksums.txt", raw.TagName)
	}
	for _, asset := range []Asset{archive, checksums} {
		if err := m.validateURL(asset.DownloadURL); err != nil {
			return nil, fmt.Errorf("release asset %s: %w", asset.Name, err)
		}
		if _, err := parseSHA256Digest(asset.Digest); err != nil {
			return nil, fmt.Errorf("release asset %s is missing a valid GitHub SHA-256 digest: %w", asset.Name, err)
		}
	}
	notes := raw.Body
	if len(notes) > maxReleaseNotesBytes {
		notes = notes[:maxReleaseNotesBytes]
	}
	return &Release{
		Tag:         strings.TrimSpace(raw.TagName),
		Version:     version,
		Name:        strings.TrimSpace(raw.Name),
		Notes:       notes,
		HTMLURL:     strings.TrimSpace(raw.HTMLURL),
		PublishedAt: raw.PublishedAt,
		Archive:     archive,
		Checksums:   checksums,
	}, nil
}

func findGitHubAsset(assets []githubAsset, name string) (Asset, bool) {
	for _, asset := range assets {
		if asset.Name != name || strings.TrimSpace(asset.BrowserDownloadURL) == "" || asset.Size <= 0 {
			continue
		}
		return Asset{
			Name:        asset.Name,
			DownloadURL: strings.TrimSpace(asset.BrowserDownloadURL),
			Size:        asset.Size,
			Digest:      strings.TrimSpace(asset.Digest),
		}, true
	}
	return Asset{}, false
}

func platformArchiveName(goos, goarch string) (string, error) {
	osName := ""
	extension := ""
	switch goos {
	case "darwin":
		osName = "Darwin"
		extension = ".zip"
	case "linux":
		osName = "Linux"
		extension = ".tar.gz"
	default:
		return "", fmt.Errorf("unsupported update operating system %s", goos)
	}
	archName := ""
	switch goarch {
	case "amd64":
		archName = "x86_64"
	case "arm64":
		archName = "arm64"
	default:
		return "", fmt.Errorf("unsupported update architecture %s", goarch)
	}
	return "lcroom_" + osName + "_" + archName + extension, nil
}

func (m *Manager) validateURL(value string) error {
	return validateTransportURL(value, m.allowInsecureHTTP)
}

func validateTransportURL(value string, allowInsecureHTTP bool) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	if parsed.Host == "" {
		return fmt.Errorf("URL host is required")
	}
	if parsed.Scheme != "https" && !(allowInsecureHTTP && parsed.Scheme == "http") {
		return fmt.Errorf("URL must use HTTPS")
	}
	return nil
}

func (m *Manager) statePath() string {
	return filepath.Join(m.dataDir, updateStateDirName, updateStateFileName)
}

func (m *Manager) loadState() (persistedState, error) {
	raw, err := os.ReadFile(m.statePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persistedState{Version: stateVersion}, nil
		}
		return persistedState{}, fmt.Errorf("read update state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(raw, &state); err != nil {
		return persistedState{}, fmt.Errorf("decode update state: %w", err)
	}
	if state.Version != stateVersion {
		return persistedState{Version: stateVersion}, nil
	}
	return state, nil
}

func (m *Manager) saveState(state persistedState) error {
	state.Version = stateVersion
	path := m.statePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create update state directory: %w", err)
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode update state: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return fmt.Errorf("create update state temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure update state temp file: %w", err)
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write update state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync update state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close update state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install update state: %w", err)
	}
	return nil
}
