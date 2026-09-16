package worktreerecovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type Journal struct {
	QuarantinePath string
	DuplicatePath  string
	Version        int
	Directory      string
	Original       string
	Root           string
	Phase          string
	Created        time.Time
	Verified       time.Time
	Device         uint64
	Inode          uint64
	Sources        []Source
	Repositories   []Repository
	ObjectStores   []string
	Repairs        []Repair
	Blockers       []string
	RetainedBytes  int64
	// VerifiedFiles describe the repaired, independently usable recovery.
	VerifiedFiles map[string]map[string]File
	MetadataMoves []MetadataMove
}

type MetadataMove struct {
	Original    string
	Destination string
	Files       map[string]File
}

// PreserveRegistration journals and atomically detaches one exact Git admin
// directory. It never runs a recursive Git removal against the old checkout.
func (j *Journal) PreserveRegistration(ctx context.Context, from string) error {
	dst := filepath.Join(j.Directory, "registrations", filepath.Base(Location("", from)))
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	for _, move := range j.MetadataMoves {
		if move.Original == from {
			if _, err := os.Lstat(from); os.IsNotExist(err) {
				return verifyTree(ctx, move.Destination, move.Files)
			}
			if err := verifyTree(ctx, from, move.Files); err != nil {
				return err
			}
			if _, err := os.Lstat(dst); !os.IsNotExist(err) {
				return fmt.Errorf("registration relocation collision: %s", dst)
			}
			if err := os.Rename(from, dst); err != nil {
				return err
			}
			if err := syncDir(filepath.Dir(from)); err != nil {
				return err
			}
			return syncDir(filepath.Dir(dst))
		}
	}
	files, err := snapshot(ctx, from)
	if err != nil {
		return err
	}
	var expected map[string]File
	for _, source := range j.Sources {
		if !within(from, source.Original) {
			continue
		}
		expected = map[string]File{}
		for rel, file := range source.Files {
			path := filepath.Join(source.Original, rel)
			if !within(path, from) {
				continue
			}
			childRel, err := filepath.Rel(from, path)
			if err != nil {
				return err
			}
			expected[childRel] = file
		}
		break
	}
	if expected == nil || !reflect.DeepEqual(files, expected) {
		return fmt.Errorf("worktree registration changed since preservation: %s; original metadata retained", from)
	}
	j.MetadataMoves = append(j.MetadataMoves, MetadataMove{Original: from, Destination: dst, Files: files})
	if err := j.save(); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		return fmt.Errorf("registration relocation collision: %s", dst)
	}
	if err := os.Rename(from, dst); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(from)); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dst))
}

func Location(base, path string) string {
	path = filepath.Clean(path)
	// Resolve parent aliases (not the selected leaf), including macOS /var,
	// consistently before and after the checkout directory has disappeared.
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		path = filepath.Join(parent, filepath.Base(path))
	}
	h := sha256.Sum256([]byte(path))
	return filepath.Join(base, hex.EncodeToString(h[:16]))
}
func (j *Journal) save() error {
	if j.Phase != "verified" && j.Phase != "removed" && j.Phase != "purged" {
		return writeJSON(filepath.Join(j.Directory, "manifest.json"), j)
	}
	for n := 0; n < 3; n++ {
		if err := writeJSON(filepath.Join(j.Directory, "manifest.json"), j); err != nil {
			return err
		}
		bytes, err := Storage(j.Directory)
		if err != nil {
			return err
		}
		if bytes == j.RetainedBytes {
			return nil
		}
		j.RetainedBytes = bytes
	}
	return writeJSON(filepath.Join(j.Directory, "manifest.json"), j)
}

func Load(directory string) (*Journal, error) {
	i, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("recovery directory identity is unsafe")
	}
	data, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	if j.Version != 1 || j.Directory != directory || !filepath.IsAbs(j.Original) || !filepath.IsAbs(j.Root) {
		return nil, fmt.Errorf("invalid recovery manifest: %s", directory)
	}
	for _, s := range j.Sources {
		if !filepath.IsAbs(s.Original) || filepath.IsAbs(s.Copy) || !within(filepath.Join(directory, s.Copy), directory) || s.Copy == "." {
			return nil, fmt.Errorf("unsafe recovery mapping")
		}
	}
	for _, r := range j.Repairs {
		if !within(r.Path, directory) || r.Path == directory {
			return nil, fmt.Errorf("unsafe repair path")
		}
	}
	for _, move := range j.MetadataMoves {
		if !within(move.Destination, filepath.Join(directory, "registrations")) || !filepath.IsAbs(move.Original) {
			return nil, fmt.Errorf("unsafe registration mapping")
		}
	}
	if !j.Verified.IsZero() && j.Phase != "purged" && (len(j.Sources) == 0 || j.Sources[0].Original != j.Original || len(j.Repositories) == 0) {
		return nil, fmt.Errorf("incomplete verified recovery manifest")
	}
	return &j, nil
}

// Lock is an OS lease: a crash releases it. The file is retained so a second
// process cannot lock a replacement inode while the first still owns it.
func Lock(base, path string) (func(), error) {
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(Location(base, path)+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("recovery operation already active: %w", err)
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}

// Prepare writes the journal before any source mutation. All bytes are read
// back independently and the sources are rehashed before the verified state.
func Prepare(ctx context.Context, base, root, path string) (*Journal, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("selected worktree must be a directory, not a symbolic link: %s", path)
	}
	root, err = resolved(root)
	if err != nil {
		return nil, err
	}
	path, err = resolved(path)
	if err != nil {
		return nil, err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	base, err = resolved(base)
	if err != nil {
		return nil, err
	}
	if within(base, path) || within(root, path) {
		return nil, fmt.Errorf("recovery and primary checkout must be outside removal tree")
	}
	directory := Location(base, path)
	if j, err := Load(directory); err == nil {
		if !j.Verified.IsZero() {
			return j, j.Verify(ctx)
		}
		return j, j.prepareCopies(ctx)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return nil, err
	}
	j := &Journal{Version: 1, Directory: directory, QuarantinePath: filepath.Join(directory, "original"), DuplicatePath: filepath.Join(directory, "verified-tree"), Original: path, Root: root, Phase: "inspecting", Created: time.Now().UTC(), Device: uint64(st.Dev), Inode: st.Ino}
	if err := j.save(); err != nil {
		return j, err
	}
	return j, j.prepareCopies(ctx)
}

func (j *Journal) prepareCopies(ctx context.Context) error {
	if err := j.checkOriginalIdentity(); err != nil {
		return err
	}
	if j.Phase == "inspecting" {
		j.Repositories = nil
		j.ObjectStores = nil
		j.Sources = nil
		err := j.inspect(ctx)
		if saveErr := j.save(); saveErr != nil {
			return saveErr
		}
		if err != nil {
			return err
		}
		for n := range j.Sources {
			files, err := snapshot(ctx, j.Sources[n].Original)
			if err != nil {
				return err
			}
			j.Sources[n].Files = files
		}
		j.Phase = "copying"
		if err := j.save(); err != nil {
			return err
		}
	}
	if err := j.CheckSources(ctx); err != nil {
		return err
	}
	var bytes int64
	for _, s := range j.Sources {
		for _, f := range s.Files {
			bytes += f.Size
		}
	}
	if err := checkSpace(j.Directory, bytes); err != nil {
		return err
	}
	// Undo only our own recorded pointer edits when resuming preparation.
	for _, r := range j.Repairs {
		data, err := readRepair(r.Path, r.Symlink)
		if err != nil {
			return err
		}
		if string(data) != string(r.Before) && string(data) != string(r.After) {
			return fmt.Errorf("recovery metadata changed: %s", r.Path)
		}
		if err := applyRepair(r.Path, r.Before, r.Symlink); err != nil {
			return err
		}
	}
	j.Repairs = nil
	if err := j.save(); err != nil {
		return err
	}
	for _, s := range j.Sources {
		dst := filepath.Join(j.Directory, s.Copy)
		if _, err := os.Lstat(dst); err == nil {
			if err := validateSubset(ctx, dst, s.Files); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		if err := copyTree(ctx, s.Original, dst); err != nil {
			return err
		}
		if err := verifyTree(ctx, dst, s.Files); err != nil {
			return err
		}
	}
	if err := j.repair(ctx); err != nil {
		return err
	}
	if err := j.verifyGit(ctx); err != nil {
		return err
	}
	if err := j.CheckSources(ctx); err != nil {
		return err
	}
	j.VerifiedFiles = map[string]map[string]File{}
	for _, s := range j.Sources {
		if err := syncTree(ctx, filepath.Join(j.Directory, s.Copy)); err != nil {
			return err
		}
		files, err := snapshot(ctx, filepath.Join(j.Directory, s.Copy))
		if err != nil {
			return err
		}
		j.VerifiedFiles[s.Copy] = files
	}
	j.Phase = "verified"
	j.Verified = time.Now().UTC()
	var err error
	j.RetainedBytes, err = Storage(j.Directory)
	if err != nil {
		return err
	}
	return j.save()
}

func (j *Journal) CheckSources(ctx context.Context) error {
	if err := j.checkOriginalIdentity(); err != nil {
		return err
	}
	for _, s := range j.Sources {
		if err := verifyTree(ctx, s.Original, s.Files); err != nil {
			return err
		}
	}
	return nil
}

func (j *Journal) checkOriginalIdentity() error {
	p, err := resolved(j.Original)
	if err != nil {
		return err
	}
	if p != j.Original {
		return fmt.Errorf("original path was replaced by a symlink")
	}
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		return err
	}
	if uint64(st.Dev) != j.Device || st.Ino != j.Inode {
		return fmt.Errorf("original worktree path was recreated; refusing removal: %s", p)
	}
	return nil
}

func (j *Journal) Verify(ctx context.Context) error {
	if j.Phase == "purged" {
		return fmt.Errorf("recovery was permanently deleted")
	}
	if j.Verified.IsZero() || len(j.VerifiedFiles) == 0 {
		return fmt.Errorf("incomplete recovery at %s (phase %s); source retained; inspect manifest before restarting", j.Directory, j.Phase)
	}
	for _, s := range j.Sources {
		files, ok := j.VerifiedFiles[s.Copy]
		if !ok {
			return fmt.Errorf("missing verification inventory")
		}
		if err := verifyTree(ctx, filepath.Join(j.Directory, s.Copy), files); err != nil {
			return err
		}
	}
	for _, move := range j.MetadataMoves {
		if _, err := os.Lstat(move.Destination); err == nil {
			if err := verifyTree(ctx, move.Destination, move.Files); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return j.verifyGit(ctx)
}

// VerifyResume also accepts the journaled promotion boundary, where the
// already-verified copy may have its temporary name while pointers in the
// original are being repaired. No source mutation occurs in this read-only check.
func (j *Journal) VerifyResume(ctx context.Context) error {
	if j.Phase != "promoting" {
		return j.Verify(ctx)
	}
	for _, source := range j.Sources {
		path := filepath.Join(j.Directory, source.Copy)
		if source.Copy == "tree" {
			duplicate := filepath.Join(j.Directory, "verified-tree")
			if _, err := os.Lstat(duplicate); err == nil {
				path = duplicate
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		if err := verifyTree(ctx, path, j.VerifiedFiles[source.Copy]); err != nil {
			return err
		}
	}
	return nil
}

// Relocate is the only source-directory mutation. A same-filesystem rename
// makes the boundary recoverable; cross-device recovery stops before mutation.
func (j *Journal) Relocate(ctx context.Context) error {
	if j.Phase == "promoting" {
		return j.promoteOriginal(ctx)
	}
	if err := j.Verify(ctx); err != nil {
		return err
	}
	quarantine := filepath.Join(j.Directory, "original")
	if _, err := os.Lstat(quarantine); err == nil {
		if _, err := os.Lstat(j.Original); !os.IsNotExist(err) {
			return fmt.Errorf("original path collision after relocation: %s", j.Original)
		}
		var st unix.Stat_t
		if err := unix.Lstat(quarantine, &st); err != nil {
			return err
		}
		if uint64(st.Dev) != j.Device || st.Ino != j.Inode {
			return fmt.Errorf("relocated directory identity changed")
		}
		if j.Phase != "discarding_duplicate" {
			if err := verifyTree(ctx, quarantine, j.Sources[0].Files); err != nil {
				return err
			}
		}
		if j.Phase != "discarding_duplicate" {
			j.Phase = "relocated"
		}
		return j.save()
	} else if !os.IsNotExist(err) {
		return err
	}
	if j.Phase == "removed" || j.Phase == "discarding_duplicate" || j.Phase == "promoted" {
		if _, err := os.Lstat(j.Original); !os.IsNotExist(err) {
			return fmt.Errorf("removed worktree path was recreated")
		}
		return nil
	}
	if err := j.CheckSources(ctx); err != nil {
		return err
	}
	j.Phase = "relocating"
	if err := j.save(); err != nil {
		return err
	}
	if err := os.Rename(j.Original, quarantine); err != nil {
		return fmt.Errorf("relocation failed; original retained (recovery must be on the same filesystem): %w", err)
	}
	if err := syncDir(filepath.Dir(j.Original)); err != nil {
		return err
	}
	if err := syncDir(j.Directory); err != nil {
		return err
	}
	if j.Phase != "discarding_duplicate" {
		j.Phase = "relocated"
	}
	return j.save()
}

// Complete retains the original inode as the final recovery tree. Only the
// independently verified COPY is discarded, so a late writer with an open
// descriptor cannot lose new data through recursive deletion of the original.
func (j *Journal) Complete(ctx context.Context) error {
	if _, err := os.Lstat(j.Original); !os.IsNotExist(err) {
		return fmt.Errorf("original path exists; removal is incomplete")
	}
	if j.Phase != "removed" && j.Phase != "discarding_duplicate" && j.Phase != "promoted" {
		if j.Phase != "promoting" {
			if err := j.Verify(ctx); err != nil {
				return err
			}
			if err := verifyTree(ctx, filepath.Join(j.Directory, "original"), j.Sources[0].Files); err != nil {
				return err
			}
			j.Phase = "promoting"
			if err := j.save(); err != nil {
				return err
			}
		}
		if err := j.promoteOriginal(ctx); err != nil {
			return err
		}
	}
	if err := j.Verify(ctx); err != nil {
		return err
	}
	duplicate := filepath.Join(j.Directory, "verified-tree")
	if _, err := os.Lstat(duplicate); err == nil {
		if err := validateSubset(ctx, duplicate, j.VerifiedFiles["tree"]); err != nil {
			return err
		}
		j.Phase = "discarding_duplicate"
		if err := j.save(); err != nil {
			return err
		}
		if err := os.RemoveAll(duplicate); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	j.Phase = "removed"
	return j.save()
}

func (j *Journal) promoteOriginal(ctx context.Context) error {
	if err := j.VerifyResume(ctx); err != nil {
		return err
	}
	if _, err := os.Lstat(j.Original); !os.IsNotExist(err) {
		return fmt.Errorf("original path was recreated")
	}
	original := filepath.Join(j.Directory, "original")
	tree := filepath.Join(j.Directory, "tree")
	duplicate := filepath.Join(j.Directory, "verified-tree")
	var st unix.Stat_t
	treeIsOriginal := unix.Lstat(tree, &st) == nil && uint64(st.Dev) == j.Device && st.Ino == j.Inode
	if !treeIsOriginal {
		if err := unix.Lstat(original, &st); err != nil {
			return err
		}
		if uint64(st.Dev) != j.Device || st.Ino != j.Inode {
			return fmt.Errorf("relocated directory identity changed")
		}
		if err := verifyTree(ctx, original, j.Sources[0].Files); err != nil {
			return err
		}
		if _, err := os.Lstat(tree); err == nil {
			if _, err := os.Lstat(duplicate); !os.IsNotExist(err) {
				return fmt.Errorf("duplicate-copy collision")
			}
			if err := verifyTree(ctx, tree, j.VerifiedFiles["tree"]); err != nil {
				return err
			}
			if err := os.Rename(tree, duplicate); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := verifyTree(ctx, duplicate, j.VerifiedFiles["tree"]); err != nil {
			return err
		}
		if err := os.Rename(original, tree); err != nil {
			return err
		}
		if err := syncDir(j.Directory); err != nil {
			return err
		}
	}
	// A crash may have applied only some pointer repairs. Accept exactly the
	// recorded before/after states, never arbitrary new metadata or files.
	actual, err := snapshot(ctx, tree)
	if err != nil {
		return err
	}
	if len(actual) != len(j.Sources[0].Files) {
		return fmt.Errorf("original recovery gained or lost files")
	}
	for rel, f := range actual {
		if !reflect.DeepEqual(f, j.Sources[0].Files[rel]) && !reflect.DeepEqual(f, j.VerifiedFiles["tree"][rel]) {
			return fmt.Errorf("original recovery changed at %s; both copies retained", filepath.Join(tree, rel))
		}
	}
	for _, r := range j.Repairs {
		if !within(r.Path, tree) {
			continue
		}
		data, err := readRepair(r.Path, r.Symlink)
		if err != nil {
			return err
		}
		if string(data) == string(r.After) {
			continue
		}
		if string(data) != string(r.Before) {
			return fmt.Errorf("pointer changed: %s", r.Path)
		}
		if err := applyRepair(r.Path, r.After, r.Symlink); err != nil {
			return err
		}
	}
	if err := syncTree(ctx, tree); err != nil {
		return err
	}
	if err := j.Verify(ctx); err != nil {
		return err
	}
	j.Phase = "promoted"
	return j.save()
}

// Restore creates an independent recovery workspace at a NEW destination.
// It never overlays a live checkout or replaces shared refs; open destination/tree.
func (j *Journal) Restore(ctx context.Context, destination string) error {
	if !filepath.IsAbs(destination) || within(destination, j.Directory) {
		return fmt.Errorf("restore requires a new absolute path outside recovery")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("restore destination already exists: %s", destination)
	}
	if err := j.Verify(ctx); err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	for _, s := range j.Sources {
		dst := filepath.Join(destination, s.Copy)
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		if err := copyTree(ctx, filepath.Join(j.Directory, s.Copy), dst); err != nil {
			return err
		}
		if err := verifyTree(ctx, dst, j.VerifiedFiles[s.Copy]); err != nil {
			return err
		}
	}
	for _, r := range j.Repairs {
		rel, err := filepath.Rel(j.Directory, r.Path)
		if err != nil {
			return err
		}
		data := strings.ReplaceAll(string(r.After), j.Directory, destination)
		if err := applyRepair(filepath.Join(destination, rel), []byte(data), r.Symlink); err != nil {
			return err
		}
	}
	if err := syncTree(ctx, destination); err != nil {
		return err
	}
	clone := *j
	clone.Directory = destination
	return clone.verifyGit(ctx)
}

// Purge requires an exact typed confirmation and a completed operation. A
// tombstone remains to protect newly recreated paths from old retries.
func (j *Journal) Purge(ctx context.Context, confirmation string) error {
	if confirmation != "Permanently delete "+j.Directory {
		return fmt.Errorf("explicit confirmation required: Permanently delete %s", j.Directory)
	}
	if j.Phase != "removed" && j.Phase != "purging" {
		return fmt.Errorf("only completed recoveries can be permanently deleted")
	}
	if j.Phase == "removed" {
		if err := j.Verify(ctx); err != nil {
			return err
		}
	}
	j.Phase = "purging"
	if err := j.save(); err != nil {
		return err
	}
	for _, s := range j.Sources {
		p := filepath.Join(j.Directory, s.Copy)
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			continue
		}
		if err := validateSubset(ctx, p, j.VerifiedFiles[s.Copy]); err != nil {
			return err
		}
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	for _, move := range j.MetadataMoves {
		if _, err := os.Lstat(move.Destination); os.IsNotExist(err) {
			continue
		}
		if err := validateSubset(ctx, move.Destination, move.Files); err != nil {
			return err
		}
		if err := os.RemoveAll(move.Destination); err != nil {
			return err
		}
	}
	// The confirmed scope is this entire recovery directory, including any
	// scratch config left by a crash. Keep only the idempotency tombstone.
	entries, err := os.ReadDir(j.Directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Name() == "manifest.json" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(j.Directory, entry.Name())); err != nil {
			return err
		}
	}
	j.Sources = nil
	j.Repositories = nil
	j.ObjectStores = nil
	j.Repairs = nil
	j.VerifiedFiles = nil
	j.MetadataMoves = nil
	j.Blockers = nil
	j.Phase = "purged"
	j.RetainedBytes = 0
	return j.save()
}

// ValidateSubset is used at interrupted duplicate-deletion boundaries. It
// accepts missing entries, never new or changed entries.
func validateSubset(ctx context.Context, path string, expected map[string]File) error {
	actual, err := snapshot(ctx, path)
	if err != nil {
		return err
	}
	for p, f := range actual {
		if !reflect.DeepEqual(f, expected[p]) {
			return fmt.Errorf("unexpected entry in interrupted operation: %s", filepath.Join(path, p))
		}
	}
	return nil
}
