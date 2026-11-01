package chatlifecycle

const (
	// Local P95 evidence is a separate contract; v7/v8 are never reinterpreted.
	localP95ReportSchemaVersion          = "wukongim/chat-lifecycle-report/v9"
	localP95ReportThresholdVersion       = "wukongim/chat-lifecycle-thresholds/v3"
	localPairedP95ReportSchemaVersion    = "wukongim/chat-lifecycle-report/v10"
	localPairedP95ReportThresholdVersion = "wukongim/chat-lifecycle-thresholds/v4"
)

func (c Config) reportVersions() (string, string) {
	if c.Thresholds.Latency.pairedP95Policy() {
		return localPairedP95ReportSchemaVersion, localPairedP95ReportThresholdVersion
	}
	if c.Thresholds.Latency.HotSendACK.P95 != 0 {
		return localP95ReportSchemaVersion, localP95ReportThresholdVersion
	}
	return ReportSchemaVersion, ReportThresholdVersion
}

// validReportVersions prevents a new local quantile from acquiring old formal,
// capacity or historical evidence authority by changing only a report header.
func validReportVersions(r Report) bool {
	if r.Thresholds.Latency.pairedP95Policy() {
		return r.SchemaVersion == localPairedP95ReportSchemaVersion && r.ThresholdVersion == localPairedP95ReportThresholdVersion && r.Profile == ProfileLocal && r.Stage == StageShakeout && r.Mode == ModeSoak
	}
	if r.Thresholds.Latency.HotSendACK.P95 != 0 {
		return r.SchemaVersion == localP95ReportSchemaVersion && r.ThresholdVersion == localP95ReportThresholdVersion && r.Profile == ProfileLocal && r.Stage == StageShakeout && r.Mode == ModeSoak
	}
	return (r.SchemaVersion == ReportSchemaVersion || r.SchemaVersion == legacyReportSchemaVersion) && r.ThresholdVersion == ReportThresholdVersion
}
