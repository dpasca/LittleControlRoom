package browserctl

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCleanupManagedPlaywrightStateExpiresInactiveStateAndProtectsActiveSharedProfile(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	old := now.Add(-72 * time.Hour)
	fresh := now.Add(-time.Hour)

	activePaths := writeManagedPlaywrightCleanupFixture(t, dataDir, "session-active", "profile-shared", old, os.Getpid(), 2048)
	staleSharedPaths := writeManagedPlaywrightCleanupFixture(t, dataDir, "session-stale-shared", "profile-shared", old, 0, 1024)
	stalePaths := writeManagedPlaywrightCleanupFixture(t, dataDir, "session-stale", "profile-stale", old, 0, 2048)
	freshPaths := writeManagedPlaywrightCleanupFixture(t, dataDir, "session-fresh", "profile-fresh", fresh, 0, 1024)
	setManagedPlaywrightCleanupPathTime(t, activePaths.ProfileDir, old)
	setManagedPlaywrightCleanupPathTime(t, stalePaths.ProfileDir, old)
	setManagedPlaywrightCleanupPathTime(t, freshPaths.ProfileDir, fresh)

	orphanProfile := filepath.Join(managedPlaywrightRootDir(dataDir), "profiles", "profile-orphan")
	if err := os.MkdirAll(orphanProfile, 0o755); err != nil {
		t.Fatalf("create orphan profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphanProfile, "cache.bin"), []byte(strings.Repeat("o", 1024)), 0o600); err != nil {
		t.Fatalf("write orphan profile: %v", err)
	}
	setManagedPlaywrightCleanupPathTime(t, orphanProfile, old)

	result, err := cleanupManagedPlaywrightStateAt(dataDir, ManagedPlaywrightCleanupPolicy{
		RetentionPeriod:       24 * time.Hour,
		CleanupInterval:       time.Hour,
		DiskUsageCeilingBytes: 0,
	}, now)
	if err != nil {
		t.Fatalf("CleanupManagedPlaywrightState() error = %v", err)
	}

	assertManagedPlaywrightCleanupPathExists(t, activePaths.SessionDir, true)
	assertManagedPlaywrightCleanupPathExists(t, activePaths.ProfileDir, true)
	assertManagedPlaywrightCleanupPathExists(t, staleSharedPaths.SessionDir, false)
	assertManagedPlaywrightCleanupPathExists(t, stalePaths.SessionDir, false)
	assertManagedPlaywrightCleanupPathExists(t, stalePaths.ProfileDir, false)
	assertManagedPlaywrightCleanupPathExists(t, freshPaths.SessionDir, true)
	assertManagedPlaywrightCleanupPathExists(t, freshPaths.ProfileDir, true)
	assertManagedPlaywrightCleanupPathExists(t, orphanProfile, false)

	if got, want := result.ActiveSessionCount, 1; got != want {
		t.Fatalf("active session count = %d, want %d", got, want)
	}
	if got, want := result.ActiveProfileCount, 1; got != want {
		t.Fatalf("active profile count = %d, want %d", got, want)
	}
	if got, want := result.RetentionSessionCount, 2; got != want {
		t.Fatalf("retention session count = %d, want %d", got, want)
	}
	if got, want := result.RetentionProfileCount, 2; got != want {
		t.Fatalf("retention profile count = %d, want %d", got, want)
	}
	if !result.DiskUsageCeilingSatisfied {
		t.Fatal("disabled disk ceiling should be satisfied")
	}

	logged := readLastManagedPlaywrightCleanupResult(t, dataDir)
	if logged.DeletedSessionCount != result.DeletedSessionCount || logged.BytesAfter != result.BytesAfter {
		t.Fatalf("logged cleanup result = %#v, want counts from %#v", logged, result)
	}
}

func TestCleanupManagedPlaywrightStateProtectsLiveOrphanProfileSingleton(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	old := now.Add(-72 * time.Hour)
	profileDir := filepath.Join(managedPlaywrightRootDir(dataDir), "profiles", "profile-live")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	lockTarget := host + "-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(filepath.Join(profileDir, "SingletonLock"), []byte(lockTarget), 0o600); err != nil {
		t.Fatalf("write singleton lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "cache.bin"), []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
		t.Fatalf("write profile data: %v", err)
	}
	setManagedPlaywrightCleanupPathTime(t, profileDir, old)

	result, err := cleanupManagedPlaywrightStateAt(dataDir, ManagedPlaywrightCleanupPolicy{
		RetentionPeriod:       24 * time.Hour,
		CleanupInterval:       time.Hour,
		DiskUsageCeilingBytes: 1,
	}, now)
	if err != nil {
		t.Fatalf("CleanupManagedPlaywrightState() error = %v", err)
	}
	assertManagedPlaywrightCleanupPathExists(t, profileDir, true)
	if got, want := result.ActiveProfileCount, 1; got != want {
		t.Fatalf("active profile count = %d, want %d", got, want)
	}
	if result.DiskUsageCeilingSatisfied {
		t.Fatal("ceiling should remain unsatisfied when only active state consumes space")
	}
}

func TestCleanupManagedPlaywrightStateEvictsOldestInactiveStateToDiskCeiling(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	oldPaths := writeManagedPlaywrightCleanupFixture(t, dataDir, "session-old", "profile-old", now.Add(-4*time.Hour), 0, 16*1024)
	newPaths := writeManagedPlaywrightCleanupFixture(t, dataDir, "session-new", "profile-new", now.Add(-time.Hour), 0, 16*1024)
	setManagedPlaywrightCleanupPathTime(t, oldPaths.ProfileDir, now.Add(-4*time.Hour))
	setManagedPlaywrightCleanupPathTime(t, newPaths.ProfileDir, now.Add(-time.Hour))

	newSessionBytes, _, err := managedPlaywrightPathUsage(newPaths.SessionDir)
	if err != nil {
		t.Fatalf("measure new session: %v", err)
	}
	newProfileBytes, _, err := managedPlaywrightPathUsage(newPaths.ProfileDir)
	if err != nil {
		t.Fatalf("measure new profile: %v", err)
	}
	ceiling := newSessionBytes + newProfileBytes + 256

	result, err := cleanupManagedPlaywrightStateAt(dataDir, ManagedPlaywrightCleanupPolicy{
		RetentionPeriod:       0,
		CleanupInterval:       time.Hour,
		DiskUsageCeilingBytes: ceiling,
	}, now)
	if err != nil {
		t.Fatalf("CleanupManagedPlaywrightState() error = %v", err)
	}
	assertManagedPlaywrightCleanupPathExists(t, oldPaths.SessionDir, false)
	assertManagedPlaywrightCleanupPathExists(t, oldPaths.ProfileDir, false)
	assertManagedPlaywrightCleanupPathExists(t, newPaths.SessionDir, true)
	assertManagedPlaywrightCleanupPathExists(t, newPaths.ProfileDir, true)
	if !result.DiskUsageCeilingSatisfied || result.BytesAfter > ceiling {
		t.Fatalf("disk ceiling result = satisfied %v, after %d, ceiling %d", result.DiskUsageCeilingSatisfied, result.BytesAfter, ceiling)
	}
	if got, want := result.DiskCeilingSessionCount, 1; got != want {
		t.Fatalf("disk-ceiling session count = %d, want %d", got, want)
	}
	if got, want := result.DiskCeilingProfileCount, 1; got != want {
		t.Fatalf("disk-ceiling profile count = %d, want %d", got, want)
	}
}

func TestRunManagedPlaywrightCleanupRunsImmediatelyAndPeriodically(t *testing.T) {
	dataDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reports := make(chan ManagedPlaywrightCleanupResult, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunManagedPlaywrightCleanup(ctx, dataDir, ManagedPlaywrightCleanupPolicy{
			RetentionPeriod:       0,
			CleanupInterval:       10 * time.Millisecond,
			DiskUsageCeilingBytes: 0,
		}, func(result ManagedPlaywrightCleanupResult, err error) {
			if err != nil {
				t.Errorf("periodic cleanup error = %v", err)
			}
			reports <- result
		})
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-reports:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for periodic cleanup")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup loop did not stop after cancellation")
	}

	file, err := os.Open(ManagedPlaywrightCleanupLogPath(dataDir))
	if err != nil {
		t.Fatalf("open cleanup log: %v", err)
	}
	defer file.Close()
	lines := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan cleanup log: %v", err)
	}
	if lines < 2 {
		t.Fatalf("cleanup log lines = %d, want at least 2", lines)
	}
}

func writeManagedPlaywrightCleanupFixture(
	t *testing.T,
	dataDir string,
	sessionKey string,
	profileKey string,
	updatedAt time.Time,
	ownerPID int,
	payloadBytes int,
) ManagedPlaywrightPaths {
	t.Helper()
	paths, err := ManagedPlaywrightPathsFor(dataDir, "codex", "/tmp/demo", sessionKey, profileKey, ManagedLaunchModeHeadless)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	state := ManagedPlaywrightState{
		SessionKey:  sessionKey,
		ProfileKey:  profileKey,
		Provider:    "codex",
		ProjectPath: "/tmp/demo",
		LaunchMode:  ManagedLaunchModeHeadless,
		Policy:      DefaultPolicy(),
		OwnerPID:    ownerPID,
		UpdatedAt:   updatedAt,
	}
	if err := WriteManagedPlaywrightState(paths, state); err != nil {
		t.Fatalf("WriteManagedPlaywrightState() error = %v", err)
	}
	payload := []byte(strings.Repeat("x", payloadBytes))
	if err := os.WriteFile(filepath.Join(paths.OutputDir, "artifact.bin"), payload, 0o600); err != nil {
		t.Fatalf("write session payload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.ProfileDir, "cache.bin"), payload, 0o600); err != nil {
		t.Fatalf("write profile payload: %v", err)
	}
	return paths
}

func setManagedPlaywrightCleanupPathTime(t *testing.T, root string, at time.Time) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, at, at)
	})
	if err != nil {
		t.Fatalf("set cleanup fixture time for %s: %v", root, err)
	}
}

func assertManagedPlaywrightCleanupPathExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if want && err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	if !want && !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat error = %v", path, err)
	}
}

func readLastManagedPlaywrightCleanupResult(t *testing.T, dataDir string) ManagedPlaywrightCleanupResult {
	t.Helper()
	file, err := os.Open(ManagedPlaywrightCleanupLogPath(dataDir))
	if err != nil {
		t.Fatalf("open cleanup log: %v", err)
	}
	defer file.Close()
	var result ManagedPlaywrightCleanupResult
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
			t.Fatalf("decode cleanup log line: %v", err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan cleanup log: %v", err)
	}
	return result
}
