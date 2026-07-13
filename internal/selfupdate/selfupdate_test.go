package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckCachesLatestStableReleaseAndUsesETag(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	executable := testExecutable(t, tempDir, "old lcroom")
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	var requests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("If-None-Match"); got == `"release-2"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"release-2"`)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "v1.1.0",
			"name":         "v1.1.0",
			"body":         "A useful update.",
			"html_url":     server.URL + "/release/v1.1.0",
			"draft":        false,
			"prerelease":   false,
			"published_at": now.Format(time.RFC3339),
			"assets": []map[string]any{
				{"name": "lcroom_Linux_x86_64.tar.gz", "browser_download_url": server.URL + "/archive", "size": 12, "digest": "sha256:" + strings.Repeat("0", 64)},
				{"name": "checksums.txt", "browser_download_url": server.URL + "/checksums", "size": 12, "digest": "sha256:" + strings.Repeat("1", 64)},
			},
		})
	}))
	defer server.Close()

	manager := New(Options{
		APIBaseURL:        server.URL,
		DataDir:           filepath.Join(tempDir, "data"),
		CurrentVersion:    "1.0.0",
		Distribution:      GitHubDistribution,
		ExecutablePath:    executable,
		GOOS:              "linux",
		GOARCH:            "amd64",
		AllowInsecureHTTP: true,
		Now:               func() time.Time { return now },
	})

	first, err := manager.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("first Check() error = %v", err)
	}
	if first.Release == nil || first.Release.Version != "v1.1.0" || !first.Checked {
		t.Fatalf("first Check() = %#v, want checked v1.1.0", first)
	}
	second, err := manager.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("cached Check() error = %v", err)
	}
	if second.Release == nil || second.Release.Version != "v1.1.0" || second.Checked {
		t.Fatalf("cached Check() = %#v, want cached v1.1.0", second)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests after cached check = %d, want 1", got)
	}
	forced, err := manager.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("forced Check() error = %v", err)
	}
	if forced.Release == nil || forced.Release.Version != "v1.1.0" || !forced.Checked {
		t.Fatalf("forced Check() = %#v, want revalidated v1.1.0", forced)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests after forced check = %d, want 2", got)
	}
}

func TestCheckSkipsSourceBuildWithoutNetwork(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	executable := testExecutable(t, tempDir, "old lcroom")
	manager := New(Options{
		APIBaseURL:        "http://127.0.0.1:1",
		DataDir:           filepath.Join(tempDir, "data"),
		CurrentVersion:    "v1.0.0",
		Distribution:      "source",
		ExecutablePath:    executable,
		GOOS:              "linux",
		GOARCH:            "amd64",
		AllowInsecureHTTP: true,
	})
	result, err := manager.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Supported || !strings.Contains(result.Reason, "source/development") {
		t.Fatalf("Check() = %#v, want unsupported source build", result)
	}
}

func TestCheckDoesNotOfferOlderRelease(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	executable := testExecutable(t, tempDir, "old lcroom")
	server := latestReleaseServer(t, "v1.9.0")
	defer server.Close()
	manager := New(Options{
		APIBaseURL:        server.URL,
		DataDir:           filepath.Join(tempDir, "data"),
		CurrentVersion:    "v2.0.0",
		Distribution:      GitHubDistribution,
		ExecutablePath:    executable,
		GOOS:              "linux",
		GOARCH:            "amd64",
		AllowInsecureHTTP: true,
	})
	result, err := manager.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Release != nil {
		t.Fatalf("Check() release = %#v, want no downgrade", result.Release)
	}
}

func TestFailedAutomaticCheckBacksOffButForcedCheckRetries(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	executable := testExecutable(t, tempDir, "old lcroom")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	manager := New(Options{
		APIBaseURL:        server.URL,
		DataDir:           filepath.Join(tempDir, "data"),
		CurrentVersion:    "v1.0.0",
		Distribution:      GitHubDistribution,
		ExecutablePath:    executable,
		GOOS:              "linux",
		GOARCH:            "amd64",
		AllowInsecureHTTP: true,
		Now:               func() time.Time { return time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC) },
	})
	if _, err := manager.Check(context.Background(), false); err == nil {
		t.Fatal("first Check() should report the server failure")
	}
	if _, err := manager.Check(context.Background(), false); err != nil {
		t.Fatalf("backed-off Check() error = %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests during backoff = %d, want 1", got)
	}
	if _, err := manager.Check(context.Background(), true); err == nil {
		t.Fatal("forced Check() should retry and report the server failure")
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests after forced retry = %d, want 2", got)
	}
}

func TestAutomaticDisableStillAllowsForcedCheck(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	executable := testExecutable(t, tempDir, "old lcroom")
	server := latestReleaseServer(t, "v1.1.0")
	defer server.Close()
	manager := New(Options{
		APIBaseURL:        server.URL,
		DataDir:           filepath.Join(tempDir, "data"),
		CurrentVersion:    "v1.0.0",
		Distribution:      GitHubDistribution,
		ExecutablePath:    executable,
		GOOS:              "linux",
		GOARCH:            "amd64",
		AllowInsecureHTTP: true,
		AutomaticDisabled: true,
	})
	automatic, err := manager.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("automatic Check() error = %v", err)
	}
	if automatic.Checked || automatic.Release != nil || !strings.Contains(automatic.Reason, "disabled") {
		t.Fatalf("automatic Check() = %#v, want disabled without network", automatic)
	}
	forced, err := manager.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("forced Check() error = %v", err)
	}
	if !forced.Checked || forced.Release == nil || forced.Release.Version != "v1.1.0" {
		t.Fatalf("forced Check() = %#v, want v1.1.0", forced)
	}
}

func TestCheckRejectsHTTPSRedirectToHTTP(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	executable := testExecutable(t, tempDir, "old lcroom")
	var insecureRequests atomic.Int32
	insecureTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		insecureRequests.Add(1)
		http.Error(w, "should not be reached", http.StatusInternalServerError)
	}))
	defer insecureTarget.Close()
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, insecureTarget.URL+"/release", http.StatusFound)
	}))
	defer api.Close()

	manager := New(Options{
		APIBaseURL:     api.URL,
		DataDir:        filepath.Join(tempDir, "data"),
		CurrentVersion: "v1.0.0",
		Distribution:   GitHubDistribution,
		ExecutablePath: executable,
		GOOS:           "linux",
		GOARCH:         "amd64",
		HTTPClient:     api.Client(),
	})
	_, err := manager.Check(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "URL must use HTTPS") {
		t.Fatalf("Check() error = %v, want HTTPS downgrade rejection", err)
	}
	if got := insecureRequests.Load(); got != 0 {
		t.Fatalf("insecure redirect requests = %d, want 0", got)
	}
}

func TestInstallDownloadsVerifiesAndReplacesBothBinaries(t *testing.T) {
	t.Parallel()
	archive := testTarGzip(t, map[string]string{
		"lcroom":  "new lcroom",
		"lcagent": "new lcagent",
	})
	archiveSHA := sha256Hex(archive)
	checksums := []byte(fmt.Sprintf("%s  lcroom_Linux_x86_64.tar.gz\n", archiveSHA))
	assets := map[string][]byte{"/archive": archive, "/checksums": checksums}
	server := assetServer(assets)
	defer server.Close()

	tempDir := t.TempDir()
	executable := testExecutable(t, tempDir, "old lcroom")
	agentPath := filepath.Join(filepath.Dir(executable), "lcagent")
	if err := os.WriteFile(agentPath, []byte("old lcagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := testInstallManager(tempDir, executable, server.Client())
	release := testRelease(server.URL, archive, checksums, archiveSHA)

	result, err := manager.Install(context.Background(), release)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if result.Version != "v1.1.0" || result.InstallDir != filepath.Dir(executable) {
		t.Fatalf("Install() result = %#v", result)
	}
	assertFileContent(t, executable, "new lcroom")
	assertFileContent(t, agentPath, "new lcagent")
	for _, path := range []string{executable, agentPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("%s mode = %o, want 755", path, info.Mode().Perm())
		}
	}
}

func TestInstallChecksumFailureLeavesCurrentBinariesUntouched(t *testing.T) {
	t.Parallel()
	archive := testTarGzip(t, map[string]string{
		"lcroom":  "new lcroom",
		"lcagent": "new lcagent",
	})
	archiveSHA := sha256Hex(archive)
	checksums := []byte(strings.Repeat("0", 64) + "  lcroom_Linux_x86_64.tar.gz\n")
	server := assetServer(map[string][]byte{"/archive": archive, "/checksums": checksums})
	defer server.Close()

	tempDir := t.TempDir()
	executable := testExecutable(t, tempDir, "old lcroom")
	agentPath := filepath.Join(filepath.Dir(executable), "lcagent")
	if err := os.WriteFile(agentPath, []byte("old lcagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := testInstallManager(tempDir, executable, server.Client())
	_, err := manager.Install(context.Background(), testRelease(server.URL, archive, checksums, archiveSHA))
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Install() error = %v, want checksum mismatch", err)
	}
	assertFileContent(t, executable, "old lcroom")
	assertFileContent(t, agentPath, "old lcagent")
}

func TestInstallPreflightsBothTargetsBeforeReplacingLcroom(t *testing.T) {
	t.Parallel()
	archive := testTarGzip(t, map[string]string{
		"lcroom":  "new lcroom",
		"lcagent": "new lcagent",
	})
	archiveSHA := sha256Hex(archive)
	checksums := []byte(fmt.Sprintf("%s  lcroom_Linux_x86_64.tar.gz\n", archiveSHA))
	server := assetServer(map[string][]byte{"/archive": archive, "/checksums": checksums})
	defer server.Close()

	tempDir := t.TempDir()
	executable := testExecutable(t, tempDir, "old lcroom")
	if err := os.Mkdir(filepath.Join(filepath.Dir(executable), "lcagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := testInstallManager(tempDir, executable, server.Client())
	_, err := manager.Install(context.Background(), testRelease(server.URL, archive, checksums, archiveSHA))
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Install() error = %v, want invalid lcagent target", err)
	}
	assertFileContent(t, executable, "old lcroom")
}

func latestReleaseServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag,
			"name":     tag,
			"assets": []map[string]any{
				{"name": "lcroom_Linux_x86_64.tar.gz", "browser_download_url": server.URL + "/archive", "size": 1, "digest": "sha256:" + strings.Repeat("0", 64)},
				{"name": "checksums.txt", "browser_download_url": server.URL + "/checksums", "size": 1, "digest": "sha256:" + strings.Repeat("1", 64)},
			},
		})
	}))
	return server
}

func testExecutable(t *testing.T, root, content string) string {
	t.Helper()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(binDir, "lcroom")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func testTarGzip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range releaseBinaryNames {
		content := []byte(files[name])
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func assetServer(assets map[string][]byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := assets[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
}

func testInstallManager(tempDir, executable string, client *http.Client) *Manager {
	return New(Options{
		DataDir:           filepath.Join(tempDir, "data"),
		CurrentVersion:    "v1.0.0",
		Distribution:      GitHubDistribution,
		ExecutablePath:    executable,
		GOOS:              "linux",
		GOARCH:            "amd64",
		HTTPClient:        client,
		AllowInsecureHTTP: true,
		VerifyBinaries:    func(context.Context, map[string]string) error { return nil },
	})
}

func testRelease(baseURL string, archive, checksums []byte, archiveSHA string) Release {
	return Release{
		Tag:     "v1.1.0",
		Version: "v1.1.0",
		Archive: Asset{
			Name:        "lcroom_Linux_x86_64.tar.gz",
			DownloadURL: baseURL + "/archive",
			Size:        int64(len(archive)),
			Digest:      "sha256:" + archiveSHA,
		},
		Checksums: Asset{
			Name:        "checksums.txt",
			DownloadURL: baseURL + "/checksums",
			Size:        int64(len(checksums)),
			Digest:      "sha256:" + sha256Hex(checksums),
		},
	}
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != want {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}
