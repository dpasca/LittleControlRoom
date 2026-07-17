package projectrun

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"
)

// PathCompletionEntry is one project-directory entry that can participate in
// run-command path completion.
type PathCompletionEntry struct {
	Name       string
	Directory  bool
	Executable bool
}

// PathCompletionQuery describes the project directory and filename prefix for
// the shell word currently being edited.
type PathCompletionQuery struct {
	Directory  string
	NamePrefix string
	FirstWord  bool

	commandPrefix string
	rawToken      string
	quote         rune
}

// ParsePathCompletion recognizes a project-relative path in the shell word at
// the end of command. Path completion is deliberately scoped to "./..." paths
// so browsing never escapes the selected project.
func ParsePathCompletion(command string, cursor int) (PathCompletionQuery, bool) {
	runes := []rune(command)
	if cursor != len(runes) {
		return PathCompletionQuery{}, false
	}

	start, activeQuote, firstWord, ok := activeShellWord(runes)
	if !ok {
		return PathCompletionQuery{}, false
	}

	rawToken := string(runes[start:])
	decoded, quote, ok := decodePathToken(rawToken, activeQuote)
	if !ok || !strings.HasPrefix(decoded, "./") {
		return PathCompletionQuery{}, false
	}

	relative := strings.TrimPrefix(decoded, "./")
	var directory, namePrefix string
	if strings.HasSuffix(relative, "/") {
		directory = strings.TrimSuffix(relative, "/")
	} else if slash := strings.LastIndex(relative, "/"); slash >= 0 {
		directory = relative[:slash]
		namePrefix = relative[slash+1:]
	} else {
		namePrefix = relative
	}
	if directory == "" {
		directory = "."
	}
	if hasParentPathComponent(directory) {
		return PathCompletionQuery{}, false
	}
	directory = path.Clean(directory)
	if directory == ".." || strings.HasPrefix(directory, "../") || path.IsAbs(directory) {
		return PathCompletionQuery{}, false
	}

	return PathCompletionQuery{
		Directory:     directory,
		NamePrefix:    namePrefix,
		FirstWord:     firstWord,
		commandPrefix: string(runes[:start]),
		rawToken:      rawToken,
		quote:         quote,
	}, true
}

// Suggestions filters a cached directory listing for this query and returns
// whole-command candidates suitable for bubbles/textinput.
func (q PathCompletionQuery) Suggestions(entries []PathCompletionEntry) []Suggestion {
	out := make([]Suggestion, 0, len(entries))
	for _, entry := range entries {
		if entry.Name == "" || !strings.HasPrefix(entry.Name, q.NamePrefix) {
			continue
		}
		if strings.HasPrefix(entry.Name, ".") && !strings.HasPrefix(q.NamePrefix, ".") {
			continue
		}
		if !entry.Directory && q.FirstWord && !entry.Executable {
			continue
		}

		suffix := strings.TrimPrefix(entry.Name, q.NamePrefix)
		completion := q.commandPrefix + q.rawToken + encodePathSuffix(suffix, q.quote)
		reason := "File under the selected project."
		if entry.Directory {
			completion += "/"
			reason = "Directory under the selected project."
		} else if q.quote != 0 {
			completion += string(q.quote)
		}
		if entry.Executable {
			reason = "Executable file under the selected project."
		}
		out = append(out, Suggestion{Command: completion, Reason: reason})
	}
	return out
}

// ReadPathCompletionEntries reads one directory beneath projectPath. Symlinks
// are omitted so an apparently project-local path cannot make discovery walk
// outside the selected project.
func ReadPathCompletionEntries(projectPath, directory string) ([]PathCompletionEntry, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return nil, fmt.Errorf("project path is required")
	}

	root, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, fmt.Errorf("resolve project path: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project path: %w", err)
	}

	directory = filepath.Clean(filepath.FromSlash(strings.TrimSpace(directory)))
	if directory == "" {
		directory = "."
	}
	if filepath.IsAbs(directory) || directory == ".." || strings.HasPrefix(directory, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("path completion directory must stay within the project")
	}

	target := filepath.Join(root, directory)
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve completion directory: %w", err)
	}
	if !pathWithinRoot(root, target) {
		return nil, fmt.Errorf("path completion directory must stay within the project")
	}

	dirEntries, err := os.ReadDir(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read completion directory: %w", err)
	}

	entries := make([]PathCompletionEntry, 0, len(dirEntries))
	for _, entry := range dirEntries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		entries = append(entries, PathCompletionEntry{
			Name:       entry.Name(),
			Directory:  info.IsDir(),
			Executable: pathEntryExecutable(entry.Name(), info),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Directory != entries[j].Directory {
			return entries[i].Directory
		}
		left := strings.ToLower(entries[i].Name)
		right := strings.ToLower(entries[j].Name)
		if left == right {
			return entries[i].Name < entries[j].Name
		}
		return left < right
	})
	return entries, nil
}

func activeShellWord(runes []rune) (start int, quote rune, firstWord bool, ok bool) {
	start = -1
	var escaped bool
	var commandHasWord bool

	for i, r := range runes {
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			switch {
			case r == quote:
				quote = 0
			case r == '\\' && quote == '"':
				escaped = true
			}
			continue
		}

		switch {
		case unicode.IsSpace(r):
			start = -1
		case isShellCommandBoundary(r):
			start = -1
			commandHasWord = false
		case r == '\\':
			if start < 0 {
				start = i
				firstWord = !commandHasWord
				commandHasWord = true
			}
			escaped = true
		case r == '\'' || r == '"':
			if start < 0 {
				start = i
				firstWord = !commandHasWord
				commandHasWord = true
			}
			quote = r
		default:
			if start < 0 {
				start = i
				firstWord = !commandHasWord
				commandHasWord = true
			}
		}
	}
	return start, quote, firstWord, start >= 0 && !escaped
}

func isShellCommandBoundary(r rune) bool {
	return strings.ContainsRune(";|&()", r)
}

func decodePathToken(raw string, activeQuote rune) (string, rune, bool) {
	if raw == "" {
		return "", 0, false
	}
	switch raw[0] {
	case '\'':
		if activeQuote != '\'' {
			return "", 0, false
		}
		return raw[1:], '\'', true
	case '"':
		if activeQuote != '"' {
			return "", 0, false
		}
		decoded, ok := decodeBackslashEscapes(raw[1:], true)
		return decoded, '"', ok
	default:
		if activeQuote != 0 {
			return "", 0, false
		}
		decoded, ok := decodeBackslashEscapes(raw, false)
		return decoded, 0, ok
	}
}

func decodeBackslashEscapes(value string, quoted bool) (string, bool) {
	runes := []rune(value)
	var out strings.Builder
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			if i+1 >= len(runes) {
				return "", false
			}
			i++
			out.WriteRune(runes[i])
		case '\'', '"':
			if !quoted {
				return "", false
			}
			out.WriteRune(runes[i])
		default:
			out.WriteRune(runes[i])
		}
	}
	return out.String(), true
}

func encodePathSuffix(value string, quote rune) string {
	switch quote {
	case '\'':
		return strings.ReplaceAll(value, "'", "'\\''")
	case '"':
		var out strings.Builder
		for _, r := range value {
			if strings.ContainsRune("\\\"$`", r) {
				out.WriteRune('\\')
			}
			out.WriteRune(r)
		}
		return out.String()
	default:
		var out strings.Builder
		for _, r := range value {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-/,:+=@%", r) {
				out.WriteRune('\\')
			}
			out.WriteRune(r)
		}
		return out.String()
	}
}

func hasParentPathComponent(value string) bool {
	for _, component := range strings.Split(value, "/") {
		if component == ".." {
			return true
		}
	}
	return false
}

func pathWithinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func pathEntryExecutable(name string, info os.FileInfo) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".bat", ".cmd", ".com", ".exe", ".ps1":
			return true
		default:
			return false
		}
	}
	return info.Mode().Perm()&0o111 != 0
}
