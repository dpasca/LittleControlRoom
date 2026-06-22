package script

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lcroom/internal/lcagent/session"
	"lcroom/internal/lcagent/tools"
)

const (
	RequirementEvidenceStatusPass          = "pass"
	RequirementEvidenceStatusWarn          = "warn"
	RequirementEvidenceStatusNotApplicable = "not_applicable"

	requirementEvidenceMaxFiles = 12
	requirementEvidenceMaxDeps  = 16
)

type RequirementEvidenceAudit struct {
	Status string                     `json:"status"`
	Scope  string                     `json:"scope,omitempty"`
	Files  []RequirementEvidenceFile  `json:"files,omitempty"`
	Issues []RequirementEvidenceIssue `json:"issues,omitempty"`
}

type RequirementEvidenceFile struct {
	Path                 string   `json:"path"`
	Kind                 string   `json:"kind,omitempty"`
	ExternalDependencies []string `json:"external_dependencies,omitempty"`
}

type RequirementEvidenceIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func (a RequirementEvidenceAudit) HasEvidence() bool {
	return strings.TrimSpace(a.Status) != "" && a.Status != RequirementEvidenceStatusNotApplicable
}

func (r *Runner) requirementEvidenceAuditForAction(action Action) RequirementEvidenceAudit {
	if r == nil {
		return RequirementEvidenceAudit{Status: RequirementEvidenceStatusNotApplicable}
	}
	paths := cleanStringList(action.FilesChanged)
	if len(paths) == 0 {
		paths = r.FilesTouched()
	}
	return r.RequirementEvidenceAudit(paths, r.verificationChecks)
}

func (r *Runner) RequirementEvidenceAudit(paths []string, checks []tools.VerificationCheck) RequirementEvidenceAudit {
	if r == nil || strings.TrimSpace(r.Files.Workspace.Root) == "" {
		return RequirementEvidenceAudit{Status: RequirementEvidenceStatusNotApplicable}
	}
	paths = requirementEvidenceTargetPaths(paths)
	if len(paths) == 0 {
		return RequirementEvidenceAudit{Status: RequirementEvidenceStatusNotApplicable}
	}
	audit := RequirementEvidenceAudit{
		Status: RequirementEvidenceStatusPass,
		Scope:  "generated_artifact_source",
	}
	for _, path := range paths {
		file, issues := r.requirementEvidenceForFile(path)
		if file.Path == "" && len(issues) == 0 {
			continue
		}
		if file.Path != "" {
			audit.Files = append(audit.Files, file)
		}
		audit.Issues = append(audit.Issues, issues...)
	}
	audit.Issues = append(audit.Issues, requirementEvidenceIssuesFromChecks(checks)...)
	if len(audit.Files) == 0 && len(audit.Issues) == 0 {
		return RequirementEvidenceAudit{Status: RequirementEvidenceStatusNotApplicable}
	}
	if len(audit.Issues) > 0 {
		audit.Status = RequirementEvidenceStatusWarn
	}
	return audit
}

func (r *Runner) RequirementEvidenceDetails(paths []string) []string {
	audit := r.RequirementEvidenceAudit(paths, nil)
	if !audit.HasEvidence() {
		return nil
	}
	return []string{requirementEvidenceAuditMessage(audit)}
}

func (r *Runner) requirementEvidenceForFile(path string) (RequirementEvidenceFile, []RequirementEvidenceIssue) {
	path = strings.TrimSpace(path)
	if path == "" || !requirementEvidenceSourcePath(path) {
		return RequirementEvidenceFile{}, nil
	}
	file := RequirementEvidenceFile{Path: path, Kind: requirementEvidenceKind(path)}
	target, err := r.Files.Workspace.ResolveRead(path)
	if err != nil {
		return file, []RequirementEvidenceIssue{{
			Code:    "artifact_read_failed",
			Path:    path,
			Message: "could not inspect generated artifact: " + err.Error(),
		}}
	}
	body, err := os.ReadFile(target)
	if err != nil {
		return file, []RequirementEvidenceIssue{{
			Code:    "artifact_read_failed",
			Path:    path,
			Message: "could not inspect generated artifact: " + err.Error(),
		}}
	}
	deps := externalDependenciesForSource(path, string(body))
	file.ExternalDependencies = deps
	if len(deps) == 0 {
		return file, nil
	}
	return file, []RequirementEvidenceIssue{{
		Code:    "external_source_dependency",
		Path:    path,
		Message: fmt.Sprintf("source imports/includes non-standard dependency evidence: %s", strings.Join(deps, ", ")),
	}}
}

func requirementEvidenceTargetPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
		if len(out) >= requirementEvidenceMaxFiles {
			break
		}
	}
	sort.Strings(out)
	return out
}

func requirementEvidenceSourcePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".m", ".mm",
		".go", ".rs", ".swift", ".kt", ".kts", ".java", ".cs", ".js", ".jsx", ".ts", ".tsx", ".py", ".rb", ".php":
		return true
	default:
		return false
	}
}

func requirementEvidenceKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".m", ".mm":
		return "c_cpp"
	case ".js", ".jsx", ".ts", ".tsx":
		return "javascript"
	default:
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		if ext == "" {
			return "source"
		}
		return ext
	}
}

func externalDependenciesForSource(path, body string) []string {
	switch requirementEvidenceKind(path) {
	case "c_cpp":
		return externalCppDependencies(body)
	default:
		return nil
	}
}

func externalCppDependencies(body string) []string {
	deps := map[string]struct{}{}
	for _, line := range strings.Split(body, "\n") {
		dep := cppIncludeDependency(line)
		if dep == "" || cppHeaderLooksStandard(dep) {
			continue
		}
		deps[dep] = struct{}{}
	}
	return sortedLimitedKeys(deps, requirementEvidenceMaxDeps)
}

func cppIncludeDependency(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "#include") {
		return ""
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "#include"))
	if line == "" {
		return ""
	}
	if line[0] == '<' {
		if end := strings.IndexByte(line, '>'); end > 1 {
			return strings.TrimSpace(line[1:end])
		}
	}
	if line[0] == '"' {
		if end := strings.IndexByte(line[1:], '"'); end >= 0 {
			return strings.TrimSpace(line[1 : end+1])
		}
	}
	return ""
}

func cppHeaderLooksStandard(dep string) bool {
	dep = strings.TrimSpace(dep)
	if dep == "" {
		return true
	}
	if strings.HasPrefix(dep, "sys/") || strings.HasPrefix(dep, "mach/") || strings.HasPrefix(dep, "linux/") || strings.HasPrefix(dep, "unistd") {
		return true
	}
	if strings.Contains(dep, "/") {
		return false
	}
	if strings.HasSuffix(dep, ".h") {
		return knownCHeader(dep)
	}
	return knownCppHeader(dep)
}

func knownCHeader(dep string) bool {
	switch dep {
	case "assert.h", "ctype.h", "errno.h", "float.h", "limits.h", "locale.h", "math.h", "setjmp.h", "signal.h", "stdarg.h", "stddef.h", "stdint.h", "stdio.h", "stdlib.h", "string.h", "time.h", "wchar.h", "wctype.h":
		return true
	default:
		return false
	}
}

func knownCppHeader(dep string) bool {
	switch dep {
	case "algorithm", "array", "atomic", "bitset", "chrono", "cmath", "condition_variable", "cstdint", "cstdio", "cstdlib", "cstring", "deque", "exception", "filesystem", "fstream", "functional", "future", "iomanip", "ios", "iostream", "istream", "iterator", "limits", "list", "map", "memory", "mutex", "numeric", "optional", "ostream", "queue", "random", "regex", "set", "sstream", "stack", "stdexcept", "string", "string_view", "thread", "tuple", "type_traits", "unordered_map", "unordered_set", "utility", "vector":
		return true
	default:
		return false
	}
}

func requirementEvidenceIssuesFromChecks(checks []tools.VerificationCheck) []RequirementEvidenceIssue {
	seen := map[string]struct{}{}
	var issues []RequirementEvidenceIssue
	for _, check := range checks {
		command := strings.TrimSpace(check.Command)
		if command == "" {
			command = strings.Join(check.Argv, " ")
		}
		for _, dep := range externalBuildDependencyEvidence(command) {
			if _, ok := seen[dep]; ok {
				continue
			}
			seen[dep] = struct{}{}
			issues = append(issues, RequirementEvidenceIssue{
				Code:    "external_build_dependency",
				Message: fmt.Sprintf("verification/build command references external dependency evidence: %s", dep),
			})
		}
	}
	return issues
}

func externalBuildDependencyEvidence(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	candidates := []string{"/opt/homebrew", "-lglfw", "glfw3", "-lSDL", "sdl2", "-lraylib", "pkg-config", "-framework OpenGL"}
	var out []string
	lower := strings.ToLower(command)
	for _, candidate := range candidates {
		if strings.Contains(lower, strings.ToLower(candidate)) {
			out = append(out, candidate)
		}
	}
	return out
}

func sortedLimitedKeys(values map[string]struct{}, limit int) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func requirementEvidenceAuditMessage(a RequirementEvidenceAudit) string {
	if len(a.Issues) == 0 {
		return "No requirement evidence issues were found."
	}
	parts := make([]string, 0, len(a.Issues))
	for i, issue := range a.Issues {
		if i >= 3 {
			parts = append(parts, fmt.Sprintf("%d more issue(s)", len(a.Issues)-i))
			break
		}
		message := strings.TrimSpace(issue.Message)
		if path := strings.TrimSpace(issue.Path); path != "" {
			message = path + ": " + message
		}
		parts = append(parts, message)
	}
	return strings.Join(parts, "; ")
}

func (r *Runner) WriteRequirementEvidenceAudit(audit RequirementEvidenceAudit) error {
	if r == nil || r.Session == nil || !audit.HasEvidence() {
		return nil
	}
	return r.Session.Write(session.Event{
		"type":       "requirement_evidence_audit",
		"session_id": r.SessionID,
		"status":     audit.Status,
		"scope":      audit.Scope,
		"files":      audit.Files,
		"issues":     audit.Issues,
	})
}
