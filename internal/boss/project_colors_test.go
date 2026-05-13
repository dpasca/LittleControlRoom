package boss

import "testing"

func TestProjectIdentityColorsAreStableAndVaried(t *testing.T) {
	t.Parallel()

	if got, again := bossProjectIdentityColor("/tmp/alpha"), bossProjectIdentityColor("/tmp/alpha"); got != again {
		t.Fatalf("project identity color should be stable: %q then %q", got, again)
	}

	seen := map[string]bool{}
	for _, identity := range []string{"/tmp/alpha", "/tmp/beta", "/tmp/gamma", "/tmp/delta", "/tmp/epsilon", "/tmp/zeta"} {
		seen[string(bossProjectIdentityColor(identity))] = true
	}
	if len(seen) < 3 {
		t.Fatalf("project identity palette should vary across visible projects, got %d colors", len(seen))
	}
}
