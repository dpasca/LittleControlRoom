package codexapp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"lcroom/internal/browserctl"
)

const embeddedHelperDirName = "embedded-helpers"

var durableLCRCLIExecutableCache = struct {
	sync.Mutex
	paths map[string]string
}{
	paths: make(map[string]string),
}

// durableLCRCLIExecutablePath pins executables produced by go run before their
// paths are handed to a long-lived embedded provider. Go build cache cleanup
// may unlink the original executable while Claude Code still needs to start an
// MCP server or safety hook from that path.
func durableLCRCLIExecutablePath(sourcePath, dataDir string) (string, error) {
	sourcePath = filepath.Clean(strings.TrimSpace(sourcePath))
	if sourcePath == "" || sourcePath == "." {
		return "", errors.New("Little Control Room executable path is empty")
	}
	if !isDisposableGoBuildExecutable(sourcePath) {
		return sourcePath, nil
	}

	dataDir = browserctl.EffectiveDataDir(dataDir)
	cacheKey := sourcePath + "\x00" + dataDir

	durableLCRCLIExecutableCache.Lock()
	defer durableLCRCLIExecutableCache.Unlock()

	if pinnedPath := durableLCRCLIExecutableCache.paths[cacheKey]; pinnedPath != "" {
		if info, err := os.Stat(pinnedPath); err == nil && info.Mode().IsRegular() {
			return pinnedPath, nil
		}
		delete(durableLCRCLIExecutableCache.paths, cacheKey)
	}

	pinnedPath, err := pinLCRCLIExecutable(sourcePath, dataDir)
	if err != nil {
		return "", err
	}
	durableLCRCLIExecutableCache.paths[cacheKey] = pinnedPath
	return pinnedPath, nil
}

func isDisposableGoBuildExecutable(path string) bool {
	userCacheDir, _ := os.UserCacheDir()
	return isDisposableGoBuildExecutableWithin(
		path,
		os.TempDir(),
		userCacheDir,
		strings.TrimSpace(os.Getenv("GOCACHE")),
	)
}

func isDisposableGoBuildExecutableWithin(path, tempDir, userCacheDir, goCacheDir string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return false
	}

	if goCacheDir != "" && pathWithinDirectory(path, goCacheDir) {
		return true
	}
	if userCacheDir != "" && pathWithinDirectory(path, filepath.Join(userCacheDir, "go-build")) {
		return true
	}
	if tempDir == "" || !pathWithinDirectory(path, tempDir) {
		return false
	}

	relative, err := filepath.Rel(filepath.Clean(tempDir), path)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "go-build" || strings.HasPrefix(part, "go-build") {
			return true
		}
	}
	return false
}

func pathWithinDirectory(path, root string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))
	if path == "" || path == "." || root == "" || root == "." {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func pinLCRCLIExecutable(sourcePath, dataDir string) (string, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", fmt.Errorf("open disposable Little Control Room executable: %w", err)
	}
	defer source.Close()

	sourceInfo, err := source.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect disposable Little Control Room executable: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return "", fmt.Errorf("Little Control Room executable is not a regular file: %s", sourcePath)
	}
	if runtime.GOOS != "windows" && sourceInfo.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("Little Control Room executable is not executable: %s", sourcePath)
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, source); err != nil {
		return "", fmt.Errorf("hash disposable Little Control Room executable: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))

	helperDir := filepath.Join(dataDir, embeddedHelperDirName)
	if err := os.MkdirAll(helperDir, 0o700); err != nil {
		return "", fmt.Errorf("create embedded helper directory: %w", err)
	}
	if err := os.Chmod(helperDir, 0o700); err != nil && runtime.GOOS != "windows" {
		return "", fmt.Errorf("secure embedded helper directory: %w", err)
	}

	extension := filepath.Ext(sourcePath)
	pinnedPath := filepath.Join(helperDir, "lcroom-"+digest+extension)
	matches, err := pinnedExecutableMatches(pinnedPath, digest)
	if err != nil {
		return "", err
	}
	if matches {
		return pinnedPath, nil
	}

	// A hard link is both fast and sufficient: removing the Go cache entry no
	// longer removes the inode. Cross-device data directories fall back to a
	// byte-for-byte copy from the already-open source file.
	if err := os.Link(sourcePath, pinnedPath); err == nil {
		matches, matchErr := pinnedExecutableMatches(pinnedPath, digest)
		if matchErr == nil && matches {
			return pinnedPath, nil
		}
		_ = os.Remove(pinnedPath)
	} else if errors.Is(err, os.ErrExist) {
		matches, matchErr := pinnedExecutableMatches(pinnedPath, digest)
		if matchErr == nil && matches {
			return pinnedPath, nil
		}
	}
	if _, seekErr := source.Seek(0, io.SeekStart); seekErr != nil {
		return "", fmt.Errorf("rewind disposable Little Control Room executable: %w", seekErr)
	}

	temp, err := os.CreateTemp(helperDir, ".lcroom-helper-*"+extension)
	if err != nil {
		return "", fmt.Errorf("create embedded helper copy: %w", err)
	}
	tempPath := temp.Name()
	cleanupTemp := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if err := temp.Chmod(0o700); err != nil {
		cleanupTemp()
		return "", fmt.Errorf("make embedded helper executable: %w", err)
	}
	if _, err := io.Copy(temp, source); err != nil {
		cleanupTemp()
		return "", fmt.Errorf("copy embedded helper executable: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanupTemp()
		return "", fmt.Errorf("sync embedded helper executable: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("close embedded helper executable: %w", err)
	}
	if err := os.Rename(tempPath, pinnedPath); err != nil {
		_ = os.Remove(tempPath)
		matches, matchErr := pinnedExecutableMatches(pinnedPath, digest)
		if matchErr == nil && matches {
			return pinnedPath, nil
		}
		return "", fmt.Errorf("install embedded helper executable: %w", err)
	}
	return pinnedPath, nil
}

func pinnedExecutableMatches(path, expectedDigest string) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open pinned Little Control Room executable: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("inspect pinned Little Control Room executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("pinned Little Control Room executable is not a regular file: %s", path)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false, fmt.Errorf("hash pinned Little Control Room executable: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)) == expectedDigest, nil
}
