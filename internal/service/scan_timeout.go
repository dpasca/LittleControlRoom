package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

type ScanProgress struct {
	Phase       string
	Detector    string
	ProjectPath string
	Current     int
	Total       int
}

func (p ScanProgress) Detail() string {
	phase := strings.TrimSpace(p.Phase)
	if phase == "" {
		phase = "running project scan"
	}
	detector := strings.TrimSpace(p.Detector)
	if detector != "" {
		phase = fmt.Sprintf("%s with %s", phase, detector)
	}

	details := []string{}
	if p.Total > 0 {
		current := p.Current
		if current < 0 {
			current = 0
		}
		if current > p.Total {
			current = p.Total
		}
		details = append(details, fmt.Sprintf("%d/%d projects", current, p.Total))
	}
	if projectPath := strings.TrimSpace(p.ProjectPath); projectPath != "" {
		details = append(details, "current "+filepath.Clean(projectPath))
	}
	if len(details) == 0 {
		return phase
	}
	return fmt.Sprintf("%s (%s)", phase, strings.Join(details, ", "))
}

type ScanTimeoutError struct {
	Progress ScanProgress
	Err      error
}

func (e *ScanTimeoutError) Error() string {
	if e == nil {
		return ""
	}
	message := ""
	if e.Err != nil {
		message = e.Err.Error()
	}
	if detail := e.Progress.Detail(); detail != "" {
		if message != "" {
			return fmt.Sprintf("scan timed out while %s: %s", detail, message)
		}
		return "scan timed out while " + detail
	}
	return message
}

func (e *ScanTimeoutError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func ScanTimeoutProgress(err error) (ScanProgress, bool) {
	var timeoutErr *ScanTimeoutError
	if errors.As(err, &timeoutErr) && timeoutErr != nil {
		return timeoutErr.Progress, true
	}
	return ScanProgress{}, false
}

func ScanTimeoutDetail(err error) (string, bool) {
	progress, ok := ScanTimeoutProgress(err)
	if !ok {
		return "", false
	}
	return progress.Detail(), true
}

type scanProgressTracker struct {
	mu       sync.Mutex
	progress ScanProgress
}

func (t *scanProgressTracker) setPhase(phase string) {
	t.set(ScanProgress{Phase: phase})
}

func (t *scanProgressTracker) setDetector(phase, detector string) {
	t.set(ScanProgress{Phase: phase, Detector: detector})
}

func (t *scanProgressTracker) setProject(phase string, current, total int, projectPath string) {
	t.set(ScanProgress{
		Phase:       phase,
		Current:     current,
		Total:       total,
		ProjectPath: projectPath,
	})
}

func (t *scanProgressTracker) set(progress ScanProgress) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.progress = progress
	t.mu.Unlock()
}

func (t *scanProgressTracker) snapshot() ScanProgress {
	if t == nil {
		return ScanProgress{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.progress
}

func (t *scanProgressTracker) wrapTimeout(err error) error {
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var timeoutErr *ScanTimeoutError
	if errors.As(err, &timeoutErr) {
		return err
	}
	return &ScanTimeoutError{
		Progress: t.snapshot(),
		Err:      err,
	}
}

func (t *scanProgressTracker) contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return t.wrapTimeout(ctx.Err())
}
