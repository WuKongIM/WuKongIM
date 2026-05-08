package raftlog

// TestingSetSnapshotWriteFileHook replaces snapshot chunk writes for white-box tests and returns a restore function.
func TestingSetSnapshotWriteFileHook(hook func(path string, data []byte) error) func() {
	previous := snapshotWriteFile
	if hook == nil {
		snapshotWriteFile = writeSyncedFile
	} else {
		snapshotWriteFile = hook
	}
	return func() {
		snapshotWriteFile = previous
	}
}
