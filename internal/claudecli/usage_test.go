package claudecli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPlanUsageReaderReadsFiveHourAndWeeklyWindows(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got, want := req.Header.Get("Authorization"), "Bearer oauth-token"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		if got, want := req.Header.Get("Anthropic-Beta"), "oauth-2025-04-20"; got != want {
			t.Errorf("Anthropic-Beta = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"five_hour":{"utilization":17.25,"resets_at":"2026-08-01T02:50:00.673899+00:00"},
			"seven_day":{"utilization":3,"resets_at":"2026-08-07T13:00:00Z"}
		}`)
	}))
	defer server.Close()

	reader := &PlanUsageReader{
		HTTPClient: server.Client(),
		Endpoint:   server.URL,
		ReadAccessToken: func(context.Context, string) (string, bool, error) {
			return "oauth-token", true, nil
		},
		LookupEnv: func(string) (string, bool) { return "", false },
	}
	usage, err := reader.Read(context.Background(), "/unused")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !usage.Available || usage.FiveHour == nil || usage.SevenDay == nil {
		t.Fatalf("usage = %#v, want both subscription windows", usage)
	}
	if got, want := usage.FiveHour.Utilization, 17.25; got != want {
		t.Fatalf("five-hour utilization = %v, want %v", got, want)
	}
	if got, want := usage.SevenDay.ResetsAt, time.Date(2026, 8, 7, 13, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("weekly reset = %v, want %v", got, want)
	}
}

func TestPlanUsageReaderSkipsSubscriptionUsageWhenAPIKeyWins(t *testing.T) {
	t.Parallel()

	credentialReads := 0
	reader := &PlanUsageReader{
		ReadAccessToken: func(context.Context, string) (string, bool, error) {
			credentialReads++
			return "oauth-token", true, nil
		},
		LookupEnv: func(key string) (string, bool) {
			if key == "ANTHROPIC_API_KEY" {
				return "api-key", true
			}
			return "", false
		},
	}
	usage, err := reader.Read(context.Background(), "/unused")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if usage.Available {
		t.Fatalf("usage = %#v, want subscription usage unavailable", usage)
	}
	if credentialReads != 0 {
		t.Fatalf("credential reads = %d, want none when API-key billing is active", credentialReads)
	}
}

func TestParseStoredClaudeAccessToken(t *testing.T) {
	t.Parallel()

	token, ok := parseStoredClaudeAccessToken([]byte(`{"claudeAiOauth":{"accessToken":" secret "}}`))
	if !ok || token != "secret" {
		t.Fatalf("parseStoredClaudeAccessToken() = %q, %t; want secret, true", token, ok)
	}
	if _, ok := parseStoredClaudeAccessToken([]byte(`{"claudeAiOauth":{}}`)); ok {
		t.Fatal("empty stored credential reported an access token")
	}
}
