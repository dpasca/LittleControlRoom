package service

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Device numbers alone do not distinguish a bind mount on the same filesystem.
func deletionMountID(dir *os.File) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", dir.Fd()))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "mnt_id:") {
			return strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "mnt_id:")), 10, 64)
		}
	}
	return 0, fmt.Errorf("cannot verify mount boundary for %s", dir.Name())
}
