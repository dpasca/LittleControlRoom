package buildinfo

import "strings"

var (
	version = "dev"
	commit  = ""
	date    = ""
)

func Version() string {
	return version
}

func Summary(binary string) string {
	parts := []string{strings.TrimSpace(binary), Version()}
	if strings.TrimSpace(parts[0]) == "" {
		parts[0] = "lcroom"
	}
	if strings.TrimSpace(commit) != "" {
		parts = append(parts, "commit="+strings.TrimSpace(commit))
	}
	if strings.TrimSpace(date) != "" {
		parts = append(parts, "date="+strings.TrimSpace(date))
	}
	return strings.Join(parts, " ")
}
