package engine

import (
	"github.com/cockroachdb/pebble/v2/vfs"
	"io"
)

// LockFile takes the same exclusive OS lock used by Pebble, without opening
// or repairing a database. The caller must close it on every exit path.
func LockFile(path string) (io.Closer, error) { return vfs.Default.Lock(path) }
