package demorecord

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultIdleTimeLimit = 2 * time.Second

type asciicastHeader struct {
	Version       int            `json:"version"`
	Term          asciicastTerm  `json:"term"`
	Timestamp     int64          `json:"timestamp"`
	IdleTimeLimit float64        `json:"idle_time_limit,omitempty"`
	Title         string         `json:"title,omitempty"`
	Env           map[string]any `json:"env,omitempty"`
}

type asciicastTerm struct {
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
	Type string `json:"type"`
}

func ExportAsciicast(reader *Reader, clip Clip, outputPath string) error {
	if reader == nil {
		return fmt.Errorf("demo recording reader is required")
	}
	manifest := reader.Manifest()
	if err := clip.Validate(manifest.DurationMS); err != nil {
		return err
	}
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		return fmt.Errorf("asciicast output path is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(outputPath))
	if err != nil {
		return fmt.Errorf("resolve asciicast output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return fmt.Errorf("create asciicast output directory: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("asciicast output already exists: %s", absolute)
		}
		return fmt.Errorf("create asciicast output: %w", err)
	}
	removeOutput := true
	defer func() {
		_ = file.Close()
		if removeOutput {
			_ = os.Remove(absolute)
		}
	}()

	initial, err := reader.FrameAt(clip.InMS)
	if err != nil {
		return err
	}
	idleLimit := time.Duration(clip.IdleTimeLimitMS) * time.Millisecond
	if clip.IdleTimeLimitMS == 0 {
		idleLimit = defaultIdleTimeLimit
	}
	header := asciicastHeader{
		Version: 3,
		Term: asciicastTerm{
			Cols: initial.Width,
			Rows: initial.Height,
			Type: "xterm-256color",
		},
		Timestamp: manifest.StartedAt.Add(time.Duration(clip.InMS) * time.Millisecond).Unix(),
		Title:     strings.TrimSpace(clip.Name),
	}
	if idleLimit > 0 {
		header.IdleTimeLimit = idleLimit.Seconds()
	}

	buffer := bufio.NewWriterSize(file, 64*1024)
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(header); err != nil {
		return fmt.Errorf("write asciicast header: %w", err)
	}
	if err := writeAsciicastEvent(encoder, 0, "o", fullScreenFrame(initial.View)); err != nil {
		return err
	}

	lastSourceMS := clip.InMS
	lastWidth := initial.Width
	lastHeight := initial.Height
	for chunkPosition := reader.ChunkIndexAt(clip.InMS); chunkPosition < len(manifest.Chunks); chunkPosition++ {
		chunk := manifest.Chunks[chunkPosition]
		if chunk.StartMS > clip.OutMS {
			break
		}
		frames, err := reader.LoadChunk(chunkPosition)
		if err != nil {
			return err
		}
		for _, frame := range frames {
			if frame.AtMS <= clip.InMS {
				continue
			}
			if frame.AtMS > clip.OutMS {
				break
			}
			interval := float64(frame.AtMS-lastSourceMS) / 1000
			if frame.Width != lastWidth || frame.Height != lastHeight {
				if err := writeAsciicastEvent(
					encoder,
					interval,
					"r",
					fmt.Sprintf("%dx%d", frame.Width, frame.Height),
				); err != nil {
					return err
				}
				interval = 0
				lastWidth = frame.Width
				lastHeight = frame.Height
			}
			if err := writeAsciicastEvent(encoder, interval, "o", fullScreenFrame(frame.View)); err != nil {
				return err
			}
			lastSourceMS = frame.AtMS
		}
	}
	exitInterval := float64(maxInt64(0, clip.OutMS-lastSourceMS)) / 1000
	if err := writeAsciicastEvent(encoder, exitInterval, "x", "0"); err != nil {
		return err
	}
	if err := buffer.Flush(); err != nil {
		return fmt.Errorf("flush asciicast output: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync asciicast output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close asciicast output: %w", err)
	}
	removeOutput = false
	return nil
}

func writeAsciicastEvent(encoder *json.Encoder, interval float64, code, data string) error {
	event := []any{interval, code, data}
	if err := encoder.Encode(event); err != nil {
		return fmt.Errorf("write asciicast event: %w", err)
	}
	return nil
}

func fullScreenFrame(view string) string {
	// Bubble Tea views use logical newlines. A terminal output stream needs a
	// carriage return as well so each rendered row starts in column zero.
	view = strings.ReplaceAll(view, "\n", "\r\n")
	return "\x1b[0m\x1b[2J\x1b[H" + view + "\x1b[0m"
}

func DefaultExportPath(recordingPath string, clip Clip) string {
	recordingPath = filepath.Clean(recordingPath)
	base := strings.TrimSuffix(filepath.Base(recordingPath), filepath.Ext(recordingPath))
	suffix := sanitizeFilePart(clip.ID)
	if suffix == "" {
		suffix = "clip"
	}
	return filepath.Join(filepath.Dir(recordingPath), base+"-"+suffix+".cast")
}

func sanitizeFilePart(value string) string {
	value = strings.TrimSpace(value)
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out.WriteRune(r)
			lastDash = false
		case !lastDash:
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}
