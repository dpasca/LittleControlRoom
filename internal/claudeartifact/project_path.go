package claudeartifact

import (
	"path/filepath"
	"strings"
)

// ProjectDirectoryName converts a working directory to the sanitized directory
// name Claude Code uses under ~/.claude/projects.
func ProjectDirectoryName(projectPath string) string {
	cleaned := filepath.Clean(projectPath)
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-':
			return r
		default:
			return '-'
		}
	}, cleaned)
}
