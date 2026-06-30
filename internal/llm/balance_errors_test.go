package llm

import (
	"fmt"
	"net/http"
	"testing"
)

func TestIsInsufficientBalanceError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "payment required status",
			err: &HTTPStatusError{
				StatusCode: http.StatusPaymentRequired,
				Status:     "402 Payment Required",
				Body:       `{"error":{"message":"Payment required"}}`,
			},
			want: true,
		},
		{
			name: "wrapped insufficient quota code",
			err: fmt.Errorf("commit assistant failed: %w", &HTTPStatusError{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Body:       `{"error":{"message":"You exceeded your current quota.","code":"insufficient_quota"}}`,
			}),
			want: true,
		},
		{
			name: "openrouter credits message",
			err: &HTTPStatusError{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Body:       `{"error":{"message":"Insufficient credits"}}`,
			},
			want: true,
		},
		{
			name: "plain balance text",
			err:  fmt.Errorf("provider request failed: insufficient balance"),
			want: true,
		},
		{
			name: "ordinary rate limit",
			err: &HTTPStatusError{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Body:       `{"error":{"message":"Rate limit exceeded","type":"rate_limit_exceeded"}}`,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsInsufficientBalanceError(tc.err); got != tc.want {
				t.Fatalf("IsInsufficientBalanceError() = %v, want %v", got, tc.want)
			}
		})
	}
}
