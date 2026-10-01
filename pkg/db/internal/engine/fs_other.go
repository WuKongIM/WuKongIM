//go:build !darwin

package engine

import "github.com/cockroachdb/pebble/v2/vfs"

// platformFS leaves other platforms on Pebble's upstream filesystem defaults.
func platformFS() vfs.FS { return nil }
