package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"lcroom/internal/events"
)

const defaultScheduledScanTimeout = 90 * time.Second
const scheduledScanFailureDetailLimit = 2000
const scheduledScanFailureRepeatInterval = 10 * time.Minute

func (s *Service) StartScheduler(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	wakeCh := s.schedulerWakeChannel()
	lastFailure := ""
	lastFailureAt := time.Time{}
	for {
		interval := s.Config().ScanInterval
		if interval <= 0 {
			select {
			case <-ctx.Done():
				return
			case <-wakeCh:
				continue
			}
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return
		case <-wakeCh:
			stopTimer(timer)
			continue
		case <-timer.C:
		}

		scanCtx, cancel := context.WithTimeout(ctx, s.scheduledScanTimeoutValue())
		_, err, started := s.tryScanWithOptions(scanCtx, ScanOptions{})
		cancel()
		if ctx.Err() != nil {
			return
		}
		if !started {
			continue
		}
		if err == nil {
			lastFailure = ""
			lastFailureAt = time.Time{}
			continue
		}

		detail := truncateRunes(strings.TrimSpace(err.Error()), scheduledScanFailureDetailLimit)
		now := time.Now()
		if detail == "" || (detail == lastFailure && now.Sub(lastFailureAt) < scheduledScanFailureRepeatInterval) {
			continue
		}
		lastFailure = detail
		lastFailureAt = now
		s.publishScheduledScanFailure(detail, errors.Is(err, context.DeadlineExceeded))
	}
}

func (s *Service) scheduledScanTimeoutValue() time.Duration {
	if s != nil && s.scheduledScanTimeout > 0 {
		return s.scheduledScanTimeout
	}
	return defaultScheduledScanTimeout
}

func (s *Service) publishScheduledScanFailure(detail string, timedOut bool) {
	if s == nil || s.bus == nil {
		return
	}
	payload := map[string]string{
		"source": "scheduled",
		"error":  strings.TrimSpace(detail),
	}
	if timedOut {
		payload["error_kind"] = "timeout"
	}
	s.bus.Publish(events.Event{
		Type:    events.ScanFailed,
		At:      time.Now(),
		Payload: payload,
	})
}

func stopTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
