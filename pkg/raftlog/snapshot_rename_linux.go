//go:build linux

package raftlog

import "golang.org/x/sys/unix"

func renameNoOverwrite(oldPath, newPath string) error {
	return normalizeNoOverwriteRenameError(unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE))
}
