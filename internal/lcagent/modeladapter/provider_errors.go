package modeladapter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ProviderFailureKind string

const (
	ProviderFailureAuth              ProviderFailureKind = "auth"
	ProviderFailureQuota             ProviderFailureKind = "quota"
	ProviderFailureRateLimited       ProviderFailureKind = "rate_limited"
	ProviderFailureTimeout           ProviderFailureKind = "timeout"
	ProviderFailureTransientHTTP     ProviderFailureKind = "transient_http"
	ProviderFailureMalformedResponse ProviderFailureKind = "malformed_response"
	ProviderFailureSchema            ProviderFailureKind = "provider_schema"
)

type ProviderError struct {
	Provider   string
	Kind       ProviderFailureKind
	StatusCode int
	Message    string
	Retryable  bool
	RetryAfter time.Duration
	Cause      error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{}
	if provider := strings.TrimSpace(e.Provider); provider != "" {
		parts = append(parts, provider)
	}
	if e.Kind != "" {
		parts = append(parts, string(e.Kind))
	}
	message := strings.TrimSpace(e.Message)
	if message == "" && e.Cause != nil {
		message = e.Cause.Error()
	}
	if message == "" {
		message = "provider request failed"
	}
	if len(parts) == 0 {
		return message
	}
	return strings.Join(parts, " ") + ": " + message
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func AsProviderError(err error) (*ProviderError, bool) {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr != nil {
		return providerErr, true
	}
	return nil, false
}

func newProviderRequestError(provider string, err error) error {
	if err == nil {
		return nil
	}
	kind := ProviderFailureTransientHTTP
	retryable := true
	if errors.Is(err, context.Canceled) {
		retryable = false
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err) {
		kind = ProviderFailureTimeout
		retryable = true
	}
	return &ProviderError{
		Provider:  provider,
		Kind:      kind,
		Message:   err.Error(),
		Retryable: retryable,
		Cause:     err,
	}
}

func newProviderHTTPError(provider string, statusCode int, message, retryAfterHeader string) error {
	kind, retryable := classifyProviderHTTPFailure(statusCode)
	return &ProviderError{
		Provider:   provider,
		Kind:       kind,
		StatusCode: statusCode,
		Message:    providerHTTPFailureMessage(statusCode, message),
		Retryable:  retryable,
		RetryAfter: parseRetryAfter(retryAfterHeader),
	}
}

func newProviderDecodeError(provider string, err error) error {
	return &ProviderError{
		Provider:  provider,
		Kind:      ProviderFailureMalformedResponse,
		Message:   "response decode failed: " + err.Error(),
		Retryable: false,
		Cause:     err,
	}
}

func newProviderSchemaError(provider, message string) error {
	return &ProviderError{
		Provider:  provider,
		Kind:      ProviderFailureSchema,
		Message:   strings.TrimSpace(message),
		Retryable: false,
	}
}

func newProviderBodyError(provider, message, errorType string) error {
	text := strings.TrimSpace(firstNonEmpty(message, errorType, "provider returned an error"))
	kind, retryable := classifyProviderErrorText(text + " " + errorType)
	return &ProviderError{
		Provider:  provider,
		Kind:      kind,
		Message:   text,
		Retryable: retryable,
	}
}

func classifyProviderHTTPFailure(statusCode int) (ProviderFailureKind, bool) {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ProviderFailureAuth, false
	case http.StatusPaymentRequired:
		return ProviderFailureQuota, false
	case http.StatusTooManyRequests:
		return ProviderFailureRateLimited, true
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return ProviderFailureTimeout, true
	case http.StatusConflict, http.StatusTooEarly:
		return ProviderFailureTransientHTTP, true
	default:
		if statusCode >= 500 && statusCode <= 599 {
			return ProviderFailureTransientHTTP, true
		}
		return ProviderFailureSchema, false
	}
}

func classifyProviderErrorText(text string) (ProviderFailureKind, bool) {
	normalized := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(normalized, "rate limit"), strings.Contains(normalized, "rate_limit"), strings.Contains(normalized, "too many request"), strings.Contains(normalized, "too_many"):
		return ProviderFailureRateLimited, true
	case strings.Contains(normalized, "quota"), strings.Contains(normalized, "insufficient_quota"), strings.Contains(normalized, "credit"):
		return ProviderFailureQuota, false
	case strings.Contains(normalized, "api key"), strings.Contains(normalized, "api_key"), strings.Contains(normalized, "unauthorized"), strings.Contains(normalized, "forbidden"), strings.Contains(normalized, "auth"):
		return ProviderFailureAuth, false
	case strings.Contains(normalized, "timeout"), strings.Contains(normalized, "timed out"):
		return ProviderFailureTimeout, true
	default:
		return ProviderFailureSchema, false
	}
}

func providerHTTPFailureMessage(statusCode int, message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Sprintf("HTTP %d", statusCode)
	}
	return fmt.Sprintf("HTTP %d: %s", statusCode, message)
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := time.Parse(http.TimeFormat, value); err == nil {
		delay := time.Until(at)
		if delay > 0 {
			return delay
		}
	}
	return 0
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
