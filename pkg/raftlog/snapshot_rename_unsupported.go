//go:build !darwin && !linux

package raftlog

func renameNoOverwrite(oldPath, newPath string) error {
	return errSnapshotNoOverwriteRenameUnsupported
}
