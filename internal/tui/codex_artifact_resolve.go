package tui

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"lcroom/internal/codexapp"
)

// Agents often name a file ("README.md") or a partial path ("fe-1b/clip.mp4")
// without saying where it lives. Guessing is how spurious picks crept in
// before, so these mentions only become picker targets when the filesystem
// confirms exactly one match in the nearest place the transcript was talking
// about: folders named in the same message, then folders of files named
// shortly before it, then the project root.

const (
	codexMentionMaxLen            = 160
	codexMentionMaxPerText        = 64
	codexMentionEvidenceEntries   = 12
	codexMentionMaxFoldersPerTier = 16
)

func codexEntryMentionsRelativeFiles(kind codexapp.TranscriptKind) bool {
	switch kind {
	case codexapp.TranscriptAgent, codexapp.TranscriptUser, codexapp.TranscriptPlan:
		return true
	}
	return false
}

func codexMentionSeparator(r rune) bool {
	if unicode.IsSpace(r) || (r >= 0x2500 && r <= 0x257F) {
		return true
	}
	switch r {
	case '`', '"', '\'', '(', ')', '[', ']', '{', '}', '<', '>', '|', ',', ';', ':', '*', '=',
		'…', '“', '”', '‘', '’', '•', '→', '·':
		return true
	}
	return false
}

// codexPlainPathMentions returns candidate project-relative names from prose
// or table text: bare file names, partial relative paths, and folder names
// that end in a slash.
func codexPlainPathMentions(text string) []string {
	var out []string
	for _, field := range strings.FieldsFunc(text, codexMentionSeparator) {
		if mention, ok := codexNormalizeMention(field); ok {
			out = append(out, mention)
			if len(out) >= codexMentionMaxPerText {
				break
			}
		}
	}
	return out
}

// codexCodeSpanMention accepts an inline code span only when it is a single
// bare name, so commands and expressions never qualify.
func codexCodeSpanMention(code string) (string, bool) {
	fields := strings.FieldsFunc(code, codexMentionSeparator)
	if len(fields) != 1 {
		return "", false
	}
	return codexNormalizeMention(fields[0])
}

func codexNormalizeMention(token string) (string, bool) {
	token = strings.TrimSpace(token)
	for strings.HasSuffix(token, ".") || strings.HasSuffix(token, "!") || strings.HasSuffix(token, "?") {
		token = token[:len(token)-1]
	}
	token = strings.TrimPrefix(token, "./")
	if token == "" || len(token) > codexMentionMaxLen {
		return "", false
	}
	isDir := strings.HasSuffix(token, "/")
	segments := strings.Split(strings.TrimSuffix(token, "/"), "/")
	for _, segment := range segments {
		if !codexMentionSegmentOK(segment) {
			return "", false
		}
	}
	if !isDir && !codexMentionFileName(segments[len(segments)-1]) {
		return "", false
	}
	return strings.Join(segments, "/"), true
}

func codexMentionSegmentOK(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	for _, r := range segment {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-@+", r) {
			return false
		}
	}
	return true
}

// codexMentionFileName requires a real-looking extension: not a version
// number ("1.2") and not an abbreviation ("e.g").
func codexMentionFileName(name string) bool {
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 {
		return false
	}
	stem, ext := name[:dot], name[dot+1:]
	if len(ext) == 0 || len(ext) > 10 {
		return false
	}
	hasLetter := false
	for _, r := range ext {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
	}
	if !hasLetter {
		return false
	}
	return len([]rune(stem)) > 1 || len(ext) > 1
}

func codexMentionOpenTarget(mention, projectPath string) (codexArtifactOpenTarget, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || mention == "" {
		return codexArtifactOpenTarget{}, false
	}
	path := filepath.Join(projectPath, filepath.FromSlash(mention))
	kind := codexArtifactKindForPath(path)
	if kind == "" {
		kind = "file"
	}
	return codexArtifactOpenTarget{
		Kind:                    kind,
		Label:                   filepath.Base(path),
		Path:                    path,
		implicitProjectRelative: true,
		inferredLocalPath:       true,
	}, true
}

// codexMentionTargets appends unseen mentions found in prose.
func codexMentionTargets(targets []codexArtifactOpenTarget, text, projectPath string, seen map[string]struct{}) []codexArtifactOpenTarget {
	for _, mention := range codexPlainPathMentions(text) {
		targets = appendCodexMentionTarget(targets, mention, projectPath, seen)
	}
	return targets
}

func appendCodexMentionTarget(targets []codexArtifactOpenTarget, mention, projectPath string, seen map[string]struct{}) []codexArtifactOpenTarget {
	if _, dup := seen[mention]; dup {
		return targets
	}
	seen[mention] = struct{}{}
	if target, ok := codexMentionOpenTarget(mention, projectPath); ok {
		targets = append(targets, target)
	}
	return targets
}

type codexPathProbe struct {
	exists bool
	isDir  bool
}

// Only call from a background command. Keep unconfirmed candidates in the scan
// state so later command output can resolve a project-relative path correctly.
func verifyCodexInferredArtifactPaths(projectPath string, targets []codexArtifactOpenTarget) []codexArtifactOpenTarget {
	out := append([]codexArtifactOpenTarget(nil), targets...)
	probes := make(map[string]codexPathProbe)
	probe := func(path string) codexPathProbe {
		if cached, ok := probes[path]; ok {
			return cached
		}
		info, err := os.Stat(path)
		result := codexPathProbe{}
		if err == nil {
			result.isDir = info.IsDir()
			result.exists = result.isDir || info.Mode().IsRegular()
		}
		probes[path] = result
		return result
	}

	var unresolved []int
	for i := range out {
		target := &out[i]
		if !target.inferredLocalPath || target.verifiedPath == target.Path {
			continue
		}
		// A bare name's nearest folder beats the project root, so it skips the
		// plain existence check and goes straight to nearby-folder resolution.
		if target.implicitProjectRelative && codexBareProjectMention(*target, projectPath) {
			unresolved = append(unresolved, i)
			continue
		}
		if probe(target.Path).exists {
			target.verifiedPath = target.Path
			continue
		}
		if target.implicitProjectRelative {
			unresolved = append(unresolved, i)
		}
	}
	if len(unresolved) == 0 {
		return out
	}

	nearby := newCodexNearbyFolders(out, probe)
	for _, i := range unresolved {
		target := &out[i]
		rel, ok := codexProjectRelativeTargetSuffix(target.Path, projectPath)
		if !ok {
			continue
		}
		tiers := [][]string(nil)
		if target.sourceLocated {
			tiers = nearby.tiers(target.sourceEntry)
		}
		if !strings.Contains(rel, "/") {
			tiers = append(tiers, []string{filepath.Clean(projectPath)})
		}
		if path, found := resolveCodexMention(rel, tiers, probe); found {
			applyResolvedCodexMention(target, path, probe(path))
		}
	}
	return out
}

func codexBareProjectMention(target codexArtifactOpenTarget, projectPath string) bool {
	rel, ok := codexProjectRelativeTargetSuffix(target.Path, projectPath)
	return ok && !strings.Contains(rel, "/")
}

// resolveCodexMention returns the existing path in the first tier that has
// one. A tier with several different matches is ambiguous, so it resolves
// nothing, and later tiers are not consulted.
func resolveCodexMention(rel string, tiers [][]string, probe func(string) codexPathProbe) (string, bool) {
	for _, folders := range tiers {
		var matches []string
		for _, folder := range folders {
			candidate := filepath.Join(folder, filepath.FromSlash(rel))
			if !probe(candidate).exists {
				continue
			}
			duplicate := false
			for _, existing := range matches {
				if existing == candidate {
					duplicate = true
				}
			}
			if !duplicate {
				matches = append(matches, candidate)
			}
		}
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0], true
		default:
			return "", false
		}
	}
	return "", false
}

func applyResolvedCodexMention(target *codexArtifactOpenTarget, path string, probed codexPathProbe) {
	switch {
	case probed.isDir:
		target.Kind = "dir"
	case codexArtifactKindForPath(path) != "":
		target.Kind = codexArtifactKindForPath(path)
	default:
		target.Kind = "file"
	}
	target.Path = path
	target.Label = filepath.Base(path)
	target.PreviewData = nil
	target.verifiedPath = path
	target.implicitProjectRelative = false
	target.resolvedProjectRelative = true
}

// codexNearbyFolders lists the folders that confirmed targets point into,
// grouped by the transcript entry that mentioned them.
type codexNearbyFolders struct {
	targetsByEntry map[int][]int
	targets        []codexArtifactOpenTarget
	probe          func(string) codexPathProbe
	memo           map[int][]string
}

func newCodexNearbyFolders(targets []codexArtifactOpenTarget, probe func(string) codexPathProbe) *codexNearbyFolders {
	nearby := &codexNearbyFolders{
		targetsByEntry: make(map[int][]int),
		targets:        targets,
		probe:          probe,
		memo:           make(map[int][]string),
	}
	for i, target := range targets {
		if !target.sourceLocated || strings.TrimSpace(target.Kind) == "url" || !filepath.IsAbs(strings.TrimSpace(target.Path)) {
			continue
		}
		if target.inferredLocalPath && target.verifiedPath != target.Path {
			continue
		}
		nearby.targetsByEntry[target.sourceEntry] = append(nearby.targetsByEntry[target.sourceEntry], i)
	}
	return nearby
}

func (n *codexNearbyFolders) foldersForEntry(entry int) []string {
	if folders, ok := n.memo[entry]; ok {
		return folders
	}
	var folders []string
	seen := make(map[string]struct{})
	for _, index := range n.targetsByEntry[entry] {
		path := filepath.Clean(strings.TrimSpace(n.targets[index].Path))
		folder := filepath.Dir(path)
		if n.probe(path).isDir {
			folder = path
		}
		if codexArtifactPathIsFilesystemRoot(folder) {
			continue
		}
		if _, dup := seen[folder]; dup {
			continue
		}
		seen[folder] = struct{}{}
		folders = append(folders, folder)
	}
	n.memo[entry] = folders
	return folders
}

// tiers returns the same entry's folders first, then those from the entries
// just before it, nearest first.
func (n *codexNearbyFolders) tiers(entry int) [][]string {
	var tiers [][]string
	seen := make(map[string]struct{})
	same := capCodexFolders(n.foldersForEntry(entry), seen)
	if len(same) > 0 {
		tiers = append(tiers, same)
	}
	var earlier []string
	for previous := entry - 1; previous >= 0 && previous >= entry-codexMentionEvidenceEntries; previous-- {
		earlier = append(earlier, capCodexFolders(n.foldersForEntry(previous), seen)...)
		if len(earlier) >= codexMentionMaxFoldersPerTier {
			break
		}
	}
	if len(earlier) > codexMentionMaxFoldersPerTier {
		earlier = earlier[:codexMentionMaxFoldersPerTier]
	}
	if len(earlier) > 0 {
		tiers = append(tiers, earlier)
	}
	return tiers
}

func capCodexFolders(folders []string, seen map[string]struct{}) []string {
	var out []string
	for _, folder := range folders {
		if _, dup := seen[folder]; dup {
			continue
		}
		seen[folder] = struct{}{}
		out = append(out, folder)
		if len(out) >= codexMentionMaxFoldersPerTier {
			break
		}
	}
	return out
}
