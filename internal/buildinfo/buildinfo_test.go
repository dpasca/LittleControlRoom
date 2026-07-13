package buildinfo

import (
	"strings"
	"testing"
)

func TestSummaryIncludesNonSourceDistribution(t *testing.T) {
	originalVersion, originalCommit, originalDate, originalDistribution := version, commit, date, distribution
	t.Cleanup(func() {
		version, commit, date, distribution = originalVersion, originalCommit, originalDate, originalDistribution
	})
	version = "v1.2.3"
	commit = "abc123"
	date = "2026-07-13"
	distribution = "github"
	got := Summary("lcroom")
	for _, want := range []string{"lcroom v1.2.3", "commit=abc123", "date=2026-07-13", "distribution=github"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Summary() = %q, want %q", got, want)
		}
	}
}

func TestSummaryOmitsSourceDistribution(t *testing.T) {
	originalDistribution := distribution
	t.Cleanup(func() { distribution = originalDistribution })
	distribution = "source"
	if got := Summary("lcroom"); strings.Contains(got, "distribution=") {
		t.Fatalf("Summary() = %q, source distribution should stay implicit", got)
	}
}
