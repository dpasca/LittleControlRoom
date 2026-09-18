//go:build darwin

package worktreerecovery

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestLegacyProvenanceMismatchResumesCopy(t *testing.T) {
	root, path, base, _ := fixture(t)
	ctx := context.Background()
	j, err := Prepare(ctx, base, root, path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a journal created before OS-assigned provenance was distinguished
	// from portable metadata. Do not try to forge the protected OS attribute.
	file := j.Sources[0].Files["source"]
	if file.Xattrs == nil {
		file.Xattrs = map[string][]byte{}
	}
	file.Xattrs["com.apple.provenance"] = []byte("old source provenance")
	j.Sources[0].Files["source"] = file
	j.Phase = "copying"
	j.Verified = time.Time{}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	j, err = Prepare(ctx, base, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Relocate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	// Other metadata and contents are still verification inputs.
	copy := j.VerifiedFiles["tree"]["source"]
	changed := copy
	changed.Digest = "different"
	if equalFile(copy, changed) {
		t.Fatal("ignored content change")
	}
	changed = copy
	changed.Xattrs = map[string][]byte{"user.recovery-test": []byte("new")}
	if equalFile(copy, changed) {
		t.Fatal("ignored user attribute change")
	}
}

func TestDigestReuseDetectsEditsWithRestoredModificationTime(t *testing.T) {
	root := t.TempDir()
	if !canCacheFileDigest(root) {
		t.Skip("requires APFS")
	}
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := WithProgress(context.Background(), nil)
	first, err := snapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	p := ctx.Value(progressKey{}).(*progressReporter)
	if len(p.hashes) != 1 {
		t.Fatal("digest not cached")
	}
	if err := verifyTree(ctx, root, first); err != nil {
		t.Fatal(err)
	}
	// Equal size and restored mtime must not make a write invisible.
	if err := os.WriteFile(path, []byte("after!"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(0, first["file"].Modified)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := verifyTree(ctx, root, first); err == nil {
		t.Fatal("cached digest hid a write")
	}
}

func BenchmarkRepeatedRecoveryVerification(b *testing.B) {
	root := b.TempDir()
	path := filepath.Join(root, "large-file")
	if !canCacheFileDigest(root) {
		b.Skip("requires APFS")
	}
	if err := os.WriteFile(path, make([]byte, 16*1024*1024), 0600); err != nil {
		b.Fatal(err)
	}
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprint(cached), func(b *testing.B) {
			ctx := context.Background()
			if cached {
				ctx = WithProgress(ctx, nil)
			}
			for n := 0; n < b.N; n++ {
				if _, err := snapshotEntries(ctx, root); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
