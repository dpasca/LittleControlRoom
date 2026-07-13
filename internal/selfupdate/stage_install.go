package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	stageManifestVersion    = 1
	maxArchiveBytes         = 256 << 20
	maxChecksumsBytes       = 1 << 20
	maxExtractedBinaryBytes = 192 << 20
	expectedMacOSTeamID     = "69NH26W767"
	expectedMacOSAuthority  = "Developer ID Application: NEWTYPE K.K. (69NH26W767)"
)

// Install the companion first and the launcher last. If the process or host
// stops between the two atomic renames, the old lcroom can offer the update
// again instead of leaving a new launcher paired with an old companion.
var releaseBinaryNames = []string{"lcagent", "lcroom"}

type stagedUpdate struct {
	dir      string
	manifest stageManifest
}

type stageManifest struct {
	Version        int                   `json:"version"`
	ReleaseVersion string                `json:"release_version"`
	ArchiveName    string                `json:"archive_name"`
	ArchiveSHA256  string                `json:"archive_sha256"`
	Files          map[string]stagedFile `json:"files"`
}

type stagedFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type replacement struct {
	name        string
	target      string
	temp        string
	backup      string
	hadExisting bool
	committed   bool
}

// Install downloads, authenticates, stages, and commits both release binaries
// with rollback protection. Callers must obtain explicit user approval before
// calling it; the method itself performs no prompting.
func (m *Manager) Install(ctx context.Context, release Release) (InstallResult, error) {
	result := InstallResult{Version: release.Version}
	eligibility := m.eligibility()
	if !eligibility.Supported {
		return result, errors.New(eligibility.Reason)
	}
	if err := m.validateInstallRelease(release); err != nil {
		return result, err
	}
	stage, err := m.stageRelease(ctx, release)
	if err != nil {
		return result, err
	}
	result.InstallDir = filepath.Dir(m.executablePath)
	warnings, err := m.installStage(ctx, stage)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)
	if err := os.RemoveAll(stage.dir); err != nil {
		result.Warnings = append(result.Warnings, "could not remove staged update: "+err.Error())
	}
	return result, nil
}

func (m *Manager) validateInstallRelease(release Release) error {
	version := canonicalVersion(release.Version)
	if version == "" || version != canonicalVersion(release.Tag) {
		return fmt.Errorf("release version and tag do not identify the same semantic version")
	}
	current := canonicalVersion(m.currentVersion)
	if current == "" || m.newerRelease(&release) == nil {
		return fmt.Errorf("release %s is not newer than current version %s", version, current)
	}
	expectedArchive, err := platformArchiveName(m.goos, m.goarch)
	if err != nil {
		return err
	}
	if release.Archive.Name != expectedArchive {
		return fmt.Errorf("release archive is %q, want %q", release.Archive.Name, expectedArchive)
	}
	if release.Checksums.Name != "checksums.txt" {
		return fmt.Errorf("release checksum asset is %q, want checksums.txt", release.Checksums.Name)
	}
	for _, asset := range []Asset{release.Archive, release.Checksums} {
		if asset.Size <= 0 {
			return fmt.Errorf("release asset %s has invalid size %d", asset.Name, asset.Size)
		}
		if err := m.validateURL(asset.DownloadURL); err != nil {
			return fmt.Errorf("release asset %s: %w", asset.Name, err)
		}
		if _, err := parseSHA256Digest(asset.Digest); err != nil {
			return fmt.Errorf("release asset %s digest: %w", asset.Name, err)
		}
	}
	return nil
}

func (m *Manager) stageRelease(ctx context.Context, release Release) (stagedUpdate, error) {
	stageRoot := filepath.Join(m.dataDir, updateStateDirName, "staged")
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		return stagedUpdate{}, fmt.Errorf("create update staging directory: %w", err)
	}
	finalDir := filepath.Join(stageRoot, strings.TrimPrefix(release.Version, "v")+"-"+m.goos+"-"+m.goarch)
	if existing, err := readAndVerifyStage(finalDir, release.Version); err == nil {
		return existing, nil
	}
	if err := os.RemoveAll(finalDir); err != nil {
		return stagedUpdate{}, fmt.Errorf("clear invalid staged update: %w", err)
	}
	tempDir, err := os.MkdirTemp(stageRoot, ".prepare-*")
	if err != nil {
		return stagedUpdate{}, fmt.Errorf("create update staging temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	checksumsPath := filepath.Join(tempDir, release.Checksums.Name)
	checksumsSHA, err := m.downloadAsset(ctx, release.Checksums, checksumsPath, maxChecksumsBytes)
	if err != nil {
		return stagedUpdate{}, err
	}
	if err := verifyAssetDigest(release.Checksums, checksumsSHA); err != nil {
		return stagedUpdate{}, err
	}
	checksums, err := readChecksums(checksumsPath)
	if err != nil {
		return stagedUpdate{}, err
	}
	expectedArchiveSHA := strings.ToLower(strings.TrimSpace(checksums[release.Archive.Name]))
	if expectedArchiveSHA == "" {
		return stagedUpdate{}, fmt.Errorf("checksums.txt does not contain %s", release.Archive.Name)
	}
	if _, err := parseSHA256Digest("sha256:" + expectedArchiveSHA); err != nil {
		return stagedUpdate{}, fmt.Errorf("checksums.txt has an invalid digest for %s: %w", release.Archive.Name, err)
	}

	archivePath := filepath.Join(tempDir, release.Archive.Name)
	archiveSHA, err := m.downloadAsset(ctx, release.Archive, archivePath, maxArchiveBytes)
	if err != nil {
		return stagedUpdate{}, err
	}
	if !strings.EqualFold(archiveSHA, expectedArchiveSHA) {
		return stagedUpdate{}, fmt.Errorf("checksum mismatch for %s", release.Archive.Name)
	}
	if err := verifyAssetDigest(release.Archive, archiveSHA); err != nil {
		return stagedUpdate{}, err
	}

	paths, err := extractReleaseBinaries(archivePath, release.Archive.Name, tempDir)
	if err != nil {
		return stagedUpdate{}, err
	}
	if err := m.verifyBinaries(ctx, paths); err != nil {
		return stagedUpdate{}, fmt.Errorf("verify staged release binaries: %w", err)
	}
	manifest := stageManifest{
		Version:        stageManifestVersion,
		ReleaseVersion: release.Version,
		ArchiveName:    release.Archive.Name,
		ArchiveSHA256:  archiveSHA,
		Files:          make(map[string]stagedFile, len(releaseBinaryNames)),
	}
	for _, name := range releaseBinaryNames {
		digest, size, err := hashFile(paths[name])
		if err != nil {
			return stagedUpdate{}, fmt.Errorf("hash staged %s: %w", name, err)
		}
		manifest.Files[name] = stagedFile{SHA256: digest, Size: size}
	}
	if err := writeStageManifest(tempDir, manifest); err != nil {
		return stagedUpdate{}, err
	}
	_ = os.Remove(archivePath)
	_ = os.Remove(checksumsPath)
	if err := os.Rename(tempDir, finalDir); err != nil {
		return stagedUpdate{}, fmt.Errorf("commit staged update: %w", err)
	}
	return readAndVerifyStage(finalDir, release.Version)
}

func (m *Manager) downloadAsset(ctx context.Context, asset Asset, destination string, maxBytes int64) (string, error) {
	if asset.Size > maxBytes {
		return "", fmt.Errorf("release asset %s is too large (%d bytes)", asset.Name, asset.Size)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("create download request for %s: %w", asset.Name, err)
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "Little-Control-Room/"+strings.TrimPrefix(canonicalVersion(m.currentVersion), "v"))
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset.Name, err)
	}
	defer resp.Body.Close()
	if err := m.validateURL(resp.Request.URL.String()); err != nil {
		return "", fmt.Errorf("download %s redirect: %w", asset.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return "", fmt.Errorf("download %s: HTTP %s: %s", asset.Name, resp.Status, strings.TrimSpace(string(body)))
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create staged %s: %w", asset.Name, err)
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if written > maxBytes {
		return "", fmt.Errorf("release asset %s exceeds the download limit", asset.Name)
	}
	if written != asset.Size {
		return "", fmt.Errorf("release asset %s size mismatch: downloaded %d bytes, expected %d", asset.Name, written, asset.Size)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync staged %s: %w", asset.Name, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close staged %s: %w", asset.Name, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyAssetDigest(asset Asset, actual string) error {
	expected, err := parseSHA256Digest(asset.Digest)
	if err != nil {
		return fmt.Errorf("release asset %s digest: %w", asset.Name, err)
	}
	if expected != "" && !strings.EqualFold(expected, actual) {
		return fmt.Errorf("GitHub digest mismatch for %s", asset.Name)
	}
	return nil
}

func parseSHA256Digest(value string) (string, error) {
	value = strings.TrimSpace(value)
	algorithm, digest, ok := strings.Cut(value, ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(algorithm), "sha256") {
		return "", fmt.Errorf("expected sha256:<hex>")
	}
	digest = strings.ToLower(strings.TrimSpace(digest))
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("expected a 64-character SHA-256 hex digest")
	}
	return digest, nil
}

func readChecksums(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read checksums.txt: %w", err)
	}
	checksums := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if filepath.Base(name) != name || name == "." {
			continue
		}
		checksums[name] = strings.ToLower(fields[0])
	}
	return checksums, nil
}

func extractReleaseBinaries(archivePath, archiveName, destination string) (map[string]string, error) {
	switch {
	case strings.HasSuffix(archiveName, ".zip"):
		return extractZipBinaries(archivePath, destination)
	case strings.HasSuffix(archiveName, ".tar.gz"):
		return extractTarGzipBinaries(archivePath, destination)
	default:
		return nil, fmt.Errorf("unsupported release archive %s", archiveName)
	}
}

func extractZipBinaries(archivePath, destination string) (map[string]string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open release zip: %w", err)
	}
	defer reader.Close()
	paths := map[string]string{}
	for _, entry := range reader.File {
		name := cleanArchiveEntryName(entry.Name)
		if !isExpectedBinaryName(name) {
			continue
		}
		if _, exists := paths[name]; exists {
			return nil, fmt.Errorf("release archive contains duplicate %s", name)
		}
		if !entry.Mode().IsRegular() {
			return nil, fmt.Errorf("release archive %s is not a regular file", name)
		}
		source, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s in release archive: %w", name, err)
		}
		path, err := writeExtractedBinary(destination, name, source)
		_ = source.Close()
		if err != nil {
			return nil, err
		}
		paths[name] = path
	}
	return requireReleaseBinaries(paths)
}

func extractTarGzipBinaries(archivePath, destination string) (map[string]string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open release tarball: %w", err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("open release gzip stream: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	paths := map[string]string{}
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read release tarball: %w", err)
		}
		name := cleanArchiveEntryName(header.Name)
		if !isExpectedBinaryName(name) {
			continue
		}
		if _, exists := paths[name]; exists {
			return nil, fmt.Errorf("release archive contains duplicate %s", name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("release archive %s is not a regular file", name)
		}
		path, err := writeExtractedBinary(destination, name, tarReader)
		if err != nil {
			return nil, err
		}
		paths[name] = path
	}
	return requireReleaseBinaries(paths)
}

func cleanArchiveEntryName(value string) string {
	value = strings.TrimPrefix(strings.ReplaceAll(value, "\\", "/"), "./")
	if strings.Contains(value, "/") || value == "." || value == ".." {
		return ""
	}
	return value
}

func isExpectedBinaryName(name string) bool {
	for _, expected := range releaseBinaryNames {
		if name == expected {
			return true
		}
	}
	return false
}

func writeExtractedBinary(destination, name string, source io.Reader) (string, error) {
	path := filepath.Join(destination, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return "", fmt.Errorf("create staged %s binary: %w", name, err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, maxExtractedBinaryBytes+1))
	if copyErr == nil && written > maxExtractedBinaryBytes {
		copyErr = fmt.Errorf("extracted binary exceeds %d bytes", maxExtractedBinaryBytes)
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("extract release binary %s: %w", name, copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close staged release binary %s: %w", name, closeErr)
	}
	return path, nil
}

func requireReleaseBinaries(paths map[string]string) (map[string]string, error) {
	for _, name := range releaseBinaryNames {
		if strings.TrimSpace(paths[name]) == "" {
			return nil, fmt.Errorf("release archive does not contain %s", name)
		}
	}
	return paths, nil
}

func verifyPlatformBinaries(ctx context.Context, paths map[string]string) error {
	if runtimeGOOS() != "darwin" {
		return nil
	}
	for _, name := range releaseBinaryNames {
		path := paths[name]
		verify := exec.CommandContext(ctx, "codesign", "--verify", "--strict", "--verbose=2", path)
		if output, err := verify.CombinedOutput(); err != nil {
			return fmt.Errorf("%s does not have a valid strict macOS code signature: %s", name, strings.TrimSpace(string(output)))
		}
		details := exec.CommandContext(ctx, "codesign", "-dv", "--verbose=4", path)
		output, err := details.CombinedOutput()
		if err != nil {
			return fmt.Errorf("inspect %s macOS code signature: %w", name, err)
		}
		text := string(output)
		if !strings.Contains(text, "TeamIdentifier="+expectedMacOSTeamID) {
			return fmt.Errorf("%s is not signed by expected Apple Developer Team ID %s", name, expectedMacOSTeamID)
		}
		if !strings.Contains(text, "Authority="+expectedMacOSAuthority) {
			return fmt.Errorf("%s is not signed by %s", name, expectedMacOSAuthority)
		}
	}
	return nil
}

// runtimeGOOS is a variable-sized seam for platform tests without changing the
// public verifier callback shape.
var runtimeGOOS = func() string { return runtime.GOOS }

func writeStageManifest(dir string, manifest stageManifest) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode staged update manifest: %w", err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write staged update manifest: %w", err)
	}
	return nil
}

func readAndVerifyStage(dir, releaseVersion string) (stagedUpdate, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return stagedUpdate{}, err
	}
	var manifest stageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return stagedUpdate{}, err
	}
	if manifest.Version != stageManifestVersion || canonicalVersion(manifest.ReleaseVersion) != canonicalVersion(releaseVersion) {
		return stagedUpdate{}, fmt.Errorf("staged update manifest does not match release")
	}
	for _, name := range releaseBinaryNames {
		expected, ok := manifest.Files[name]
		if !ok {
			return stagedUpdate{}, fmt.Errorf("staged update manifest is missing %s", name)
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return stagedUpdate{}, fmt.Errorf("staged update %s is not a regular file", name)
		}
		actual, size, err := hashFile(path)
		if err != nil {
			return stagedUpdate{}, err
		}
		if size != expected.Size || !strings.EqualFold(actual, expected.SHA256) {
			return stagedUpdate{}, fmt.Errorf("staged update %s failed integrity verification", name)
		}
	}
	return stagedUpdate{dir: dir, manifest: manifest}, nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func (m *Manager) installStage(ctx context.Context, stage stagedUpdate) ([]string, error) {
	if _, err := readAndVerifyStage(stage.dir, stage.manifest.ReleaseVersion); err != nil {
		return nil, fmt.Errorf("verify staged update before install: %w", err)
	}
	targetDir := filepath.Dir(m.executablePath)
	if info, err := os.Stat(targetDir); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return nil, fmt.Errorf("inspect update target directory %s: %w", targetDir, err)
	}
	replacements := make([]replacement, 0, len(releaseBinaryNames))
	for _, name := range releaseBinaryNames {
		target := filepath.Join(targetDir, name)
		if name == "lcroom" {
			target = m.executablePath
		}
		hadExisting, err := inspectReplacementTarget(target)
		if err != nil {
			return nil, err
		}
		replacements = append(replacements, replacement{name: name, target: target, hadExisting: hadExisting})
	}
	defer func() {
		for _, item := range replacements {
			if item.temp != "" {
				_ = os.Remove(item.temp)
			}
		}
	}()

	preparedPaths := map[string]string{}
	for i := range replacements {
		item := &replacements[i]
		temp, err := os.CreateTemp(targetDir, "."+item.name+"-update-*")
		if err != nil {
			return nil, fmt.Errorf("prepare %s update in %s: %w", item.name, targetDir, err)
		}
		item.temp = temp.Name()
		if err := copyPreparedBinary(temp, filepath.Join(stage.dir, item.name), stage.manifest.Files[item.name]); err != nil {
			_ = temp.Close()
			return nil, fmt.Errorf("prepare %s replacement: %w", item.name, err)
		}
		preparedPaths[item.name] = item.temp
	}
	if err := m.verifyBinaries(ctx, preparedPaths); err != nil {
		return nil, fmt.Errorf("verify prepared release binaries: %w", err)
	}
	for i := range replacements {
		item := &replacements[i]
		if !item.hadExisting {
			continue
		}
		backup, err := reserveSiblingPath(targetDir, "."+item.name+"-previous-*")
		if err != nil {
			return nil, fmt.Errorf("reserve %s rollback path: %w", item.name, err)
		}
		item.backup = backup
	}

	for i := range replacements {
		item := &replacements[i]
		if item.hadExisting {
			if err := os.Rename(item.target, item.backup); err != nil {
				rollbackReplacements(replacements[:i])
				return nil, fmt.Errorf("move existing %s aside: %w", item.name, err)
			}
		}
		if err := os.Rename(item.temp, item.target); err != nil {
			if item.hadExisting {
				_ = os.Rename(item.backup, item.target)
			}
			rollbackReplacements(replacements[:i])
			return nil, fmt.Errorf("install updated %s: %w", item.name, err)
		}
		item.temp = ""
		item.committed = true
	}
	if err := syncDirectory(targetDir); err != nil {
		rollbackReplacements(replacements)
		return nil, fmt.Errorf("sync updated binary directory: %w", err)
	}

	warnings := []string{}
	for i := range replacements {
		item := &replacements[i]
		if item.backup == "" {
			continue
		}
		if err := os.Remove(item.backup); err != nil {
			warnings = append(warnings, fmt.Sprintf("could not remove previous %s backup %s: %v", item.name, item.backup, err))
		}
	}
	return warnings, nil
}

func inspectReplacementTarget(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect update target %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("update target %s is not a regular file", path)
	}
	return true, nil
}

func copyPreparedBinary(destination *os.File, sourcePath string, expected stagedFile) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hash), source)
	if err != nil {
		return err
	}
	if written != expected.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected.SHA256) {
		return fmt.Errorf("staged binary changed while preparing installation")
	}
	if err := destination.Chmod(0o755); err != nil {
		return err
	}
	if err := destination.Sync(); err != nil {
		return err
	}
	return destination.Close()
}

func reserveSiblingPath(dir, pattern string) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func rollbackReplacements(replacements []replacement) {
	for i := len(replacements) - 1; i >= 0; i-- {
		item := replacements[i]
		if !item.committed {
			continue
		}
		_ = os.Remove(item.target)
		if item.hadExisting && item.backup != "" {
			_ = os.Rename(item.backup, item.target)
		}
	}
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
