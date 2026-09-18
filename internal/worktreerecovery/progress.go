package worktreerecovery

import (
	"context"
	"io"
	"time"
)

type Progress struct {
	Stage   string
	Path    string
	Entries int64
	Bytes   int64
}
type progressKey struct{}
type progressReporter struct {
	hashes  map[fileIdentity]string
	devices map[uint64]bool
	notify  func(Progress)
	current Progress
	last    time.Time
}

// WithProgress creates one sequential recovery worker scope with a throttled
// observer and an ephemeral cache for unchanged APFS file hashes.
// The callback must be quick; the service publishes an event, never UI work.
func WithProgress(ctx context.Context, notify func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, &progressReporter{notify: notify})
}
func reportProgress(ctx context.Context, stage, path string, entries, bytes int64) {
	p, _ := ctx.Value(progressKey{}).(*progressReporter)
	if p == nil || p.notify == nil {
		return
	}
	changed := p.current.Stage != stage
	if changed {
		p.current = Progress{Stage: stage}
	}
	p.current.Path = path
	p.current.Entries += entries
	p.current.Bytes += bytes
	// Bound the total event rate, including rapid transitions between small stores.
	if p.last.IsZero() || time.Since(p.last) >= 500*time.Millisecond {
		p.last = time.Now()
		p.notify(p.current)
	}
}

type progressReader struct {
	ctx         context.Context
	reader      io.Reader
	stage, path string
}

func (r progressReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(b)
	reportProgress(r.ctx, r.stage, r.path, 0, int64(n))
	return n, err
}
