package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const terminalHealthCheckEveryTicks = int((5 * time.Second) / spinnerTickInterval)

type terminalHealthSnapshot struct {
	Supported       bool
	PaneID          string
	AlternateScreen bool
	CursorHidden    bool
}

func (s terminalHealthSnapshot) Healthy() bool {
	return !s.Supported || (s.AlternateScreen && s.CursorHidden)
}

type terminalHealthCheckMsg struct {
	snapshot terminalHealthSnapshot
	err      error
}

type terminalRepairFinishedMsg struct {
	automatic bool
}

var terminalHealthProbe = probeTmuxTerminalHealth

func (m *Model) requestTerminalHealthCheckCmd() tea.Cmd {
	if m == nil || m.terminalHealthCheckInFlight || m.terminalRepairInFlight || strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return nil
	}
	m.terminalHealthCheckInFlight = true
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		snapshot, err := terminalHealthProbe(ctx)
		return terminalHealthCheckMsg{snapshot: snapshot, err: err}
	}
}

func probeTmuxTerminalHealth(parent context.Context) (terminalHealthSnapshot, error) {
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return terminalHealthSnapshot{}, nil
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	ttyPath, err := terminalTTYPath(ctx, os.Stdout)
	if err != nil {
		return terminalHealthSnapshot{}, nil
	}
	output, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_tty}\t#{alternate_on}\t#{cursor_flag}\t#{pane_id}").Output()
	if err != nil {
		return terminalHealthSnapshot{}, fmt.Errorf("inspect tmux terminal state: %w", err)
	}
	return parseTmuxTerminalHealth(string(output), ttyPath)
}

func terminalTTYPath(ctx context.Context, output *os.File) (string, error) {
	if output == nil {
		return "", errors.New("terminal output is unavailable")
	}
	cmd := exec.CommandContext(ctx, "tty")
	cmd.Stdin = output
	raw, err := cmd.Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(path, "/dev/") {
		return "", fmt.Errorf("unexpected tty path %q", path)
	}
	return path, nil
}

func parseTmuxTerminalHealth(output, ttyPath string) (terminalHealthSnapshot, error) {
	ttyPath = filepath.Clean(strings.TrimSpace(ttyPath))
	if ttyPath == "." || ttyPath == "" {
		return terminalHealthSnapshot{}, nil
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 4 || filepath.Clean(strings.TrimSpace(fields[0])) != ttyPath {
			continue
		}
		return terminalHealthSnapshot{
			Supported:       true,
			PaneID:          strings.TrimSpace(fields[3]),
			AlternateScreen: strings.TrimSpace(fields[1]) == "1",
			CursorHidden:    strings.TrimSpace(fields[2]) == "0",
		}, nil
	}
	if err := scanner.Err(); err != nil {
		return terminalHealthSnapshot{}, fmt.Errorf("read tmux terminal state: %w", err)
	}
	return terminalHealthSnapshot{}, nil
}

func (m Model) applyTerminalHealthCheckMsg(msg terminalHealthCheckMsg) (tea.Model, tea.Cmd) {
	m.terminalHealthCheckInFlight = false
	if msg.err != nil || msg.snapshot.Healthy() || m.terminalRepairInFlight {
		return m, nil
	}
	m.terminalRepairInFlight = true
	detail := terminalHealthProblem(msg.snapshot)
	m.appendErrorLogEntry("Terminal state drift detected", errors.New(detail), "")
	m.status = "Terminal state was reset outside LCR; repairing display and paste handling..."
	return m, m.terminalRepairCmd(true)
}

func terminalHealthProblem(snapshot terminalHealthSnapshot) string {
	problems := make([]string, 0, 2)
	if !snapshot.AlternateScreen {
		problems = append(problems, "alternate screen is disabled")
	}
	if !snapshot.CursorHidden {
		problems = append(problems, "hardware cursor is visible")
	}
	detail := strings.Join(problems, " and ")
	if detail == "" {
		detail = "terminal modes do not match the running TUI"
	}
	if strings.TrimSpace(snapshot.PaneID) != "" {
		detail = "tmux pane " + strings.TrimSpace(snapshot.PaneID) + ": " + detail
	}
	return detail
}

func (m Model) beginTerminalRepair(automatic bool) (tea.Model, tea.Cmd) {
	if m.terminalRepairInFlight {
		m.status = "Terminal repair is already in progress"
		return m, nil
	}
	m.terminalRepairInFlight = true
	if automatic {
		m.status = "Terminal state was reset outside LCR; repairing display and paste handling..."
	} else {
		m.status = "Reinitializing terminal display and paste handling..."
	}
	return m, m.terminalRepairCmd(automatic)
}

func (m Model) terminalRepairCmd(automatic bool) tea.Cmd {
	commands := []tea.Cmd{
		tea.ExitAltScreen,
		tea.EnterAltScreen,
		tea.HideCursor,
		tea.EnableBracketedPaste,
	}
	if m.mouseEnabled {
		commands = append(commands, tea.EnableMouseCellMotion)
	} else {
		commands = append(commands, tea.DisableMouse)
	}
	commands = append(commands, func() tea.Msg {
		return terminalRepairFinishedMsg{automatic: automatic}
	})
	return tea.Sequence(commands...)
}

func (m Model) applyTerminalRepairFinishedMsg(msg terminalRepairFinishedMsg) (tea.Model, tea.Cmd) {
	m.terminalRepairInFlight = false
	if msg.automatic {
		m.status = "Terminal display and paste handling repaired. Restart LCR if input still feels wrong."
	} else {
		m.status = "Terminal display and paste handling reinitialized"
	}
	return m, nil
}
