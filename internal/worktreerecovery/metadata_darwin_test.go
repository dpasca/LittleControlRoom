//go:build darwin

package worktreerecovery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryBlocksUnsupportedMacMetadata(t *testing.T) {
	for _, kind := range []string{"ACL", "flags"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "data")
			if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("/bin/chmod", "+a", "everyone allow read", path)
			if kind == "flags" {
				cmd = exec.Command("/usr/bin/chflags", "hidden", path)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("set fixture metadata: %s %v", out, err)
			}
			if _, err := snapshot(context.Background(), root); err == nil || !strings.Contains(err.Error(), kind) {
				t.Fatalf("unsupported %s not reported: %v", kind, err)
			}
		})
	}
}
