//go:build darwin

package worktreerecovery

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"os/exec"
	"strconv"
	"strings"
)

// macOS ACLs are not returned by listxattr. Until we can round-trip them with a
// native ACL API, reject them explicitly rather than verifying a lossy copy.
// -B escapes newlines in names, and -l gives directory entries a mode prefix;
// only ACL records have a numeric ordinal followed by a colon.
func checkPlatformMetadata(ctx context.Context, paths ...string) error {
	for len(paths) > 0 {
		n := min(len(paths), 128)
		if err := checkPlatformMetadataBatch(ctx, paths[:n]); err != nil {
			return err
		}
		paths = paths[n:]
	}
	return ctx.Err()
}

func checkPlatformMetadataBatch(ctx context.Context, paths []string) error {
	for _, flags := range []string{"-ldeB", "-lAeRB"} {
		cmd := exec.CommandContext(ctx, "/bin/ls", append([]string{flags}, paths...)...)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("cannot inspect macOS ACLs at %s: %w", strings.Join(paths, ", "), err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || !strings.HasSuffix(fields[0], ":") {
				continue
			}
			if _, err := strconv.ParseUint(strings.TrimSuffix(fields[0], ":"), 10, 64); err == nil {
				return fmt.Errorf("extended ACL requires preservation review: %s (%s)", strings.Join(paths, ", "), strings.TrimSpace(line))
			}
		}
	}
	return nil
}

func checkFileFlags(path string, stat *unix.Stat_t) error {
	if stat.Flags != 0 {
		return fmt.Errorf("filesystem flags require preservation review: %s (0x%x)", path, stat.Flags)
	}
	return nil
}
