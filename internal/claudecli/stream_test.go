package claudecli

import "testing"

func TestManagedStreamVersion(t *testing.T) {
	for _, tt := range []struct {
		version string
		want    bool
	}{
		{"2.1.284 (Claude Code)", true}, {"2.1.285 (Claude Code)\n", true}, {"2.2.0", true}, {"3.0.0", true},
		{"2.1.283", false}, {"2.0.999", false}, {"1.9.999", false}, {"", false},
		{"error running Claude", false}, {"2.1.284-dev", false}, {"2.1.284.extra", false},
	} {
		t.Run(tt.version, func(t *testing.T) {
			if got := supportsManagedStreamVersion(tt.version); got != tt.want {
				t.Fatalf("supports(%q) = %v", tt.version, got)
			}
		})
	}
}
