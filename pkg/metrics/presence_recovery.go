package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// PresenceRecoverySnapshot is the fixed aggregate recovery census supplied by
// composition. Decoded bytes are structural estimates, never heap/RSS limits.
type PresenceRecoverySnapshot struct {
	ResultCensusEnabled                                                                                                   bool
	QueuedCalls, ActiveBatches, ResultCalls, Members, FencedUIDs, OwnedUIDBytes                                           int
	ResultLimited, ExecutionLimited                                                                                       bool
	BatchesTotal, BatchCallsTotal, BatchUIDsTotal                                                                         uint64
	BatchCallsHighWater, BatchUIDsHighWater                                                                               int
	ResultRows, ResultDecodedBytes, PageRows, PageDecodedBytes                                                            uint64
	ResultRowsHighWater, ResultDecodedBytesHighWater, PageRowsHighWater, PageDecodedBytesHighWater, DecodedBytesHighWater uint64
	ResultBudgetEnabled                                                                                                   bool
	OwnedResultRows, OwnedResultBytes, OwnedResultRowsHighWater, OwnedResultBytesHighWater, ResultBudgetRejected          uint64
}

// SetRecoverySnapshot binds a runtime generation's read-only census. Replacing
// it after a drained restart detaches the previous runtime; nil publishes zeros.
func (m *PresenceMetrics) SetRecoverySnapshot(snapshot func() PresenceRecoverySnapshot) {
	if m == nil || m.recovery == nil {
		return
	}
	m.recovery.mu.Lock()
	m.recovery.snapshot = snapshot
	m.recovery.mu.Unlock()
}

// One scrape captures scalar state once, then emits immutable metrics without
// holding the callback lock or the runtime's ownership lock during serialization.
type presenceRecoveryCollector struct {
	mu          sync.RWMutex
	snapshot    func() PresenceRecoverySnapshot
	descriptors [29]*prometheus.Desc
}

func newPresenceRecoveryCollector(labels prometheus.Labels) *presenceRecoveryCollector {
	c := &presenceRecoveryCollector{}
	definitions := [...][2]string{
		{"queued_calls", "Original recovery calls currently waiting in the FIFO."},
		{"active_batches", "Actual provider batches still owning execution or results."},
		{"result_calls", "Original-call result responsibilities reserved until actual provider completion and installation."},
		{"members", "Waiting and active recovery caller ownership entries."},
		{"fenced_uids", "Distinct fenced UID keys retained by unfinished recovery work."},
		{"owned_uid_bytes", "Logical UID clone and scheduler metadata admission bytes; excludes responses and RSS."},
		{"result_limited", "One when a nonempty FIFO cannot dispatch because legacy original-call result credits are full."},
		{"execution_limited", "One when a nonempty FIFO cannot dispatch because physical execution credits are full."},
		{"batches_total", "Provider execution batches dispatched in this recovery runtime generation."},
		{"batch_calls_total", "Original work calls dispatched before the provider's fresh proof recheck."},
		{"batch_uids_total", "Owned UID items dispatched before the provider's fresh proof recheck."},
		{"batch_calls_high_water", "Largest original-call width dispatched in this recovery runtime generation."},
		{"batch_uids_high_water", "Largest owned UID item count dispatched in one batch."},
		{"result_rows", "Route rows currently retained in recovery result accumulators or completed provider replies."},
		{"result_decoded_bytes", "Structural accumulator/reply bytes: visible slice capacity and live route strings, aliases charged per reference; excludes maps, errors, hidden backing prefixes, wire buffers, allocator and RSS."},
		{"page_rows", "Route rows currently retained in transient decoded owner pages, separate from accumulators."},
		{"page_decoded_bytes", "Structural decoded owner-page bytes under the same exclusions as result_decoded_bytes."},
		{"result_rows_high_water", "Maximum concurrent accumulator/reply rows in this runtime generation."},
		{"result_decoded_bytes_high_water", "Maximum concurrent structural accumulator/reply bytes in this runtime generation."},
		{"page_rows_high_water", "Maximum concurrent transient decoded page rows in this runtime generation."},
		{"page_decoded_bytes_high_water", "Maximum concurrent structural decoded page bytes in this runtime generation."},
		{"decoded_bytes_high_water", "Maximum concurrent structural accumulator plus page bytes; not a heap/RSS bound."},
		{"result_census_enabled", "One when opt-in decoded result/page census is enabled; disabled result/page zeros are unobserved, not proof of empty ownership."},
		{"result_budget_enabled", "One when all provider paths reserve controlled decoded ownership before allocation; independent of diagnostic sampling."},
		{"owned_result_rows", "Route backing capacity references charged across results, old/new copies, pages and scratch."},
		{"owned_result_bytes", "Prepaid controlled decoded backing, cloned strings and lease metadata; excludes raw wire, selector maps, Directory, allocator overhead and RSS."},
		{"owned_result_rows_high_water", "Maximum concurrent charged route backing references in this runtime generation."},
		{"owned_result_bytes_high_water", "Maximum concurrent controlled decoded ownership bytes in this runtime generation."},
		{"result_budget_rejected_total", "Result allocation reservations rejected before ownership acquisition in this runtime generation."},
	}
	for i, d := range definitions {
		c.descriptors[i] = prometheus.NewDesc("wukongim_presence_recovery_"+d[0], d[1], nil, labels)
	}
	return c
}

func (c *presenceRecoveryCollector) Describe(out chan<- *prometheus.Desc) {
	for _, d := range c.descriptors {
		out <- d
	}
}

func (c *presenceRecoveryCollector) Collect(out chan<- prometheus.Metric) {
	c.mu.RLock()
	snapshot := c.snapshot
	c.mu.RUnlock()
	var s PresenceRecoverySnapshot
	if snapshot != nil {
		s = snapshot()
	}
	boolean := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}
	values := [...]float64{float64(s.QueuedCalls), float64(s.ActiveBatches), float64(s.ResultCalls), float64(s.Members), float64(s.FencedUIDs), float64(s.OwnedUIDBytes), boolean(s.ResultLimited), boolean(s.ExecutionLimited),
		float64(s.BatchesTotal), float64(s.BatchCallsTotal), float64(s.BatchUIDsTotal), float64(s.BatchCallsHighWater), float64(s.BatchUIDsHighWater),
		float64(s.ResultRows), float64(s.ResultDecodedBytes), float64(s.PageRows), float64(s.PageDecodedBytes), float64(s.ResultRowsHighWater), float64(s.ResultDecodedBytesHighWater), float64(s.PageRowsHighWater), float64(s.PageDecodedBytesHighWater), float64(s.DecodedBytesHighWater), boolean(s.ResultCensusEnabled), boolean(s.ResultBudgetEnabled), float64(s.OwnedResultRows), float64(s.OwnedResultBytes), float64(s.OwnedResultRowsHighWater), float64(s.OwnedResultBytesHighWater), float64(s.ResultBudgetRejected)}
	for i, value := range values {
		kind := prometheus.GaugeValue
		if (i >= 8 && i <= 10) || i == 28 {
			kind = prometheus.CounterValue
		}
		out <- prometheus.MustNewConstMetric(c.descriptors[i], kind, value)
	}
}
