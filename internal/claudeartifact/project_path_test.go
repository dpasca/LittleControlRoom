package claudeartifact

import "testing"

func TestProjectDirectoryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{
			input: "/Users/davide/dev/repos/Foo",
			want:  "-Users-davide-dev-repos-Foo",
		},
		{
			input: "/Users/davide/dev/repos/demo_tviking--improve-techno-viking-model",
			want:  "-Users-davide-dev-repos-demo-tviking--improve-techno-viking-model",
		},
		{
			input: "/Users/davide/Library/CloudStorage/Dropbox/Family Room/jun_it_citizenship",
			want:  "-Users-davide-Library-CloudStorage-Dropbox-Family-Room-jun-it-citizenship",
		},
		{
			input: "/tmp/.hidden@project",
			want:  "-tmp--hidden-project",
		},
	}
	for _, tt := range tests {
		if got := ProjectDirectoryName(tt.input); got != tt.want {
			t.Errorf("ProjectDirectoryName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
