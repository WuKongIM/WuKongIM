package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var conversationListSizeBuckets = []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 200, 256}

// ConversationMetrics exposes bounded membership-directory and Channel-head
// hydration costs. Labels deliberately exclude UID and channel identity.
type ConversationMetrics struct {
	listTotal              *prometheus.CounterVec
	listDuration           *prometheus.HistogramVec
	listScannedCandidates  *prometheus.HistogramVec
	listReturnedItems      *prometheus.HistogramVec
	listDeletes            *prometheus.HistogramVec
	listUnresolved         *prometheus.HistogramVec
	hydrationTotal         *prometheus.CounterVec
	hydrationDuration      *prometheus.HistogramVec
	hydrationItems         *prometheus.HistogramVec
	hydrationRemoteCalls   *prometheus.HistogramVec
	hydrationLocalReads    *prometheus.HistogramVec
	membershipMutationRows *prometheus.CounterVec
}

func newConversationMetrics(registry prometheus.Registerer, labels prometheus.Labels) *ConversationMetrics {
	m := &ConversationMetrics{
		listTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_conversation_directory_list_total", Help: "Membership-backed conversation directory requests.", ConstLabels: labels,
		}, []string{"result", "done"}),
		listDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_conversation_directory_list_duration_seconds", Help: "Membership-backed conversation directory request latency.", ConstLabels: labels, Buckets: gatewayFrameDurationBuckets,
		}, []string{"result", "done"}),
		listScannedCandidates: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_conversation_directory_scanned_candidates", Help: "Membership candidates scanned per directory request.", ConstLabels: labels, Buckets: conversationListSizeBuckets,
		}, []string{"result", "done"}),
		listReturnedItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_conversation_directory_returned_items", Help: "Transient conversations returned per directory request.", ConstLabels: labels, Buckets: conversationListSizeBuckets,
		}, []string{"result", "done"}),
		listDeletes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_conversation_directory_deletes", Help: "Directory deletion keys returned per request.", ConstLabels: labels, Buckets: conversationListSizeBuckets,
		}, []string{"result", "done"}),
		listUnresolved: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_conversation_directory_unresolved", Help: "Retryable channel keys returned per directory request.", ConstLabels: labels, Buckets: conversationListSizeBuckets,
		}, []string{"result", "done"}),
		hydrationTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_conversation_hydration_batch_total", Help: "Channel-head hydration batches by result.", ConstLabels: labels,
		}, []string{"result"}),
		hydrationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_conversation_hydration_batch_duration_seconds", Help: "Channel-head hydration batch latency.", ConstLabels: labels, Buckets: gatewayFrameDurationBuckets,
		}, []string{"result"}),
		hydrationItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_conversation_hydration_batch_items", Help: "Channel-head items per hydration batch.", ConstLabels: labels, Buckets: conversationListSizeBuckets,
		}, []string{"result"}),
		hydrationRemoteCalls: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_conversation_hydration_remote_batch_calls", Help: "Cross-node calls per origin hydration batch.", ConstLabels: labels, Buckets: conversationListSizeBuckets,
		}, []string{"result"}),
		hydrationLocalReads: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_conversation_hydration_local_reads", Help: "Leader-local channel-head reads per hydration batch.", ConstLabels: labels, Buckets: conversationListSizeBuckets,
		}, []string{"result"}),
		membershipMutationRows: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_conversation_membership_mutation_rows_total", Help: "Successfully proposed UID-directory mutation rows by directory and operation.", ConstLabels: labels,
		}, []string{"directory", "operation"}),
	}
	registry.MustRegister(
		m.listTotal, m.listDuration, m.listScannedCandidates, m.listReturnedItems,
		m.listDeletes, m.listUnresolved, m.hydrationTotal, m.hydrationDuration,
		m.hydrationItems, m.hydrationRemoteCalls, m.hydrationLocalReads,
		m.membershipMutationRows,
	)
	return m
}

// ObserveMembershipMutation records actual successful UID-directory proposal rows.
func (m *ConversationMetrics) ObserveMembershipMutation(directory, operation string, rows int) {
	if m == nil || rows <= 0 {
		return
	}
	directory = boundedMembershipDirectory(directory)
	operation = boundedMembershipOperation(operation)
	m.membershipMutationRows.WithLabelValues(directory, operation).Add(float64(rows))
}

func boundedMembershipDirectory(directory string) string {
	switch directory {
	case "ordinary", "cmd":
		return directory
	default:
		return "unknown"
	}
}

func boundedMembershipOperation(operation string) string {
	switch operation {
	case "upsert", "tombstone", "read_seq", "hide", "activate", "ack":
		return operation
	default:
		return "unknown"
	}
}

// ObserveDirectoryList records one membership page and its client-visible outcomes.
func (m *ConversationMetrics) ObserveDirectoryList(result string, done bool, duration time.Duration, scanned, returned, deletes, unresolved int) {
	if m == nil {
		return
	}
	result = boundedConversationResult(result)
	doneLabel := strconv.FormatBool(done)
	m.listTotal.WithLabelValues(result, doneLabel).Inc()
	m.listDuration.WithLabelValues(result, doneLabel).Observe(nonNegativeConversationDuration(duration).Seconds())
	m.listScannedCandidates.WithLabelValues(result, doneLabel).Observe(float64(nonNegativeConversationCount(scanned)))
	m.listReturnedItems.WithLabelValues(result, doneLabel).Observe(float64(nonNegativeConversationCount(returned)))
	m.listDeletes.WithLabelValues(result, doneLabel).Observe(float64(nonNegativeConversationCount(deletes)))
	m.listUnresolved.WithLabelValues(result, doneLabel).Observe(float64(nonNegativeConversationCount(unresolved)))
}

// ObserveHydrationBatch records route grouping and local read amplification.
func (m *ConversationMetrics) ObserveHydrationBatch(result string, duration time.Duration, items, remoteCalls, localReads int) {
	if m == nil {
		return
	}
	result = boundedConversationResult(result)
	m.hydrationTotal.WithLabelValues(result).Inc()
	m.hydrationDuration.WithLabelValues(result).Observe(nonNegativeConversationDuration(duration).Seconds())
	m.hydrationItems.WithLabelValues(result).Observe(float64(nonNegativeConversationCount(items)))
	m.hydrationRemoteCalls.WithLabelValues(result).Observe(float64(nonNegativeConversationCount(remoteCalls)))
	m.hydrationLocalReads.WithLabelValues(result).Observe(float64(nonNegativeConversationCount(localReads)))
}

func boundedConversationResult(result string) string {
	switch result {
	case "ok", "invalid_request", "not_configured", "error":
		return result
	default:
		return "error"
	}
}

func nonNegativeConversationDuration(duration time.Duration) time.Duration {
	if duration < 0 {
		return 0
	}
	return duration
}

func nonNegativeConversationCount(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func nonNegative(value int) int {
	return nonNegativeConversationCount(value)
}
