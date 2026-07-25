package metrics

import (
	"math"
	"testing"
	"time"

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
	metrics.SetBackupCaptureOwnedSlots(19)
	metrics.ObserveBackupCaptureLeaseTakeover()
	metrics.ObserveBackupCaptureLeaseFenced()
	metrics.SetBackupSourcePin(17, 90*time.Second, 32<<20, 64<<20)
	metrics.ObserveBackupSlotRebase(17, "pin_age", 12*time.Second, "")
	metrics.ObserveBackupSlotRebase(17, "unbounded", -time.Second, "secret backend error")
	metrics.SetBackupAuditDebt(23)
	metrics.SetBackupAuditLastSuccess(1_753_400_123_000)
	metrics.ObserveBackupAuditCorruption("ciphertext", "secondary")
	metrics.AddBackupAuditRepairBytes("secondary", 4096)
	metrics.ObserveBackupAuditUnrecoverable()

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
	require.Equal(t, float64(19), requireMetricFamily(t, families, "wukongim_backup_capture_owned_slots").GetMetric()[0].GetGauge().GetValue())
	require.Equal(t, float64(1), requireMetricFamily(t, families, "wukongim_backup_capture_lease_takeovers_total").GetMetric()[0].GetCounter().GetValue())
	require.Equal(t, float64(1), requireMetricFamily(t, families, "wukongim_backup_capture_lease_fenced_total").GetMetric()[0].GetCounter().GetValue())
	require.Equal(t, float64(90), findMetricByLabels(t,
		requireMetricFamily(t, families, "wukongim_backup_source_pin_age_seconds"),
		map[string]string{"node_id": "7", "node_name": "node-7", "hash_slot": "17"},
	).GetGauge().GetValue())
	require.Equal(t, float64(32<<20), findMetricByLabels(t,
		requireMetricFamily(t, families, "wukongim_backup_source_pinned_bytes"),
		map[string]string{"node_id": "7", "node_name": "node-7", "hash_slot": "17"},
	).GetGauge().GetValue())
	require.Equal(t, float64(64<<20), requireMetricFamily(t, families, "wukongim_backup_source_node_pinned_bytes").GetMetric()[0].GetGauge().GetValue())
	rebases := requireMetricFamily(t, families, "wukongim_backup_slot_rebases_total")
	require.Equal(t, float64(1), findMetricByLabels(t, rebases, map[string]string{
		"node_id": "7", "node_name": "node-7", "hash_slot": "17",
		"reason": "pin_age", "outcome": "success", "failure_category": "none",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, rebases, map[string]string{
		"node_id": "7", "node_name": "node-7", "hash_slot": "17",
		"reason": "unknown", "outcome": "failure", "failure_category": "unknown",
	}).GetCounter().GetValue())
	durations := requireMetricFamily(t, families, "wukongim_backup_slot_rebase_duration_seconds")
	require.Equal(t, uint64(1), findMetricByLabels(t, durations, map[string]string{
		"node_id": "7", "node_name": "node-7", "hash_slot": "17",
		"reason": "pin_age", "outcome": "success", "failure_category": "none",
	}).GetHistogram().GetSampleCount())
	require.Equal(t, float64(23), requireMetricFamily(
		t, families, "wukongim_backup_audit_debt_objects",
	).GetMetric()[0].GetGauge().GetValue())
	require.Equal(t, float64(1_753_400_123), requireMetricFamily(
		t, families, "wukongim_backup_audit_last_success_timestamp_seconds",
	).GetMetric()[0].GetGauge().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t,
		requireMetricFamily(t, families, "wukongim_backup_audit_corruptions_total"),
		map[string]string{
			"node_id": "7", "node_name": "node-7",
			"category": "ciphertext", "repository": "secondary",
		},
	).GetCounter().GetValue())
	require.Equal(t, float64(4096), findMetricByLabels(t,
		requireMetricFamily(t, families, "wukongim_backup_audit_repair_bytes_total"),
		map[string]string{
			"node_id": "7", "node_name": "node-7", "repository": "secondary",
		},
	).GetCounter().GetValue())
	require.Equal(t, float64(1), requireMetricFamily(
		t, families, "wukongim_backup_audit_unrecoverable_failures_total",
	).GetMetric()[0].GetCounter().GetValue())
}
