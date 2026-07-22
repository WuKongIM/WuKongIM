package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackupConfigFingerprintIgnoresNodeLocalStagingDirectory(t *testing.T) {
	first := BackupConfig{RepositoryID: "repository", StagingDir: "/var/lib/wukongim/node-1/backup-staging"}
	second := first
	second.StagingDir = "/var/lib/wukongim/node-2/backup-staging"

	firstFingerprint, err := backupConfigFingerprint(first, "cluster", 256)
	require.NoError(t, err)
	secondFingerprint, err := backupConfigFingerprint(second, "cluster", 256)
	require.NoError(t, err)

	require.Equal(t, firstFingerprint, secondFingerprint)
}
