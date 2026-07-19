package cli

import (
	"testing"

	"lcroom/internal/demorecord"
)

func TestSelectDemoClipByIDOrOneBasedNumber(t *testing.T) {
	t.Parallel()

	clips := []demorecord.Clip{
		{ID: "overview", Name: "Overview", InMS: 0, OutMS: 1000},
		{ID: "details", Name: "Details", InMS: 1000, OutMS: 2000},
	}
	for _, test := range []struct {
		selector string
		wantID   string
	}{
		{selector: "overview", wantID: "overview"},
		{selector: "2", wantID: "details"},
	} {
		clip, err := selectDemoClip(clips, test.selector)
		if err != nil {
			t.Fatalf("selectDemoClip(%q): %v", test.selector, err)
		}
		if clip.ID != test.wantID {
			t.Fatalf("selectDemoClip(%q) = %q, want %q", test.selector, clip.ID, test.wantID)
		}
	}
}

func TestSelectDemoClipRequiresSelectorWhenSeveralExist(t *testing.T) {
	t.Parallel()

	clips := []demorecord.Clip{
		{ID: "one", InMS: 0, OutMS: 1000},
		{ID: "two", InMS: 1000, OutMS: 2000},
	}
	if _, err := selectDemoClip(clips, ""); err == nil {
		t.Fatal("selectDemoClip accepted an ambiguous empty selector")
	}
}
