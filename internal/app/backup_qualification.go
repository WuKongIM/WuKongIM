package app

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// backupQualifiedRevision is empty in ordinary builds. The backup qualification
// workflow sets it to the exact tested Git revision when it produces a
// deployable binary.
var backupQualifiedRevision string

// BackupBuildQualification binds production backup enablement to one clean,
// qualification-tested source revision.
type BackupBuildQualification struct {
	// BuildRevision is the VCS revision recorded by the Go toolchain.
	BuildRevision string
	// QualifiedRevision is injected only while packaging a successful
	// qualification workflow revision.
	QualifiedRevision string
	// BuildModified reports whether the Go toolchain observed local source
	// modifications while producing the binary.
	BuildModified bool
}

// CurrentBackupBuildQualification returns the immutable evidence carried by
// the running binary.
func CurrentBackupBuildQualification() BackupBuildQualification {
	qualification := BackupBuildQualification{
		QualifiedRevision: strings.ToLower(
			strings.TrimSpace(backupQualifiedRevision),
		),
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return qualification
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			qualification.BuildRevision = strings.ToLower(
				strings.TrimSpace(setting.Value),
			)
		case "vcs.modified":
			qualification.BuildModified =
				strings.EqualFold(strings.TrimSpace(setting.Value), "true")
		}
	}
	return qualification
}

// ValidateBackupBuildQualification rejects automatic backup unless the
// deployable binary was produced from the exact clean revision whose complete
// qualification workflow passed.
func ValidateBackupBuildQualification(
	qualification BackupBuildQualification,
) error {
	buildRevision := strings.ToLower(
		strings.TrimSpace(qualification.BuildRevision),
	)
	qualifiedRevision := strings.ToLower(
		strings.TrimSpace(qualification.QualifiedRevision),
	)
	if !validGitRevision(buildRevision) ||
		!validGitRevision(qualifiedRevision) ||
		buildRevision != qualifiedRevision ||
		qualification.BuildModified {
		return fmt.Errorf(
			"%w: automatic backup requires an exact clean qualified build revision",
			ErrInvalidConfig,
		)
	}
	return nil
}

func validGitRevision(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, ch := range value {
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') {
			continue
		}
		return false
	}
	return true
}
