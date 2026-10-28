package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeCleanupFingerprintIncludesUnparsedRemainder(t *testing.T) {
	f := newClaudeCleanupFixture(t)
	data := []byte("{}\n" + strings.Repeat("unparsed data\n", 10000))
	f.write(t, filepath.Join(f.home, "transcript"), data, f.old)
	root, err := os.OpenRoot(f.home)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := readClaudeCleanupFile(context.Background(), root, "transcript", func(reader io.Reader) {
		prefix := make([]byte, 3)
		if _, err := io.ReadFull(reader, prefix); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil || file.Size != int64(len(data)) || file.Digest != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("incomplete fingerprint: %+v, %v", file, err)
	}
}

func TestClaudeCleanupReadRejectsConcurrentChangesAndCancellation(t *testing.T) {
	for _, change := range []string{"replace", "append", "cancel"} {
		t.Run(change, func(t *testing.T) {
			f := newClaudeCleanupFixture(t)
			path := f.write(t, filepath.Join(f.home, "transcript"), []byte("original"), f.old)
			root, err := os.OpenRoot(f.home)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err = readClaudeCleanupFile(ctx, root, "transcript", func(reader io.Reader) {
				switch change {
				case "replace":
					if err := os.Rename(path, path+".old"); err != nil {
						t.Fatal(err)
					}
					// Same content, size and mtime still has a different file identity.
					f.write(t, path, []byte("original"), f.old)
				case "append":
					f.write(t, path, []byte("original plus activity"), time.Now())
				case "cancel":
					cancel()
				}
			})
			if err == nil || (change == "cancel" && !errors.Is(err, context.Canceled)) {
				t.Fatalf("accepted %s: %v", change, err)
			}
		})
	}
}

func TestClaudeCleanupIgnoresNonSessionSymlinksInStorageTotal(t *testing.T) {
	f := newClaudeCleanupFixture(t)
	f.transcript(t, f.rootPath, "old", "", f.old)
	f.transcript(t, f.rootPath, "newest", "", time.Now())
	f.write(t, filepath.Join(f.home, "debug", "log"), []byte("debug data"), f.old)
	if err := os.Symlink("log", filepath.Join(f.home, "debug", "latest")); err != nil {
		t.Fatal(err)
	}
	audit := f.audit(t, CodexCleanupStale)
	if audit.Storage.Partial || len(audit.Groups) != 1 || audit.Excluded.Newest != 1 {
		t.Fatalf("ordinary debug symlink changed audit: %+v", audit)
	}
	var retained int64
	for _, group := range audit.Retained {
		retained += group.Bytes
	}
	if retained+audit.RecoverableBytes != audit.Storage.TotalBytes {
		t.Fatal("storage total does not reconcile")
	}
}

func TestClaudeCleanupRetainedExplainsMultipleWorkingDirectories(t *testing.T) {
	f := newClaudeCleanupFixture(t)
	old := f.transcript(t, f.rootPath, "old", "", f.old)
	data, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.ReplaceAll(string(data), f.rootPath, f.rootPath+"-other")
	f.write(t, old, append(data, moved...), f.old)
	f.transcript(t, f.rootPath, "newest", "", time.Now())
	audit := f.audit(t, CodexCleanupStale)
	if len(audit.Groups) != 0 || audit.Excluded.Uncertain != 1 || audit.Excluded.Newest != 1 {
		t.Fatalf("multiple-folder tree was not protected: %+v", audit)
	}
	for _, group := range audit.Retained {
		if group.Path == f.rootPath && strings.Contains(group.Reason, "multiple working directories") && strings.Contains(group.Reason, "Newest session") {
			return
		}
	}
	t.Fatalf("missing retention reasons: %+v", audit.Retained)
}
