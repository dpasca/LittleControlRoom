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

	"golang.org/x/sys/unix"
)

func run(t *testing.T, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestUnbornHeadDoesNotHideBrokenRef(t *testing.T) {
	path := t.TempDir()
	run(t, path, "init", "-b", "master")
	head, err := repositoryHead(context.Background(), path)
	if err != nil || head != "unborn:refs/heads/master" {
		t.Fatalf("unborn HEAD: %q %v", head, err)
	}
	if err := os.WriteFile(filepath.Join(path, ".git", "refs", "heads", "master"), []byte("broken ref\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if head, err := repositoryHead(context.Background(), path); err == nil {
		t.Fatalf("broken ref accepted as %q", head)
	}
}

func TestRecoveryResumesBetweenPromotionRenames(t *testing.T) {
	for _, stage := range []string{"copy moved", "original promoted"} {
		t.Run(stage, func(t *testing.T) {
			root, path, base, _ := fixture(t)
			ctx := context.Background()
			j, err := Prepare(ctx, base, root, path)
			if err != nil {
				t.Fatal(err)
			}
			originalInfo, err := os.Stat(filepath.Join(path, "source"))
			if err != nil {
				t.Fatal(err)
			}
			if err := j.Relocate(ctx); err != nil {
				t.Fatal(err)
			}
			j.Phase = "promoting"
			if err := j.save(); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(j.Directory, "tree"), j.DuplicatePath); err != nil {
				t.Fatal(err)
			}
			if stage == "original promoted" {
				if err := os.Rename(j.QuarantinePath, filepath.Join(j.Directory, "tree")); err != nil {
					t.Fatal(err)
				}
			}
			j, err = Load(j.Directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.VerifyResume(ctx); err != nil {
				t.Fatal(err)
			}
			if err := j.Relocate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := j.Complete(ctx); err != nil {
				t.Fatal(err)
			}
			retained, err := os.Stat(filepath.Join(j.Directory, "tree", "source"))
			if err != nil || !os.SameFile(originalInfo, retained) {
				t.Fatalf("original inode not retained: %v", err)
			}
			if _, err := os.Lstat(j.DuplicatePath); !os.IsNotExist(err) {
				t.Fatal("duplicate retained after completion")
			}
		})
	}
}

func TestRecoveryResumesIncompleteCopyWithoutDuplicateBackup(t *testing.T) {
	root, path, base, _ := fixture(t)
	ctx := context.Background()
	j, err := Prepare(ctx, base, root, path)
	if err != nil {
		t.Fatal(err)
	}
	j.Phase = "copying"
	j.Verified = time.Time{}
	j.VerifiedFiles = nil
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(j.Directory, "tree", "dependency", "local")); err != nil {
		t.Fatal(err)
	}
	resumed, err := Prepare(ctx, base, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Directory != j.Directory {
		t.Fatal("retry created a duplicate recovery")
	}
	if err := resumed.Verify(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryRejectsSelectedSymlink(t *testing.T) {
	root, path, base, _ := fixture(t)
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), base, root, link); err == nil {
		t.Fatal("selected symlink followed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("symlink target lost")
	}
}

func TestRecoveryRegistrationMutationBoundaries(t *testing.T) {
	for _, stage := range []string{"before move", "after move", "concurrent change"} {
		t.Run(stage, func(t *testing.T) {
			root, path, base, _ := fixture(t)
			ctx := context.Background()
			j, err := Prepare(ctx, base, root, path)
			if err != nil {
				t.Fatal(err)
			}
			from := j.Repositories[0].GitDir
			if stage == "concurrent change" {
				if err := os.WriteFile(filepath.Join(from, "HEAD"), []byte("ref: refs/heads/master\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := j.PreserveRegistration(ctx, from); err == nil {
					t.Fatal("concurrent metadata change accepted")
				}
				return
			}
			files, err := snapshot(ctx, from)
			if err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(j.Directory, "registrations", filepath.Base(Location("", from)))
			if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
				t.Fatal(err)
			}
			j.MetadataMoves = append(j.MetadataMoves, MetadataMove{Original: from, Destination: dst, Files: files})
			if err := j.save(); err != nil {
				t.Fatal(err)
			}
			if stage == "after move" {
				if err := os.Rename(from, dst); err != nil {
					t.Fatal(err)
				}
			}
			j, err = Load(j.Directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.PreserveRegistration(ctx, from); err != nil {
				t.Fatal(err)
			}
			if err := j.PreserveRegistration(ctx, from); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecoveryCopiesExternalAlternateClosure(t *testing.T) {
	root, path, base, _ := fixture(t)
	ctx := context.Background()
	external := filepath.Join(filepath.Dir(root), "external.git")
	run(t, root, "init", "--bare", external)
	object := run(t, external, "hash-object", "-w", filepath.Join(path, "dependency", "local"))
	if err := os.WriteFile(filepath.Join(path, "cache", "objects", "info", "alternates"), []byte(filepath.Join(external, "objects")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
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
	if err := os.RemoveAll(external); err != nil {
		t.Fatal(err)
	}
	if got := run(t, filepath.Join(j.Directory, "tree", "dependency"), "cat-file", "-p", object); got != "untracked user file" {
		t.Fatalf("borrowed object missing: %q", got)
	}
	if err := j.Verify(ctx); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "root")
	path := filepath.Join(base, "task")
	recovery := filepath.Join(base, "recoveries")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	run(t, root, "init", "-b", "master")
	os.WriteFile(filepath.Join(root, "source"), []byte("source"), 0644)
	run(t, root, "add", ".")
	run(t, root, "commit", "-m", "initial")
	run(t, root, "worktree", "add", "-b", "task", path)
	cache := filepath.Join(path, "cache")
	run(t, root, "clone", "--bare", root, cache)
	checkout := filepath.Join(path, "dependency")
	run(t, root, "clone", "--shared", cache, checkout)
	os.WriteFile(filepath.Join(checkout, "source"), []byte("unreachable data"), 0644)
	run(t, checkout, "add", "source")
	blob := run(t, checkout, "rev-parse", ":source")
	run(t, checkout, "reset", "--hard", "HEAD")
	os.WriteFile(filepath.Join(checkout, "local"), []byte("untracked user file"), 0755)
	os.WriteFile(filepath.Join(path, "commondir"), []byte("ordinary working file"), 0644)
	os.MkdirAll(filepath.Join(path, "docs", "info"), 0755)
	os.WriteFile(filepath.Join(path, "docs", "info", "alternates"), []byte("ordinary documentation"), 0644)
	if err := os.Symlink("local", filepath.Join(checkout, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(checkout, "local"), filepath.Join(checkout, "absolute-link")); err != nil {
		t.Fatal(err)
	}
	return root, path, recovery, blob
}

func TestOfflinePreservationRestoreAndExplicitPurge(t *testing.T) {
	root, path, base, blob := fixture(t)
	ctx := context.Background()
	j, err := Prepare(ctx, base, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Repositories) != 3 {
		t.Fatalf("repositories=%d", len(j.Repositories))
	}
	if err := j.Relocate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := j.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{"commondir": "ordinary working file", "docs/info/alternates": "ordinary documentation"} {
		data, err := os.ReadFile(filepath.Join(j.Directory, "tree", rel))
		if err != nil || string(data) != want {
			t.Fatalf("ordinary working file rewritten: %s %q %v", rel, data, err)
		}
	}
	if got := run(t, filepath.Join(j.Directory, "tree", "dependency"), "cat-file", "-p", blob); got != "unreachable data" {
		t.Fatal(got)
	}
	destination := filepath.Join(filepath.Dir(root), "restored")
	if err := j.Restore(ctx, destination); err != nil {
		t.Fatal(err)
	}
	if err := j.Restore(ctx, destination); err == nil {
		t.Fatal("restore overwrote existing destination")
	}
	if err := j.Purge(ctx, ""); err == nil {
		t.Fatal("purge accepted without confirmation")
	}
	if err := j.Purge(ctx, "Permanently delete "+j.Directory); err != nil {
		t.Fatal(err)
	}
	if got := run(t, filepath.Join(destination, "tree", "dependency"), "cat-file", "-p", blob); got != "unreachable data" {
		t.Fatal(got)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "tree", "dependency", "absolute-link")); err != nil || string(data) != "untracked user file" {
		t.Fatalf("restored absolute symlink depends on deleted paths: %q %v", data, err)
	}
	if err := j.Verify(ctx); err == nil {
		t.Fatal("purged recovery verified")
	}
}

func TestRecoveryRejectsMutationAndCorruption(t *testing.T) {
	for _, kind := range []string{"source", "backup", "recreated", "active lock"} {
		t.Run(kind, func(t *testing.T) {
			root, path, base, _ := fixture(t)
			ctx := context.Background()
			if kind == "active lock" {
				os.WriteFile(filepath.Join(path, "dependency", ".git", "index.lock"), nil, 0600)
				j, err := Prepare(ctx, base, root, path)
				if err == nil || len(j.Blockers) == 0 {
					t.Fatal("lock accepted")
				}
				return
			}
			j, err := Prepare(ctx, base, root, path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "source":
				os.WriteFile(filepath.Join(path, "dependency", "local"), []byte("new work"), 0755)
			case "backup":
				os.WriteFile(filepath.Join(j.Directory, "tree", "dependency", "local"), []byte("corruption"), 0755)
			case "recreated":
				if err := j.Relocate(ctx); err != nil {
					t.Fatal(err)
				}
				os.Mkdir(path, 0700)
				os.WriteFile(filepath.Join(path, "new"), []byte("keep"), 0600)
			}
			if err := j.Relocate(ctx); err == nil {
				t.Fatal("unsafe relocation accepted")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("original lost")
			}
		})
	}
}

func TestRecoveryResumesMutationBoundaries(t *testing.T) {
	for _, phase := range []string{"verified", "relocating", "relocated", "promoting", "promoted", "discarding_duplicate", "removed"} {
		t.Run(phase, func(t *testing.T) {
			root, path, base, _ := fixture(t)
			ctx := context.Background()
			j, err := Prepare(ctx, base, root, path)
			if err != nil {
				t.Fatal(err)
			}
			if phase != "verified" {
				if err := j.Relocate(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "promoted" || phase == "discarding_duplicate" {
				j.Phase = "promoting"
				if err := j.save(); err != nil {
					t.Fatal(err)
				}
				if err := j.promoteOriginal(ctx); err != nil {
					t.Fatal(err)
				}
				if phase == "discarding_duplicate" {
					os.Remove(filepath.Join(j.Directory, "verified-tree", "source"))
				}
			}
			if phase == "removed" {
				if err := j.Complete(ctx); err != nil {
					t.Fatal(err)
				}
			}
			j.Phase = phase
			if err := j.save(); err != nil {
				t.Fatal(err)
			}
			j, err = Load(j.Directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.Relocate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := j.Complete(ctx); err != nil {
				t.Fatal(err)
			}
			if err := j.Complete(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInsufficientSpaceAndExclusiveLease(t *testing.T) {
	base := t.TempDir()
	if err := checkSpace(base, 1<<62); err == nil {
		t.Fatal("impossible capacity accepted")
	}
	unlock, err := Lock(base, "/test/worktree")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if unlock2, err := Lock(base, "/test/worktree"); err == nil {
		unlock2()
		t.Fatal("concurrent lease accepted")
	}
}

func TestExtendedAttributesAndAllocatedStorage(t *testing.T) {
	root, path, base, _ := fixture(t)
	file := filepath.Join(path, "dependency", "local")
	name := "user.lcr-test"
	if err := unix.Lsetxattr(file, name, []byte("metadata"), 0); err != nil {
		t.Skip(err)
	}
	j, err := Prepare(context.Background(), base, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	bytes, err := Storage(j.Directory)
	if err != nil || bytes <= 0 {
		t.Fatalf("bytes=%d err=%v", bytes, err)
	}
}

func TestRecoveryKeepsWorkingLockfilesAndOmitsUnrelatedGitStores(t *testing.T) {
	root, path, base, _ := fixture(t)
	ctx := context.Background()
	other := filepath.Join(filepath.Dir(root), "other-task")
	run(t, root, "worktree", "add", "-b", "other-task", other)
	otherAdmin := run(t, other, "rev-parse", "--absolute-git-dir")
	if err := os.WriteFile(filepath.Join(otherAdmin, "index.lock"), []byte("another checkout is busy"), 0600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(root, ".git", "modules", "unused")
	if err := os.MkdirAll(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, "index.lock"), []byte("unrelated metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"uv.lock", "build/playwright/cleanup.lock", "build/runtime/.lock", "dependency/tool.py.lock"} {
		p := filepath.Join(path, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0600); err != nil {
			t.Fatal(err)
		}
	}
	j, err := Prepare(ctx, base, root, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range j.Sources {
		if within(otherAdmin, source.Original) || within(unrelated, source.Original) {
			t.Fatalf("copied unrelated Git metadata: %s", source.Original)
		}
	}
	// Activity in a different worktree must not invalidate this preservation.
	if err := os.WriteFile(filepath.Join(otherAdmin, "index.lock"), []byte("still busy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := j.Relocate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	j, err = Load(j.Directory)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(filepath.Dir(root), "restored")
	if err := j.Restore(ctx, destination); err != nil {
		t.Fatalf("recovery depends on live primary: %v", err)
	}
	for _, rel := range []string{"uv.lock", "build/playwright/cleanup.lock", "build/runtime/.lock", "dependency/tool.py.lock"} {
		data, err := os.ReadFile(filepath.Join(destination, "tree", rel))
		if err != nil || string(data) != rel {
			t.Fatalf("lockfile %s lost: %q %v", rel, data, err)
		}
	}
	run(t, filepath.Join(destination, "tree"), "fsck", "--full")
}

func TestRecoveryRejectsAddedCommonMetadata(t *testing.T) {
	root, path, base, _ := fixture(t)
	ctx := context.Background()
	j, err := Prepare(ctx, base, root, path)
	if err != nil {
		t.Fatal(err)
	}
	common, err := j.mapped(filepath.Join(root, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(common, "unexpected"), []byte("new metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := j.Verify(ctx); err == nil {
		t.Fatal("new metadata bypassed repeated Git verification")
	}
}

func TestCopyFileHasIndependentContents(t *testing.T) {
	base := t.TempDir()
	from, to := filepath.Join(base, "source"), filepath.Join(base, "copy")
	if err := os.WriteFile(from, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(from, to); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from, []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(to); err != nil || string(data) != "original" {
		t.Fatalf("source write changed copy: %q %v", data, err)
	}
	if err := os.WriteFile(to, []byte("changed copy"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(from); err != nil || string(data) != "changed source" {
		t.Fatalf("copy write changed source: %q %v", data, err)
	}
}

func TestRecoveryResumesAfterDeviceRenumbering(t *testing.T) {
	for _, stage := range []string{"verified", "relocated", "promoted"} {
		t.Run(stage, func(t *testing.T) {
			root, path, base, _ := fixture(t)
			ctx := context.Background()
			j, err := Prepare(ctx, base, root, path)
			if err != nil {
				t.Fatal(err)
			}
			if stage != "verified" {
				if err := j.Relocate(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "promoted" {
				if err := j.Complete(ctx); err != nil {
					t.Fatal(err)
				}
			}
			j.Device++
			j.RecoveryDevice++
			if err := j.save(); err != nil {
				t.Fatal(err)
			}
			j, err = Load(j.Directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.Relocate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := j.Complete(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLegacyInspectionDeviceRetryRequiresNoPreservedData(t *testing.T) {
	for _, artifact := range []bool{false, true} {
		t.Run(fmt.Sprint(artifact), func(t *testing.T) {
			root, path, base, _ := fixture(t)
			ctx := context.Background()
			lock := filepath.Join(path, "dependency", ".git", "index.lock")
			if err := os.WriteFile(lock, nil, 0600); err != nil {
				t.Fatal(err)
			}
			j, err := Prepare(ctx, base, root, path)
			if err == nil || j.Phase != "inspecting" {
				t.Fatalf("expected inspection blocker: %v", err)
			}
			if err := os.Remove(lock); err != nil {
				t.Fatal(err)
			}
			j.RecoveryDevice = 0
			j.RecoveryInode = 0
			j.Device++
			if err := j.save(); err != nil {
				t.Fatal(err)
			}
			if artifact {
				if err := os.WriteFile(filepath.Join(j.Directory, "keep"), []byte("preserved"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			j, err = Prepare(ctx, base, root, path)
			if artifact {
				if err == nil {
					t.Fatal("rebound a journal with preserved data")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := j.Relocate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := j.Complete(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecoveryDeviceRenumberingRequiresRecoveryAnchor(t *testing.T) {
	root, path, base, _ := fixture(t)
	ctx := context.Background()
	j, err := Prepare(ctx, base, root, path)
	if err != nil {
		t.Fatal(err)
	}
	j.Device++
	j.RecoveryDevice++
	j.RecoveryInode++
	if err := j.Relocate(ctx); err == nil {
		t.Fatal("accepted replaced recovery anchor")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("original changed")
	}
}
