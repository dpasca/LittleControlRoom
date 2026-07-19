package demorecord

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Reader struct {
	path     string
	manifest Manifest
}

func Open(path string) (*Reader, error) {
	path, err := NormalizeRecordingPath(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(path, ManifestFileName))
	if err != nil {
		return nil, fmt.Errorf("read demo recording manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse demo recording manifest: %w", err)
	}
	if manifest.Version != FormatVersion {
		return nil, fmt.Errorf("unsupported demo recording version %d", manifest.Version)
	}
	sort.SliceStable(manifest.Chunks, func(i, j int) bool {
		return manifest.Chunks[i].Index < manifest.Chunks[j].Index
	})
	sort.SliceStable(manifest.InteractionMS, func(i, j int) bool {
		return manifest.InteractionMS[i] < manifest.InteractionMS[j]
	})
	for i, chunk := range manifest.Chunks {
		if chunk.Index < 0 || strings.TrimSpace(chunk.File) == "" {
			return nil, fmt.Errorf("invalid demo chunk at manifest position %d", i)
		}
	}
	return &Reader{path: path, manifest: manifest}, nil
}

func (r *Reader) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

func (r *Reader) Manifest() Manifest {
	if r == nil {
		return Manifest{}
	}
	manifest := r.manifest
	manifest.Chunks = append([]ChunkMeta(nil), r.manifest.Chunks...)
	manifest.InteractionMS = append([]int64(nil), r.manifest.InteractionMS...)
	return manifest
}

func (r *Reader) ChunkIndexAt(atMS int64) int {
	if r == nil || len(r.manifest.Chunks) == 0 {
		return -1
	}
	index := sort.Search(len(r.manifest.Chunks), func(i int) bool {
		return r.manifest.Chunks[i].StartMS > atMS
	}) - 1
	if index < 0 {
		return 0
	}
	return index
}

func (r *Reader) LoadChunk(position int) ([]Frame, error) {
	if r == nil {
		return nil, fmt.Errorf("demo recording reader is required")
	}
	if position < 0 || position >= len(r.manifest.Chunks) {
		return nil, fmt.Errorf("demo chunk position %d is out of range", position)
	}
	meta := r.manifest.Chunks[position]
	path, err := r.chunkPath(meta)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open demo frame chunk: %w", err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("open demo frame compression: %w", err)
	}
	defer gzipReader.Close()

	decoder := json.NewDecoder(bufio.NewReader(gzipReader))
	frames := make([]Frame, 0, meta.Frames)
	var lines []string
	for {
		var encoded diskFrame
		if err := decoder.Decode(&encoded); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode demo frame chunk: %w", err)
		}
		switch {
		case encoded.Full != nil:
			lines = strings.Split(*encoded.Full, "\n")
		case encoded.LineCount >= 0 && len(lines) > 0:
			next := make([]string, encoded.LineCount)
			copy(next, lines)
			for _, change := range encoded.Delta {
				if change.Index < 0 || change.Index >= len(next) {
					return nil, fmt.Errorf("demo frame delta line %d is out of range", change.Index)
				}
				next[change.Index] = change.Text
			}
			lines = next
		default:
			return nil, fmt.Errorf("demo frame chunk starts without a full frame")
		}
		frames = append(frames, Frame{
			AtMS:   encoded.AtMS,
			Width:  encoded.Width,
			Height: encoded.Height,
			View:   strings.Join(lines, "\n"),
		})
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("demo frame chunk %d is empty", meta.Index)
	}
	return frames, nil
}

func (r *Reader) FrameAt(atMS int64) (Frame, error) {
	chunkPosition := r.ChunkIndexAt(atMS)
	if chunkPosition < 0 {
		return Frame{}, fmt.Errorf("demo recording has no frames")
	}
	frames, err := r.LoadChunk(chunkPosition)
	if err != nil {
		return Frame{}, err
	}
	index := sort.Search(len(frames), func(i int) bool {
		return frames[i].AtMS > atMS
	}) - 1
	if index >= 0 {
		return frames[index], nil
	}
	if chunkPosition > 0 {
		previous, err := r.LoadChunk(chunkPosition - 1)
		if err != nil {
			return Frame{}, err
		}
		return previous[len(previous)-1], nil
	}
	return frames[0], nil
}

func (r *Reader) chunkPath(meta ChunkMeta) (string, error) {
	relative := filepath.Clean(filepath.FromSlash(meta.File))
	if relative == "." || filepath.IsAbs(relative) {
		return "", fmt.Errorf("invalid demo chunk path %q", meta.File)
	}
	path := filepath.Join(r.path, relative)
	rel, err := filepath.Rel(r.path, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("demo chunk path escapes the recording: %q", meta.File)
	}
	return path, nil
}

func LoadEdits(recordingPath string) (EditProject, error) {
	recordingPath, err := NormalizeRecordingPath(recordingPath)
	if err != nil {
		return EditProject{}, err
	}
	raw, err := os.ReadFile(filepath.Join(recordingPath, EditsFileName))
	if errors.Is(err, os.ErrNotExist) {
		return DefaultEditProject(), nil
	}
	if err != nil {
		return EditProject{}, fmt.Errorf("read demo edits: %w", err)
	}
	var project EditProject
	if err := json.Unmarshal(raw, &project); err != nil {
		return EditProject{}, fmt.Errorf("parse demo edits: %w", err)
	}
	if project.Version != FormatVersion {
		return EditProject{}, fmt.Errorf("unsupported demo edits version %d", project.Version)
	}
	if project.Clips == nil {
		project.Clips = []Clip{}
	}
	return project, nil
}

func SaveEdits(recordingPath string, project EditProject, durationMS int64) error {
	recordingPath, err := NormalizeRecordingPath(recordingPath)
	if err != nil {
		return err
	}
	if project.Version == 0 {
		project.Version = FormatVersion
	}
	if project.Version != FormatVersion {
		return fmt.Errorf("unsupported demo edits version %d", project.Version)
	}
	seen := make(map[string]struct{}, len(project.Clips))
	for i := range project.Clips {
		project.Clips[i].ID = strings.TrimSpace(project.Clips[i].ID)
		project.Clips[i].Name = strings.TrimSpace(project.Clips[i].Name)
		if project.Clips[i].Name == "" {
			project.Clips[i].Name = project.Clips[i].ID
		}
		if err := project.Clips[i].Validate(durationMS); err != nil {
			return err
		}
		if _, duplicate := seen[project.Clips[i].ID]; duplicate {
			return fmt.Errorf("duplicate clip id %q", project.Clips[i].ID)
		}
		seen[project.Clips[i].ID] = struct{}{}
	}
	return writeJSONAtomic(filepath.Join(recordingPath, EditsFileName), project)
}
