package llm

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

func IsInsufficientBalanceError(err error) bool {
	if err == nil {
		return false
	}

	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) && httpErr != nil {
		if httpErr.StatusCode == http.StatusPaymentRequired {
			return true
		}
		if providerBalanceBodyTextIndicatesInsufficientBalance(httpErr.Body) {
			return true
		}
	}

	return providerBalanceTextIndicatesInsufficientBalance(err.Error())
}

func providerBalanceBodyTextIndicatesInsufficientBalance(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}

	var payload any
	if err := json.Unmarshal([]byte(body), &payload); err == nil {
		for _, value := range providerBalanceStrings(payload) {
			if providerBalanceTextIndicatesInsufficientBalance(value) {
				return true
			}
		}
		return false
	}

	return providerBalanceTextIndicatesInsufficientBalance(body)
}

func providerBalanceStrings(value any) []string {
	switch typed := value.(type) {
	case map[string]any:
		out := []string{}
		for key, nested := range typed {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "code", "type", "message", "error", "reason", "detail", "details":
				if text, ok := nested.(string); ok {
					out = append(out, text)
					continue
				}
			}
			out = append(out, providerBalanceStrings(nested)...)
		}
		return out
	case []any:
		out := []string{}
		for _, nested := range typed {
			out = append(out, providerBalanceStrings(nested)...)
		}
		return out
	case string:
		return []string{typed}
	default:
		return nil
	}
}

func providerBalanceTextIndicatesInsufficientBalance(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if normalized == "" {
		return false
	}

	for _, token := range []string{
		"insufficient_quota",
		"insufficient quota",
		"insufficient_balance",
		"insufficient balance",
		"insufficient credits",
		"insufficient credit",
		"insufficient funds",
		"not enough credits",
		"not enough credit",
		"out of credits",
		"out of credit",
		"credit balance",
		"billing hard limit",
		"payment required",
	} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}
