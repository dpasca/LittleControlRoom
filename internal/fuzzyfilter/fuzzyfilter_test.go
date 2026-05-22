package fuzzyfilter

import "testing"

func TestMatchAcceptsFragmentsAndFuzzyInitials(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		candidates []string
		want       bool
	}{
		{
			name:       "case insensitive fragment",
			query:      "control",
			candidates: []string{"LittleControlRoom"},
			want:       true,
		},
		{
			name:       "separator insensitive fragment",
			query:      "helpertools",
			candidates: []string{"helper-tools"},
			want:       true,
		},
		{
			name:       "ordered fuzzy initials",
			query:      "lcr",
			candidates: []string{"LittleControlRoom"},
			want:       true,
		},
		{
			name:       "all tokens must match",
			query:      "little room",
			candidates: []string{"LittleControlRoom"},
			want:       true,
		},
		{
			name:       "tokens may match different candidates",
			query:      "little browser",
			candidates: []string{"LittleControlRoom", "/tmp/session-browser"},
			want:       true,
		},
		{
			name:       "out of order misses",
			query:      "rlc",
			candidates: []string{"LittleControlRoom"},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Match(tt.query, tt.candidates...); got != tt.want {
				t.Fatalf("Match(%q, %#v) = %v, want %v", tt.query, tt.candidates, got, tt.want)
			}
		})
	}
}
