//go:build darwin || linux

package projectrun

import (
	"bufio"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

func configureManagedCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func managedProcessGroupID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

func terminateManagedCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return ErrNotRunning
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

func currentProcessGroups() (map[int]int, error) {
	out, err := exec.Command("ps", "-Ao", "pid=,pgid=").Output()
	if err != nil {
		return nil, fmt.Errorf("list process groups: %w", err)
	}
	groups := map[int]int{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		pgid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		groups[pid] = pgid
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read process groups: %w", err)
	}
	return groups, nil
}

func currentListeningPorts() (map[int][]int, error) {
	out, err := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn").Output()
	if err != nil {
		return nil, fmt.Errorf("list listening tcp ports: %w", err)
	}
	portsByPID := map[int][]int{}
	currentPID := 0
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(strings.TrimSpace(line[1:]))
			if err != nil {
				currentPID = 0
				continue
			}
			currentPID = pid
		case 'n':
			if currentPID == 0 {
				continue
			}
			port, ok := parseLsofPort(line[1:])
			if !ok {
				continue
			}
			portsByPID[currentPID] = append(portsByPID[currentPID], port)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read lsof output: %w", err)
	}
	for pid, ports := range portsByPID {
		portsByPID[pid] = dedupeSortedPorts(ports)
	}
	return portsByPID, nil
}

func parseLsofPort(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	lastColon := strings.LastIndex(value, ":")
	if lastColon < 0 || lastColon == len(value)-1 {
		return 0, false
	}
	portPart := value[lastColon+1:]
	for i, r := range portPart {
		if r < '0' || r > '9' {
			portPart = portPart[:i]
			break
		}
	}
	if portPart == "" {
		return 0, false
	}
	port, err := strconv.Atoi(portPart)
	if err != nil || port <= 0 {
		return 0, false
	}
	return port, true
}

func dedupeSortedPorts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	sort.Ints(values)
	out := values[:0]
	last := -1
	for _, value := range values {
		if len(out) > 0 && value == last {
			continue
		}
		out = append(out, value)
		last = value
	}
	return append([]int(nil), out...)
}
