//go:build !darwin

package worktreerecovery

func cloneFile(from, to string) (bool, error) { return false, nil }
