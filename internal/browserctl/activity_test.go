package browserctl

import (
	"strings"
	"testing"
)

func TestSessionActivityAttentionMessageOnlySurvivesUserWait(t *testing.T) {
	waiting := SessionActivity{
		State:            SessionActivityStateWaitingForUser,
		AttentionMessage: "  Sign in to the managed browser.  ",
	}.Normalize()
	if got, want := waiting.AttentionMessage, "Sign in to the managed browser."; got != want {
		t.Fatalf("waiting attention message = %q, want %q", got, want)
	}

	active := waiting
	active.State = SessionActivityStateActive
	if got := active.Normalize().AttentionMessage; got != "" {
		t.Fatalf("active attention message = %q, want empty", got)
	}
}

func TestSessionActivityAttentionMessageIsBounded(t *testing.T) {
	activity := SessionActivity{
		State:            SessionActivityStateWaitingForUser,
		AttentionMessage: strings.Repeat("界", 900),
	}.Normalize()
	if got := len([]rune(activity.AttentionMessage)); got != 800 {
		t.Fatalf("attention message runes = %d, want 800 including ellipsis", got)
	}
}

func TestSessionActivityAttentionMessageRemovesTerminalControls(t *testing.T) {
	activity := SessionActivity{
		State:            SessionActivityStateWaitingForUser,
		AttentionMessage: "Approve\x1b[31m login\x00 now.\nThen return.",
	}.Normalize()
	if got, want := activity.AttentionMessage, "Approve[31m login now.\nThen return."; got != want {
		t.Fatalf("attention message = %q, want %q", got, want)
	}
}
