package scanner

import "testing"

func TestPathScopeAllowsIncludedPath(t *testing.T) {
	scope := NewPathScope([]string{"/tmp/work"}, nil)
	if !scope.Allows("/tmp/work/demo") {
		t.Fatalf("expected included child path to be allowed")
	}
	if scope.Allows("/tmp/other/demo") {
		t.Fatalf("expected path outside include paths to be rejected")
	}
}

func TestPathScopeExcludeOverridesInclude(t *testing.T) {
	scope := NewPathScope([]string{"/tmp/work"}, []string{"/tmp/work/archive"})
	if !scope.Allows("/tmp/work/demo") {
		t.Fatalf("expected non-excluded included path to be allowed")
	}
	if scope.Allows("/tmp/work/archive/old") {
		t.Fatalf("expected exclude path to win over include path")
	}
}

func TestPathScopeAlwaysExcludesManagedInternalPaths(t *testing.T) {
	scope := NewPathScope(nil, nil).WithAlwaysExcluded("/tmp/.little-control-room/internal-workspaces")

	if scope.Allows("/tmp/.little-control-room/internal-workspaces/lcroom-codex-helper-123") {
		t.Fatalf("expected managed internal workspace root to be rejected")
	}
	if scope.Allows("/var/folders/demo/lcroom-codex-helper-legacy") {
		t.Fatalf("expected legacy helper prefix path to be rejected")
	}
	if !scope.Allows("/tmp/demo") {
		t.Fatalf("expected unrelated path to remain allowed")
	}
}
