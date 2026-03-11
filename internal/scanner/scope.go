package scanner

import (
	"path/filepath"
	"strings"
)

type PathScope struct {
	IncludePaths []string
	ExcludePaths []string
}

func NewPathScope(includePaths, excludePaths []string) PathScope {
	return PathScope{
		IncludePaths: append([]string(nil), includePaths...),
		ExcludePaths: append([]string(nil), excludePaths...),
	}
}

func (s PathScope) Allows(path string) bool {
	cleanPath := filepath.Clean(path)
	if isWithinAnyPrefix(cleanPath, s.ExcludePaths) {
		return false
	}
	if len(s.IncludePaths) == 0 {
		return true
	}
	return isWithinAnyPrefix(cleanPath, s.IncludePaths)
}

func isWithinAnyPrefix(path string, prefixes []string) bool {
	path = filepath.Clean(path)
	for _, prefix := range prefixes {
		prefix = filepath.Clean(prefix)
		if path == prefix {
			return true
		}
		rel, err := filepath.Rel(prefix, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
