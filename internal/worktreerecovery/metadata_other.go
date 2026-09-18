//go:build !darwin

package worktreerecovery

import (
	"context"
	"golang.org/x/sys/unix"
)

// Linux POSIX ACLs are included in the xattr inventory.
func checkPlatformMetadata(ctx context.Context, paths ...string) error { return ctx.Err() }

func checkFileFlags(path string, stat *unix.Stat_t) error { return nil }

func preservedFileFlags(stat *unix.Stat_t) uint32 { return 0 }

func copyFileFlags(path string, stat *unix.Stat_t) error { return nil }

func systemManagedXattr(name string) bool { return false }

func canCacheFileDigest(path string) bool { return false }
