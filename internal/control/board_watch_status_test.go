package control

import (
	"testing"
	"time"
)

// Review state and mailbox delivery are separate facts: a canceled or expired
// request whose wake was actually claimed must report the real delivery.
func TestDeliveryReportsActualMailboxFactsBeforeTerminalReviewStates(t *testing.T) {
	now := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	for _, review := range []BoardWatchEventState{BoardWatchEventCanceled, BoardWatchEventExpired, BoardWatchEventReviewed, BoardWatchEventQueued} {
		for _, mailbox := range []EngineerMessageState{EngineerMessageDelivering, EngineerMessageDelivered} {
			status := BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: review, MessageID: "m1"}, MessageState: mailbox}
			if got := status.Delivery(now); got != string(mailbox) {
				t.Errorf("review %s with mailbox %s reported delivery=%s", review, mailbox, got)
			}
		}
	}
}

func TestDeliveryFallsBackToReviewStateWhenNothingWasDelivered(t *testing.T) {
	now := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	cases := []struct {
		name   string
		status BoardWatchEventStatus
		want   string
	}{
		{"canceled and withdrawn", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventCanceled, MessageID: "m"}, MessageState: EngineerMessageFailed}, "canceled"},
		{"canceled before any wake", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventCanceled}}, "canceled"},
		{"canceled with wake still queued", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventCanceled, MessageID: "m"}, MessageState: EngineerMessageQueued}, "canceled"},
		{"expired at read time", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventExpired}}, "expired"},
		{"expired with failed wake", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventExpired, MessageID: "m"}, MessageState: EngineerMessageFailed}, "expired"},
		{"pending", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventPending}}, "pending_enqueue"},
		{"pending with an unlinked queued wake", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventPending, MessageID: "m"}, MessageState: EngineerMessageQueued}, "queued"},
		{"reviewed without a wake", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventReviewed}}, "not_sent"},
		{"queued", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventQueued, MessageID: "m"}, MessageState: EngineerMessageQueued}, "queued"},
		{"queued waiting", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventQueued, MessageID: "m"}, MessageState: EngineerMessageQueued, MessageError: "Recipient busy"}, "queued_waiting"},
		{"failed", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventQueued, MessageID: "m"}, MessageState: EngineerMessageFailed, MessageExpiresAt: future}, "failed"},
		{"failed after deadline", BoardWatchEventStatus{BoardWatchEvent: BoardWatchEvent{State: BoardWatchEventQueued, MessageID: "m"}, MessageState: EngineerMessageFailed, MessageExpiresAt: past}, "expired"},
	}
	for _, tc := range cases {
		if got := tc.status.Delivery(now); got != tc.want {
			t.Errorf("%s: delivery = %s, want %s", tc.name, got, tc.want)
		}
	}
}
