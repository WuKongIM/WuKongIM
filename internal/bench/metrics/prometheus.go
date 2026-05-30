package metrics

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	WukongIMV2BottleneckGateway       = "gateway_dispatch"
	WukongIMV2BottleneckChannelV2     = "channelv2_append"
	WukongIMV2BottleneckStorageCommit = "storage_commit"
	WukongIMV2BottleneckMixed         = "mixed_backpressure"
	WukongIMV2BottleneckUnobserved    = "no_observed_queue_pressure"
)

const (
	wukongIMV2GatewayQueuePressureRatio = 0.25
	wukongIMV2LatencyPressureSeconds    = 0.02
)

// PrometheusSample is one parsed Prometheus text exposition sample.
type PrometheusSample struct {
	// Name is the metric family or sample name.
	Name string
	// Labels contains the parsed low-cardinality labels for this sample.
	Labels map[string]string
	// Value is the sample value.
	Value float64
}

// PrometheusSnapshot is a parsed point-in-time Prometheus text exposition.
type PrometheusSnapshot struct {
	// Samples contains all parsed metric samples in input order.
	Samples []PrometheusSample
}

// WukongIMV2Attribution summarizes gateway vs ChannelV2 pressure from two snapshots.
type WukongIMV2Attribution struct {
	// Classification is the coarse bottleneck class inferred from observed pressure.
	Classification string
	// Reasons explains the evidence that produced the classification.
	Reasons []string

	GatewayQueueDepth                float64
	GatewayQueueCapacity             float64
	GatewayQueueRatio                float64
	GatewayDispatchWaitP99Seconds    float64
	GatewayBatchRecordsP50           float64
	ChannelV2ReactorMailboxDepthMax  float64
	ChannelV2WorkerQueueDepthMax     float64
	ChannelV2AppendP99Seconds        float64
	ChannelV2MetaResolveP99Seconds   float64
	ChannelV2MetaApplyP99Seconds     float64
	ChannelV2RuntimeAppendP99Seconds float64
	ChannelV2WorkerTaskP99Seconds    float64
	ChannelV2AppendBatchRecordsP50   float64
	StorageCommitQueueDepthMax       float64
	StorageCommitBatchRequestsP50    float64
	StorageCommitBatchRecordsP50     float64
	StorageCommitP99Seconds          float64
	StorageCommitTotalP99Seconds     float64
}

// ParsePrometheusText parses the simple Prometheus text exposition emitted by WuKongIM.
func ParsePrometheusText(r io.Reader) (PrometheusSnapshot, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var out PrometheusSnapshot
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sample, err := parsePrometheusSample(line)
		if err != nil {
			return PrometheusSnapshot{}, fmt.Errorf("line %d: %w", lineNo, err)
		}
		out.Samples = append(out.Samples, sample)
	}
	if err := scanner.Err(); err != nil {
		return PrometheusSnapshot{}, err
	}
	return out, nil
}

func parsePrometheusSample(line string) (PrometheusSample, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return PrometheusSample{}, fmt.Errorf("expected metric and value")
	}
	value, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return PrometheusSample{}, fmt.Errorf("parse value: %w", err)
	}
	name, labels, err := parsePrometheusMetric(fields[0])
	if err != nil {
		return PrometheusSample{}, err
	}
	return PrometheusSample{Name: name, Labels: labels, Value: value}, nil
}

func parsePrometheusMetric(raw string) (string, map[string]string, error) {
	open := strings.IndexByte(raw, '{')
	if open < 0 {
		name := strings.TrimSpace(raw)
		if name == "" {
			return "", nil, fmt.Errorf("missing metric name")
		}
		return name, nil, nil
	}
	close := strings.LastIndexByte(raw, '}')
	if close < open {
		return "", nil, fmt.Errorf("unterminated label set")
	}
	name := strings.TrimSpace(raw[:open])
	if name == "" {
		return "", nil, fmt.Errorf("missing metric name")
	}
	labels, err := parsePrometheusLabels(raw[open+1 : close])
	if err != nil {
		return "", nil, err
	}
	return name, labels, nil
}

func parsePrometheusLabels(raw string) (map[string]string, error) {
	labels := map[string]string{}
	for _, part := range splitPrometheusLabels(raw) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("invalid label %q", part)
		}
		value = strings.TrimSpace(value)
		if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			return nil, fmt.Errorf("invalid label value %q", value)
		}
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return nil, fmt.Errorf("parse label value: %w", err)
		}
		labels[strings.TrimSpace(key)] = unquoted
	}
	return labels, nil
}

func splitPrometheusLabels(raw string) []string {
	var parts []string
	start := 0
	inQuote := false
	escaped := false
	for i, r := range raw {
		if escaped {
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = inQuote
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				parts = append(parts, raw[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, raw[start:])
	return parts
}

// AnalyzeWukongIMV2Prometheus classifies gateway vs ChannelV2 pressure between two snapshots.
func AnalyzeWukongIMV2Prometheus(before, after PrometheusSnapshot) WukongIMV2Attribution {
	report := WukongIMV2Attribution{
		Classification: WukongIMV2BottleneckUnobserved,
	}
	report.GatewayQueueDepth, _ = after.maxGauge("wukongim_gateway_async_send_queue_depth")
	report.GatewayQueueCapacity, _ = after.maxGauge("wukongim_gateway_async_send_queue_capacity")
	if report.GatewayQueueCapacity > 0 {
		report.GatewayQueueRatio = report.GatewayQueueDepth / report.GatewayQueueCapacity
	}
	report.GatewayDispatchWaitP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_gateway_async_send_dispatch_wait_duration_seconds")
	report.GatewayBatchRecordsP50, _ = histogramQuantileDelta(0.50, before, after, "wukongim_gateway_async_send_batch_records")

	report.ChannelV2ReactorMailboxDepthMax, _ = after.maxGauge("wukongim_channelv2_reactor_mailbox_depth")
	report.ChannelV2WorkerQueueDepthMax, _ = after.maxGauge("wukongim_channelv2_worker_queue_depth")
	report.ChannelV2AppendP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_channelv2_append_duration_seconds")
	report.ChannelV2MetaResolveP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_resolve"})
	report.ChannelV2MetaApplyP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_apply"})
	report.ChannelV2RuntimeAppendP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "runtime_append"})
	report.ChannelV2WorkerTaskP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_channelv2_worker_task_duration_seconds")
	report.ChannelV2AppendBatchRecordsP50, _ = histogramQuantileDelta(0.50, before, after, "wukongim_channelv2_append_batch_records")
	report.StorageCommitQueueDepthMax, _ = after.maxGauge("wukongim_storage_commit_queue_depth")
	report.StorageCommitBatchRequestsP50, _ = histogramQuantileDelta(0.50, before, after, "wukongim_storage_commit_batch_requests")
	report.StorageCommitBatchRecordsP50, _ = histogramQuantileDelta(0.50, before, after, "wukongim_storage_commit_batch_records")
	report.StorageCommitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_storage_commit_batch_duration_seconds", map[string]string{"stage": "commit"})
	report.StorageCommitTotalP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_storage_commit_batch_duration_seconds", map[string]string{"stage": "total"})

	gatewayPressure := false
	if report.GatewayQueueRatio >= wukongIMV2GatewayQueuePressureRatio {
		gatewayPressure = true
		report.Reasons = append(report.Reasons, "gateway async SEND queue ratio is high")
	}
	if report.GatewayDispatchWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		gatewayPressure = true
		report.Reasons = append(report.Reasons, "gateway async SEND dispatch wait p99 is high")
	}

	channelPressure := false
	if report.ChannelV2ReactorMailboxDepthMax > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 reactor mailbox has queued events")
	}
	if report.ChannelV2WorkerQueueDepthMax > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 worker pool has queued tasks")
	}
	if report.ChannelV2AppendP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 append p99 is high")
	}
	if report.ChannelV2MetaResolveP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta resolve p99 is high")
	}
	if report.ChannelV2MetaApplyP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta apply p99 is high")
	}
	if report.ChannelV2RuntimeAppendP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 runtime append p99 is high")
	}
	if report.ChannelV2WorkerTaskP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 worker task p99 is high")
	}

	storagePressure := false
	if report.StorageCommitQueueDepthMax > 0 {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage commit queue has pending requests")
	}
	if report.StorageCommitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage physical commit p99 is high")
	}
	if report.StorageCommitTotalP99Seconds >= wukongIMV2LatencyPressureSeconds {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage grouped commit total p99 is high")
	}

	switch {
	case gatewayPressure && (channelPressure || storagePressure):
		report.Classification = WukongIMV2BottleneckMixed
	case gatewayPressure:
		report.Classification = WukongIMV2BottleneckGateway
	case storagePressure:
		report.Classification = WukongIMV2BottleneckStorageCommit
	case channelPressure:
		report.Classification = WukongIMV2BottleneckChannelV2
	}
	return report
}

func (s PrometheusSnapshot) maxGauge(name string) (float64, bool) {
	found := false
	var maxValue float64
	for _, sample := range s.Samples {
		if sample.Name != name {
			continue
		}
		if !found || sample.Value > maxValue {
			maxValue = sample.Value
			found = true
		}
	}
	return maxValue, found
}

type histogramBucket struct {
	le    float64
	count float64
}

func histogramQuantileDelta(q float64, before, after PrometheusSnapshot, family string) (float64, bool) {
	return histogramQuantileDeltaMatching(q, before, after, family, nil)
}

func histogramQuantileDeltaMatching(q float64, before, after PrometheusSnapshot, family string, labels map[string]string) (float64, bool) {
	beforeBuckets := before.histogramBucketsMatching(family, labels)
	afterBuckets := after.histogramBucketsMatching(family, labels)
	if len(afterBuckets) == 0 {
		return 0, false
	}
	buckets := make([]histogramBucket, 0, len(afterBuckets))
	for le, afterCount := range afterBuckets {
		delta := afterCount - beforeBuckets[le]
		if delta < 0 {
			delta = 0
		}
		buckets = append(buckets, histogramBucket{le: le, count: delta})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].le < buckets[j].le })
	return histogramQuantile(q, buckets)
}

func (s PrometheusSnapshot) histogramBucketsMatching(family string, labels map[string]string) map[float64]float64 {
	out := map[float64]float64{}
	bucketName := family + "_bucket"
	for _, sample := range s.Samples {
		if sample.Name != bucketName {
			continue
		}
		if !prometheusLabelsMatch(sample.Labels, labels) {
			continue
		}
		rawLE := sample.Labels["le"]
		le, err := parsePrometheusLE(rawLE)
		if err != nil {
			continue
		}
		out[le] += sample.Value
	}
	return out
}

func prometheusLabelsMatch(got map[string]string, want map[string]string) bool {
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

func parsePrometheusLE(raw string) (float64, error) {
	if raw == "+Inf" || raw == "Inf" {
		return math.Inf(1), nil
	}
	return strconv.ParseFloat(raw, 64)
}

func histogramQuantile(q float64, buckets []histogramBucket) (float64, bool) {
	if len(buckets) == 0 {
		return 0, false
	}
	total := buckets[len(buckets)-1].count
	if total <= 0 {
		return 0, false
	}
	rank := q * total
	prevCount := 0.0
	prevBound := 0.0
	for _, bucket := range buckets {
		if bucket.count < rank {
			prevCount = bucket.count
			if !math.IsInf(bucket.le, 1) {
				prevBound = bucket.le
			}
			continue
		}
		if math.IsInf(bucket.le, 1) {
			return prevBound, true
		}
		inBucket := bucket.count - prevCount
		if inBucket <= 0 {
			return bucket.le, true
		}
		fraction := (rank - prevCount) / inBucket
		return prevBound + (bucket.le-prevBound)*fraction, true
	}
	return buckets[len(buckets)-1].le, true
}
