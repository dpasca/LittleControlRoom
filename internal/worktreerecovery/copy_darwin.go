//go:build darwin

package worktreerecovery

import (
	"errors"

	"golang.org/x/sys/unix"
)

// APFS clones share storage until either file is written. Unlike hard links,
// later edits in the source cannot change the independently verified recovery.
func cloneFile(from, to string) (bool, error) {
	err := unix.Clonefile(from, to, unix.CLONE_NOFOLLOW)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EINVAL) {
		return false, nil
	}
	return err == nil, err
}
