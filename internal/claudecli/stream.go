package claudecli

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ManagedStreamMinimumVersion is the oldest version validated with LCR's
// command receipts, background task lifecycle and session-state ownership.
const ManagedStreamMinimumVersion = "2.1.284"

// SupportsManagedStream runs during session construction, never during a UI
// snapshot. Unknown/older executables retain foreground ownership.
func SupportsManagedStream(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "claude", "--version").Output()
	return err == nil && supportsManagedStreamVersion(string(output))
}

func supportsManagedStreamVersion(output string) bool {
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return false
	}
	parts := strings.Split(fields[0], ".")
	if len(parts) != 3 {
		return false
	}
	minimum := [3]int{2, 1, 284}
	var version [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return false
		}
		version[i] = n
	}
	for i, n := range version {
		if n != minimum[i] {
			return n > minimum[i]
		}
	}
	return true
}
