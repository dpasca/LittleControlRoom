package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"lcroom/internal/events"
)

const serveScanEventLogRepeatWindow = 10 * time.Minute

func logServeScanEvents(ctx context.Context, eventCh <-chan events.Event, output io.Writer) {
	if ctx == nil {
		ctx = context.Background()
	}
	if output == nil {
		output = io.Discard
	}
	recentMessages := make(map[string]time.Time)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			message := serveScanEventLogMessage(event)
			if message == "" {
				continue
			}
			at := event.At
			if at.IsZero() {
				at = time.Now()
			}
			if previousAt, seen := recentMessages[message]; seen {
				elapsed := at.Sub(previousAt)
				if elapsed >= 0 && elapsed <= serveScanEventLogRepeatWindow {
					continue
				}
			}
			recentMessages[message] = at
			pruneServeScanEventLogMessages(recentMessages, at)
			fmt.Fprintln(output, message)
		}
	}
}

func pruneServeScanEventLogMessages(recentMessages map[string]time.Time, now time.Time) {
	if len(recentMessages) <= 64 {
		return
	}
	oldestMessage := ""
	oldestAt := now
	for message, at := range recentMessages {
		if elapsed := now.Sub(at); elapsed > serveScanEventLogRepeatWindow {
			delete(recentMessages, message)
			continue
		}
		if oldestMessage == "" || at.Before(oldestAt) {
			oldestMessage = message
			oldestAt = at
		}
	}
	if len(recentMessages) > 64 && oldestMessage != "" {
		delete(recentMessages, oldestMessage)
	}
}

func serveScanEventLogMessage(event events.Event) string {
	switch event.Type {
	case events.ScanFailed:
		label := "scheduled scan failed"
		if strings.TrimSpace(event.Payload["error_kind"]) == "timeout" {
			label = "scheduled scan timed out"
		}
		detail := strings.Join(strings.Fields(event.Payload["error"]), " ")
		if detail == "" {
			detail = "the project scan did not complete"
		}
		return label + ": " + detail
	case events.ScanCompleted:
		count := strings.TrimSpace(event.Payload["git_metadata_timeouts"])
		if count == "" || count == "0" {
			return ""
		}
		label := "projects"
		if count == "1" {
			label = "project"
		}
		message := fmt.Sprintf("scan warning: Git metadata reads timed out for %s %s", count, label)
		paths := make([]string, 0, 8)
		for _, path := range strings.Split(event.Payload["git_metadata_timeout_path_samples"], "\n") {
			if path = strings.TrimSpace(path); path != "" {
				paths = append(paths, path)
			}
		}
		if len(paths) > 0 {
			message += ": " + strings.Join(paths, ", ")
		}
		return message
	default:
		return ""
	}
}
