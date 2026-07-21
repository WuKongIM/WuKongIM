package metrics

import (
	"math"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestBackupMetricsPreserveUnknownEvidenceAndBoundLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newBackupMetrics(registry, prometheus.Labels{"node_id": "7", "node_name": "node-7"})

	families, err := registry.Gather()
	require.NoError(t, err)
	age := requireMetricFamily(t, families, "wukongim_backup_recovery_point_age_seconds")
	require.True(t, math.IsNaN(age.GetMetric()[0].GetGauge().GetValue()))
	verificationAge := requireMetricFamily(t, families, "wukongim_backup_verification_age_seconds")
	require.True(t, math.IsNaN(verificationAge.GetMetric()[0].GetGauge().GetValue()))
	doctor := requireMetricFamily(t, families, "wukongim_backup_doctor_health")
	require.Equal(t, float64(1), findMetricByLabels(t, doctor, map[string]string{
		"node_id": "7", "node_name": "node-7", "state": "unknown",
	}).GetGauge().GetValue())

	ageSeconds := int64(17)
	metrics.SetBackupControllerLeader(true)
	metrics.SetBackupDoctorHealth("healthy")
	metrics.SetBackupActive(true)
	metrics.SetBackupRecoveryPointAgeSeconds(&ageSeconds)
	metrics.SetBackupVerificationAgeSeconds(&ageSeconds)
	metrics.ObserveBackupFailure("unbounded source error")
	metrics.SetBackupRestoreProgress(13, 8, 256)

	families, err = registry.Gather()
	require.NoError(t, err)
	require.Equal(t, float64(17), requireMetricFamily(t, families, "wukongim_backup_recovery_point_age_seconds").GetMetric()[0].GetGauge().GetValue())
	require.Equal(t, float64(17), requireMetricFamily(t, families, "wukongim_backup_verification_age_seconds").GetMetric()[0].GetGauge().GetValue())
	require.Equal(t, float64(1), requireMetricFamily(t, families, "wukongim_backup_controller_leader").GetMetric()[0].GetGauge().GetValue())
	require.Equal(t, float64(1), requireMetricFamily(t, families, "wukongim_backup_job_active").GetMetric()[0].GetGauge().GetValue())
	doctor = requireMetricFamily(t, families, "wukongim_backup_doctor_health")
	require.Equal(t, float64(1), findMetricByLabels(t, doctor, map[string]string{
		"node_id": "7", "node_name": "node-7", "state": "healthy",
	}).GetGauge().GetValue())
	require.Equal(t, float64(0), findMetricByLabels(t, doctor, map[string]string{
		"node_id": "7", "node_name": "node-7", "state": "unknown",
	}).GetGauge().GetValue())

	failures := requireMetricFamily(t, families, "wukongim_backup_failures_total")
	require.Equal(t, float64(1), findMetricByLabels(t, failures, map[string]string{
		"node_id": "7", "node_name": "node-7", "category": "unknown",
	}).GetCounter().GetValue())
	progress := requireMetricFamily(t, families, "wukongim_backup_restore_partitions")
	for phase, want := range map[string]float64{"installed": 13, "verified": 8, "total": 256} {
		require.Equal(t, want, findMetricByLabels(t, progress, map[string]string{
			"node_id": "7", "node_name": "node-7", "phase": phase,
		}).GetGauge().GetValue(), phase)
	}
}
