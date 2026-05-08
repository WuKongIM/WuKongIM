//go:build darwin

package raftlog

import "golang.org/x/sys/unix"

func renameNoOverwrite(oldPath, newPath string) error {
	return normalizeNoOverwriteRenameError(unix.RenameatxNp(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_EXCL))
}
