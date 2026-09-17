//go:build !darwin

package worktreerecovery

import (
	"context"
	"golang.org/x/sys/unix"
)

// Linux POSIX ACLs are included in the xattr inventory.
func checkPlatformMetadata(ctx context.Context, path string) error { return ctx.Err() }

func checkFileFlags(path string, stat *unix.Stat_t) error { return nil }
