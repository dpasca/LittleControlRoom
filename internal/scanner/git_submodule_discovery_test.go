package scanner

import (
	"path/filepath"
	"testing"
)

func TestSubmoduleGitDirectoryOwnership(t *testing.T) {
	for _, test := range []struct {
		path string
		want bool
	}{
		{"repo/.git/modules/Apps/Game/Assets", true},
		{"repo/.git/worktrees/feature/modules/Apps/Game/Assets", true},
		{"repo/.git/modules/Assets/modules/textures", true},
		{"repo/.git", false},
		{"Assets/.git", false},
		{"modules/Assets/.git", false},
		{"repo/.git/worktrees/modules", false},
	} {
		t.Run(test.path, func(t *testing.T) {
			if got := isSubmoduleGitDir(filepath.Join(t.TempDir(), filepath.FromSlash(test.path))); got != test.want {
				t.Fatalf("isSubmoduleGitDir() = %v, want %v", got, test.want)
			}
		})
	}
}
