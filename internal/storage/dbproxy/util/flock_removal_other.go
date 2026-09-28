//go:build !windows

package util

// TryLockForRemoval preserves ordinary TryLock behavior on non-Windows hosts.
func TryLockForRemoval(lockPath string) (*Lock, error) {
	return TryLock(lockPath)
}
