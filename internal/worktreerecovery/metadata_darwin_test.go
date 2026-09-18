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
				cmd = exec.Command("/usr/bin/chflags", "uchg", path)
				t.Cleanup(func() { _ = exec.Command("/usr/bin/chflags", "nouchg", path).Run() })
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

func TestRecoveryPreservesHarmlessMacFlags(t *testing.T) {
	root, path, base, _ := fixture(t)
	file := filepath.Join(path, ".DS_Store")
	if err := os.WriteFile(file, []byte("finder data"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/bin/chflags", "hidden,nodump", file).CombinedOutput(); err != nil {
		t.Fatalf("flags: %s %v", out, err)
	}
	link := filepath.Join(path, "dependency", "absolute-link")
	if out, err := exec.Command("/usr/bin/chflags", "-h", "hidden", link).CombinedOutput(); err != nil {
		t.Fatalf("link flags: %s %v", out, err)
	}
	ctx := context.Background()
	j, err := Prepare(ctx, base, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Relocate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(filepath.Dir(root), "restored")
	if err := j.Restore(ctx, destination); err != nil {
		t.Fatal(err)
	}
	files, err := snapshot(ctx, filepath.Join(destination, "tree"))
	if err != nil {
		t.Fatal(err)
	}
	if files[".DS_Store"].Flags != 0x8001 {
		t.Fatalf("flags lost: %#v", files[".DS_Store"])
	}
	if files["dependency/absolute-link"].Flags != 0x8000 {
		t.Fatal("pointer repair lost symlink flags")
	}
	if out, err := exec.Command("/usr/bin/chflags", "nohidden", filepath.Join(j.Directory, "tree", ".DS_Store")).CombinedOutput(); err != nil {
		t.Fatalf("alter flags: %s %v", out, err)
	}
	if err := j.Verify(ctx); err == nil {
		t.Fatal("flag change bypassed verification")
	}
}
