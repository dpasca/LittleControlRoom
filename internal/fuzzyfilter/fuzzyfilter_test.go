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

func TestMatchWithFragmentsKeepsFuzzyOffLongText(t *testing.T) {
	folder := "build/review-clips/fe-1b/"
	if !MatchWithFragments("clips", []string{"title.mp4"}, []string{folder}) {
		t.Fatal("folder fragment should match")
	}
	if !MatchWithFragments("fe1b title", []string{"title.mp4"}, []string{folder}) {
		t.Fatal("tokens may match the name and the folder")
	}
	if MatchWithFragments("zzz", []string{"title.mp4"}, nil) {
		t.Fatal("unmatched tokens must fail")
	}
	if MatchWithFragments("acx", []string{"beta_notes.md"}, []string{"/var/folders/fy/9y_kz/T/TestA/001/"}) {
		t.Fatal("ordered characters must not match across a long folder path")
	}
}
