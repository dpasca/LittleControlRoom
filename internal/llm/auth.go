package llm

import (
	"net/http"
	"strings"
)

type OpenAICompatibleAuthHeader string

const (
	OpenAICompatibleAuthHeaderBearer OpenAICompatibleAuthHeader = "bearer"
	OpenAICompatibleAuthHeaderAPIKey OpenAICompatibleAuthHeader = "api-key"
)

func normalizeOpenAICompatibleAuthHeader(header OpenAICompatibleAuthHeader) OpenAICompatibleAuthHeader {
	switch strings.ToLower(strings.TrimSpace(string(header))) {
	case string(OpenAICompatibleAuthHeaderAPIKey):
		return OpenAICompatibleAuthHeaderAPIKey
	default:
		return OpenAICompatibleAuthHeaderBearer
	}
}

func setOpenAICompatibleAPIKeyHeader(header http.Header, apiKey string, authHeader OpenAICompatibleAuthHeader) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return
	}
	switch normalizeOpenAICompatibleAuthHeader(authHeader) {
	case OpenAICompatibleAuthHeaderAPIKey:
		header.Set("api-key", apiKey)
	default:
		header.Set("Authorization", "Bearer "+apiKey)
	}
}
