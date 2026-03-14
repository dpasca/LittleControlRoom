package runtimeguard

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var ErrBusy = errors.New("little control room runtime already active")

type Owner struct {
	PID       int       `json:"pid"`
	Mode      string    `json:"mode"`
	DBPath    string    `json:"db_path"`
	Hostname  string    `json:"hostname"`
	CWD       string    `json:"cwd"`
	Command   string    `json:"command,omitempty"`
	Args      []string  `json:"args,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

type Lease struct {
	path  string
	file  *os.File
	owner Owner
}

func Acquire(dbPath, mode string) (*Lease, *Owner, error) {
	lockPath := lockPathForDB(dbPath)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create runtime lock directory: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open runtime lock: %w", err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		owner := readOwner(file)
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, owner, ErrBusy
		}
		return nil, owner, fmt.Errorf("lock runtime lease: %w", err)
	}

	owner := currentOwner(dbPath, mode)
	if err := writeOwner(file, owner); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, nil, err
	}

	return &Lease{
		path:  lockPath,
		file:  file,
		owner: owner,
	}, nil, nil
}

func (l *Lease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	errUnlock := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	errClose := l.file.Close()
	l.file = nil
	switch {
	case errUnlock != nil:
		return errUnlock
	case errClose != nil:
		return errClose
	default:
		return nil
	}
}

func (l *Lease) Owner() Owner {
	if l == nil {
		return Owner{}
	}
	return l.owner
}

func lockPathForDB(dbPath string) string {
	clean := filepath.Clean(strings.TrimSpace(dbPath))
	if clean == "" {
		clean = "little-control-room.sqlite"
	}
	return clean + ".runtime.lock"
}

func currentOwner(dbPath, mode string) Owner {
	host, _ := os.Hostname()
	cwd, _ := os.Getwd()
	return Owner{
		PID:       os.Getpid(),
		Mode:      strings.TrimSpace(mode),
		DBPath:    filepath.Clean(strings.TrimSpace(dbPath)),
		Hostname:  strings.TrimSpace(host),
		CWD:       strings.TrimSpace(cwd),
		Command:   strings.Join(os.Args, " "),
		Args:      append([]string(nil), os.Args...),
		StartedAt: time.Now(),
	}
}

func writeOwner(file *os.File, owner Owner) error {
	if file == nil {
		return errors.New("runtime lock file unavailable")
	}
	payload, err := json.MarshalIndent(owner, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime owner: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate runtime lock: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek runtime lock: %w", err)
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write runtime lock: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync runtime lock: %w", err)
	}
	return nil
}

func readOwner(file *os.File) *Owner {
	if file == nil {
		return nil
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil
	}
	raw, err := os.ReadFile(file.Name())
	if err != nil {
		return nil
	}
	var owner Owner
	if err := json.Unmarshal(raw, &owner); err != nil {
		return nil
	}
	return &owner
}

func FindPeerProcess(dbPath string) (*Owner, error) {
	dbPath = filepath.Clean(strings.TrimSpace(dbPath))
	if dbPath == "" {
		return nil, nil
	}

	out, err := exec.Command("ps", "-Ao", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("scan runtime processes: %w", err)
	}

	selfPID := os.Getpid()
	parentPID := os.Getppid()
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		pid, _, command, ok := parseProcessLine(scanner.Text())
		if !ok {
			continue
		}
		if pid == selfPID || pid == parentPID {
			continue
		}
		mode, processDBPath, ok := parseGuardedRuntimeCommand(command)
		if !ok {
			continue
		}
		if processDBPath != dbPath {
			continue
		}
		return &Owner{
			PID:       pid,
			Mode:      mode,
			DBPath:    processDBPath,
			Command:   command,
			Args:      strings.Fields(command),
			CWD:       "",
			Hostname:  "",
			StartedAt: time.Time{},
		}, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read runtime processes: %w", err)
	}
	return nil, nil
}

func parseProcessLine(line string) (int, int, string, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 3 {
		return 0, 0, "", false
	}
	var pid, ppid int
	if _, err := fmt.Sscanf(fields[0], "%d", &pid); err != nil {
		return 0, 0, "", false
	}
	if _, err := fmt.Sscanf(fields[1], "%d", &ppid); err != nil {
		return 0, 0, "", false
	}
	command := strings.TrimSpace(strings.Join(fields[2:], " "))
	if command == "" {
		return 0, 0, "", false
	}
	return pid, ppid, command, true
}

func parseGuardedRuntimeCommand(command string) (string, string, bool) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return "", "", false
	}

	mode := ""
	dbPath := ""
	for i, field := range fields {
		switch {
		case isGuardedMode(field) && lcroomCommandNearby(fields, i):
			mode = field
		case field == "--db" && i+1 < len(fields):
			dbPath = filepath.Clean(fields[i+1])
		case strings.HasPrefix(field, "--db="):
			dbPath = filepath.Clean(strings.TrimPrefix(field, "--db="))
		}
	}
	if mode == "" || dbPath == "" {
		return "", "", false
	}
	return mode, dbPath, true
}

func lcroomCommandNearby(fields []string, modeIndex int) bool {
	for i := 0; i < modeIndex; i++ {
		field := fields[i]
		switch {
		case field == brandBinaryName():
			return true
		case strings.HasSuffix(field, "/"+brandBinaryName()):
			return true
		case field == "./cmd/"+brandBinaryName():
			return true
		case strings.HasSuffix(field, "/cmd/"+brandBinaryName()):
			return true
		}
	}
	return false
}

func brandBinaryName() string {
	return "lcroom"
}

func isGuardedMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "classify", "tui", "serve":
		return true
	default:
		return false
	}
}
