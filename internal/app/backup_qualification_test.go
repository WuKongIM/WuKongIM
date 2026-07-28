package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackupBuildQualificationRequiresExactCleanRevision(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"

	require.NoError(t, ValidateBackupBuildQualification(BackupBuildQualification{
		BuildRevision:     revision,
		QualifiedRevision: revision,
	}))

	tests := []struct {
		name          string
		qualification BackupBuildQualification
	}{
		{name: "missing build revision", qualification: BackupBuildQualification{
			QualifiedRevision: revision,
		}},
		{name: "missing qualified revision", qualification: BackupBuildQualification{
			BuildRevision: revision,
		}},
		{name: "different revision", qualification: BackupBuildQualification{
			BuildRevision:     revision,
			QualifiedRevision: "1123456789abcdef0123456789abcdef01234567",
		}},
		{name: "modified source", qualification: BackupBuildQualification{
			BuildRevision:     revision,
			QualifiedRevision: revision,
			BuildModified:     true,
		}},
		{name: "invalid revision", qualification: BackupBuildQualification{
			BuildRevision:     "main",
			QualifiedRevision: "main",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateBackupBuildQualification(test.qualification)
			require.ErrorIs(t, err, ErrInvalidConfig)
			require.Contains(t, err.Error(), "qualified build")
		})
	}
}

func TestBackupStartupRejectsUnqualifiedBuild(t *testing.T) {
	cfg := validEnabledBackupConfig(t)
	app := &App{cfg: Config{
		DataDir: t.TempDir(),
		Backup:  cfg,
	}}

	err := app.applyConfigDefaults()
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Contains(t, err.Error(), "qualified build")
}
