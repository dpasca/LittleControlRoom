package browserctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/keyedmutex"
)

const (
	DefaultManagedPlaywrightStateRetention        = 30 * 24 * time.Hour
	DefaultManagedPlaywrightStateCleanupInterval  = 6 * time.Hour
	DefaultManagedPlaywrightDiskUsageCeilingBytes = int64(2 * 1024 * 1024 * 1024)

	managedPlaywrightCleanupLogMaxBytes       = int64(1024 * 1024)
	managedPlaywrightCleanupLogErrorLimit     = 32
	managedPlaywrightCleanupLogErrorMaxLength = 1024
	managedPlaywrightInitializationGrace      = time.Minute
	managedPlaywrightCleanupLogFileMode       = os.FileMode(0o600)
	managedPlaywrightCleanupDirectoryMode     = os.FileMode(0o755)
	managedPlaywrightCleanupReasonRetention   = "retention"
	managedPlaywrightCleanupReasonCeiling     = "disk_ceiling"
)

type ManagedPlaywrightCleanupPolicy struct {
	RetentionPeriod       time.Duration
	CleanupInterval       time.Duration
	DiskUsageCeilingBytes int64
}

func DefaultManagedPlaywrightCleanupPolicy() ManagedPlaywrightCleanupPolicy {
	return ManagedPlaywrightCleanupPolicy{
		RetentionPeriod:       DefaultManagedPlaywrightStateRetention,
		CleanupInterval:       DefaultManagedPlaywrightStateCleanupInterval,
		DiskUsageCeilingBytes: DefaultManagedPlaywrightDiskUsageCeilingBytes,
	}
}

func (p ManagedPlaywrightCleanupPolicy) Validate() error {
	if p.RetentionPeriod < 0 {
		return fmt.Errorf("managed Playwright state retention must be >= 0")
	}
	if p.CleanupInterval <= 0 {
		return fmt.Errorf("managed Playwright state cleanup interval must be > 0")
	}
	if p.DiskUsageCeilingBytes < 0 {
		return fmt.Errorf("managed Playwright disk usage ceiling must be >= 0")
	}
	return nil
}

type ManagedPlaywrightCleanupResult struct {
	StartedAt                 time.Time `json:"started_at"`
	CompletedAt               time.Time `json:"completed_at"`
	RetentionPeriod           string    `json:"retention_period"`
	DiskUsageCeilingBytes     int64     `json:"disk_usage_ceiling_bytes"`
	BytesBefore               int64     `json:"bytes_before"`
	BytesAfter                int64     `json:"bytes_after"`
	BytesReclaimed            int64     `json:"bytes_reclaimed"`
	ActiveSessionCount        int       `json:"active_session_count"`
	ActiveProfileCount        int       `json:"active_profile_count"`
	ProtectedSessionCount     int       `json:"protected_session_count"`
	ProtectedProfileCount     int       `json:"protected_profile_count"`
	DeletedSessionCount       int       `json:"deleted_session_count"`
	DeletedProfileCount       int       `json:"deleted_profile_count"`
	RetentionSessionCount     int       `json:"retention_session_count"`
	RetentionProfileCount     int       `json:"retention_profile_count"`
	DiskCeilingSessionCount   int       `json:"disk_ceiling_session_count"`
	DiskCeilingProfileCount   int       `json:"disk_ceiling_profile_count"`
	RemovedCleanupTrashBytes  int64     `json:"removed_cleanup_trash_bytes"`
	DiskUsageCeilingSatisfied bool      `json:"disk_usage_ceiling_satisfied"`
	ErrorCount                int       `json:"error_count"`
	OmittedErrorCount         int       `json:"omitted_error_count,omitempty"`
	Errors                    []string  `json:"errors,omitempty"`
}

type ManagedPlaywrightCleanupReporter func(ManagedPlaywrightCleanupResult, error)

type managedPlaywrightCleanupSession struct {
	Key       string
	Path      string
	Size      int64
	LastUsed  time.Time
	State     ManagedPlaywrightState
	StateOK   bool
	IsDir     bool
	Active    bool
	Protected bool
	Deleted   bool
}

type managedPlaywrightCleanupProfile struct {
	Key       string
	Path      string
	Size      int64
	LastUsed  time.Time
	IsDir     bool
	Active    bool
	Protected bool
	Deleted   bool
}

type managedPlaywrightCleanupInventory struct {
	Sessions map[string]*managedPlaywrightCleanupSession
	Profiles map[string]*managedPlaywrightCleanupProfile
}

var managedPlaywrightCleanupLocks keyedmutex.Locker

func ManagedPlaywrightCleanupLogPath(dataDir string) string {
	return filepath.Join(managedPlaywrightRootDir(dataDir), "cleanup.log")
}

func CleanupManagedPlaywrightState(dataDir string, policy ManagedPlaywrightCleanupPolicy) (ManagedPlaywrightCleanupResult, error) {
	return cleanupManagedPlaywrightStateAt(dataDir, policy, time.Now().UTC())
}

func RunManagedPlaywrightCleanup(
	ctx context.Context,
	dataDir string,
	policy ManagedPlaywrightCleanupPolicy,
	reporter ManagedPlaywrightCleanupReporter,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := policy.Validate(); err != nil {
		if reporter != nil {
			reporter(ManagedPlaywrightCleanupResult{}, err)
		}
		return
	}

	run := func() {
		result, err := CleanupManagedPlaywrightState(dataDir, policy)
		if reporter != nil {
			reporter(result, err)
		}
	}
	run()

	ticker := time.NewTicker(policy.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func cleanupManagedPlaywrightStateAt(
	dataDir string,
	policy ManagedPlaywrightCleanupPolicy,
	now time.Time,
) (ManagedPlaywrightCleanupResult, error) {
	result := ManagedPlaywrightCleanupResult{
		StartedAt:             now.UTC(),
		RetentionPeriod:       policy.RetentionPeriod.String(),
		DiskUsageCeilingBytes: policy.DiskUsageCeilingBytes,
	}
	if policy.RetentionPeriod < 0 {
		return result, fmt.Errorf("managed Playwright state retention must be >= 0")
	}
	if policy.DiskUsageCeilingBytes < 0 {
		return result, fmt.Errorf("managed Playwright disk usage ceiling must be >= 0")
	}

	var cleanupErrors []error
	recordError := func(err error) {
		if err == nil {
			return
		}
		cleanupErrors = append(cleanupErrors, err)
		result.ErrorCount++
		if len(result.Errors) >= managedPlaywrightCleanupLogErrorLimit {
			result.OmittedErrorCount++
			return
		}
		message := err.Error()
		if len(message) > managedPlaywrightCleanupLogErrorMaxLength {
			message = message[:managedPlaywrightCleanupLogErrorMaxLength] + "..."
		}
		result.Errors = append(result.Errors, message)
	}

	err := withManagedPlaywrightCleanupLock(dataDir, func() error {
		before, usageErr := managedPlaywrightStateDiskUsage(dataDir)
		result.BytesBefore = before
		recordError(usageErr)

		removedTrashBytes, trashErr := removeManagedPlaywrightCleanupTrash(dataDir)
		result.RemovedCleanupTrashBytes = removedTrashBytes
		recordError(trashErr)

		inventory, inventoryErrors := loadManagedPlaywrightCleanupInventory(dataDir, now)
		for _, inventoryErr := range inventoryErrors {
			recordError(inventoryErr)
		}
		protectManagedPlaywrightCleanupInventory(dataDir, inventory, now, recordError)
		result.ActiveSessionCount, result.ProtectedSessionCount = managedPlaywrightSessionProtectionCounts(inventory)
		result.ActiveProfileCount, result.ProtectedProfileCount = managedPlaywrightProfileProtectionCounts(inventory)

		if policy.RetentionPeriod > 0 {
			cutoff := now.Add(-policy.RetentionPeriod)
			removeManagedPlaywrightExpiredSessions(dataDir, inventory, cutoff, &result, recordError)
			refs := managedPlaywrightProfileReferenceCounts(inventory)
			removeManagedPlaywrightExpiredProfiles(dataDir, inventory, refs, cutoff, &result, recordError)
		}

		currentBytes, currentUsageErr := managedPlaywrightStateDiskUsage(dataDir)
		recordError(currentUsageErr)
		if policy.DiskUsageCeilingBytes > 0 && currentBytes > policy.DiskUsageCeilingBytes {
			enforceManagedPlaywrightDiskCeiling(
				dataDir,
				inventory,
				currentBytes,
				policy.DiskUsageCeilingBytes,
				&result,
				recordError,
			)
		}

		after, afterUsageErr := managedPlaywrightStateDiskUsage(dataDir)
		result.BytesAfter = after
		recordError(afterUsageErr)
		result.BytesReclaimed = max(int64(0), result.BytesBefore-result.BytesAfter)
		result.DiskUsageCeilingSatisfied = policy.DiskUsageCeilingBytes == 0 || result.BytesAfter <= policy.DiskUsageCeilingBytes
		result.CompletedAt = time.Now().UTC()

		if logErr := appendManagedPlaywrightCleanupLog(dataDir, result); logErr != nil {
			recordError(fmt.Errorf("write managed Playwright cleanup log: %w", logErr))
		}
		return nil
	})
	if err != nil {
		recordError(err)
	}
	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now().UTC()
	}
	return result, errors.Join(cleanupErrors...)
}

func managedPlaywrightRootDir(dataDir string) string {
	return filepath.Join(EffectiveDataDir(dataDir), "browser", "playwright")
}

func managedPlaywrightCleanupLockPath(dataDir string) string {
	return filepath.Join(managedPlaywrightRootDir(dataDir), "cleanup.lock")
}

func managedPlaywrightCleanupTrashDir(dataDir string) string {
	return filepath.Join(managedPlaywrightRootDir(dataDir), ".cleanup-trash")
}

func withManagedPlaywrightCleanupLock(dataDir string, fn func() error) error {
	if fn == nil {
		return nil
	}
	lockPath := managedPlaywrightCleanupLockPath(dataDir)
	unlockLocal := managedPlaywrightCleanupLocks.Lock(lockPath)
	defer unlockLocal()

	lockFile, err := lockManagedPlaywrightState(lockPath)
	if err != nil {
		return err
	}
	defer unlockManagedPlaywrightState(lockFile)
	return fn()
}

func loadManagedPlaywrightCleanupInventory(dataDir string, now time.Time) (managedPlaywrightCleanupInventory, []error) {
	rootDir := managedPlaywrightRootDir(dataDir)
	inventory := managedPlaywrightCleanupInventory{
		Sessions: make(map[string]*managedPlaywrightCleanupSession),
		Profiles: make(map[string]*managedPlaywrightCleanupProfile),
	}
	var inventoryErrors []error

	sessionEntries, err := os.ReadDir(filepath.Join(rootDir, "sessions"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		inventoryErrors = append(inventoryErrors, fmt.Errorf("list managed Playwright sessions: %w", err))
	}
	for _, entry := range sessionEntries {
		path := filepath.Join(rootDir, "sessions", entry.Name())
		size, lastModified, usageErr := managedPlaywrightPathUsage(path)
		if usageErr != nil {
			inventoryErrors = append(inventoryErrors, fmt.Errorf("inspect managed Playwright session %q: %w", entry.Name(), usageErr))
		}
		state, stateErr := ReadManagedPlaywrightState(dataDir, entry.Name())
		stateOK := stateErr == nil
		lastUsed := lastModified
		if stateOK && !state.UpdatedAt.IsZero() {
			lastUsed = state.UpdatedAt.UTC()
		}
		session := &managedPlaywrightCleanupSession{
			Key:      entry.Name(),
			Path:     path,
			Size:     size,
			LastUsed: lastUsed,
			State:    state,
			StateOK:  stateOK,
			IsDir:    entry.IsDir(),
		}
		session.Active = stateOK && managedPlaywrightStateIsActive(state)
		session.Protected = session.Active || (!stateOK && managedPlaywrightCleanupEntryIsInitializing(lastUsed, now))
		inventory.Sessions[session.Key] = session
	}

	profileEntries, err := os.ReadDir(filepath.Join(rootDir, "profiles"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		inventoryErrors = append(inventoryErrors, fmt.Errorf("list managed Playwright profiles: %w", err))
	}
	for _, entry := range profileEntries {
		path := filepath.Join(rootDir, "profiles", entry.Name())
		size, lastModified, usageErr := managedPlaywrightPathUsage(path)
		if usageErr != nil {
			inventoryErrors = append(inventoryErrors, fmt.Errorf("inspect managed Playwright profile %q: %w", entry.Name(), usageErr))
		}
		active := managedPlaywrightProfileMayBeActive(path)
		profile := &managedPlaywrightCleanupProfile{
			Key:       entry.Name(),
			Path:      path,
			Size:      size,
			LastUsed:  lastModified,
			IsDir:     entry.IsDir(),
			Active:    active,
			Protected: active,
		}
		inventory.Profiles[profile.Key] = profile
	}
	return inventory, inventoryErrors
}

func protectManagedPlaywrightCleanupInventory(
	dataDir string,
	inventory managedPlaywrightCleanupInventory,
	now time.Time,
	recordError func(error),
) {
	referencedProfiles := make(map[string]bool)
	for _, session := range inventory.Sessions {
		if !session.StateOK {
			continue
		}
		profile := inventory.Profiles[session.State.ProfileKey]
		if profile == nil {
			continue
		}
		referencedProfiles[profile.Key] = true
		if session.LastUsed.After(profile.LastUsed) {
			profile.LastUsed = session.LastUsed
		}
		if session.Active {
			profile.Active = true
			profile.Protected = true
		} else if session.Protected {
			profile.Protected = true
		}
	}
	for _, profile := range inventory.Profiles {
		if !referencedProfiles[profile.Key] && managedPlaywrightCleanupEntryIsInitializing(profile.LastUsed, now) {
			profile.Protected = true
		}
	}

	foreground, ok, err := readManagedPlaywrightForegroundState(dataDir)
	if err != nil {
		recordError(fmt.Errorf("read managed Playwright foreground state during cleanup: %w", err))
		return
	}
	if !ok || !managedPlaywrightStateIsActive(foreground) {
		return
	}
	if session := inventory.Sessions[foreground.SessionKey]; session != nil {
		session.Active = true
		session.Protected = true
	}
	if profile := inventory.Profiles[foreground.ProfileKey]; profile != nil {
		profile.Active = true
		profile.Protected = true
		if foreground.UpdatedAt.After(profile.LastUsed) {
			profile.LastUsed = foreground.UpdatedAt.UTC()
		}
	}
}

func managedPlaywrightStateIsActive(state ManagedPlaywrightState) bool {
	for _, pid := range []int{state.OwnerPID, state.MCPPID, state.BrowserPID} {
		if pid > 0 && processIsAlive(pid) {
			return true
		}
	}
	return false
}

func managedPlaywrightProfileMayBeActive(profilePath string) bool {
	lockPath := filepath.Join(profilePath, "SingletonLock")
	if _, err := os.Lstat(lockPath); err != nil {
		return false
	}
	target, ok := singletonLockTarget(lockPath)
	if !ok {
		return true
	}
	host, pid, ok := parseSingletonLockTarget(target)
	if !ok {
		return true
	}
	localHost, err := os.Hostname()
	if err != nil || strings.TrimSpace(localHost) == "" || strings.TrimSpace(host) != strings.TrimSpace(localHost) {
		return true
	}
	return processIsAlive(pid)
}

func managedPlaywrightCleanupEntryIsInitializing(lastUsed, now time.Time) bool {
	if lastUsed.IsZero() {
		return false
	}
	return !lastUsed.Before(now.Add(-managedPlaywrightInitializationGrace))
}

func removeManagedPlaywrightExpiredSessions(
	dataDir string,
	inventory managedPlaywrightCleanupInventory,
	cutoff time.Time,
	result *ManagedPlaywrightCleanupResult,
	recordError func(error),
) {
	for _, session := range sortedManagedPlaywrightCleanupSessions(inventory) {
		if session.Protected || session.LastUsed.After(cutoff) {
			continue
		}
		removed, err := removeManagedPlaywrightCleanupSession(dataDir, session, cutoff)
		if err != nil {
			session.Protected = true
			recordError(err)
			continue
		}
		if removed {
			recordManagedPlaywrightSessionRemoval(result, managedPlaywrightCleanupReasonRetention)
		}
	}
}

func removeManagedPlaywrightExpiredProfiles(
	dataDir string,
	inventory managedPlaywrightCleanupInventory,
	refs map[string]int,
	cutoff time.Time,
	result *ManagedPlaywrightCleanupResult,
	recordError func(error),
) {
	for _, profile := range sortedManagedPlaywrightCleanupProfiles(inventory) {
		if profile.Protected || refs[profile.Key] > 0 || profile.LastUsed.After(cutoff) {
			continue
		}
		removed, err := removeManagedPlaywrightCleanupProfile(dataDir, profile)
		if err != nil {
			profile.Protected = true
			recordError(err)
			continue
		}
		if removed {
			recordManagedPlaywrightProfileRemoval(result, managedPlaywrightCleanupReasonRetention)
		}
	}
}

func enforceManagedPlaywrightDiskCeiling(
	dataDir string,
	inventory managedPlaywrightCleanupInventory,
	currentBytes int64,
	ceilingBytes int64,
	result *ManagedPlaywrightCleanupResult,
	recordError func(error),
) {
	refs := managedPlaywrightProfileReferenceCounts(inventory)
	for currentBytes > ceilingBytes {
		session, profile := oldestManagedPlaywrightCleanupCandidate(inventory, refs)
		if session == nil && profile == nil {
			return
		}
		if session != nil {
			removed, err := removeManagedPlaywrightCleanupSession(dataDir, session, time.Time{})
			if err != nil {
				session.Protected = true
				recordError(err)
				continue
			}
			if !removed {
				continue
			}
			currentBytes = max(int64(0), currentBytes-session.Size)
			if session.StateOK && refs[session.State.ProfileKey] > 0 {
				refs[session.State.ProfileKey]--
			}
			recordManagedPlaywrightSessionRemoval(result, managedPlaywrightCleanupReasonCeiling)
			continue
		}

		removed, err := removeManagedPlaywrightCleanupProfile(dataDir, profile)
		if err != nil {
			profile.Protected = true
			recordError(err)
			continue
		}
		if !removed {
			continue
		}
		currentBytes = max(int64(0), currentBytes-profile.Size)
		recordManagedPlaywrightProfileRemoval(result, managedPlaywrightCleanupReasonCeiling)
	}
}

func removeManagedPlaywrightCleanupSession(
	dataDir string,
	session *managedPlaywrightCleanupSession,
	retentionCutoff time.Time,
) (bool, error) {
	if session == nil || session.Deleted || session.Protected {
		return false, nil
	}
	if !session.IsDir {
		removedPath, err := moveManagedPlaywrightPathToCleanupTrash(dataDir, "session", session.Path)
		if err != nil {
			return false, fmt.Errorf("stage managed Playwright session %q for cleanup: %w", session.Key, err)
		}
		if removedPath == "" {
			return false, nil
		}
		session.Deleted = true
		return true, removeManagedPlaywrightCleanupTrashPath(removedPath)
	}

	removedPath := ""
	err := WithManagedPlaywrightStateLock(dataDir, session.Key, func() error {
		current, readErr := ReadManagedPlaywrightState(dataDir, session.Key)
		if readErr == nil {
			session.State = current
			session.StateOK = true
			session.LastUsed = current.UpdatedAt.UTC()
			session.Active = managedPlaywrightStateIsActive(current)
			session.Protected = session.Active
			if session.Active || (!retentionCutoff.IsZero() && current.UpdatedAt.After(retentionCutoff)) {
				return nil
			}
		}
		var moveErr error
		removedPath, moveErr = moveManagedPlaywrightPathToCleanupTrash(dataDir, "session", session.Path)
		return moveErr
	})
	if err != nil {
		return false, fmt.Errorf("remove managed Playwright session %q: %w", session.Key, err)
	}
	if removedPath == "" {
		return false, nil
	}
	session.Deleted = true
	if err := removeManagedPlaywrightCleanupTrashPath(removedPath); err != nil {
		return true, fmt.Errorf("remove staged managed Playwright session %q: %w", session.Key, err)
	}
	return true, nil
}

func removeManagedPlaywrightCleanupProfile(dataDir string, profile *managedPlaywrightCleanupProfile) (bool, error) {
	if profile == nil || profile.Deleted || profile.Protected {
		return false, nil
	}
	if profile.IsDir && managedPlaywrightProfileMayBeActive(profile.Path) {
		profile.Active = true
		profile.Protected = true
		return false, nil
	}
	removedPath, err := moveManagedPlaywrightPathToCleanupTrash(dataDir, "profile", profile.Path)
	if err != nil {
		return false, fmt.Errorf("stage managed Playwright profile %q for cleanup: %w", profile.Key, err)
	}
	if removedPath == "" {
		return false, nil
	}
	profile.Deleted = true
	if err := removeManagedPlaywrightCleanupTrashPath(removedPath); err != nil {
		return true, fmt.Errorf("remove staged managed Playwright profile %q: %w", profile.Key, err)
	}
	return true, nil
}

func moveManagedPlaywrightPathToCleanupTrash(dataDir, kind, path string) (string, error) {
	trashDir := managedPlaywrightCleanupTrashDir(dataDir)
	if err := os.MkdirAll(trashDir, managedPlaywrightCleanupDirectoryMode); err != nil {
		return "", err
	}
	for attempt := 0; attempt < 100; attempt++ {
		name := strings.TrimSpace(kind) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if attempt > 0 {
			name += "-" + strconv.Itoa(attempt)
		}
		target := filepath.Join(trashDir, name)
		err := os.Rename(path, target)
		if err == nil {
			return target, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if _, statErr := os.Lstat(target); statErr == nil {
			continue
		}
		return "", err
	}
	return "", fmt.Errorf("could not allocate cleanup trash path")
}

func removeManagedPlaywrightCleanupTrash(dataDir string) (int64, error) {
	trashDir := managedPlaywrightCleanupTrashDir(dataDir)
	entries, err := os.ReadDir(trashDir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var removedBytes int64
	var removeErrors []error
	for _, entry := range entries {
		path := filepath.Join(trashDir, entry.Name())
		size, _, usageErr := managedPlaywrightPathUsage(path)
		if usageErr != nil {
			removeErrors = append(removeErrors, usageErr)
		}
		if err := removeManagedPlaywrightCleanupTrashPath(path); err != nil {
			removeErrors = append(removeErrors, err)
			continue
		}
		removedBytes += size
	}
	return removedBytes, errors.Join(removeErrors...)
}

func removeManagedPlaywrightCleanupTrashPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return os.RemoveAll(path)
}

func managedPlaywrightProfileReferenceCounts(inventory managedPlaywrightCleanupInventory) map[string]int {
	refs := make(map[string]int)
	for _, session := range inventory.Sessions {
		if session.Deleted || !session.StateOK || session.State.ProfileKey == "" {
			continue
		}
		refs[session.State.ProfileKey]++
	}
	return refs
}

func oldestManagedPlaywrightCleanupCandidate(
	inventory managedPlaywrightCleanupInventory,
	refs map[string]int,
) (*managedPlaywrightCleanupSession, *managedPlaywrightCleanupProfile) {
	var oldestSession *managedPlaywrightCleanupSession
	for _, session := range inventory.Sessions {
		if session.Deleted || session.Protected {
			continue
		}
		if oldestSession == nil || managedPlaywrightCleanupOlder(session.LastUsed, session.Key, oldestSession.LastUsed, oldestSession.Key) {
			oldestSession = session
		}
	}
	var oldestProfile *managedPlaywrightCleanupProfile
	for _, profile := range inventory.Profiles {
		if profile.Deleted || profile.Protected || refs[profile.Key] > 0 {
			continue
		}
		if oldestProfile == nil || managedPlaywrightCleanupOlder(profile.LastUsed, profile.Key, oldestProfile.LastUsed, oldestProfile.Key) {
			oldestProfile = profile
		}
	}
	if oldestSession == nil {
		return nil, oldestProfile
	}
	if oldestProfile == nil {
		return oldestSession, nil
	}
	if managedPlaywrightCleanupOlder(oldestProfile.LastUsed, oldestProfile.Key, oldestSession.LastUsed, oldestSession.Key) {
		return nil, oldestProfile
	}
	return oldestSession, nil
}

func sortedManagedPlaywrightCleanupSessions(inventory managedPlaywrightCleanupInventory) []*managedPlaywrightCleanupSession {
	items := make([]*managedPlaywrightCleanupSession, 0, len(inventory.Sessions))
	for _, session := range inventory.Sessions {
		items = append(items, session)
	}
	sort.Slice(items, func(i, j int) bool {
		return managedPlaywrightCleanupOlder(items[i].LastUsed, items[i].Key, items[j].LastUsed, items[j].Key)
	})
	return items
}

func sortedManagedPlaywrightCleanupProfiles(inventory managedPlaywrightCleanupInventory) []*managedPlaywrightCleanupProfile {
	items := make([]*managedPlaywrightCleanupProfile, 0, len(inventory.Profiles))
	for _, profile := range inventory.Profiles {
		items = append(items, profile)
	}
	sort.Slice(items, func(i, j int) bool {
		return managedPlaywrightCleanupOlder(items[i].LastUsed, items[i].Key, items[j].LastUsed, items[j].Key)
	})
	return items
}

func managedPlaywrightCleanupOlder(leftAt time.Time, leftKey string, rightAt time.Time, rightKey string) bool {
	if leftAt.Equal(rightAt) {
		return leftKey < rightKey
	}
	if leftAt.IsZero() {
		return true
	}
	if rightAt.IsZero() {
		return false
	}
	return leftAt.Before(rightAt)
}

func managedPlaywrightSessionProtectionCounts(inventory managedPlaywrightCleanupInventory) (int, int) {
	active := 0
	protected := 0
	for _, session := range inventory.Sessions {
		if session.Active {
			active++
		}
		if session.Protected {
			protected++
		}
	}
	return active, protected
}

func managedPlaywrightProfileProtectionCounts(inventory managedPlaywrightCleanupInventory) (int, int) {
	active := 0
	protected := 0
	for _, profile := range inventory.Profiles {
		if profile.Active {
			active++
		}
		if profile.Protected {
			protected++
		}
	}
	return active, protected
}

func recordManagedPlaywrightSessionRemoval(result *ManagedPlaywrightCleanupResult, reason string) {
	if result == nil {
		return
	}
	result.DeletedSessionCount++
	if reason == managedPlaywrightCleanupReasonRetention {
		result.RetentionSessionCount++
	} else if reason == managedPlaywrightCleanupReasonCeiling {
		result.DiskCeilingSessionCount++
	}
}

func recordManagedPlaywrightProfileRemoval(result *ManagedPlaywrightCleanupResult, reason string) {
	if result == nil {
		return
	}
	result.DeletedProfileCount++
	if reason == managedPlaywrightCleanupReasonRetention {
		result.RetentionProfileCount++
	} else if reason == managedPlaywrightCleanupReasonCeiling {
		result.DiskCeilingProfileCount++
	}
}

func managedPlaywrightStateDiskUsage(dataDir string) (int64, error) {
	rootDir := managedPlaywrightRootDir(dataDir)
	var total int64
	var usageErrors []error
	for _, name := range []string{"sessions", "profiles", ".cleanup-trash"} {
		size, _, err := managedPlaywrightPathUsage(filepath.Join(rootDir, name))
		total += size
		if err != nil {
			usageErrors = append(usageErrors, err)
		}
	}
	return total, errors.Join(usageErrors...)
}

func managedPlaywrightPathUsage(path string) (int64, time.Time, error) {
	var total int64
	var lastModified time.Time
	var walkErrors []error
	err := filepath.WalkDir(path, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			walkErrors = append(walkErrors, walkErr)
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			walkErrors = append(walkErrors, infoErr)
			return nil
		}
		if info.ModTime().After(lastModified) {
			lastModified = info.ModTime().UTC()
		}
		if !info.IsDir() {
			total += max(int64(0), info.Size())
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		walkErrors = append(walkErrors, err)
	}
	return total, lastModified, errors.Join(walkErrors...)
}

func appendManagedPlaywrightCleanupLog(dataDir string, result ManagedPlaywrightCleanupResult) error {
	path := ManagedPlaywrightCleanupLogPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), managedPlaywrightCleanupDirectoryMode); err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if info, statErr := os.Stat(path); statErr == nil && info.Size()+int64(len(raw)) > managedPlaywrightCleanupLogMaxBytes {
		rotatedPath := path + ".1"
		if removeErr := os.Remove(rotatedPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return removeErr
		}
		if renameErr := os.Rename(path, rotatedPath); renameErr != nil {
			return renameErr
		}
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, managedPlaywrightCleanupLogFileMode)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(managedPlaywrightCleanupLogFileMode); err != nil {
		return err
	}
	_, err = file.Write(raw)
	return err
}
