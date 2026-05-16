package codexapp

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

const lcagentReviewDiffMaxBytes = 48 * 1024

func lcagentReviewPrompt(projectPath string) (string, bool, error) {
	status, err := lcagentGitOutput(projectPath, "status", "--short")
	if err != nil {
		return "", false, fmt.Errorf("read git status for LCAgent review: %w", err)
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return "", false, nil
	}
	diffStat, err := lcagentGitOutput(projectPath, "diff", "--no-ext-diff", "HEAD", "--stat")
	if err != nil {
		return "", false, fmt.Errorf("read git diff stat for LCAgent review: %w", err)
	}
	diff, err := lcagentGitOutputLimited(projectPath, lcagentReviewDiffMaxBytes, "diff", "--no-ext-diff", "HEAD", "--")
	if err != nil {
		return "", false, fmt.Errorf("read git diff for LCAgent review: %w", err)
	}
	var b strings.Builder
	b.WriteString("Review the current uncommitted changes in this repository.\n\n")
	b.WriteString("Requirements:\n")
	b.WriteString("- Do not edit files.\n")
	b.WriteString("- Focus on concrete bugs, regressions, missing tests, risky behavior, and unclear verification.\n")
	b.WriteString("- Use read_file/search/file_outline if you need more context before judging a change.\n")
	b.WriteString("- Do not use apply_patch or replace_text.\n")
	b.WriteString("- Finish with final_response. Put findings first, with file paths and line references when possible. If no issues are found, say that clearly and mention residual risk.\n\n")
	b.WriteString("Git status --short:\n")
	b.WriteString(fencedBlock(status))
	if strings.TrimSpace(diffStat) != "" {
		b.WriteString("\nGit diff --stat HEAD:\n")
		b.WriteString(fencedBlock(strings.TrimSpace(diffStat)))
	}
	if strings.TrimSpace(diff) != "" {
		b.WriteString("\nGit diff HEAD")
		if len(diff) >= lcagentReviewDiffMaxBytes {
			b.WriteString(" (truncated)")
		}
		b.WriteString(":\n")
		b.WriteString(fencedBlock(strings.TrimSpace(diff)))
	}
	return b.String(), true, nil
}

func lcagentGitOutput(projectPath string, args ...string) (string, error) {
	return lcagentGitOutputLimited(projectPath, 0, args...)
}

func lcagentGitOutputLimited(projectPath string, limit int, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", projectPath}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("%s", detail)
	}
	out := stdout.String()
	if limit > 0 && len(out) > limit {
		out = out[:limit] + "\n[diff truncated]\n"
	}
	return out, nil
}

func fencedBlock(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		text = "(empty)"
	}
	return "```text\n" + text + "\n```\n"
}
