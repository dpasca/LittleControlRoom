// Package worktreerecovery implements local, independently verified worktree
// preservation. A recovery is durable user data, never a disposable cache.
package worktreerecovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type File struct {
	UID      uint32
	GID      uint32
	Modified int64 `json:",omitempty"`
	Mode     os.FileMode
	Size     int64
	Digest   string
	Link     string
	Xattrs   map[string][]byte `json:",omitempty"`
}

// Snapshot never follows links. Special files and unreadable metadata block
// preservation rather than quietly producing an incomplete backup.
func snapshot(ctx context.Context, root string) (map[string]File, error) {
	if err := checkPlatformMetadata(ctx, root); err != nil {
		return nil, err
	}
	files := map[string]File{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		i, err := os.Lstat(path)
		if err != nil {
			return err
		}
		f := File{Mode: i.Mode()}
		var stat unix.Stat_t
		if err := unix.Lstat(path, &stat); err != nil {
			return err
		}
		if err := checkFileFlags(path, &stat); err != nil {
			return err
		}
		f.UID, f.GID = stat.Uid, stat.Gid
		switch {
		case i.Mode().IsRegular():
			h := sha256.New()
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, in)
			closeErr := in.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			f.Size, f.Digest = i.Size(), hex.EncodeToString(h.Sum(nil))
			f.Modified = i.ModTime().UnixNano()
		case i.Mode()&os.ModeSymlink != 0:
			f.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		case i.IsDir():
		default:
			return fmt.Errorf("unsupported special file: %s", path)
		}
		f.Xattrs, err = readXattrs(path)
		if err != nil {
			return fmt.Errorf("read metadata %s: %w", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[rel] = f
		return nil
	})
	return files, err
}

func readXattrs(path string) (map[string][]byte, error) {
	n, err := unix.Llistxattr(path, nil)
	if errors.Is(err, unix.ENOTSUP) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	names := make([]byte, n)
	n, err = unix.Llistxattr(path, names)
	if err != nil {
		return nil, err
	}
	attrs := map[string][]byte{}
	for _, name := range strings.Split(string(names[:n]), "\x00") {
		if name == "" {
			continue
		}
		n, err := unix.Lgetxattr(path, name, nil)
		if err != nil {
			return nil, err
		}
		value := make([]byte, n)
		n, err = unix.Lgetxattr(path, name, value)
		if err != nil {
			return nil, err
		}
		attrs[name] = value[:n]
	}
	return attrs, nil
}

func copyTree(ctx context.Context, from, to string) error {
	return filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		i, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(dst); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		switch {
		case i.IsDir():
			err = os.Mkdir(dst, 0700)
		case i.Mode().IsRegular():
			var in, out *os.File
			in, err = os.Open(path)
			if err != nil {
				return err
			}
			out, err = os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				in.Close()
				return err
			}
			_, err = io.Copy(out, in)
			err = errors.Join(err, in.Close(), out.Sync(), out.Close())
		case i.Mode()&os.ModeSymlink != 0:
			var link string
			link, err = os.Readlink(path)
			if err == nil {
				err = os.Symlink(link, dst)
			}
		default:
			return fmt.Errorf("unsupported file: %s", path)
		}
		if err != nil {
			return err
		}
		attrs, err := readXattrs(path)
		if err != nil {
			return err
		}
		for name, value := range attrs {
			if err := unix.Lsetxattr(dst, name, value, 0); err != nil {
				return fmt.Errorf("preserve xattr %s on %s: %w", name, dst, err)
			}
		}
		var sourceStat, destinationStat unix.Stat_t
		if err := unix.Lstat(path, &sourceStat); err != nil {
			return err
		}
		if err := unix.Lstat(dst, &destinationStat); err != nil {
			return err
		}
		if sourceStat.Uid != destinationStat.Uid || sourceStat.Gid != destinationStat.Gid {
			if err := unix.Lchown(dst, int(sourceStat.Uid), int(sourceStat.Gid)); err != nil {
				return fmt.Errorf("preserve ownership %s: %w", path, err)
			}
		}
		if i.Mode()&os.ModeSymlink == 0 {
			if err := os.Chmod(dst, i.Mode()); err != nil {
				return err
			}
		}
		if i.Mode().IsRegular() {
			if err := os.Chtimes(dst, i.ModTime(), i.ModTime()); err != nil {
				return err
			}
		}
		return nil
	})
}

func verifyTree(ctx context.Context, path string, want map[string]File) error {
	got, err := snapshot(ctx, path)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		var changes []error
		for rel, expected := range want {
			if !reflect.DeepEqual(got[rel], expected) {
				changes = append(changes, fmt.Errorf("contents or metadata changed: %s", filepath.Join(path, rel)))
			}
		}
		for rel := range got {
			if _, ok := want[rel]; !ok {
				changes = append(changes, fmt.Errorf("new entry since inspection: %s", filepath.Join(path, rel)))
			}
		}
		return errors.Join(changes...)
	}
	return nil
}

func within(path, root string) bool {
	r, err := filepath.Rel(root, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(os.PathSeparator))
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".journal-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func syncTree(ctx context.Context, root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		return errors.Join(f.Sync(), f.Close())
	})
	if err != nil {
		return err
	}
	for n := len(dirs) - 1; n >= 0; n-- {
		if err := syncDir(dirs[n]); err != nil {
			return err
		}
	}
	return nil
}

// Storage counts current allocated blocks, not estimates or logical file size.
func Storage(path string) (int64, error) {
	var size int64
	seen := map[[2]uint64]bool{}
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		var st unix.Stat_t
		if err := unix.Lstat(p, &st); err != nil {
			return err
		}
		key := [2]uint64{uint64(st.Dev), st.Ino}
		if !seen[key] {
			size += st.Blocks * 512
			seen[key] = true
		}
		return nil
	})
	return size, err
}

func checkSpace(path string, bytes int64) error {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return err
	}
	available := uint64(st.Bavail) * uint64(st.Bsize)
	if uint64(bytes)+64*1024*1024 > available {
		return fmt.Errorf("insufficient recovery space: need %d bytes plus 64 MiB reserve; available %d", bytes, available)
	}
	return nil
}

func readRepair(path string, symlink bool) ([]byte, error) {
	if symlink {
		value, err := os.Readlink(path)
		return []byte(value), err
	}
	return os.ReadFile(path)
}

func applyRepair(path string, data []byte, symlink bool) error {
	if symlink {
		attrs, err := readXattrs(path)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := os.Symlink(string(data), path); err != nil {
			return err
		}
		for name, value := range attrs {
			if err := unix.Lsetxattr(path, name, value, 0); err != nil {
				return err
			}
		}
		return syncDir(filepath.Dir(path))
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return errors.Join(err, f.Sync(), f.Close(), os.Chtimes(path, time.Unix(0, info.ModTime().UnixNano()), info.ModTime()))
}
