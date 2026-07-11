package commandguard

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const DirectRMDenialReason = "direct rm commands are disabled in Little Control Room agent sessions; use targeted file or patch tools, or ask the user to run intentional cleanup manually"

// ContainsDirectRM reports whether a shell program contains a literal rm
// invocation. It parses shell structure so quoted examples such as
// `printf 'rm -rf /'` are not mistaken for executable commands.
func ContainsDirectRM(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		file, err = syntax.NewParser(syntax.Variant(syntax.LangZsh)).Parse(strings.NewReader(command), "")
		if err != nil {
			return false
		}
	}
	found := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		argv, ok := literalArgv(call.Args)
		if ok && ArgvContainsDirectRM(argv) {
			found = true
			return false
		}
		return true
	})
	return found
}

// ArgvContainsDirectRM recognizes a direct rm invocation or one contained in a
// literal script passed to a common shell executable.
func ArgvContainsDirectRM(argv []string) bool {
	if ArgvInvokesRM(argv) {
		return true
	}
	script, ok := shellScriptFromArgv(argv)
	return ok && ContainsDirectRM(script)
}

// ArgvInvokesRM recognizes rm directly and behind common execution wrappers.
// It intentionally does not classify arbitrary scripts or programs that may
// delete files internally.
func ArgvInvokesRM(argv []string) bool {
	argv = unwrapExecutionWrappers(argv)
	return len(argv) > 0 && commandBase(argv[0]) == "rm"
}

func unwrapExecutionWrappers(argv []string) []string {
	argv = cleanArgv(argv)
	for len(argv) > 0 {
		switch commandBase(argv[0]) {
		case "command", "builtin", "nohup":
			argv = skipLeadingOptions(argv[1:], nil)
		case "exec":
			argv = skipLeadingOptions(argv[1:], map[string]bool{"-a": true})
		case "env":
			argv = unwrapEnv(argv[1:])
		case "sudo":
			argv = unwrapSudo(argv[1:])
		default:
			return argv
		}
	}
	return nil
}

func shellScriptFromArgv(argv []string) (string, bool) {
	argv = unwrapExecutionWrappers(argv)
	if len(argv) < 3 {
		return "", false
	}
	switch commandBase(argv[0]) {
	case "sh", "bash", "dash", "ksh", "zsh":
	default:
		return "", false
	}
	for i := 1; i < len(argv)-1; i++ {
		option := argv[i]
		if option == "--" || !strings.HasPrefix(option, "-") {
			return "", false
		}
		if strings.Contains(strings.TrimLeft(option, "-"), "c") {
			return argv[i+1], true
		}
	}
	return "", false
}

func literalArgv(words []*syntax.Word) ([]string, bool) {
	argv := make([]string, 0, len(words))
	for _, word := range words {
		value, ok := literalWord(word)
		if !ok {
			break
		}
		argv = append(argv, value)
	}
	return argv, len(argv) > 0
}

func literalWord(word *syntax.Word) (string, bool) {
	if word == nil {
		return "", false
	}
	var b strings.Builder
	for _, part := range word.Parts {
		value, ok := literalWordPart(part)
		if !ok {
			return "", false
		}
		b.WriteString(value)
	}
	return b.String(), true
}

func literalWordPart(part syntax.WordPart) (string, bool) {
	switch value := part.(type) {
	case *syntax.Lit:
		return value.Value, true
	case *syntax.SglQuoted:
		return value.Value, true
	case *syntax.DblQuoted:
		var b strings.Builder
		for _, nested := range value.Parts {
			text, ok := literalWordPart(nested)
			if !ok {
				return "", false
			}
			b.WriteString(text)
		}
		return b.String(), true
	default:
		return "", false
	}
}

func unwrapEnv(argv []string) []string {
	argv = cleanArgv(argv)
	for len(argv) > 0 {
		arg := argv[0]
		switch {
		case arg == "--":
			return argv[1:]
		case arg == "-u" || arg == "--unset" || arg == "-C" || arg == "--chdir":
			if len(argv) < 2 {
				return nil
			}
			argv = argv[2:]
		case strings.HasPrefix(arg, "-"):
			argv = argv[1:]
		case isEnvironmentAssignment(arg):
			argv = argv[1:]
		default:
			return argv
		}
	}
	return nil
}

func unwrapSudo(argv []string) []string {
	return skipLeadingOptions(cleanArgv(argv), map[string]bool{
		"-C": true, "--close-from": true,
		"-D": true, "--chdir": true,
		"-g": true, "--group": true,
		"-h": true, "--host": true,
		"-p": true, "--prompt": true,
		"-R": true, "--chroot": true,
		"-r": true, "--role": true,
		"-t": true, "--type": true,
		"-T": true, "--command-timeout": true,
		"-u": true, "--user": true,
	})
}

func skipLeadingOptions(argv []string, optionsWithValues map[string]bool) []string {
	argv = cleanArgv(argv)
	for len(argv) > 0 {
		arg := argv[0]
		if arg == "--" {
			return argv[1:]
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			return argv
		}
		if optionsWithValues != nil && optionsWithValues[arg] {
			if len(argv) < 2 {
				return nil
			}
			argv = argv[2:]
			continue
		}
		argv = argv[1:]
	}
	return nil
}

func cleanArgv(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, arg := range argv {
		if arg = strings.TrimSpace(arg); arg != "" {
			out = append(out, arg)
		}
	}
	return out
}

func commandBase(command string) string {
	return strings.ToLower(filepath.Base(strings.TrimSpace(command)))
}

func isEnvironmentAssignment(value string) bool {
	name, _, ok := strings.Cut(value, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}
