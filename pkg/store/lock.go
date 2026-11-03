package store

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrLocked is a data directory that another Store has open.
var ErrLocked = errors.New("store: data directory in use")

// lockDir takes an exclusive lock on dir's lock file and holds it until the
// file is closed. The kernel drops it when the process dies, so there is
// nothing to clean up after a crash.
func lockDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(
		filepath.Join(dir, "lock"),
		os.O_RDWR|os.O_CREATE,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			err = ErrLocked
		} else {
			err = fmt.Errorf("store: lock: %w", err)
		}

		return nil, errors.Join(err, f.Close())
	}

	return f, nil
}

// freeSpace is the bytes an unprivileged process can still write in dir.
func freeSpace(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("store: statfs: %w", err)
	}

	// The kernel never reports a negative block size.
	free := st.Bavail * uint64(st.Bsize) //nolint:gosec // Bsize > 0.

	// The count is capped at int64's maximum.
	return int64(min(free, math.MaxInt64)), nil //nolint:gosec // min bounds it.
}
