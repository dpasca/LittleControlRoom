package demorecord

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	discoveryReferenceVersion = 1
	discoveryDirName          = ".discovery"
)

type RecordingStatus string

const (
	RecordingStatusActive     RecordingStatus = "active"
	RecordingStatusFinalizing RecordingStatus = "finalizing"
	RecordingStatusFinalized  RecordingStatus = "finalized"
	RecordingStatusFailed     RecordingStatus = "failed"
)

// Association identifies the LCR project or embedded session visible when a
// recording starts. These fields are optional because launch-time recordings
// can begin before the TUI has a selected project or a provider session.
type Association struct {
	ProjectPath string `json:"project_path,omitempty"`
	Provider    string `json:"provider,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
}

func (a Association) Normalize() Association {
	projectPath := strings.TrimSpace(a.ProjectPath)
	if projectPath != "" {
		projectPath = filepath.Clean(projectPath)
	}
	return Association{
		ProjectPath: projectPath,
		Provider:    strings.ToLower(strings.TrimSpace(a.Provider)),
		SessionID:   strings.TrimSpace(a.SessionID),
	}
}

// Resource is safe to keep inside LCR's query executor. PackagePath must be
// removed before serialization unless the caller has explicit path authority.
type Resource struct {
	ID            string
	Status        RecordingStatus
	PackagePath   string
	StartedAt     time.Time
	CompletedAt   time.Time
	DurationMS    int64
	FormatVersion int
	FrameCount    int64
	DroppedFrames uint64
	Association   Association
	UpdatedAt     time.Time
}

type discoveryReference struct {
	Version       int             `json:"version"`
	ID            string          `json:"id"`
	Status        RecordingStatus `json:"status"`
	PackagePath   string          `json:"package_path"`
	OwnerPID      int             `json:"owner_pid,omitempty"`
	StartedAt     time.Time       `json:"started_at"`
	CompletedAt   time.Time       `json:"completed_at,omitempty"`
	DurationMS    int64           `json:"duration_ms,omitempty"`
	FormatVersion int             `json:"format_version"`
	Association   Association     `json:"association,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Discovery struct {
	dataDir      string
	now          func() time.Time
	processAlive func(int) bool
}

func NewDiscovery(dataDir string) *Discovery {
	return &Discovery{
		dataDir:      filepath.Clean(strings.TrimSpace(dataDir)),
		now:          time.Now,
		processAlive: recordingProcessAlive,
	}
}

func (d *Discovery) Latest(ctx context.Context) (Resource, bool, error) {
	if d == nil || strings.TrimSpace(d.dataDir) == "" || d.dataDir == "." {
		return Resource{}, false, nil
	}
	candidates := make(map[string]Resource)
	if err := d.readReferences(ctx, candidates); err != nil {
		return Resource{}, false, err
	}
	if err := d.readDefaultPackages(ctx, candidates); err != nil {
		return Resource{}, false, err
	}

	active := make([]Resource, 0, len(candidates))
	finalized := make([]Resource, 0, len(candidates))
	for _, candidate := range candidates {
		switch candidate.Status {
		case RecordingStatusActive, RecordingStatusFinalizing:
			active = append(active, candidate)
		case RecordingStatusFinalized:
			finalized = append(finalized, candidate)
		}
	}
	if len(active) > 0 {
		sort.SliceStable(active, func(i, j int) bool {
			if active[i].StartedAt.Equal(active[j].StartedAt) {
				return active[i].UpdatedAt.After(active[j].UpdatedAt)
			}
			return active[i].StartedAt.After(active[j].StartedAt)
		})
		return active[0], true, nil
	}
	if len(finalized) == 0 {
		return Resource{}, false, nil
	}
	sort.SliceStable(finalized, func(i, j int) bool {
		left := finalized[i].CompletedAt
		right := finalized[j].CompletedAt
		if left.IsZero() {
			left = finalized[i].StartedAt
		}
		if right.IsZero() {
			right = finalized[j].StartedAt
		}
		return left.After(right)
	})
	return finalized[0], true, nil
}

func (d *Discovery) readReferences(ctx context.Context, candidates map[string]Resource) error {
	dir := d.referenceDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read demo recording discovery references: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var reference discoveryReference
		if json.Unmarshal(raw, &reference) != nil || reference.Version != discoveryReferenceVersion {
			continue
		}
		resource, ok := d.resourceFromReference(reference)
		if !ok {
			continue
		}
		candidates[resource.PackagePath] = resource
	}
	return nil
}

func (d *Discovery) readDefaultPackages(ctx context.Context, candidates map[string]Resource) error {
	dir := d.recordingsDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read demo recording directory: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".lcrdemo") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if _, exists := candidates[path]; exists {
			continue
		}
		reader, err := Open(path)
		if err != nil {
			continue
		}
		manifest := reader.Manifest()
		if manifest.CompletedAt == nil {
			// Current binaries always create an active discovery reference. An
			// unreferenced incomplete package is a legacy or interrupted capture,
			// not evidence that a recorder is still active.
			continue
		}
		candidates[reader.Path()] = resourceFromManifest(reader.Path(), manifest, discoveryReference{})
	}
	return nil
}

func (d *Discovery) resourceFromReference(reference discoveryReference) (Resource, bool) {
	path, err := NormalizeRecordingPath(reference.PackagePath)
	if err != nil {
		return Resource{}, false
	}
	reader, err := Open(path)
	if err != nil {
		return Resource{}, false
	}
	manifest := reader.Manifest()
	resource := resourceFromManifest(reader.Path(), manifest, reference)
	if manifest.CompletedAt != nil {
		resource.Status = RecordingStatusFinalized
		return resource, true
	}
	if reference.Status != RecordingStatusActive && reference.Status != RecordingStatusFinalizing {
		return Resource{}, false
	}
	if reference.OwnerPID <= 0 || d.processAlive == nil || !d.processAlive(reference.OwnerPID) {
		return Resource{}, false
	}
	resource.Status = reference.Status
	elapsed := d.now().Sub(resource.StartedAt).Milliseconds()
	if elapsed > resource.DurationMS {
		resource.DurationMS = elapsed
	}
	return resource, true
}

func resourceFromManifest(path string, manifest Manifest, reference discoveryReference) Resource {
	completedAt := time.Time{}
	if manifest.CompletedAt != nil {
		completedAt = manifest.CompletedAt.UTC()
	}
	id := strings.TrimSpace(manifest.ID)
	if !validRecordingID(id) {
		id = strings.TrimSpace(reference.ID)
	}
	if !validRecordingID(id) {
		id = legacyRecordingID(path, manifest)
	}
	updatedAt := reference.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = completedAt
	}
	if updatedAt.IsZero() {
		updatedAt = manifest.StartedAt
	}
	status := reference.Status
	if manifest.CompletedAt != nil {
		status = RecordingStatusFinalized
	}
	return Resource{
		ID:            id,
		Status:        status,
		PackagePath:   filepath.Clean(path),
		StartedAt:     manifest.StartedAt.UTC(),
		CompletedAt:   completedAt,
		DurationMS:    manifest.DurationMS,
		FormatVersion: manifest.Version,
		FrameCount:    manifest.FrameCount,
		DroppedFrames: manifest.DroppedFrames,
		Association:   reference.Association.Normalize(),
		UpdatedAt:     updatedAt.UTC(),
	}
}

func legacyRecordingID(path string, manifest Manifest) string {
	seed := strings.Join([]string{
		filepath.Clean(path),
		fmt.Sprintf("%d", manifest.Version),
		manifest.StartedAt.UTC().Format(time.RFC3339Nano),
	}, "\n")
	sum := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("rec_%x", sum[:12])
}

func (d *Discovery) writeReference(reference discoveryReference) error {
	if d == nil || strings.TrimSpace(d.dataDir) == "" || d.dataDir == "." {
		return nil
	}
	reference.ID = strings.TrimSpace(reference.ID)
	reference.PackagePath = filepath.Clean(strings.TrimSpace(reference.PackagePath))
	if reference.ID == "" || reference.PackagePath == "" || reference.PackagePath == "." {
		return errors.New("demo recording discovery reference requires an id and package path")
	}
	reference.Version = discoveryReferenceVersion
	reference.Association = reference.Association.Normalize()
	if reference.FormatVersion == 0 {
		reference.FormatVersion = FormatVersion
	}
	if reference.UpdatedAt.IsZero() {
		reference.UpdatedAt = d.now().UTC()
	} else {
		reference.UpdatedAt = reference.UpdatedAt.UTC()
	}
	if err := os.MkdirAll(d.referenceDir(), 0o700); err != nil {
		return fmt.Errorf("create demo recording discovery directory: %w", err)
	}
	path := filepath.Join(d.referenceDir(), referenceFileName(reference.ID))
	if err := writeJSONAtomic(path, reference); err != nil {
		return fmt.Errorf("write demo recording discovery reference: %w", err)
	}
	return nil
}

func (d *Discovery) referenceDir() string {
	return filepath.Join(d.recordingsDir(), discoveryDirName)
}

func (d *Discovery) recordingsDir() string {
	return filepath.Join(d.dataDir, "demo-recordings")
}

func referenceFileName(id string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(id)))
	return fmt.Sprintf("%x.json", sum[:16])
}

func recordingProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
