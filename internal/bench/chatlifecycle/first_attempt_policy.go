package chatlifecycle

import (
	"bytes"
	"time"
)

const (
	firstAttemptBudget                       = 5 * time.Second
	localFirstAttemptReportSchemaVersion     = "wukongim/chat-lifecycle-report/v11"
	localFirstAttemptReportThresholdVersion  = "wukongim/chat-lifecycle-thresholds/v5"
	formalFirstAttemptReportSchemaVersion    = "wukongim/chat-lifecycle-report/v12"
	formalFirstAttemptReportThresholdVersion = "wukongim/chat-lifecycle-thresholds/v6"
)

// IsCurrentP99ReportSchema identifies current P99 evidence contracts.
// Callers must also validate the report body and its stage before admission.
func IsCurrentP99ReportSchema(schema string) bool {
	return schema == ReportSchemaVersion || schema == formalFirstAttemptReportSchemaVersion
}

func isCurrentReportSchema(schema string) bool {
	return IsCurrentP99ReportSchema(schema) || schema == localP95ReportSchemaVersion || pairedReportSchema(schema)
}

func firstAttemptReportVersions(profile Profile) (string, string) {
	if profile == ProfileFormal {
		return formalFirstAttemptReportSchemaVersion, formalFirstAttemptReportThresholdVersion
	}
	return localFirstAttemptReportSchemaVersion, localFirstAttemptReportThresholdVersion
}

// validFirstAttemptOverride permits only the reviewed original-attempt budget.
func validFirstAttemptOverride(profile Profile, stage Stage, mode Mode, limits LatencyThresholds) bool {
	switch {
	case limits.FirstAttemptTimeout == 0:
		return true
	case limits.FirstAttemptTimeout != firstAttemptBudget:
		return false
	case profile == ProfileFormal:
		return (stage == StageFormal && (mode == ModeSoak || mode == ModeCapacity)) ||
			(stage == StageRehearsal && mode == ModeSoak)
	default:
		return profile == ProfileLocal && stage == StageShakeout && mode == ModeSoak && limits.pairedP95Policy()
	}
}

// pairedReportSchema shares unchanged sync/gate validation across the two local
// paired-P95 contracts. Only v11 permits the original-attempt override.
func pairedReportSchema(schema string) bool {
	return schema == localPairedP95ReportSchemaVersion || schema == localFirstAttemptReportSchemaVersion
}

// validFirstAttemptReportWire requires one explicit canonical original-attempt
// budget in the selected contract. JSON decoding must not hide a null, alias,
// or duplicate policy declaration.
func validFirstAttemptReportWire(encoded []byte, report Report) bool {
	if report.SchemaVersion != localFirstAttemptReportSchemaVersion && report.SchemaVersion != formalFirstAttemptReportSchemaVersion {
		return true
	}
	thresholds, err := pairedReportChild(encoded, "thresholds")
	if err != nil {
		return false
	}
	latency, err := pairedReportChild(thresholds, "latency")
	if err != nil {
		return false
	}
	budget, err := pairedReportChild(latency, "first_attempt_timeout")
	return err == nil && bytes.Equal(bytes.TrimSpace(budget), []byte("5000000000"))
}
