//go:build darwin || linux

package mqttowner

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockDir takes a non-blocking exclusive flock. flock binds to the open file
// description, so a second open conflicts even inside the same process.
func lockDir(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
