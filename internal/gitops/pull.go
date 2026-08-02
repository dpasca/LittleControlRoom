package gitops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	pullCommandOutputLimit   = 8 * 1024
	pullProgressPartialLimit = 4 * 1024
	pullProgressEmitInterval = 100 * time.Millisecond
	pullLFSProgressPoll      = 100 * time.Millisecond
	pullCommandWaitDelay     = 2 * time.Second
)

// Pull commands are bounded by inactivity, not total wall-clock time. A large
// pack or LFS download can therefore run for as long as it keeps reporting
// transfer progress.
var defaultPullStallTimeout = 60 * time.Second

type PullPhase string

const (
	PullPhaseFetch       PullPhase = "fetch"
	PullPhaseFastForward PullPhase = "fast_forward"
)

type PullProgress struct {
	Phase   PullPhase
	Detail  string
	At      time.Time
	Elapsed time.Duration
}

type PullOptions struct {
	StallTimeout time.Duration
	Progress     func(PullProgress)
}

type PullResult struct {
	Upstream           string
	FetchedCommit      string
	FetchCompleted     bool
	FastForwarded      bool
	PendingFastForward bool
}

type PullStallError struct {
	Path         string
	Phase        PullPhase
	StallTimeout time.Duration
	LastProgress string
}

func (e *PullStallError) Error() string {
	phase := pullPhaseLabel(e.Phase)
	message := fmt.Sprintf("%s %s stalled after %s without progress", phase, e.Path, e.StallTimeout.Round(time.Millisecond))
	if detail := strings.TrimSpace(e.LastProgress); detail != "" {
		message += "; last progress: " + detail
	}
	return message
}

func (e *PullStallError) Unwrap() error {
	return context.DeadlineExceeded
}

func Pull(ctx context.Context, path string) error {
	_, err := PullWithOptions(ctx, path, PullOptions{})
	return err
}

func PullWithOptions(ctx context.Context, path string, options PullOptions) (PullResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.StallTimeout <= 0 {
		options.StallTimeout = defaultPullStallTimeout
	}

	startedAt := time.Now()
	emit := func(phase PullPhase, detail string) {
		if options.Progress == nil {
			return
		}
		now := time.Now()
		options.Progress(PullProgress{
			Phase:   phase,
			Detail:  strings.TrimSpace(detail),
			At:      now,
			Elapsed: now.Sub(startedAt),
		})
	}

	upstream, err := pullGitOutput(ctx, path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return PullResult{}, fmt.Errorf("resolve pull upstream for %s: %w", path, err)
	}
	result := PullResult{Upstream: upstream}

	emit(PullPhaseFetch, "Contacting "+upstream)
	err = runPullGitCommand(ctx, path, PullPhaseFetch, options.StallTimeout, emit, "fetch", "--progress")
	if err != nil {
		return result, fmt.Errorf("pull %s: %w", path, err)
	}
	result.FetchCompleted = true
	emit(PullPhaseFetch, "Fetch complete")

	head, err := pullGitOutput(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		inspectFetchedPullState(path, upstream, &result)
		return result, fmt.Errorf("read local HEAD after fetching %s: %w", path, err)
	}
	upstreamCommit, err := pullGitOutput(ctx, path, "rev-parse", upstream)
	if err != nil {
		inspectFetchedPullState(path, upstream, &result)
		return result, fmt.Errorf("read fetched upstream for %s: %w", path, err)
	}
	result.FetchedCommit = upstreamCommit
	if head == upstreamCommit {
		return result, nil
	}

	canFastForward, err := pullRefIsAncestor(ctx, path, head, upstreamCommit)
	if err != nil {
		inspectFetchedPullState(path, upstream, &result)
		return result, fmt.Errorf("check fetched history for %s: %w", path, err)
	}
	if !canFastForward {
		localContainsUpstream, ancestorErr := pullRefIsAncestor(ctx, path, upstreamCommit, head)
		if ancestorErr != nil {
			inspectFetchedPullState(path, upstream, &result)
			return result, fmt.Errorf("check local history for %s: %w", path, ancestorErr)
		}
		if localContainsUpstream {
			return result, nil
		}
		return result, fmt.Errorf("fetched %s, but the current branch has diverged from %s; no local update was attempted", path, upstream)
	}

	currentHead, err := pullGitOutput(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		inspectFetchedPullState(path, upstream, &result)
		return result, fmt.Errorf("recheck local HEAD before updating %s: %w", path, err)
	}
	if currentHead != head {
		inspectFetchedPullState(path, upstream, &result)
		return result, fmt.Errorf("fetched %s, but local HEAD changed before the fast-forward; no local update was attempted", path)
	}

	result.PendingFastForward = true
	emit(PullPhaseFastForward, "Applying fast-forward to "+upstream)
	err = runPullGitCommand(
		ctx,
		path,
		PullPhaseFastForward,
		options.StallTimeout,
		emit,
		"merge", "--ff-only", "--progress", upstreamCommit,
	)
	if err != nil {
		inspectPullResultAfterUpdate(ctx, path, upstreamCommit, &result)
		return result, fmt.Errorf("pull %s after fetch completed: %w", path, err)
	}
	result.FastForwarded = true
	result.PendingFastForward = false
	emit(PullPhaseFastForward, "Fast-forward complete")
	return result, nil
}

func runPullGitCommand(
	ctx context.Context,
	path string,
	phase PullPhase,
	stallTimeout time.Duration,
	emit func(PullPhase, string),
	args ...string,
) error {
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	writer := newPullCommandWriter(phase, emit)
	lfsProgressFile, err := os.CreateTemp("", "lcroom-git-lfs-progress-*")
	if err != nil {
		return fmt.Errorf("prepare Git LFS progress tracking: %w", err)
	}
	lfsProgressPath := lfsProgressFile.Name()
	if closeErr := lfsProgressFile.Close(); closeErr != nil {
		_ = os.Remove(lfsProgressPath)
		return fmt.Errorf("prepare Git LFS progress tracking: %w", closeErr)
	}
	defer os.Remove(lfsProgressPath)

	commandArgs := append([]string{"-C", path}, args...)
	cmd := exec.CommandContext(commandCtx, "git", commandArgs...)
	cmd.WaitDelay = pullCommandWaitDelay
	cmd.Stdout = writer
	cmd.Stderr = writer
	cmd.Env = append(os.Environ(), "GIT_LFS_PROGRESS="+lfsProgressPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", pullPhaseLabel(phase), err)
	}
	stopLFSProgress := followPullLFSProgress(lfsProgressPath, writer)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	stopAndFlush := func() {
		stopLFSProgress()
		writer.Flush()
	}
	timer := time.NewTimer(stallTimeout)
	defer timer.Stop()

	for {
		select {
		case commandErr := <-done:
			stopAndFlush()
			if commandErr == nil {
				return nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("%s canceled: %w", pullPhaseLabel(phase), ctxErr)
			}
			return pullCommandError(phase, commandErr, writer.Output())
		case <-writer.Activity():
			resetPullStallTimer(timer, stallTimeout)
		case <-timer.C:
			select {
			case commandErr := <-done:
				stopAndFlush()
				if commandErr == nil {
					return nil
				}
				if ctxErr := ctx.Err(); ctxErr != nil {
					return fmt.Errorf("%s canceled: %w", pullPhaseLabel(phase), ctxErr)
				}
				return pullCommandError(phase, commandErr, writer.Output())
			default:
			}

			inactiveFor := time.Since(writer.LastActivityAt())
			if inactiveFor < stallTimeout {
				resetPullStallTimer(timer, stallTimeout-inactiveFor)
				continue
			}
			lastProgress := writer.LastProgress()
			cancel()
			commandErr := <-done
			stopAndFlush()
			if commandErr == nil {
				return nil
			}
			return &PullStallError{
				Path:         path,
				Phase:        phase,
				StallTimeout: stallTimeout,
				LastProgress: lastProgress,
			}
		case <-ctx.Done():
			select {
			case commandErr := <-done:
				stopAndFlush()
				if commandErr == nil {
					return nil
				}
				return fmt.Errorf("%s canceled: %w", pullPhaseLabel(phase), ctx.Err())
			default:
			}
			cancel()
			commandErr := <-done
			stopAndFlush()
			if commandErr == nil {
				return nil
			}
			return fmt.Errorf("%s canceled: %w", pullPhaseLabel(phase), ctx.Err())
		}
	}
}

func pullGitOutput(ctx context.Context, path string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", path}, args...)
	out, err := exec.CommandContext(ctx, "git", commandArgs...).CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		if trimmed != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, trimmed)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func pullRefIsAncestor(ctx context.Context, path, ancestor, descendant string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "merge-base", "--is-ancestor", ancestor, descendant)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func inspectPullResultAfterUpdate(ctx context.Context, path, upstreamCommit string, result *PullResult) {
	if result == nil {
		return
	}
	inspectCtx := ctx
	cancel := func() {}
	if ctx == nil || ctx.Err() != nil {
		inspectCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	}
	defer cancel()

	head, err := pullGitOutput(inspectCtx, path, "rev-parse", "HEAD")
	if err != nil {
		return
	}
	if head == upstreamCommit {
		result.FastForwarded = true
		result.PendingFastForward = false
		return
	}
	if pending, ancestorErr := pullRefIsAncestor(inspectCtx, path, head, upstreamCommit); ancestorErr == nil {
		result.PendingFastForward = pending
	}
}

func inspectFetchedPullState(path, upstream string, result *PullResult) {
	if result == nil || strings.TrimSpace(upstream) == "" {
		return
	}
	inspectCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	head, err := pullGitOutput(inspectCtx, path, "rev-parse", "HEAD")
	if err != nil {
		return
	}
	upstreamCommit, err := pullGitOutput(inspectCtx, path, "rev-parse", upstream)
	if err != nil {
		return
	}
	result.FetchedCommit = upstreamCommit
	if head == upstreamCommit {
		result.PendingFastForward = false
		return
	}
	if pending, ancestorErr := pullRefIsAncestor(inspectCtx, path, head, upstreamCommit); ancestorErr == nil {
		result.PendingFastForward = pending
	}
}

func pullCommandError(phase PullPhase, err error, output string) error {
	if output = strings.TrimSpace(output); output != "" {
		return fmt.Errorf("%s failed: %w: %s", pullPhaseLabel(phase), err, output)
	}
	return fmt.Errorf("%s failed: %w", pullPhaseLabel(phase), err)
}

func pullPhaseLabel(phase PullPhase) string {
	switch phase {
	case PullPhaseFastForward:
		return "git fast-forward"
	default:
		return "git fetch"
	}
}

func resetPullStallTimer(timer *time.Timer, timeout time.Duration) {
	if timeout <= 0 {
		timeout = time.Millisecond
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(timeout)
}

func followPullLFSProgress(path string, writer *pullCommandWriter) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		ticker := time.NewTicker(pullLFSProgressPoll)
		defer ticker.Stop()
		var offset int64
		readProgress := func() {
			file, err := os.Open(path)
			if err != nil {
				return
			}
			defer file.Close()
			if info, statErr := file.Stat(); statErr == nil && info.Size() < offset {
				offset = 0
			}
			if _, err := file.Seek(offset, io.SeekStart); err != nil {
				return
			}
			read, err := io.Copy(writer, file)
			if err == nil {
				offset += read
			}
		}
		for {
			select {
			case <-ticker.C:
				readProgress()
			case <-stop:
				readProgress()
				return
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}

type pullCommandWriter struct {
	mu             sync.Mutex
	emitMu         sync.Mutex
	phase          PullPhase
	emit           func(PullPhase, string)
	activity       chan struct{}
	lastActivityAt time.Time
	lastEmitAt     time.Time
	lastProgress   string
	pendingDetail  string
	partial        []byte
	output         []byte
}

func newPullCommandWriter(phase PullPhase, emit func(PullPhase, string)) *pullCommandWriter {
	now := time.Now()
	return &pullCommandWriter{
		phase:          phase,
		emit:           emit,
		activity:       make(chan struct{}, 1),
		lastActivityAt: now,
	}
}

func (w *pullCommandWriter) Write(p []byte) (int, error) {
	now := time.Now()
	detail := ""

	w.mu.Lock()
	w.lastActivityAt = now
	w.appendOutput(p)
	w.partial = append(w.partial, p...)
	if len(w.partial) > pullProgressPartialLimit {
		w.partial = append(w.partial[:0], w.partial[len(w.partial)-pullProgressPartialLimit:]...)
	}
	for {
		separator := pullProgressSeparator(w.partial)
		if separator < 0 {
			break
		}
		line := cleanPullProgressDetail(string(w.partial[:separator]))
		w.partial = w.partial[separator+1:]
		if line != "" {
			w.pendingDetail = line
			w.lastProgress = line
		}
	}
	if w.pendingDetail != "" && (w.lastEmitAt.IsZero() || now.Sub(w.lastEmitAt) >= pullProgressEmitInterval) {
		detail = w.pendingDetail
		w.pendingDetail = ""
		w.lastEmitAt = now
	}
	w.mu.Unlock()

	select {
	case w.activity <- struct{}{}:
	default:
	}
	if detail != "" && w.emit != nil {
		w.emitMu.Lock()
		w.emit(w.phase, detail)
		w.emitMu.Unlock()
	}
	return len(p), nil
}

func (w *pullCommandWriter) Flush() {
	w.mu.Lock()
	detail := cleanPullProgressDetail(string(w.partial))
	w.partial = nil
	if detail == "" {
		detail = w.pendingDetail
	}
	w.pendingDetail = ""
	if detail != "" {
		w.lastProgress = detail
	}
	w.mu.Unlock()

	if detail != "" && w.emit != nil {
		w.emitMu.Lock()
		w.emit(w.phase, detail)
		w.emitMu.Unlock()
	}
}

func (w *pullCommandWriter) Activity() <-chan struct{} {
	return w.activity
}

func (w *pullCommandWriter) LastActivityAt() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastActivityAt
}

func (w *pullCommandWriter) LastProgress() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastProgress
}

func (w *pullCommandWriter) Output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	output := strings.ReplaceAll(string(w.output), "\r", "\n")
	return strings.TrimSpace(output)
}

func (w *pullCommandWriter) appendOutput(p []byte) {
	if len(p) >= pullCommandOutputLimit {
		w.output = append(w.output[:0], p[len(p)-pullCommandOutputLimit:]...)
		return
	}
	overflow := len(w.output) + len(p) - pullCommandOutputLimit
	if overflow > 0 {
		copy(w.output, w.output[overflow:])
		w.output = w.output[:len(w.output)-overflow]
	}
	w.output = append(w.output, p...)
}

func pullProgressSeparator(value []byte) int {
	for i, b := range value {
		if b == '\r' || b == '\n' {
			return i
		}
	}
	return -1
}

func cleanPullProgressDetail(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "remote: ")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	cleaned := runes[:0]
	for _, r := range runes {
		if r >= ' ' && r != '\u007f' {
			cleaned = append(cleaned, r)
		}
	}
	const maxRunes = 240
	if len(cleaned) > maxRunes {
		cleaned = append(cleaned[:maxRunes-1], '…')
	}
	return string(cleaned)
}
