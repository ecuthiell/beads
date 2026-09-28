//go:build windows

package util

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/lockfile"
	"golang.org/x/sys/windows"
)

var reOpenLockFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

// TryLockForRemoval is TryLock with Windows delete sharing enabled. Use it only
// for a control file that the lease holder must remove before releasing the lock.
// Unlock alone does not remove the file.
func TryLockForRemoval(lockPath string) (*Lock, error) {
	if err := reOpenLockFile.Find(); err != nil {
		return nil, fmt.Errorf("util: resolving ReOpenFile: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return nil, fmt.Errorf("util: creating lock directory: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // caller-derived control path
	if err != nil {
		return nil, fmt.Errorf("util: opening lock file: %w", err)
	}
	// Let os.OpenFile handle creation and native path forms, then reopen the same
	// object with delete sharing before acquiring a lock on the replacement handle.
	raw, _, reopenErr := reOpenLockFile.Call(f.Fd(), windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, 0)
	closeErr := f.Close()
	handle := windows.Handle(raw)
	if handle == windows.InvalidHandle {
		return nil, fmt.Errorf("util: reopening lock file: %w", &os.PathError{Op: "reopen", Path: lockPath, Err: reopenErr})
	}
	if closeErr != nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("util: closing initial lock handle: %w", closeErr)
	}
	if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("util: disabling lock handle inheritance: %w", err)
	}
	f = os.NewFile(raw, lockPath)
	if err := lockfile.FlockExclusiveNonBlocking(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}
