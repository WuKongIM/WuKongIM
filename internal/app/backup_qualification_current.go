//go:build !e2e

package app

func validateCurrentBackupBuildQualification() error {
	return ValidateBackupBuildQualification(
		CurrentBackupBuildQualification(),
	)
}
