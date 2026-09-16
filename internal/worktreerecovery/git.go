package worktreerecovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Repository struct {
	Path    string
	GitDir  string
	Common  string
	Objects string
	Refs    string
	Head    string
}

type Source struct {
	Original string
	Copy     string
	Files    map[string]File
}

type Repair struct {
	Symlink bool
	Path    string
	Before  []byte
	After   []byte
}

func git(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "--no-replace-objects", "-c", "core.fsmonitor=false", "-C", path}, args...)...)
	// Verification must never consult the caller's object/index overrides or
	// lazily fetch missing objects from a promisor remote.
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("Git %s at %s failed: %w", args[0], path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func resolved(path string) (string, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// Inspect collects every repository and every blocking reason. It does not
// infer ownership from cache names or ignore rules, and never uses the network.
func (j *Journal) inspect(ctx context.Context) error {
	var problems []error
	paths := []string{j.Original}
	err := filepath.WalkDir(j.Original, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			problems = append(problems, err)
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == j.Original {
			return nil
		}
		if d.Name() == ".git" {
			if d.Type()&os.ModeSymlink != 0 {
				problems = append(problems, fmt.Errorf("Git metadata symlink: %s", p))
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if metadata, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
			if metadata.Mode().IsRegular() {
				data, readErr := os.ReadFile(filepath.Join(p, ".git"))
				if readErr != nil {
					problems = append(problems, readErr)
					return nil
				}
				if !strings.HasPrefix(string(data), "gitdir: ") {
					return nil
				}
			}
			if _, err := git(ctx, p, "rev-parse", "--absolute-git-dir"); err == nil {
				paths = append(paths, p)
			} else {
				problems = append(problems, err)
			}
		} else if _, err := os.Stat(filepath.Join(p, "HEAD")); err == nil {
			if _, err := os.Stat(filepath.Join(p, "objects")); err == nil {
				paths = append(paths, p)
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		problems = append(problems, err)
	}
	j.Sources = []Source{{Original: j.Original, Copy: "tree"}}
	for _, p := range paths {
		r := Repository{Path: p}
		var err error
		r.GitDir, err = git(ctx, p, "rev-parse", "--absolute-git-dir")
		if err != nil {
			problems = append(problems, err)
			continue
		}
		r.Common, err = git(ctx, p, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			problems = append(problems, err)
			continue
		}
		r.GitDir, err = resolved(r.GitDir)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		r.Common, err = resolved(r.Common)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if within(r.Common, j.Original) {
			registrations, err := git(ctx, p, "worktree", "list", "--porcelain")
			if err != nil {
				problems = append(problems, err)
			}
			for _, line := range strings.Split(registrations, "\n") {
				if strings.HasPrefix(line, "worktree ") {
					consumer := strings.TrimPrefix(line, "worktree ")
					if !within(consumer, j.Original) {
						problems = append(problems, fmt.Errorf("external linked worktree %s depends on %s", consumer, r.Common))
					}
				}
			}
		}
		pointer := filepath.Join(p, ".git")
		if _, err := os.Lstat(pointer); os.IsNotExist(err) && r.GitDir != p {
			problems = append(problems, fmt.Errorf("repository metadata does not match directory: %s", p))
			continue
		}
		j.addSource(r.Common)
		j.addSource(r.GitDir)
		r.Objects, err = git(ctx, p, "cat-file", "--batch-all-objects", "--batch-check=%(objectname)")
		if err != nil {
			problems = append(problems, err)
		}
		r.Refs, err = git(ctx, p, "for-each-ref", "--format=%(objectname) %(refname)")
		if err != nil {
			problems = append(problems, err)
		}
		r.Head, err = git(ctx, p, "rev-parse", "HEAD")
		if err != nil {
			problems = append(problems, err)
		}
		j.Repositories = append(j.Repositories, r)
	}
	// Only Git object-store metadata defines an alternate, not an ordinary
	// working file that happens to be named info/alternates.
	seenStores := map[string]bool{}
	addStore := func(path string) {
		if !seenStores[path] {
			seenStores[path] = true
			j.ObjectStores = append(j.ObjectStores, path)
		}
	}
	for _, repo := range j.Repositories {
		addStore(filepath.Join(repo.Common, "objects"))
	}
	for n := 0; n < len(j.ObjectStores); n++ {
		store := j.ObjectStores[n]
		p := filepath.Join(store, "info", "alternates")
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			problems = append(problems, err)
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "\"") {
				problems = append(problems, fmt.Errorf("quoted alternate requires review: %s", p))
				continue
			}
			if !filepath.IsAbs(line) {
				line = filepath.Join(store, line)
			}
			dep, err := resolved(line)
			if err != nil {
				problems = append(problems, fmt.Errorf("missing alternate %s: %w", line, err))
				continue
			}
			addStore(dep)
			j.addSource(dep)
		}
	}
	for n := 0; n < len(j.Sources); n++ {
		err := filepath.WalkDir(j.Sources[n].Original, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				problems = append(problems, err)
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if strings.HasSuffix(d.Name(), ".lock") {
				problems = append(problems, fmt.Errorf("active or stale lock requires review: %s", p))
			}
			metadata := n > 0
			for _, r := range j.Repositories {
				if within(p, r.GitDir) || within(p, r.Common) {
					metadata = true
					break
				}
			}
			if metadata && d.Type()&os.ModeSymlink != 0 {
				problems = append(problems, fmt.Errorf("Git metadata symlink requires review: %s", p))
			}
			return nil
		})
		if err != nil {
			problems = append(problems, err)
		}
	}
	// Drop overlapping roots after the closure is known, before assigning any
	// persisted copy mapping or copying bytes.
	if len(j.Sources) > 1 {
		rest := append([]Source(nil), j.Sources[1:]...)
		sort.Slice(rest, func(a, b int) bool { return len(rest[a].Original) < len(rest[b].Original) })
		j.Sources = j.Sources[:1]
		for _, source := range rest {
			j.addSource(source.Original)
		}
	}
	for _, source := range j.Sources {
		if within(j.Directory, source.Original) {
			problems = append(problems, fmt.Errorf("recovery storage is inside a required dependency: %s", source.Original))
		}
		if source.Original != j.Original && within(j.Original, source.Original) {
			problems = append(problems, fmt.Errorf("shared metadata contains the removal tree: %s", source.Original))
		}
	}
	j.Blockers = nil
	for _, p := range problems {
		j.Blockers = append(j.Blockers, p.Error())
	}
	return errors.Join(problems...)
}

func (j *Journal) addSource(path string) {
	for _, s := range j.Sources {
		if within(path, s.Original) {
			return
		}
	}
	// Keep prior mappings stable; a parent discovered later must not duplicate
	// a child store already selected. Repository common stores precede alternates.
	j.Sources = append(j.Sources, Source{Original: path, Copy: fmt.Sprintf("stores/%d", len(j.Sources))})
}

func (j *Journal) mapped(path string) (string, error) {
	for _, s := range j.Sources {
		if within(path, s.Original) {
			rel, _ := filepath.Rel(s.Original, path)
			return filepath.Join(j.Directory, s.Copy, rel), nil
		}
	}
	return "", fmt.Errorf("dependency outside preserved closure: %s", path)
}

func (j *Journal) repair(ctx context.Context) error {
	for _, s := range j.Sources {
		for rel, f := range s.Files {
			if f.Mode&os.ModeSymlink != 0 && filepath.IsAbs(f.Link) {
				target, err := j.mapped(filepath.Clean(f.Link))
				if err != nil {
					continue
				}
				to := filepath.Join(j.Directory, s.Copy, rel)
				j.Repairs = append(j.Repairs, Repair{Path: to, Before: []byte(f.Link), After: []byte(target), Symlink: true})
				if err := j.save(); err != nil {
					return err
				}
				if err := applyRepair(to, []byte(target), true); err != nil {
					return err
				}
			}
			if !f.Mode.IsRegular() {
				continue
			}
			name := filepath.Base(rel)
			from := filepath.Join(s.Original, rel)
			to := filepath.Join(j.Directory, s.Copy, rel)
			metadata := s.Copy != "tree"
			pointer := false
			for _, repo := range j.Repositories {
				metadata = metadata || within(from, repo.GitDir) || within(from, repo.Common)
				pointer = pointer || from == filepath.Join(repo.Path, ".git")
			}
			if !metadata && !pointer {
				continue
			}
			adminPointer := false
			for _, repo := range j.Repositories {
				adminPointer = adminPointer || filepath.Dir(from) == repo.GitDir
			}
			adminPointer = adminPointer || (metadata && filepath.Base(filepath.Dir(filepath.Dir(from))) == "worktrees")
			objectAlternate := false
			for _, store := range j.ObjectStores {
				objectAlternate = objectAlternate || from == filepath.Join(store, "info", "alternates")
			}
			if name == ".git" && !pointer {
				continue
			}
			if (name == "gitdir" || name == "commondir") && !adminPointer {
				continue
			}
			if name == "alternates" && !objectAlternate {
				continue
			}
			if name != ".git" && name != "commondir" && name != "gitdir" && !(name == "alternates" && filepath.Base(filepath.Dir(rel)) == "info") {
				continue
			}
			var replacement string
			data, err := os.ReadFile(to)
			if err != nil {
				return err
			}
			switch {
			case name == ".git" && strings.HasPrefix(string(data), "gitdir: "):
				target := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(from), target)
				}
				target, err = j.mapped(filepath.Clean(target))
				if err != nil {
					return err
				}
				replacement = "gitdir: " + target + "\n"
			case name == "gitdir":
				target := strings.TrimSpace(string(data))
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(from), target)
				}
				mapped, mapErr := j.mapped(filepath.Clean(target))
				if mapErr != nil {
					// A copied shared store must not keep live registrations
					// pointing at unrelated working directories. Retain their
					// original bytes in the manifest, with inert local pointers.
					mapped = filepath.Join(j.Directory, "unrestored-worktrees", filepath.Base(Location("", target)), ".git")
				}
				replacement = mapped + "\n"
			case name == "commondir":
				target := strings.TrimSpace(string(data))
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(from), target)
				}
				target, err = j.mapped(filepath.Clean(target))
				if err != nil {
					return err
				}
				replacement = target + "\n"
			case name == "alternates" && filepath.Base(filepath.Dir(rel)) == "info":
				var lines []string
				for _, target := range strings.Split(strings.TrimSpace(string(data)), "\n") {
					if target == "" {
						continue
					}
					if !filepath.IsAbs(target) {
						target = filepath.Join(filepath.Dir(filepath.Dir(from)), target)
					}
					target, err = resolved(target)
					if err != nil {
						return err
					}
					target, err = j.mapped(target)
					if err != nil {
						return err
					}
					lines = append(lines, target)
				}
				replacement = strings.Join(lines, "\n") + "\n"
			default:
				continue
			}
			if replacement == string(data) {
				continue
			}
			j.Repairs = append(j.Repairs, Repair{Path: to, Before: data, After: []byte(replacement)})
			if err := j.save(); err != nil {
				return err
			}
			if err := applyRepair(to, []byte(replacement), false); err != nil {
				return err
			}
		}
	}
	// Local origins and core.worktree can otherwise direct a restored checkout
	// back into a deleted tree (or, worse, into a live primary checkout).
	seen := map[string]bool{}
	for _, r := range j.Repositories {
		config, err := j.mapped(filepath.Join(r.Common, "config"))
		if err != nil {
			return err
		}
		if seen[config] {
			continue
		}
		seen[config] = true
		before, err := os.ReadFile(config)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(j.Directory, ".config-")
		if err != nil {
			return err
		}
		name := tmp.Name()
		_, err = tmp.Write(before)
		closeErr := tmp.Close()
		if err != nil {
			os.Remove(name)
			return err
		}
		if closeErr != nil {
			os.Remove(name)
			return closeErr
		}
		for _, key := range []string{"core.worktree", "remote.origin.url"} {
			value, err := git(ctx, j.Directory, "config", "--file", name, "--get", key)
			if err != nil {
				continue
			}
			var target string
			if key == "core.worktree" {
				target, err = j.mapped(r.Path)
			} else {
				local := strings.TrimPrefix(value, "file://")
				if !filepath.IsAbs(local) {
					continue
				}
				target, err = j.mapped(filepath.Clean(local))
				if strings.HasPrefix(value, "file://") {
					target = "file://" + target
				}
			}
			if err != nil {
				continue
			}
			if _, err := git(ctx, j.Directory, "config", "--file", name, key, target); err != nil {
				os.Remove(name)
				return err
			}
		}
		after, err := os.ReadFile(name)
		os.Remove(name)
		if err != nil {
			return err
		}
		if string(before) == string(after) {
			continue
		}
		j.Repairs = append(j.Repairs, Repair{Path: config, Before: before, After: after})
		if err := j.save(); err != nil {
			return err
		}
		if err := applyRepair(config, after, false); err != nil {
			return err
		}
	}
	return nil
}

func (j *Journal) verifyGit(ctx context.Context) error {
	for _, r := range j.Repositories {
		p, err := j.mapped(r.Path)
		if err != nil {
			return err
		}
		objects, err := git(ctx, p, "cat-file", "--batch-all-objects", "--batch-check=%(objectname)")
		if err != nil {
			return err
		}
		a, b := strings.Fields(objects), strings.Fields(r.Objects)
		sort.Strings(a)
		sort.Strings(b)
		if strings.Join(a, "\n") != strings.Join(b, "\n") {
			return fmt.Errorf("object inventory mismatch: %s", p)
		}
		refs, err := git(ctx, p, "for-each-ref", "--format=%(objectname) %(refname)")
		if err != nil {
			return err
		}
		if refs != r.Refs {
			return fmt.Errorf("refs mismatch: %s", p)
		}
		head, err := git(ctx, p, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		if head != r.Head {
			return fmt.Errorf("HEAD mismatch: %s", p)
		}
		if _, err := git(ctx, p, "fsck", "--full", "--no-dangling"); err != nil {
			return err
		}
	}
	return nil
}
