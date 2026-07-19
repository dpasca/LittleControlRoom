package demorecord

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultChunkDuration = time.Minute
	defaultCaptureBuffer = 1024
	interactionSpacing   = 2 * time.Second
)

type RecorderOptions struct {
	Now           func() time.Time
	ChunkDuration time.Duration
	CaptureBuffer int
}

type captureFrame struct {
	at     time.Time
	width  int
	height int
	view   string
}

type stopRequest struct {
	at time.Time
}

// Recorder accepts complete views without performing disk work on the caller's
// goroutine. Complete frames make bounded queue coalescing and delta encoding
// safe: every accepted frame can reconstruct the screen independently of
// frames that arrived before it.
type Recorder struct {
	path      string
	startedAt time.Time
	now       func() time.Time
	frames    chan captureFrame
	activity  chan time.Time
	stop      chan stopRequest
	done      chan struct{}

	closed  atomic.Bool
	dropped atomic.Uint64

	errMu sync.RWMutex
	err   error
}

func NewRecorder(path string, opts RecorderOptions) (*Recorder, error) {
	path, err := NormalizeRecordingPath(path)
	if err != nil {
		return nil, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ChunkDuration <= 0 {
		opts.ChunkDuration = defaultChunkDuration
	}
	if opts.CaptureBuffer <= 0 {
		opts.CaptureBuffer = defaultCaptureBuffer
	}

	if info, statErr := os.Stat(path); statErr == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("recording path already exists: %s", path)
		}
		return nil, fmt.Errorf("recording path is an existing file: %s", path)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect recording path: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Join(path, ChunksDirName), 0o700); err != nil {
		return nil, fmt.Errorf("create recording directory: %w", err)
	}

	startedAt := opts.Now()
	recorder := &Recorder{
		path:      path,
		startedAt: startedAt,
		now:       opts.Now,
		frames:    make(chan captureFrame, opts.CaptureBuffer),
		activity:  make(chan time.Time, 256),
		stop:      make(chan stopRequest, 1),
		done:      make(chan struct{}),
	}
	manifest := Manifest{
		Version:   FormatVersion,
		StartedAt: startedAt,
		Chunks:    []ChunkMeta{},
	}
	if err := writeJSONAtomic(filepath.Join(path, ManifestFileName), manifest); err != nil {
		return nil, fmt.Errorf("initialize recording manifest: %w", err)
	}

	go recorder.run(manifest, opts.ChunkDuration)
	return recorder, nil
}

// MarkInteraction records only a coarse timestamp. The key, mouse button, and
// any entered text are intentionally not retained.
func (r *Recorder) MarkInteraction() {
	if r == nil || r.closed.Load() {
		return
	}
	select {
	case r.activity <- r.now():
	default:
	}
}

func (r *Recorder) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

// Capture queues a complete rendered view. It never waits for compression or
// filesystem I/O. If the bounded queue is saturated, the frame is skipped and
// the loss is recorded in the manifest.
func (r *Recorder) Capture(width, height int, view string) {
	if r == nil || r.closed.Load() || width <= 0 || height <= 0 {
		return
	}
	frame := captureFrame{
		at:     r.now(),
		width:  width,
		height: height,
		view:   view,
	}
	select {
	case r.frames <- frame:
	default:
		r.dropped.Add(1)
	}
}

func (r *Recorder) Err() error {
	if r == nil {
		return nil
	}
	r.errMu.RLock()
	defer r.errMu.RUnlock()
	return r.err
}

func (r *Recorder) DroppedFrames() uint64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	if r.closed.CompareAndSwap(false, true) {
		r.stop <- stopRequest{at: r.now()}
	}
	<-r.done
	return r.Err()
}

func (r *Recorder) setErr(err error) {
	if err == nil {
		return
	}
	r.errMu.Lock()
	defer r.errMu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func (r *Recorder) run(manifest Manifest, chunkDuration time.Duration) {
	defer close(r.done)

	var chunk *chunkWriter
	var lastWritten captureFrame
	haveLast := false
	flushTicker := time.NewTicker(time.Second)
	defer flushTicker.Stop()

	closeChunk := func() bool {
		if chunk == nil {
			return true
		}
		meta, err := chunk.close()
		if err != nil {
			r.setErr(err)
			return false
		}
		manifest.Chunks = append(manifest.Chunks, meta)
		manifest.FrameCount += int64(meta.Frames)
		chunk = nil
		if err := writeJSONAtomic(filepath.Join(r.path, ManifestFileName), manifest); err != nil {
			r.setErr(fmt.Errorf("update recording manifest: %w", err))
			return false
		}
		return true
	}

	writeFrame := func(frame captureFrame) bool {
		if haveLast &&
			frame.width == lastWritten.width &&
			frame.height == lastWritten.height &&
			frame.view == lastWritten.view {
			return true
		}
		atMS := maxInt64(0, frame.at.Sub(r.startedAt).Milliseconds())
		if chunk == nil || time.Duration(atMS-chunk.meta.StartMS)*time.Millisecond >= chunkDuration {
			if !closeChunk() {
				return false
			}
			var err error
			chunk, err = newChunkWriter(r.path, len(manifest.Chunks), atMS)
			if err != nil {
				r.setErr(err)
				return false
			}
		}
		if err := chunk.write(Frame{
			AtMS:   atMS,
			Width:  frame.width,
			Height: frame.height,
			View:   frame.view,
		}); err != nil {
			r.setErr(err)
			return false
		}
		lastWritten = frame
		haveLast = true
		manifest.DurationMS = maxInt64(manifest.DurationMS, atMS)
		return true
	}

	running := true
	recordInteraction := func(at time.Time) {
		atMS := maxInt64(0, at.Sub(r.startedAt).Milliseconds())
		if count := len(manifest.InteractionMS); count > 0 {
			previous := manifest.InteractionMS[count-1]
			if time.Duration(atMS-previous)*time.Millisecond < interactionSpacing {
				return
			}
		}
		manifest.InteractionMS = append(manifest.InteractionMS, atMS)
	}
	for running {
		select {
		case frame := <-r.frames:
			if r.Err() == nil {
				_ = writeFrame(frame)
			}
		case at := <-r.activity:
			recordInteraction(at)
		case <-flushTicker.C:
			if chunk != nil {
				if err := chunk.flush(); err != nil {
					r.setErr(err)
				}
			}
		case request := <-r.stop:
			for {
				select {
				case frame := <-r.frames:
					if r.Err() == nil {
						_ = writeFrame(frame)
					}
				case at := <-r.activity:
					recordInteraction(at)
				default:
					running = false
				}
				if !running {
					break
				}
			}
			completedAt := request.at
			manifest.CompletedAt = &completedAt
			manifest.DurationMS = maxInt64(manifest.DurationMS, request.at.Sub(r.startedAt).Milliseconds())
		}
	}

	_ = closeChunk()
	manifest.DroppedFrames = r.dropped.Load()
	if err := writeJSONAtomic(filepath.Join(r.path, ManifestFileName), manifest); err != nil {
		r.setErr(fmt.Errorf("finalize recording manifest: %w", err))
	}
}

type diskLineChange struct {
	Index int    `json:"i"`
	Text  string `json:"v"`
}

type diskFrame struct {
	AtMS      int64            `json:"t"`
	Width     int              `json:"w"`
	Height    int              `json:"h"`
	Full      *string          `json:"f,omitempty"`
	LineCount int              `json:"n,omitempty"`
	Delta     []diskLineChange `json:"d,omitempty"`
}

type chunkWriter struct {
	path        string
	partialPath string
	file        *os.File
	gzip        *gzip.Writer
	buffer      *bufio.Writer
	encoder     *json.Encoder
	meta        ChunkMeta
	lines       []string
}

func newChunkWriter(recordingPath string, index int, startMS int64) (*chunkWriter, error) {
	name := fmt.Sprintf("%06d.frames.jsonl.gz", index)
	path := filepath.Join(recordingPath, ChunksDirName, name)
	partialPath := path + ".partial"
	file, err := os.OpenFile(partialPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create demo frame chunk: %w", err)
	}
	gzipWriter, err := gzip.NewWriterLevel(file, gzip.DefaultCompression)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("initialize demo frame compression: %w", err)
	}
	buffer := bufio.NewWriterSize(gzipWriter, 64*1024)
	writer := &chunkWriter{
		path:        path,
		partialPath: partialPath,
		file:        file,
		gzip:        gzipWriter,
		buffer:      buffer,
		encoder:     json.NewEncoder(buffer),
		meta: ChunkMeta{
			Index:   index,
			File:    filepath.ToSlash(filepath.Join(ChunksDirName, name)),
			StartMS: startMS,
			EndMS:   startMS,
		},
	}
	writer.encoder.SetEscapeHTML(false)
	return writer, nil
}

func (w *chunkWriter) write(frame Frame) error {
	nextLines := strings.Split(frame.View, "\n")
	encoded := diskFrame{
		AtMS:   frame.AtMS,
		Width:  frame.Width,
		Height: frame.Height,
	}
	if w.meta.Frames == 0 {
		view := frame.View
		encoded.Full = &view
	} else {
		encoded.LineCount = len(nextLines)
		// LineCount carries truncation, so only changed lines that still exist
		// in the next frame need explicit values.
		limit := len(nextLines)
		for i := 0; i < limit; i++ {
			oldLine := ""
			if i < len(w.lines) {
				oldLine = w.lines[i]
			}
			newLine := ""
			if i < len(nextLines) {
				newLine = nextLines[i]
			}
			if oldLine != newLine {
				encoded.Delta = append(encoded.Delta, diskLineChange{Index: i, Text: newLine})
			}
		}
		if deltaTextSize(encoded.Delta) >= len(frame.View) {
			view := frame.View
			encoded.Full = &view
			encoded.LineCount = 0
			encoded.Delta = nil
		}
	}
	if err := w.encoder.Encode(encoded); err != nil {
		return fmt.Errorf("write demo frame: %w", err)
	}
	w.lines = nextLines
	w.meta.EndMS = frame.AtMS
	w.meta.Frames++
	return nil
}

func (w *chunkWriter) flush() error {
	if w == nil {
		return nil
	}
	if err := w.buffer.Flush(); err != nil {
		return fmt.Errorf("flush demo frame buffer: %w", err)
	}
	if err := w.gzip.Flush(); err != nil {
		return fmt.Errorf("flush demo frame compression: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("sync demo frame chunk: %w", err)
	}
	return nil
}

func (w *chunkWriter) close() (ChunkMeta, error) {
	if w == nil {
		return ChunkMeta{}, nil
	}
	if err := w.buffer.Flush(); err != nil {
		return ChunkMeta{}, fmt.Errorf("flush demo frame buffer: %w", err)
	}
	if err := w.gzip.Close(); err != nil {
		return ChunkMeta{}, fmt.Errorf("close demo frame compression: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return ChunkMeta{}, fmt.Errorf("sync demo frame chunk: %w", err)
	}
	if err := w.file.Close(); err != nil {
		return ChunkMeta{}, fmt.Errorf("close demo frame chunk: %w", err)
	}
	if err := os.Rename(w.partialPath, w.path); err != nil {
		return ChunkMeta{}, fmt.Errorf("finalize demo frame chunk: %w", err)
	}
	info, err := os.Stat(w.path)
	if err != nil {
		return ChunkMeta{}, fmt.Errorf("inspect demo frame chunk: %w", err)
	}
	w.meta.Bytes = info.Size()
	return w.meta, nil
}

func deltaTextSize(changes []diskLineChange) int {
	total := 0
	for _, change := range changes {
		total += len(change.Text) + 12
	}
	return total
}

func writeJSONAtomic(path string, value any) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
