package localapi

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

var ErrLocked = errors.New("lock held")

// TryLock acquires a process lock until its returned file is closed. Keep the
// entry on disk so all contenders open the same inode.
func TryLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("inspect lock %s: %w", path, err)
	}
	if stat.Mode&(unix.S_IFMT|0777) != unix.S_IFREG|0600 || stat.Uid != uint32(os.Getuid()) {
		unix.Close(fd)
		return nil, fmt.Errorf("expected a user-private regular lock file: %s", path)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("%s: %w", path, ErrLocked)
		}
		return nil, fmt.Errorf("acquire lock %s: %w", path, err)
	}
	return os.NewFile(uintptr(fd), path), nil
}
