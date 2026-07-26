package metrics

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var backupFailureCategories = map[string]struct{}{
	"doctor": {}, "checkpoint": {}, "frontier_conflict": {}, "leadership": {}, "audit": {}, "gc": {},
	"capture_runtime": {},
	"restore_state":   {}, "restore_canceled": {}, "restore_partition_install": {},
}

var backupRebaseReasons = map[string]struct{}{
	"pin_age": {}, "node_byte_budget": {}, "source_compacted": {}, "source_remapped": {},
	"generation_bytes": {}, "generation_segments": {}, "generation_age": {},
	"audit_corruption": {},
}

var backupRebaseFailureCategories = map[string]struct{}{
	"none": {}, "rebase_begin": {}, "rebase_rotate": {}, "pin_release": {}, "rebase_capture": {},
	"rebase_validate": {}, "rebase_audit": {}, "rebase_promote": {}, "rebase_fenced": {},
	"compaction_plan": {}, "compaction_budget": {}, "unknown": {},
}

// BackupMetrics exposes low-cardinality backup and restore SLO evidence.
type BackupMetrics struct {
	checkpointAge      prometheus.Gauge
	controllerLeader   prometheus.Gauge
	doctorHealth       *prometheus.GaugeVec
	failures           *prometheus.CounterVec
	restoreProgress    *prometheus.GaugeVec
	captureOwnedSlots  prometheus.Gauge
	captureTakeovers   prometheus.Counter
	captureFenced      prometheus.Counter
	sourcePinAge       *prometheus.GaugeVec
	sourcePinnedBytes  *prometheus.GaugeVec
	sourceNodeBytes    prometheus.Gauge
	slotRebases        *prometheus.CounterVec
	slotRebaseSeconds  *prometheus.HistogramVec
	auditDebt          prometheus.Gauge
	auditLastSuccess   prometheus.Gauge
	auditCorruptions   *prometheus.CounterVec
	auditRepairBytes   *prometheus.CounterVec
	auditUnrecoverable prometheus.Counter
}

func newBackupMetrics(registry prometheus.Registerer, labels prometheus.Labels) *BackupMetrics {
	m := &BackupMetrics{
		checkpointAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_backup_checkpoint_age_seconds", Help: "Age of the latest immutable continuous-capture checkpoint; NaN means unknown.", ConstLabels: labels,
		}),
		controllerLeader: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_backup_controller_leader", Help: "Whether this node is the current backup Controller coordinator.", ConstLabels: labels,
		}),
		doctorHealth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "wukongim_backup_doctor_health", Help: "Current backup doctor health as a one-hot bounded state.", ConstLabels: labels,
		}, []string{"state"}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_backup_failures_total", Help: "Backup and restore failures grouped by bounded category.", ConstLabels: labels,
		}, []string{"category"}),
		restoreProgress: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "wukongim_backup_restore_partitions", Help: "Restore logical partition progress by bounded phase.", ConstLabels: labels,
		}, []string{"phase"}),
		captureOwnedSlots: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_backup_capture_owned_slots", Help: "Number of Hash Slots whose exact capture lease is currently held by this node.", ConstLabels: labels,
		}),
		captureTakeovers: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wukongim_backup_capture_lease_takeovers_total", Help: "Durable capture lease takeovers completed by this node.", ConstLabels: labels,
		}),
		captureFenced: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wukongim_backup_capture_lease_fenced_total", Help: "Capture attempts rejected by current Slot authority or durable lease fencing.", ConstLabels: labels,
		}),
		sourcePinAge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "wukongim_backup_source_pin_age_seconds", Help: "Age of each locally held backup source-log compaction pin.", ConstLabels: labels,
		}, []string{"hash_slot"}),
		sourcePinnedBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "wukongim_backup_source_pinned_bytes", Help: "Estimated source-log bytes retained by each locally held backup pin.", ConstLabels: labels,
		}, []string{"hash_slot"}),
		sourceNodeBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_backup_source_node_pinned_bytes", Help: "Aggregate estimated source-log bytes retained by backup pins on this node.", ConstLabels: labels,
		}),
		slotRebases: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_backup_slot_rebases_total", Help: "Slot generation rebase attempts grouped by bounded reason, outcome, and failure category.", ConstLabels: labels,
		}, []string{"hash_slot", "reason", "outcome", "failure_category"}),
		slotRebaseSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_backup_slot_rebase_duration_seconds", Help: "Elapsed Slot generation rebase lifecycle at each terminal attempt observation, grouped by bounded reason, outcome, and failure category.", ConstLabels: labels,
			Buckets: []float64{1, 5, 15, 30, 60, 300, 900, 3600},
		}, []string{"hash_slot", "reason", "outcome", "failure_category"}),
		auditDebt: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_backup_audit_debt_objects", Help: "Number of committed backup artifacts awaiting full remote validation.", ConstLabels: labels,
		}),
		auditLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_backup_audit_last_success_timestamp_seconds", Help: "Unix timestamp of the latest successful full artifact validation; zero means none.", ConstLabels: labels,
		}),
		auditCorruptions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_backup_audit_corruptions_total", Help: "Detected backup artifact corruptions by bounded category.", ConstLabels: labels,
		}, []string{"category"}),
		auditRepairBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_backup_audit_repair_bytes_total", Help: "Stored bytes copied by backup integrity repair.", ConstLabels: labels,
		}, nil),
		auditUnrecoverable: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wukongim_backup_audit_unrecoverable_failures_total", Help: "Dual-repository audit failures that cannot rebase from live source.", ConstLabels: labels,
		}),
	}
	registry.MustRegister(
		m.checkpointAge, m.controllerLeader,
		m.doctorHealth, m.failures, m.restoreProgress,
		m.captureOwnedSlots, m.captureTakeovers, m.captureFenced,
		m.sourcePinAge, m.sourcePinnedBytes, m.sourceNodeBytes,
		m.slotRebases, m.slotRebaseSeconds,
		m.auditDebt, m.auditLastSuccess, m.auditCorruptions,
		m.auditRepairBytes, m.auditUnrecoverable,
	)
	m.checkpointAge.Set(math.NaN())
	m.SetBackupDoctorHealth("unknown")
	m.SetBackupRestoreProgress(0, 0, 0)
	return m
}

// SetBackupAuditDebt records the bounded number of artifacts awaiting full validation.
func (m *BackupMetrics) SetBackupAuditDebt(objects uint64) {
	if m != nil {
		m.auditDebt.Set(float64(objects))
	}
}

// SetBackupAuditLastSuccess records a UTC millisecond timestamp; zero means none.
func (m *BackupMetrics) SetBackupAuditLastSuccess(unixMillis int64) {
	if m == nil {
		return
	}
	if unixMillis < 0 {
		unixMillis = 0
	}
	m.auditLastSuccess.Set(float64(unixMillis) / 1000)
}

// ObserveBackupAuditCorruption records one bounded corruption class without
// exposing repository topology.
func (m *BackupMetrics) ObserveBackupAuditCorruption(category, _ string) {
	if m == nil {
		return
	}
	switch category {
	case "missing", "checksum", "ciphertext", "commit_proof":
	default:
		category = "unknown"
	}
	m.auditCorruptions.WithLabelValues(category).Inc()
}

// AddBackupAuditRepairBytes records exact positive stored repair bytes.
func (m *BackupMetrics) AddBackupAuditRepairBytes(_ string, bytes int64) {
	if m == nil || bytes <= 0 {
		return
	}
	m.auditRepairBytes.WithLabelValues().Add(float64(bytes))
}

// ObserveBackupAuditUnrecoverable records dual-copy loss without live source.
func (m *BackupMetrics) ObserveBackupAuditUnrecoverable() {
	if m != nil {
		m.auditUnrecoverable.Inc()
	}
}

// SetBackupSourcePin records one bounded per-Slot hold plus the node aggregate.
func (m *BackupMetrics) SetBackupSourcePin(hashSlot uint16, age time.Duration, slotBytes, nodeBytes uint64) {
	if m == nil {
		return
	}
	if age < 0 {
		age = 0
	}
	slot := strconv.FormatUint(uint64(hashSlot), 10)
	m.sourcePinAge.WithLabelValues(slot).Set(age.Seconds())
	m.sourcePinnedBytes.WithLabelValues(slot).Set(float64(slotBytes))
	m.sourceNodeBytes.Set(float64(nodeBytes))
}

// ObserveBackupSlotRebase records one terminal rebase attempt with bounded labels.
func (m *BackupMetrics) ObserveBackupSlotRebase(hashSlot uint16, reason string, duration time.Duration, failureCategory string) {
	if m == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if _, ok := backupRebaseReasons[reason]; !ok {
		reason = "unknown"
	}
	failureCategory = strings.TrimSpace(failureCategory)
	outcome := "failure"
	if failureCategory == "" {
		failureCategory = "none"
		outcome = "success"
	} else if _, ok := backupRebaseFailureCategories[failureCategory]; !ok {
		failureCategory = "unknown"
	}
	if duration < 0 {
		duration = 0
	}
	labels := []string{
		strconv.FormatUint(uint64(hashSlot), 10), reason, outcome, failureCategory,
	}
	m.slotRebases.WithLabelValues(labels...).Inc()
	m.slotRebaseSeconds.WithLabelValues(labels...).Observe(duration.Seconds())
}

// ObserveBackupCaptureLeaseTakeover records one durable Slot lease takeover.
func (m *BackupMetrics) ObserveBackupCaptureLeaseTakeover() {
	if m != nil {
		m.captureTakeovers.Inc()
	}
}

// ObserveBackupCaptureLeaseFenced records one stale or non-leader capture attempt.
func (m *BackupMetrics) ObserveBackupCaptureLeaseFenced() {
	if m != nil {
		m.captureFenced.Inc()
	}
}

// SetBackupCaptureOwnedSlots records the current bounded local lease count.
func (m *BackupMetrics) SetBackupCaptureOwnedSlots(slots int) {
	if m == nil {
		return
	}
	if slots < 0 {
		slots = 0
	}
	m.captureOwnedSlots.Set(float64(slots))
}

// SetBackupCheckpointAgeSeconds preserves absent checkpoint evidence as NaN.
func (m *BackupMetrics) SetBackupCheckpointAgeSeconds(age *int64) {
	if m == nil {
		return
	}
	if age == nil {
		m.checkpointAge.Set(math.NaN())
		return
	}
	value := *age
	if value < 0 {
		value = 0
	}
	m.checkpointAge.Set(float64(value))
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
