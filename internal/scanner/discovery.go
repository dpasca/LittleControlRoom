package scanner

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

type Discovery struct {
	Roots    []string
	MaxDepth int
}

func DiscoverGitProjects(cfg Discovery) ([]string, error) {
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 4
	}
	seen := map[string]struct{}{}
	var out []string

	for _, root := range cfg.Roots {
		root = filepath.Clean(root)
		if root == "" {
			continue
		}
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}

			rel, relErr := filepath.Rel(root, path)
			if relErr == nil && rel != "." {
				depth := strings.Count(rel, string(filepath.Separator)) + 1
				if depth > cfg.MaxDepth {
					return filepath.SkipDir
				}
			}

			name := d.Name()
			if strings.HasPrefix(name, ".") && path != root && name != ".git" {
				return filepath.SkipDir
			}

			if name == ".git" {
				project := filepath.Dir(path)
				if _, ok := seen[project]; !ok {
					seen[project] = struct{}{}
					out = append(out, project)
				}
				return filepath.SkipDir
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}

	sort.Strings(out)
	return out, nil
}

func IsWithinAnyRoot(path string, roots []string) bool {
	path = filepath.Clean(path)
	for _, root := range roots {
		root = filepath.Clean(root)
		if path == root {
			return true
		}
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
