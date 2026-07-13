package buildinfo

import "strings"

var (
	version      = "dev"
	commit       = ""
	date         = ""
	distribution = "source"
)

func Version() string {
	return version
}

// Distribution identifies who owns updates for this build. Official GitHub
// release archives set this to "github". Source builds keep the default so a
// future package manager can opt out of the built-in updater explicitly.
func Distribution() string {
	return strings.ToLower(strings.TrimSpace(distribution))
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
	if value := Distribution(); value != "" && value != "source" {
		parts = append(parts, "distribution="+value)
	}
	return strings.Join(parts, " ")
}
