package worktreerecovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

type fileIdentity struct {
	Device            uint64
	Inode             uint64
	Size              int64
	Modified, Changed unix.Timespec
}

func digestIdentity(st *unix.Stat_t) fileIdentity {
	return fileIdentity{uint64(st.Dev), st.Ino, st.Size, st.Mtim, st.Ctim}
}

// Reuse reads only within this worker, on APFS with nanosecond change times.
// Every snapshot still checks attributes/flags/ownership; neither this cache nor
// a stat-based verification result is written to a recovery manifest.
func fileDigest(ctx context.Context, path string, before *unix.Stat_t) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	key := digestIdentity(before)
	p, _ := ctx.Value(progressKey{}).(*progressReporter)
	cache := false
	if p != nil {
		if p.devices == nil {
			p.devices = map[uint64]bool{}
			p.hashes = map[fileIdentity]string{}
		}
		allowed, known := p.devices[key.Device]
		if !known {
			allowed = canCacheFileDigest(path)
			p.devices[key.Device] = allowed
		}
		cache = allowed
		if cache {
			if digest, ok := p.hashes[key]; ok {
				reportProgress(ctx, "Reading and verifying files", path, 0, before.Size)
				return digest, nil
			}
		}
	}
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	var opened unix.Stat_t
	if err := unix.Fstat(int(in.Fd()), &opened); err != nil {
		in.Close()
		return "", err
	}
	if digestIdentity(&opened) != key {
		in.Close()
		return "", fmt.Errorf("file changed before verification: %s", path)
	}
	h := sha256.New()
	_, err = io.Copy(h, progressReader{ctx: ctx, reader: in, stage: "Reading and verifying files", path: path})
	var after unix.Stat_t
	statErr := unix.Fstat(int(in.Fd()), &after)
	closeErr := in.Close()
	if err != nil {
		return "", err
	}
	if statErr != nil {
		return "", statErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if digestIdentity(&after) != key {
		return "", fmt.Errorf("file changed during verification: %s", path)
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if cache {
		p.hashes[key] = digest
	}
	return digest, nil
}
