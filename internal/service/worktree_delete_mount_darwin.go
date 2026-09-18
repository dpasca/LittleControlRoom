package service

import "os"

// Darwin mount boundaries have distinct device identities.
func deletionMountID(dir *os.File) (uint64, error) { return 0, nil }
