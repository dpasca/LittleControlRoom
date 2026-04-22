package sessionclassify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"

	"lcroom/internal/llm"
)

type classificationFailureKind string

const (
	classificationFailureKindTimeout            classificationFailureKind = "timeout"
	classificationFailureKindConnectionFailed   classificationFailureKind = "connection_failed"
	classificationFailureKindOpenFileLimit      classificationFailureKind = "open_file_limit"
	classificationFailureKindRateLimited        classificationFailureKind = "rate_limited"
	classificationFailureKindServiceUnavailable classificationFailureKind = "service_unavailable"
)

func classificationFailureMetadata(err error) (classificationFailureKind, string) {
	normalized := normalizedClassificationErrorMessage(err)
	switch {
	case isClassificationRateLimitedError(err, normalized):
		return classificationFailureKindRateLimited, "model provider rate limited the assessment request"
	case isClassificationServiceUnavailableError(err, normalized):
		return classificationFailureKindServiceUnavailable, "model provider was unavailable while assessing the latest session"
	case isClassificationTimeoutError(err, normalized):
		return classificationFailureKindTimeout, "request timed out while contacting the model; network connectivity or provider availability may be degraded"
	case isClassificationOpenFileLimitError(err, normalized):
		return classificationFailureKindOpenFileLimit, "local open-file limit was reached while assessing the latest session; too many helper processes or open files may already be active"
	case isClassificationConnectionFailureError(err, normalized):
		return classificationFailureKindConnectionFailed, "could not reach the model; network connectivity or provider availability may be degraded"
	default:
		return "", ""
	}
}

func normalizedClassificationErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(err.Error()))
}

func isClassificationRateLimitedError(err error, normalized string) bool {
	var httpErr *llm.HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests
	}
	return strings.Contains(normalized, "429") ||
		strings.Contains(normalized, "too many requests") ||
		strings.Contains(normalized, "rate limit")
}

func isClassificationServiceUnavailableError(err error, normalized string) bool {
	var httpErr *llm.HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusRequestTimeout || httpErr.StatusCode >= 500
	}
	return strings.Contains(normalized, "503") ||
		strings.Contains(normalized, "502") ||
		strings.Contains(normalized, "504") ||
		strings.Contains(normalized, "500") ||
		strings.Contains(normalized, "service unavailable") ||
		strings.Contains(normalized, "bad gateway")
}

func isClassificationTimeoutError(err error, normalized string) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}
	return strings.Contains(normalized, "context deadline exceeded") ||
		strings.Contains(normalized, "i/o timeout") ||
		strings.Contains(normalized, "timeout awaiting response headers") ||
		strings.Contains(normalized, "client.timeout exceeded")
}

func isClassificationOpenFileLimitError(err error, normalized string) bool {
	return errors.Is(err, syscall.EMFILE) ||
		errors.Is(err, syscall.ENFILE) ||
		strings.Contains(normalized, "too many open files") ||
		strings.Contains(normalized, "os error 24")
}

func isClassificationConnectionFailureError(err error, normalized string) bool {
	if isClassificationTimeoutError(err, normalized) {
		return false
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	return errors.Is(err, syscall.EADDRNOTAVAIL) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTDOWN) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETDOWN) ||
		errors.Is(err, syscall.ENETRESET) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EPIPE) ||
		strings.Contains(normalized, "connection refused") ||
		strings.Contains(normalized, "connection reset by peer") ||
		strings.Contains(normalized, "broken pipe") ||
		strings.Contains(normalized, "dial tcp") ||
		strings.Contains(normalized, "no such host") ||
		strings.Contains(normalized, "network is unreachable")
}
