package codexapp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDisposableGoBuildExecutableDetection(t *testing.T) {
	tempDir := filepath.Join(string(filepath.Separator), "tmp")
	userCacheDir := filepath.Join(string(filepath.Separator), "users", "demo", "cache")
	goCacheDir := filepath.Join(string(filepath.Separator), "custom", "go-cache")

	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "macOS Go cache",
			path: filepath.Join(userCacheDir, "go-build", "69", "hash-d", "lcroom"),
			want: true,
		},
		{
			name: "temporary go run build",
			path: filepath.Join(tempDir, "go-build12345", "b001", "exe", "lcroom"),
			want: true,
		},
		{
			name: "custom Go cache",
			path: filepath.Join(goCacheDir, "ab", "hash-d", "lcroom"),
			want: true,
		},
		{
			name: "installed executable",
			path: filepath.Join(string(filepath.Separator), "usr", "local", "bin", "lcroom"),
			want: false,
		},
		{
			name: "unrelated cache executable",
			path: filepath.Join(userCacheDir, "other-app", "lcroom"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDisposableGoBuildExecutableWithin(tt.path, tempDir, userCacheDir, goCacheDir)
			if got != tt.want {
				t.Fatalf("isDisposableGoBuildExecutableWithin(%q) = %t, want %t", tt.path, got, tt.want)
			}
		})
	}
}

func TestDurableLCRCLIExecutablePathPinsDisposableBinary(t *testing.T) {
	tempRoot := t.TempDir()
	sourceDir := filepath.Join(tempRoot, "go-build12345", "b001", "exe")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "lcroom")
	sourceContent := []byte("#!/bin/sh\nexit 0\n")
	if runtime.GOOS == "windows" {
		sourcePath += ".exe"
		sourceContent = []byte("test executable")
	}
	if err := os.WriteFile(sourcePath, sourceContent, 0o700); err != nil {
		t.Fatal(err)
	}

	dataDir := filepath.Join(tempRoot, "lcr-data")
	pinnedPath, err := durableLCRCLIExecutablePath(sourcePath, dataDir)
	if err != nil {
		t.Fatalf("durableLCRCLIExecutablePath() error = %v", err)
	}
	if pinnedPath == sourcePath {
		t.Fatalf("durableLCRCLIExecutablePath() = source path %q, want pinned path", pinnedPath)
	}
	if !strings.HasPrefix(pinnedPath, filepath.Join(dataDir, embeddedHelperDirName)+string(filepath.Separator)) {
		t.Fatalf("pinned path = %q, want path below embedded helper directory", pinnedPath)
	}
	if runtime.GOOS != "windows" {
		sourceInfo, sourceErr := os.Stat(sourcePath)
		pinnedInfo, pinnedErr := os.Stat(pinnedPath)
		if sourceErr != nil || pinnedErr != nil {
			t.Fatalf("stat source/pinned executable: source=%v pinned=%v", sourceErr, pinnedErr)
		}
		if !os.SameFile(sourceInfo, pinnedInfo) {
			t.Fatal("pinned executable is not a hard link to the disposable source")
		}
	}

	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("remove disposable source: %v", err)
	}
	got, err := os.ReadFile(pinnedPath)
	if err != nil {
		t.Fatalf("read pinned executable after source removal: %v", err)
	}
	if string(got) != string(sourceContent) {
		t.Fatalf("pinned executable = %q, want %q", got, sourceContent)
	}
	if runtime.GOOS != "windows" {
		if err := exec.Command(pinnedPath).Run(); err != nil {
			t.Fatalf("execute pinned helper after source removal: %v", err)
		}
	}

	secondPath, err := durableLCRCLIExecutablePath(sourcePath, dataDir)
	if err != nil {
		t.Fatalf("second durableLCRCLIExecutablePath() error = %v", err)
	}
	if secondPath != pinnedPath {
		t.Fatalf("second pinned path = %q, want %q", secondPath, pinnedPath)
	}
}

func TestDurableLCRCLIExecutablePathKeepsInstalledBinaryPath(t *testing.T) {
	sourcePath := filepath.Join(string(filepath.Separator), "usr", "local", "bin", "lcroom")
	got, err := durableLCRCLIExecutablePath(sourcePath, t.TempDir())
	if err != nil {
		t.Fatalf("durableLCRCLIExecutablePath() error = %v", err)
	}
	if got != sourcePath {
		t.Fatalf("durableLCRCLIExecutablePath() = %q, want %q", got, sourcePath)
	}
}
