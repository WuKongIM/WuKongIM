package chatlifecycle

import (
	"bytes"
	"time"
)

const (
	localFirstAttemptReportSchemaVersion    = "wukongim/chat-lifecycle-report/v11"
	localFirstAttemptReportThresholdVersion = "wukongim/chat-lifecycle-thresholds/v5"
)

// validFirstAttemptOverride keeps the optional original-attempt budget within
// the explicitly separate local diagnostic policy, never formal or capacity.
func validFirstAttemptOverride(profile Profile, stage Stage, mode Mode, limits LatencyThresholds) bool {
	return limits.FirstAttemptTimeout == 0 || (limits.FirstAttemptTimeout == 5*time.Second &&
		profile == ProfileLocal && stage == StageShakeout && mode == ModeSoak && limits.pairedP95Policy())
}

// pairedReportSchema shares unchanged sync/gate validation across the two local
// paired-P95 contracts. Only v11 permits the original-attempt override.
func pairedReportSchema(schema string) bool {
	return schema == localPairedP95ReportSchemaVersion || schema == localFirstAttemptReportSchemaVersion
}

// validFirstAttemptReportWire requires one explicit canonical budget in v11.
// JSON decoding must not hide a null, alias or duplicate policy declaration.
func validFirstAttemptReportWire(encoded []byte, report Report) bool {
	if report.SchemaVersion != localFirstAttemptReportSchemaVersion {
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
