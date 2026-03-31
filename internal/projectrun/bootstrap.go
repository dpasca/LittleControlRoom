package projectrun

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func ensureRuntimeDependencies(projectPath, runCommand string) error {
	bootstrapCommand, ok := packageManagerBootstrapCommand(runCommand)
	if !ok {
		return nil
	}

	shellProgram := defaultShellProgram()
	cmd := exec.Command(shellProgram, "-lc", bootstrapCommand)
	cmd.Dir = projectPath
	output := &bytes.Buffer{}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		details := strings.TrimSpace(output.String())
		if details == "" {
			return fmt.Errorf("run install command: %w", err)
		}
		return fmt.Errorf("run install command: %w: %s", err, details)
	}
	return nil
}

func packageManagerBootstrapCommand(runCommand string) (string, bool) {
	runCommand = strings.TrimSpace(runCommand)
	if runCommand == "" {
		return "", false
	}

	workingDirectory := ""
	commandPart := runCommand
	if prefix, rest := splitLeadingCdCommand(runCommand); prefix != "" {
		workingDirectory = prefix
		commandPart = rest
	}

	manager, installCommand, isInstall := detectPackageManagerCommand(commandPart)
	if manager == "" || isInstall {
		return "", false
	}
	condition := dependencyMissingCondition(manager)
	bootstrap := "if " + condition + "; then " + installCommand + "; fi"
	if workingDirectory != "" {
		bootstrap = workingDirectory + " && " + bootstrap
	}
	return bootstrap, true
}

func splitLeadingCdCommand(runCommand string) (string, string) {
	runCommand = strings.TrimSpace(runCommand)
	firstAnd := strings.Index(runCommand, "&&")
	if firstAnd < 0 {
		return "", runCommand
	}
	prefix := strings.TrimSpace(runCommand[:firstAnd])
	rest := strings.TrimSpace(runCommand[firstAnd+2:])
	if !strings.HasPrefix(prefix, "cd ") || rest == "" {
		return "", runCommand
	}
	return prefix, rest
}

func detectPackageManagerCommand(command string) (manager string, installCommand string, isInstall bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", "", false
	}
	manager, index := parseKnownPackageManager(fields)
	if manager == "" {
		return "", "", false
	}
	isInstall = index+1 < len(fields) && (fields[index+1] == "install" || fields[index+1] == "ci")
	return manager, managerInstallCommand(fields, index), isInstall
}

func parseKnownPackageManager(fields []string) (string, int) {
	if len(fields) == 0 {
		return "", -1
	}
	switch fields[0] {
	case "pnpm", "yarn", "bun", "npm":
		return fields[0], 0
	case "corepack":
		if len(fields) < 2 {
			return "", -1
		}
		switch fields[1] {
		case "pnpm", "yarn", "bun", "npm":
			return fields[1], 1
		}
		return "", -1
	default:
		return "", -1
	}
}

func managerInstallCommand(fields []string, managerIndex int) string {
	if managerIndex < 0 || managerIndex >= len(fields) {
		return ""
	}
	if managerIndex > 0 && fields[0] == "corepack" {
		if managerIndex+1 >= len(fields) {
			return ""
		}
		return "corepack " + fields[managerIndex] + " install"
	}
	return fields[managerIndex] + " install"
}

func dependencyMissingCondition(manager string) string {
	switch manager {
	case "pnpm":
		return "[ ! -d node_modules ] || [ ! -f pnpm-lock.yaml ]"
	case "yarn":
		return "[ ! -d node_modules ] || [ ! -f yarn.lock ]"
	case "bun":
		return "[ ! -d node_modules ] || [ ! -f bun.lock ] && [ ! -f bun.lockb ]"
	case "npm":
		return "[ ! -d node_modules ] || [ ! -f package-lock.json ]"
	default:
		return "[ ! -d node_modules ]"
	}
}
