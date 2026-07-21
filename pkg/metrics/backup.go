package metrics

import (
	"math"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

var backupFailureCategories = map[string]struct{}{
	"doctor": {}, "coordination_state": {}, "schedule": {}, "trigger": {}, "config_drift": {},
	"capture_canceled": {}, "stale_partition_owner": {}, "partition_capture": {}, "publish": {},
	"restore_state": {}, "restore_canceled": {}, "restore_partition_install": {},
	"retention": {}, "audit": {},
}

// BackupMetrics exposes low-cardinality backup and restore SLO evidence.
type BackupMetrics struct {
	recoveryPointAge prometheus.Gauge
	verificationAge  prometheus.Gauge
	controllerLeader prometheus.Gauge
	doctorHealth     *prometheus.GaugeVec
	active           prometheus.Gauge
	failures         *prometheus.CounterVec
	restoreProgress  *prometheus.GaugeVec
}

func newBackupMetrics(registry prometheus.Registerer, labels prometheus.Labels) *BackupMetrics {
	m := &BackupMetrics{
		recoveryPointAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_backup_recovery_point_age_seconds", Help: "Age of the latest verified cluster restore point; NaN means unknown.", ConstLabels: labels,
		}),
		verificationAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_backup_verification_age_seconds", Help: "Age of the latest successful full remote restore-point audit; NaN means unknown.", ConstLabels: labels,
		}),
		controllerLeader: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_backup_controller_leader", Help: "Whether this node is the current backup Controller coordinator.", ConstLabels: labels,
		}),
		doctorHealth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "wukongim_backup_doctor_health", Help: "Current backup doctor health as a one-hot bounded state.", ConstLabels: labels,
		}, []string{"state"}),
		active: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_backup_job_active", Help: "Whether one cluster backup job is active.", ConstLabels: labels,
		}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_backup_failures_total", Help: "Backup and restore failures grouped by bounded category.", ConstLabels: labels,
		}, []string{"category"}),
		restoreProgress: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "wukongim_backup_restore_partitions", Help: "Restore logical partition progress by bounded phase.", ConstLabels: labels,
		}, []string{"phase"}),
	}
	registry.MustRegister(m.recoveryPointAge, m.verificationAge, m.controllerLeader, m.doctorHealth, m.active, m.failures, m.restoreProgress)
	m.recoveryPointAge.Set(math.NaN())
	m.verificationAge.Set(math.NaN())
	m.SetBackupDoctorHealth("unknown")
	m.SetBackupRestoreProgress(0, 0, 0)
	return m
}

// SetBackupVerificationAgeSeconds preserves absent audit evidence as NaN.
func (m *BackupMetrics) SetBackupVerificationAgeSeconds(age *int64) {
	if m == nil {
		return
	}
	if age == nil {
		m.verificationAge.Set(math.NaN())
		return
	}
	value := *age
	if value < 0 {
		value = 0
	}
	m.verificationAge.Set(float64(value))
}

// SetBackupControllerLeader records coordinator ownership on this node.
func (m *BackupMetrics) SetBackupControllerLeader(leader bool) {
	if m == nil {
		return
	}
	if leader {
		m.controllerLeader.Set(1)
	} else {
		m.controllerLeader.Set(0)
	}
}

// SetBackupDoctorHealth records a one-hot bounded doctor state.
func (m *BackupMetrics) SetBackupDoctorHealth(state string) {
	if m == nil {
		return
	}
	state = strings.TrimSpace(state)
	if state != "unknown" && state != "healthy" && state != "failed" {
		state = "unknown"
	}
	m.doctorHealth.Reset()
	for _, candidate := range []string{"unknown", "healthy", "failed"} {
		value := 0.0
		if candidate == state {
			value = 1
		}
		m.doctorHealth.WithLabelValues(candidate).Set(value)
	}
}

// SetBackupActive records whether one backup job is active.
func (m *BackupMetrics) SetBackupActive(active bool) {
	if m == nil {
		return
	}
	if active {
		m.active.Set(1)
	} else {
		m.active.Set(0)
	}
}

// SetBackupRecoveryPointAgeSeconds preserves absent evidence as NaN.
func (m *BackupMetrics) SetBackupRecoveryPointAgeSeconds(age *int64) {
	if m == nil {
		return
	}
	if age == nil {
		m.recoveryPointAge.Set(math.NaN())
		return
	}
	value := *age
	if value < 0 {
		value = 0
	}
	m.recoveryPointAge.Set(float64(value))
}

// ObserveBackupFailure increments one bounded failure category.
func (m *BackupMetrics) ObserveBackupFailure(category string) {
	if m == nil {
		return
	}
	category = strings.TrimSpace(category)
	if _, ok := backupFailureCategories[category]; !ok {
		category = "unknown"
	}
	m.failures.WithLabelValues(category).Inc()
}

// SetBackupRestoreProgress records total, installed, and verified partitions.
func (m *BackupMetrics) SetBackupRestoreProgress(installed, verified, total int) {
	if m == nil {
		return
	}
	m.restoreProgress.WithLabelValues("total").Set(float64(total))
	m.restoreProgress.WithLabelValues("installed").Set(float64(installed))
	m.restoreProgress.WithLabelValues("verified").Set(float64(verified))
}
