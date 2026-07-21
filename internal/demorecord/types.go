package demorecord

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	FormatVersion = 1

	ManifestFileName = "manifest.json"
	EditsFileName    = "edits.json"
	ChunksDirName    = "frames"
)

// Manifest describes a compact, chunked LCR demo recording.
type Manifest struct {
	Version       int         `json:"version"`
	StartedAt     time.Time   `json:"started_at"`
	CompletedAt   *time.Time  `json:"completed_at,omitempty"`
	DurationMS    int64       `json:"duration_ms"`
	FrameCount    int64       `json:"frame_count"`
	DroppedFrames uint64      `json:"dropped_frames,omitempty"`
	InteractionMS []int64     `json:"interaction_ms,omitempty"`
	Chunks        []ChunkMeta `json:"chunks"`
}

// ChunkMeta describes one independently decompressible group of frames.
type ChunkMeta struct {
	Index   int    `json:"index"`
	File    string `json:"file"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Frames  int    `json:"frames"`
	Bytes   int64  `json:"bytes"`
}

// Frame is a complete Bubble Tea view at a point on the source timeline.
type Frame struct {
	AtMS   int64
	Width  int
	Height int
	View   string
}

// EditProject is a non-destructive edit decision list for a recording.
type EditProject struct {
	Version int    `json:"version"`
	Clips   []Clip `json:"clips"`
}

// Clip identifies a source timeline interval. Times always refer to the
// original recording, before playback speed or idle-time compression.
type Clip struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	InMS            int64  `json:"in_ms"`
	OutMS           int64  `json:"out_ms"`
	IdleTimeLimitMS int64  `json:"idle_time_limit_ms,omitempty"`
	SmartTiming     bool   `json:"smart_timing,omitempty"`
}

func (c Clip) Validate(durationMS int64) error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("clip id is required")
	}
	if c.InMS < 0 {
		return fmt.Errorf("clip %q starts before the recording", c.ID)
	}
	if c.OutMS <= c.InMS {
		return fmt.Errorf("clip %q must end after it starts", c.ID)
	}
	if durationMS > 0 && c.OutMS > durationMS {
		return fmt.Errorf("clip %q ends after the recording", c.ID)
	}
	if c.IdleTimeLimitMS < -1 {
		return fmt.Errorf("clip %q has a negative idle-time limit", c.ID)
	}
	return nil
}

func NormalizeRecordingPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("recording path is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve recording path: %w", err)
	}
	return absolute, nil
}

func DefaultRecordingName(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return "lcr-demo-" + now.Format("20060102-150405") + ".lcrdemo"
}

func DefaultEditProject() EditProject {
	return EditProject{
		Version: FormatVersion,
		Clips:   []Clip{},
	}
}

func NextClipID(project EditProject) string {
	used := make(map[string]struct{}, len(project.Clips))
	for _, clip := range project.Clips {
		used[strings.TrimSpace(clip.ID)] = struct{}{}
	}
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("clip-%d", i)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}
