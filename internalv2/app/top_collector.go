package app

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/shirou/gopsutil/v4/process"
)

const (
	topCounterGatewaySendTotal   = "gateway.send.total"
	topCounterGatewaySendWKProto = "gateway.send.wkproto"
	topCounterSendackSuccess     = "gateway.sendack.success"
	topCounterSendackError       = "gateway.sendack.error"
	topCounterGatewaySessionErr  = "gateway.session_error"
	topCounterMessageAppendOK    = "message.append.ok"
	topHistogramMessageAppend    = "message.append"

	topCounterDeliveryRoutes  = "delivery.routes"
	topCounterDeliveryPushOK  = "delivery.push.ok"
	topCounterDeliveryPushErr = "delivery.push.err"

	topGaugeStorageCommitDepth         = "storage.commit.message.depth"
	topGaugeStorageCommitCapacity      = "storage.commit.message.capacity"
	topGaugeDeliveryRetryQueueDepth    = "delivery.retry.depth"
	topGaugeDeliveryAckBindings        = "delivery.ack.bindings"
	topGaugeDeliveryRecipientQueue     = "delivery.recipient.queue.depth"
	topGaugeDeliveryRecipientQueueCap  = "delivery.recipient.queue.capacity"
	topHistogramChannelV2Append        = "channelv2.append"
	topHistogramStorageCommitBatchRows = "storage.commit.batch.records"
	topHistogramStorageCommitBatchMS   = "storage.commit.batch.commit"
	topHistogramDeliveryPush           = "delivery.push"

	topMaxHistogramValuesPerSample = 2048
	topAlertSignalWindow           = 10 * time.Second
	topAlertRetention              = 10 * time.Minute
	topMaxRetainedAlerts           = 200
)

// topCollectorOptions configures the node-local wkcli top collector.
type topCollectorOptions struct {
	// NodeID is the local cluster node identity used when the cluster snapshot is unavailable.
	NodeID uint64
	// NodeName is the operator-facing local node name.
	NodeName string
	// CollectInterval controls how often Start records a runtime sample.
	CollectInterval time.Duration
	// HistoryWindow bounds retained in-memory samples.
	HistoryWindow time.Duration
	// ClusterSnapshot returns the latest local clusterv2 readiness snapshot.
	ClusterSnapshot func() clusterv2.Snapshot
	// MetricsEnabled reports whether the optional Prometheus endpoint is enabled.
	MetricsEnabled bool
	// ResourceSampler returns local process CPU and memory usage for top snapshots.
	ResourceSampler func() topResourceSample
}

type topCollector struct {
	mu      sync.Mutex
	options topCollectorOptions

	counters map[string]uint64
	gauges   map[string]int64
	histos   map[string][]float64
	alerts   map[string]*topAlertState
	// alertOrder stores alert fingerprints in creation order for bounded retention.
	alertOrder []string
	alertSeq   uint64
	// ring stores fixed-capacity samples ordered by head/count.
	ring []topSample
	// head is the next ring slot written by recordSampleAt.
	head int
	// count is the number of valid samples currently retained.
	count int

	cancel context.CancelFunc
	done   chan struct{}
}

type topSample struct {
	at       time.Time
	counters map[string]uint64
	gauges   map[string]int64
	histos   map[string][]float64
	cluster  clusterv2.Snapshot
	resource topResourceSample
}

// topResourceSample captures local process resource counters for one top sample.
type topResourceSample struct {
	// CPUPercent is process CPU usage since the previous sample.
	CPUPercent float64
	// MemoryRSSBytes is resident process memory in bytes.
	MemoryRSSBytes uint64
	// MemoryVMSBytes is virtual process memory in bytes.
	MemoryVMSBytes uint64
	// Goroutines is the current goroutine count.
	Goroutines int
	// Threads is the current OS thread count when available.
	Threads int
}

type topAlertSignal struct {
	severity  string
	component string
	kind      string
	message   string
	hint      string
	evidence  map[string]string
}

type topAlertState struct {
	alert accessapi.TopAlert
}

type topProcessStats interface {
	Percent(time.Duration) (float64, error)
	MemoryInfo() (*process.MemoryInfoStat, error)
	NumThreads() (int32, error)
}

type topGopsutilResourceSampler struct {
	process    topProcessStats
	goroutines func() int
}

func newTopCollector(options topCollectorOptions) *topCollector {
	if options.CollectInterval <= 0 {
		options.CollectInterval = time.Second
	}
	if options.HistoryWindow <= 0 {
		options.HistoryWindow = 5 * time.Minute
	}
	if options.ResourceSampler == nil {
		sampler := defaultTopGopsutilResourceSampler()
		options.ResourceSampler = sampler.sample
	}
	return &topCollector{
		options:  options,
		counters: make(map[string]uint64),
		gauges:   make(map[string]int64),
		histos:   make(map[string][]float64),
		alerts:   make(map[string]*topAlertState),
		ring:     make([]topSample, topRingCapacity(options.CollectInterval, options.HistoryWindow)),
	}
}

func topRingCapacity(interval, window time.Duration) int {
	if interval <= 0 {
		interval = time.Second
	}
	n := int(window/interval) + 2
	if n < 2 {
		return 2
	}
	return n
}

func (c *topCollector) Start(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	c.cancel = cancel
	c.done = done
	interval := c.options.CollectInterval
	c.mu.Unlock()

	go c.run(runCtx, interval, done)
	return nil
}

func (c *topCollector) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		c.mu.Lock()
		if c.done == done {
			c.cancel = nil
			c.done = nil
		}
		c.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *topCollector) run(ctx context.Context, interval time.Duration, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	c.recordSampleAt(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			c.recordSampleAt(now)
		}
	}
}

func (c *topCollector) ObserveGatewaySend(protocol string, bytes int) {
	if c == nil {
		return
	}
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		protocol = "unknown"
	}
	c.addCounter(topCounterGatewaySendTotal, 1)
	c.addCounter("gateway.send."+protocol, 1)
	_ = bytes
}

func (c *topCollector) ObserveGatewaySendack(reason, source, class string) {
	if c == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(reason), "success") {
		c.addCounter(topCounterSendackSuccess, 1)
		return
	}
	c.addCounter(topCounterSendackError, 1)
	_, _ = source, class
}

func (c *topCollector) ObserveGatewaySessionError(protocol, closeReason, class string) {
	if c == nil {
		return
	}
	protocol = safeTopLabel(protocol)
	closeReason = safeTopLabel(closeReason)
	class = safeTopLabel(class)
	if class == "none" {
		class = closeReason
	}
	if closeReason == "none" {
		closeReason = class
	}
	c.addCounter(topCounterGatewaySessionErr, 1)
	c.addCounter(strings.Join([]string{topCounterGatewaySessionErr, protocol, closeReason, class}, "."), 1)
}

func (c *topCollector) ObserveMessageAppend(path, result string, d time.Duration) {
	if c == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(result), "ok") {
		c.addCounter(topCounterMessageAppendOK, 1)
	}
	c.observeDurationMS(topHistogramMessageAppend, d)
	_ = path
}

func (c *topCollector) SetQueue(component, pool, queue, priority string, depth, capacity int64) {
	if c == nil {
		return
	}
	key := topPressureKey(component, pool, queue, priority)
	c.setGauge(key+".depth", depth)
	c.setGauge(key+".capacity", capacity)
}

func (c *topCollector) SetInflight(component, pool string, inflight, workers int64) {
	if c == nil {
		return
	}
	key := topPressureKey(component, pool, "inflight", "none")
	c.setGauge(key+".inflight", inflight)
	c.setGauge(key+".workers", workers)
}

func (c *topCollector) SetChannelV2RuntimeCount(reactorID int, role string, count int64) {
	if c == nil {
		return
	}
	c.setGauge("channelv2.runtime."+safeTopLabel(channelV2ReactorPoolLabel(reactorID))+"."+safeTopLabel(role), count)
}

func (c *topCollector) SetChannelV2FollowerParked(reactorID int, count int64) {
	if c == nil {
		return
	}
	c.setGauge("channelv2.follower_parked."+safeTopLabel(channelV2ReactorPoolLabel(reactorID)), count)
}

func (c *topCollector) SetChannelV2ReactorMailbox(reactorID int, priority string, depth, capacity int64) {
	if c == nil {
		return
	}
	pool := channelV2ReactorPoolLabel(reactorID)
	key := "channelv2.reactor_mailbox." + safeTopLabel(pool) + "." + safeTopLabel(priority)
	c.setGauge(key+".depth", depth)
	c.setGauge(key+".capacity", capacity)
	c.SetQueue("channelv2", pool, "mailbox", priority, depth, capacity)
}

func (c *topCollector) SetChannelV2WorkerQueue(pool string, depth, capacity int64) {
	if c == nil {
		return
	}
	key := "channelv2.worker." + safeTopLabel(pool)
	c.setGauge(key+".queue_depth", depth)
	c.setGauge(key+".queue_capacity", capacity)
	c.SetQueue("channelv2", pool, "worker", "none", depth, capacity)
}

func (c *topCollector) SetChannelV2WorkerInflight(pool string, inflight, workers int64) {
	if c == nil {
		return
	}
	key := "channelv2.worker." + safeTopLabel(pool)
	c.setGauge(key+".inflight", inflight)
	c.setGauge(key+".workers", workers)
	c.SetInflight("channelv2", pool, inflight, workers)
}

func (c *topCollector) ObserveChannelV2AppendLatency(mode string, d time.Duration) {
	if c == nil {
		return
	}
	c.observeDurationMS(topHistogramChannelV2Append, d)
	c.observeDurationMS("channelv2.append.mode."+safeTopLabel(mode), d)
}

func (c *topCollector) ObserveChannelV2AppendStage(stage, result string, d time.Duration) {
	if c == nil || !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return
	}
	c.observeDurationMS("channelv2.stage."+safeTopLabel(stage), d)
}

func (c *topCollector) SetStorageCommitQueue(depth, capacity int64) {
	if c == nil {
		return
	}
	c.setGauge(topGaugeStorageCommitDepth, depth)
	c.setGauge(topGaugeStorageCommitCapacity, capacity)
	c.SetQueue(dbRuntimeComponent, dbMessageCommitPool, dbMessageCommitQueue, dbRuntimeQueuePriority, depth, capacity)
}

func (c *topCollector) ObserveStorageCommitRequest(lane, result string, d time.Duration) {
	if c == nil {
		return
	}
	c.observeDurationMS("storage.commit.request."+safeTopLabel(lane)+"/"+safeTopLabel(result), d)
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		c.addCounter("storage.commit.request.err", 1)
	}
}

func (c *topCollector) ObserveStorageCommitBatch(records int, commitDuration time.Duration) {
	if c == nil {
		return
	}
	c.observeValue(topHistogramStorageCommitBatchRows, float64(records))
	c.observeDurationMS(topHistogramStorageCommitBatchMS, commitDuration)
}

func (c *topCollector) SetDeliveryRetryQueueDepth(depth int64) {
	if c == nil {
		return
	}
	c.setGauge(topGaugeDeliveryRetryQueueDepth, depth)
}

func (c *topCollector) SetDeliveryAckBindings(count int64) {
	if c == nil {
		return
	}
	c.setGauge(topGaugeDeliveryAckBindings, count)
}

func (c *topCollector) SetDeliveryRecipientQueue(depth, capacity int64) {
	if c == nil {
		return
	}
	c.setGauge(topGaugeDeliveryRecipientQueue, depth)
	c.setGauge(topGaugeDeliveryRecipientQueueCap, capacity)
	c.SetQueue("delivery", "recipient", "queue", "none", depth, capacity)
}

func (c *topCollector) ObserveDeliveryRoutes(routes int) {
	if c == nil || routes <= 0 {
		return
	}
	c.addCounter(topCounterDeliveryRoutes, uint64(routes))
}

func (c *topCollector) ObserveDeliveryPush(result string, accepted int, d time.Duration) {
	if c == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(result), "ok") {
		if accepted <= 0 {
			accepted = 1
		}
		c.addCounter(topCounterDeliveryPushOK, uint64(accepted))
	} else {
		c.addCounter(topCounterDeliveryPushErr, 1)
	}
	c.observeDurationMS(topHistogramDeliveryPush, d)
}

func topPressureKey(component, pool, queue, priority string) string {
	return "pressure." + safeTopLabel(component) + "." + safeTopLabel(pool) + "." + safeTopLabel(queue) + "." + safeTopLabel(priority)
}

func safeTopLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "none"
	}
	return strings.ReplaceAll(s, ".", "_")
}

func (c *topCollector) addCounter(key string, delta uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counters[key] += delta
}

func (c *topCollector) setGauge(key string, value int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gauges[key] = value
}

func (c *topCollector) observeDurationMS(key string, d time.Duration) {
	c.observeValue(key, float64(d)/float64(time.Millisecond))
}

func (c *topCollector) observeValue(key string, value float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.histos[key]) >= topMaxHistogramValuesPerSample {
		return
	}
	c.histos[key] = append(c.histos[key], value)
}

func (c *topCollector) recordSampleAt(at time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	counters := cloneUint64Map(c.counters)
	gauges := cloneInt64Map(c.gauges)
	histos := cloneHistos(c.histos)
	c.histos = make(map[string][]float64)
	c.mu.Unlock()

	cluster := c.clusterSnapshot()
	resource := c.resourceSample()

	c.mu.Lock()
	c.ring[c.head] = topSample{
		at:       at.UTC(),
		counters: counters,
		gauges:   gauges,
		histos:   histos,
		cluster:  cluster,
		resource: resource,
	}
	c.head = (c.head + 1) % len(c.ring)
	if c.count < len(c.ring) {
		c.count++
	}
	c.updateAlertsLocked(at.UTC(), c.alertSignalsLocked())
	c.pruneAlertsLocked(at.UTC())
	c.mu.Unlock()
}

func (c *topCollector) clusterSnapshot() clusterv2.Snapshot {
	if c.options.ClusterSnapshot == nil {
		return clusterv2.Snapshot{NodeID: c.options.NodeID}
	}
	snapshot := c.options.ClusterSnapshot()
	if snapshot.NodeID == 0 {
		snapshot.NodeID = c.options.NodeID
	}
	return snapshot
}

func (c *topCollector) clone() *topCollector {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := newTopCollector(c.options)
	out.counters = cloneUint64Map(c.counters)
	out.gauges = cloneInt64Map(c.gauges)
	out.histos = cloneHistos(c.histos)
	out.alerts = cloneTopAlertStates(c.alerts)
	out.alertOrder = append([]string(nil), c.alertOrder...)
	out.alertSeq = c.alertSeq
	out.ring = make([]topSample, len(c.ring))
	for i := range c.ring {
		out.ring[i] = cloneTopSample(c.ring[i])
	}
	out.head = c.head
	out.count = c.count
	return out
}

func (c *topCollector) resourceSample() topResourceSample {
	if c.options.ResourceSampler != nil {
		return c.options.ResourceSampler()
	}
	return collectTopResourceSample()
}

func (c *topCollector) SnapshotTop(_ context.Context, query accessapi.TopSnapshotQuery) (accessapi.TopSnapshot, error) {
	if c == nil {
		return accessapi.TopSnapshot{}, accessapi.ErrTopWarmingUp
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	window := c.windowLocked(query.Window)
	if len(window) < 2 {
		return accessapi.TopSnapshot{}, accessapi.ErrTopWarmingUp
	}
	first := window[0]
	last := window[len(window)-1]
	seconds := last.at.Sub(first.at).Seconds()
	traffic := buildTraffic(window, seconds)
	clients := buildClients(window, seconds)
	resources := buildResources(window)
	pressure := c.buildPressureLocked(last, query.Limit)
	verdict := buildTopVerdict(last.cluster, traffic, pressure)
	alerts := c.snapshotAlertsLocked(query.Limit)

	snapshot := accessapi.TopSnapshot{
		Version:       "top/v1",
		Scope:         "local_node",
		GeneratedAt:   time.Now().UTC(),
		WindowSeconds: int(last.at.Sub(first.at).Seconds()),
		Node:          c.topNodeSnapshot(last.cluster),
		Verdict:       verdict,
		Resources:     resources,
		Alerts:        alerts,
		Sources: accessapi.TopSources{
			Collector:       accessapi.TopSourceStatus{Available: true, SampleCount: len(window)},
			ClusterSnapshot: accessapi.TopSourceStatus{Available: true, SampleCount: 1},
			Metrics:         accessapi.TopMetricsSource{Enabled: c.options.MetricsEnabled, Required: false},
		},
	}
	if includeTraffic(query.View) {
		snapshot.Traffic = traffic
	}
	if includeClients(query.View) {
		snapshot.Clients = clients
	}
	if includePressure(query.View) {
		snapshot.Pressure = pressure
	}
	if includeChannelV2(query.View) {
		snapshot.ChannelV2 = buildChannelV2(window)
	}
	if includeStorage(query.View) {
		snapshot.Storage = buildStorage(window)
	}
	if includeDelivery(query.View) {
		snapshot.Delivery = buildDelivery(window, seconds)
	}
	return snapshot, nil
}

func collectTopResourceSample() topResourceSample {
	return defaultTopGopsutilResourceSampler().sample()
}

func defaultTopGopsutilResourceSampler() topGopsutilResourceSampler {
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		proc = nil
	}
	return topGopsutilResourceSampler{
		process:    proc,
		goroutines: runtime.NumGoroutine,
	}
}

func (s topGopsutilResourceSampler) sample() topResourceSample {
	out := topResourceSample{}
	if s.goroutines != nil {
		out.Goroutines = s.goroutines()
	}
	if s.process == nil {
		return out
	}
	if cpuPercent, err := s.process.Percent(0); err == nil && cpuPercent > 0 {
		out.CPUPercent = cpuPercent
	}
	if memoryInfo, err := s.process.MemoryInfo(); err == nil && memoryInfo != nil {
		out.MemoryRSSBytes = memoryInfo.RSS
		out.MemoryVMSBytes = memoryInfo.VMS
	}
	if threads, err := s.process.NumThreads(); err == nil && threads > 0 {
		out.Threads = int(threads)
	}
	return out
}

func (c *topCollector) windowLocked(window time.Duration) []topSample {
	if c.count == 0 || len(c.ring) == 0 {
		return nil
	}
	if window <= 0 {
		window = 10 * time.Second
	}
	lastIndex := (c.head - 1 + len(c.ring)) % len(c.ring)
	last := c.ring[lastIndex].at
	cutoff := last.Add(-window)
	out := make([]topSample, 0, c.count)
	oldest := (c.head - c.count + len(c.ring)) % len(c.ring)
	for i := 0; i < c.count; i++ {
		sample := c.ring[(oldest+i)%len(c.ring)]
		if !sample.at.Before(cutoff) && !sample.at.After(last) {
			out = append(out, cloneTopSample(sample))
		}
	}
	return out
}

func rate(first, last map[string]uint64, key string, seconds float64) float64 {
	if seconds <= 0 {
		return 0
	}
	return float64(counterDelta(first, last, key)) / seconds
}

func counterDelta(first, last map[string]uint64, key string) uint64 {
	if last[key] < first[key] {
		return 0
	}
	return last[key] - first[key]
}

func counterTotalByPrefix(counters map[string]uint64, prefix string) uint64 {
	var total uint64
	for key, value := range counters {
		if strings.HasPrefix(key, prefix) {
			total += value
		}
	}
	return total
}

func counterDeltaByPrefix(first, last map[string]uint64, prefix string) uint64 {
	var total uint64
	seen := make(map[string]struct{})
	for key := range first {
		if strings.HasPrefix(key, prefix) {
			total += counterDelta(first, last, key)
			seen[key] = struct{}{}
		}
	}
	for key := range last {
		if strings.HasPrefix(key, prefix) {
			if _, ok := seen[key]; !ok {
				total += counterDelta(first, last, key)
			}
		}
	}
	return total
}

func rateByPrefix(first, last map[string]uint64, prefix string, seconds float64) float64 {
	if seconds <= 0 {
		return 0
	}
	return float64(counterDeltaByPrefix(first, last, prefix)) / seconds
}

func buildTraffic(window []topSample, seconds float64) *accessapi.TopTraffic {
	first := window[0]
	last := window[len(window)-1]
	send := rate(first.counters, last.counters, topCounterGatewaySendTotal, seconds)
	success := rate(first.counters, last.counters, topCounterSendackSuccess, seconds)
	errors := rate(first.counters, last.counters, topCounterSendackError, seconds)
	appendOK := rate(first.counters, last.counters, topCounterMessageAppendOK, seconds)
	totalSendack := success + errors
	errorRate := 0.0
	if totalSendack > 0 {
		errorRate = errors / totalSendack
	}
	values := histogramValues(window, topHistogramMessageAppend)
	fanoutRate := 0.0
	if send > 0 {
		fanoutRate = rate(first.counters, last.counters, topCounterDeliveryRoutes, seconds) / send
	}
	return &accessapi.TopTraffic{
		SendPerSec:         send,
		SendackPerSec:      totalSendack,
		SendackErrorPerSec: errors,
		SendackErrorRate:   errorRate,
		AppendPerSec:       appendOK,
		AppendP50MS:        percentile(values, 0.50),
		AppendP99MS:        percentile(values, 0.99),
		DeliverPerSec:      rate(first.counters, last.counters, topCounterDeliveryPushOK, seconds),
		FanoutRate:         fanoutRate,
	}
}

func buildClients(window []topSample, seconds float64) *accessapi.TopClients {
	first := window[0]
	last := window[len(window)-1]
	const openPrefix = "gateway.connection.open."
	const closePrefix = "gateway.connection.close."

	opens := counterTotalByPrefix(last.counters, openPrefix)
	closes := counterTotalByPrefix(last.counters, closePrefix)
	connections := int64(0)
	if opens > closes {
		connections = int64(opens - closes)
	}
	byProtocol := make(map[string]int64)
	protocols := connectionProtocols(last.counters, openPrefix, closePrefix)
	for _, protocol := range protocols {
		open := last.counters[openPrefix+protocol]
		close := last.counters[closePrefix+protocol]
		if open > close {
			byProtocol[protocol] = int64(open - close)
		}
	}
	if len(byProtocol) == 0 {
		byProtocol = nil
	}
	return &accessapi.TopClients{
		Connections:           connections,
		ConnectionsByProtocol: byProtocol,
		AuthFailPerSec:        rate(first.counters, last.counters, "gateway.auth.fail", seconds),
		ClosePerSec:           rateByPrefix(first.counters, last.counters, closePrefix, seconds),
	}
}

func buildResources(window []topSample) *accessapi.TopResources {
	if len(window) == 0 {
		return nil
	}
	last := window[len(window)-1]
	return &accessapi.TopResources{
		CPUPercent:     last.resource.CPUPercent,
		MemoryRSSBytes: last.resource.MemoryRSSBytes,
		MemoryVMSBytes: last.resource.MemoryVMSBytes,
		Goroutines:     last.resource.Goroutines,
		Threads:        last.resource.Threads,
	}
}

func connectionProtocols(counters map[string]uint64, prefixes ...string) []string {
	seen := make(map[string]struct{})
	for key := range counters {
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				protocol := strings.TrimPrefix(key, prefix)
				if protocol != "" {
					seen[protocol] = struct{}{}
				}
			}
		}
	}
	protocols := make([]string, 0, len(seen))
	for protocol := range seen {
		protocols = append(protocols, protocol)
	}
	sort.Strings(protocols)
	return protocols
}

func buildChannelV2(window []topSample) *accessapi.TopChannelV2 {
	last := window[len(window)-1]
	out := &accessapi.TopChannelV2{
		WorkerQueueDepthByPool:    make(map[string]int64),
		WorkerQueueCapacityByPool: make(map[string]int64),
		WorkerInflightByPool:      make(map[string]int64),
		WorkerCapacityByPool:      make(map[string]int64),
		StageP99MS:                make(map[string]float64),
		AppendP99MS:               percentile(histogramValues(window, topHistogramChannelV2Append), 0.99),
	}
	for key, value := range last.gauges {
		switch {
		case strings.HasPrefix(key, "channelv2.runtime."):
			parts := strings.Split(key, ".")
			if len(parts) == 4 {
				switch parts[3] {
				case "leader":
					out.ActiveLeader += value
				case "follower":
					out.ActiveFollower += value
				}
			}
		case strings.HasPrefix(key, "channelv2.follower_parked."):
			out.FollowerParked += value
		case strings.HasPrefix(key, "channelv2.reactor_mailbox.") && strings.HasSuffix(key, ".depth"):
			if value > out.ReactorMailboxDepthMax {
				out.ReactorMailboxDepthMax = value
			}
		case strings.HasPrefix(key, "channelv2.reactor_mailbox.") && strings.HasSuffix(key, ".capacity"):
			if value > out.ReactorMailboxCapacityMax {
				out.ReactorMailboxCapacityMax = value
			}
		case strings.HasPrefix(key, "channelv2.worker.") && strings.HasSuffix(key, ".queue_depth"):
			pool := strings.TrimSuffix(strings.TrimPrefix(key, "channelv2.worker."), ".queue_depth")
			out.WorkerQueueDepthByPool[pool] = value
		case strings.HasPrefix(key, "channelv2.worker.") && strings.HasSuffix(key, ".queue_capacity"):
			pool := strings.TrimSuffix(strings.TrimPrefix(key, "channelv2.worker."), ".queue_capacity")
			out.WorkerQueueCapacityByPool[pool] = value
		case strings.HasPrefix(key, "channelv2.worker.") && strings.HasSuffix(key, ".inflight"):
			pool := strings.TrimSuffix(strings.TrimPrefix(key, "channelv2.worker."), ".inflight")
			out.WorkerInflightByPool[pool] = value
		case strings.HasPrefix(key, "channelv2.worker.") && strings.HasSuffix(key, ".workers"):
			pool := strings.TrimSuffix(strings.TrimPrefix(key, "channelv2.worker."), ".workers")
			out.WorkerCapacityByPool[pool] = value
		}
	}
	out.ActiveTotal = out.ActiveLeader + out.ActiveFollower
	stageKeys := histogramKeys(window, "channelv2.stage.")
	for _, key := range stageKeys {
		stage := strings.TrimPrefix(key, "channelv2.stage.")
		p99 := percentile(histogramValues(window, key), 0.99)
		out.StageP99MS[stage] = p99
		if p99 > out.StageP99MS[out.HotStage] || out.HotStage == "" {
			out.HotStage = stage
		}
	}
	if len(out.WorkerQueueDepthByPool) == 0 {
		out.WorkerQueueDepthByPool = nil
	}
	if len(out.WorkerQueueCapacityByPool) == 0 {
		out.WorkerQueueCapacityByPool = nil
	}
	if len(out.WorkerInflightByPool) == 0 {
		out.WorkerInflightByPool = nil
	}
	if len(out.WorkerCapacityByPool) == 0 {
		out.WorkerCapacityByPool = nil
	}
	if len(out.StageP99MS) == 0 {
		out.StageP99MS = nil
	}
	return out
}

func buildStorage(window []topSample) *accessapi.TopStorage {
	last := window[len(window)-1]
	queue := accessapi.TopStorageCommitQueue{
		Store:              "message",
		Depth:              last.gauges[topGaugeStorageCommitDepth],
		Capacity:           last.gauges[topGaugeStorageCommitCapacity],
		RequestP99MSByLane: make(map[string]float64),
		BatchRecordsP50:    percentile(histogramValues(window, topHistogramStorageCommitBatchRows), 0.50),
		BatchCommitP99MS:   percentile(histogramValues(window, topHistogramStorageCommitBatchMS), 0.99),
	}
	for _, key := range histogramKeys(window, "storage.commit.request.") {
		lane := strings.TrimPrefix(key, "storage.commit.request.")
		queue.RequestP99MSByLane[lane] = percentile(histogramValues(window, key), 0.99)
	}
	if len(queue.RequestP99MSByLane) == 0 {
		queue.RequestP99MSByLane = nil
	}
	return &accessapi.TopStorage{CommitQueues: []accessapi.TopStorageCommitQueue{queue}}
}

func buildDelivery(window []topSample, seconds float64) *accessapi.TopDelivery {
	first := window[0]
	last := window[len(window)-1]
	ok := rate(first.counters, last.counters, topCounterDeliveryPushOK, seconds)
	errs := rate(first.counters, last.counters, topCounterDeliveryPushErr, seconds)
	errorRate := 0.0
	if ok+errs > 0 {
		errorRate = errs / (ok + errs)
	}
	return &accessapi.TopDelivery{
		PushPerSec:             ok,
		RoutesPerSec:           rate(first.counters, last.counters, topCounterDeliveryRoutes, seconds),
		PushP99MS:              percentile(histogramValues(window, topHistogramDeliveryPush), 0.99),
		RetryQueueDepth:        last.gauges[topGaugeDeliveryRetryQueueDepth],
		AckBindings:            last.gauges[topGaugeDeliveryAckBindings],
		RecipientQueueDepth:    last.gauges[topGaugeDeliveryRecipientQueue],
		RecipientQueueCapacity: last.gauges[topGaugeDeliveryRecipientQueueCap],
		ErrorRate:              errorRate,
	}
}

func histogramValues(window []topSample, key string) []float64 {
	var out []float64
	for i := 1; i < len(window); i++ {
		out = append(out, window[i].histos[key]...)
	}
	return out
}

func histogramKeys(window []topSample, prefix string) []string {
	seen := make(map[string]struct{})
	for _, sample := range window {
		for key := range sample.histos {
			if strings.HasPrefix(key, prefix) {
				seen[key] = struct{}{}
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(p * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func (c *topCollector) topNodeSnapshot(snapshot clusterv2.Snapshot) accessapi.TopNodeSnapshot {
	readyParts := map[string]bool{
		"routes":   snapshot.RoutesReady,
		"slots":    snapshot.SlotsReady,
		"channels": snapshot.ChannelsReady,
	}
	return accessapi.TopNodeSnapshot{
		ID:               snapshot.NodeID,
		Name:             c.options.NodeName,
		Ready:            snapshot.RoutesReady && snapshot.SlotsReady && snapshot.ChannelsReady,
		ReadyParts:       readyParts,
		StateRevision:    snapshot.StateRevision,
		ControllerLeader: snapshot.ControllerLead,
		SlotCount:        snapshot.SlotCount,
		HashSlotCount:    snapshot.HashSlotCount,
	}
}

func (c *topCollector) buildPressureLocked(sample topSample, limit int) *accessapi.TopPressure {
	if limit <= 0 {
		limit = 20
	}
	byKey := make(map[string]*accessapi.TopPressureItem)
	for key, value := range sample.gauges {
		parts := strings.Split(key, ".")
		if len(parts) != 6 || parts[0] != "pressure" {
			continue
		}
		base := strings.Join(parts[:5], ".")
		item := byKey[base]
		if item == nil {
			item = &accessapi.TopPressureItem{
				Component: parts[1],
				Pool:      parts[2],
				Queue:     parts[3],
				Priority:  parts[4],
			}
			byKey[base] = item
		}
		switch parts[5] {
		case "depth":
			item.Depth = value
		case "capacity":
			item.Capacity = value
		case "inflight":
			item.Inflight = value
		case "workers":
			item.Workers = value
		}
	}

	items := make([]accessapi.TopPressureItem, 0, len(byKey))
	componentScores := make(map[string]float64)
	overall := "ok"
	for _, item := range byKey {
		item.Score = pressureScore(item.Depth, item.Capacity)
		if inflightScore := inflightPressureScore(item.Inflight, item.Workers); inflightScore > item.Score {
			item.Score = inflightScore
		}
		item.Level = pressureLevel(item.Score)
		item.Hint = pressureHint(*item)
		items = append(items, *item)
		if item.Score > componentScores[item.Component] {
			componentScores[item.Component] = item.Score
		}
		if severityRank(item.Level) > severityRank(overall) {
			overall = item.Level
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Component != items[j].Component {
			return items[i].Component < items[j].Component
		}
		if items[i].Pool != items[j].Pool {
			return items[i].Pool < items[j].Pool
		}
		if items[i].Queue != items[j].Queue {
			return items[i].Queue < items[j].Queue
		}
		return items[i].Priority < items[j].Priority
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return &accessapi.TopPressure{
		OverallLevel:    overall,
		ComponentScores: componentScores,
		Top:             items,
	}
}

func pressureScore(depth, capacity int64) float64 {
	if capacity > 0 {
		return float64(depth) / float64(capacity)
	}
	if depth > 0 {
		return 1
	}
	return 0
}

func inflightPressureScore(inflight, workers int64) float64 {
	if workers <= 0 {
		return 0
	}
	return pressureScore(inflight, workers)
}

func pressureLevel(score float64) string {
	switch {
	case score >= 0.95:
		return "critical"
	case score >= 0.80:
		return "degraded"
	case score >= 0.60:
		return "busy"
	default:
		return "ok"
	}
}

func pressureHint(item accessapi.TopPressureItem) string {
	if item.Level == "ok" {
		return ""
	}
	if item.Workers > 0 && item.Inflight >= item.Workers {
		return "inflight work is at worker capacity"
	}
	if item.Capacity <= 0 && item.Depth > 0 {
		return "queue has depth but no capacity"
	}
	return "queue depth is approaching capacity"
}

func buildTopVerdict(snapshot clusterv2.Snapshot, traffic *accessapi.TopTraffic, pressure *accessapi.TopPressure) accessapi.TopVerdict {
	if !snapshot.RoutesReady || !snapshot.SlotsReady || !snapshot.ChannelsReady {
		return accessapi.TopVerdict{
			Level:   "critical",
			Summary: "cluster runtime is not ready",
			Reasons: readinessReasons(snapshot),
		}
	}
	level := "ok"
	summary := "runtime healthy"
	reasons := make([]string, 0, 2)
	if pressure != nil && severityRank(pressure.OverallLevel) > severityRank(level) {
		level = pressure.OverallLevel
		if level != "ok" && len(pressure.Top) > 0 {
			top := pressure.Top[0]
			reasons = append(reasons, top.Component+"/"+top.Pool+" pressure")
		}
		if level != "ok" {
			summary = "runtime pressure detected"
		}
	}
	if traffic != nil && traffic.SendackErrorRate >= 0.05 && severityRank(level) < severityRank("degraded") {
		level = "degraded"
		reasons = append(reasons, "sendack error rate >= 5%")
		summary = "sendack error rate is high"
	}
	return accessapi.TopVerdict{Level: level, Summary: summary, Reasons: reasons}
}

func readinessReasons(snapshot clusterv2.Snapshot) []string {
	reasons := make([]string, 0, 3)
	if !snapshot.RoutesReady {
		reasons = append(reasons, "routes not ready")
	}
	if !snapshot.SlotsReady {
		reasons = append(reasons, "slots not ready")
	}
	if !snapshot.ChannelsReady {
		reasons = append(reasons, "channelv2 not ready")
	}
	return reasons
}

func (c *topCollector) alertSignalsLocked() []topAlertSignal {
	if c.count == 0 || len(c.ring) == 0 {
		return nil
	}
	lastIndex := (c.head - 1 + len(c.ring)) % len(c.ring)
	last := c.ring[lastIndex]
	signals := readinessAlertSignals(last.cluster)

	window := c.windowLocked(topAlertSignalWindow)
	if len(window) >= 2 {
		first := window[0]
		seconds := last.at.Sub(first.at).Seconds()
		traffic := buildTraffic(window, seconds)
		if traffic.SendackErrorRate >= 0.05 && traffic.SendackErrorPerSec > 0 {
			signals = append(signals, topAlertSignal{
				severity:  "error",
				component: "gateway",
				kind:      "sendack_error_rate_high",
				message:   "sendack error rate is high",
				hint:      "inspect gateway sendack failures and append errors",
				evidence: map[string]string{
					"window":                topAlertSignalWindow.String(),
					"sendack_error_rate":    topAlertEvidenceFloat(traffic.SendackErrorRate),
					"sendack_error_per_sec": topAlertEvidenceFloat(traffic.SendackErrorPerSec),
					"threshold.degraded":    "0.05",
				},
			})
		}
		if signal, ok := gatewaySessionErrorAlertSignal(first, last); ok {
			signals = append(signals, signal)
		}
	}

	pressure := c.buildPressureLocked(last, topMaxRetainedAlerts)
	if pressure != nil {
		for _, item := range pressure.Top {
			if item.Level == "ok" {
				continue
			}
			signals = append(signals, topAlertSignal{
				severity:  topAlertSeverityFromLevel(item.Level),
				component: item.Component,
				kind:      "pressure_high",
				message:   pressureAlertMessage(item),
				hint:      item.Hint,
				evidence:  pressureAlertEvidence(item),
			})
		}
	}
	return signals
}

func gatewaySessionErrorAlertSignal(first, last topSample) (topAlertSignal, bool) {
	prefix := topCounterGatewaySessionErr + "."
	bestKey := ""
	var bestCount uint64
	seen := make(map[string]struct{})
	for key := range first.counters {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		seen[key] = struct{}{}
		count := counterDelta(first.counters, last.counters, key)
		if count > bestCount || count == bestCount && count > 0 && key < bestKey {
			bestKey = key
			bestCount = count
		}
	}
	for key := range last.counters {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		count := counterDelta(first.counters, last.counters, key)
		if count > bestCount || count == bestCount && count > 0 && (bestKey == "" || key < bestKey) {
			bestKey = key
			bestCount = count
		}
	}
	if bestCount == 0 || bestKey == "" {
		return topAlertSignal{}, false
	}
	labels := strings.Split(strings.TrimPrefix(bestKey, prefix), ".")
	if len(labels) != 3 {
		return topAlertSignal{}, false
	}
	window := last.at.Sub(first.at)
	if window < 0 {
		window = 0
	}
	return topAlertSignal{
		severity:  "error",
		component: "gateway",
		kind:      "session_error",
		message:   "gateway session errors observed",
		hint:      "inspect gateway close reason and protocol error handling",
		evidence: map[string]string{
			"protocol":     labels[0],
			"close_reason": labels[1],
			"class":        labels[2],
			"count":        fmt.Sprintf("%d", bestCount),
			"window":       window.String(),
		},
	}, true
}

func readinessAlertSignals(snapshot clusterv2.Snapshot) []topAlertSignal {
	signals := make([]topAlertSignal, 0, 3)
	if !snapshot.RoutesReady {
		signals = append(signals, topAlertSignal{
			severity:  "critical",
			component: "cluster",
			kind:      "ready_part_down",
			message:   "routes not ready",
			hint:      "check clusterv2 routing state and node links",
			evidence:  map[string]string{"ready_part": "routes", "ready": "false"},
		})
	}
	if !snapshot.SlotsReady {
		signals = append(signals, topAlertSignal{
			severity:  "critical",
			component: "cluster",
			kind:      "ready_part_down",
			message:   "slots not ready",
			hint:      "check slot raft leaders and metadata readiness",
			evidence:  map[string]string{"ready_part": "slots", "ready": "false"},
		})
	}
	if !snapshot.ChannelsReady {
		signals = append(signals, topAlertSignal{
			severity:  "critical",
			component: "channelv2",
			kind:      "ready_part_down",
			message:   "channelv2 not ready",
			hint:      "check channel runtime bootstrap and replication readiness",
			evidence:  map[string]string{"ready_part": "channels", "ready": "false"},
		})
	}
	return signals
}

func (c *topCollector) updateAlertsLocked(at time.Time, signals []topAlertSignal) {
	if c.alerts == nil {
		c.alerts = make(map[string]*topAlertState)
	}
	nodeID, nodeName := c.alertNodeIdentityLocked()
	active := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		signal = normalizeTopAlertSignal(signal)
		fingerprint := topAlertFingerprint(signal)
		if _, ok := active[fingerprint]; ok {
			continue
		}
		active[fingerprint] = struct{}{}
		state := c.alerts[fingerprint]
		if state == nil {
			c.alertSeq++
			state = &topAlertState{alert: accessapi.TopAlert{
				ID:          fmt.Sprintf("top-alert-%d", c.alertSeq),
				Fingerprint: fingerprint,
				FirstSeen:   at,
			}}
			c.alerts[fingerprint] = state
			c.alertOrder = append(c.alertOrder, fingerprint)
		}
		state.alert.NodeID = nodeID
		state.alert.NodeName = nodeName
		state.alert.Severity = signal.severity
		state.alert.Component = signal.component
		state.alert.Kind = signal.kind
		state.alert.Message = signal.message
		state.alert.Hint = signal.hint
		state.alert.Evidence = cloneStringMap(signal.evidence)
		state.alert.LastSeen = at
		state.alert.Count++
		state.alert.Active = true
		state.alert.ResolvedAt = nil
	}
	for fingerprint, state := range c.alerts {
		if _, ok := active[fingerprint]; ok || !state.alert.Active {
			continue
		}
		resolvedAt := at
		state.alert.Active = false
		state.alert.ResolvedAt = &resolvedAt
	}
}

func (c *topCollector) alertNodeIdentityLocked() (uint64, string) {
	if c.count == 0 || len(c.ring) == 0 {
		return c.options.NodeID, c.options.NodeName
	}
	lastIndex := (c.head - 1 + len(c.ring)) % len(c.ring)
	nodeID := c.ring[lastIndex].cluster.NodeID
	if nodeID == 0 {
		nodeID = c.options.NodeID
	}
	return nodeID, c.options.NodeName
}

func (c *topCollector) pruneAlertsLocked(now time.Time) {
	if len(c.alertOrder) == 0 {
		return
	}
	retained := c.alertOrder[:0]
	for _, fingerprint := range c.alertOrder {
		state := c.alerts[fingerprint]
		if state == nil {
			continue
		}
		if !state.alert.Active && now.Sub(state.alert.LastSeen) > topAlertRetention {
			delete(c.alerts, fingerprint)
			continue
		}
		retained = append(retained, fingerprint)
	}
	for len(retained) > topMaxRetainedAlerts {
		delete(c.alerts, retained[0])
		retained = retained[1:]
	}
	c.alertOrder = retained
}

func (c *topCollector) snapshotAlertsLocked(limit int) *accessapi.TopAlerts {
	if len(c.alerts) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	active := make([]accessapi.TopAlert, 0, len(c.alerts))
	recent := make([]accessapi.TopAlert, 0, len(c.alerts))
	counts := accessapi.TopAlertCounts{}
	for _, state := range c.alerts {
		if state == nil {
			continue
		}
		alert := cloneTopAlert(state.alert)
		recent = append(recent, alert)
		counts.Recent++
		if !alert.Active {
			continue
		}
		active = append(active, alert)
		counts.Active++
		switch alert.Severity {
		case "critical":
			counts.Critical++
		case "error":
			counts.Error++
		case "warn":
			counts.Warning++
		}
	}
	sort.Slice(active, func(i, j int) bool { return topAlertLess(active[i], active[j]) })
	sort.Slice(recent, func(i, j int) bool { return topAlertLess(recent[i], recent[j]) })
	if len(active) > limit {
		active = active[:limit]
	}
	if len(recent) > limit {
		recent = recent[:limit]
	}
	return &accessapi.TopAlerts{
		Counts: counts,
		Active: active,
		Recent: recent,
	}
}

func normalizeTopAlertSignal(signal topAlertSignal) topAlertSignal {
	signal.severity = strings.TrimSpace(signal.severity)
	signal.component = safeTopLabel(signal.component)
	signal.kind = safeTopLabel(signal.kind)
	signal.message = strings.TrimSpace(signal.message)
	signal.hint = strings.TrimSpace(signal.hint)
	if signal.severity == "" {
		signal.severity = "warn"
	}
	if signal.component == "none" {
		signal.component = "runtime"
	}
	if signal.kind == "none" {
		signal.kind = "status"
	}
	if signal.message == "" {
		signal.message = signal.component + " " + signal.kind
	}
	return signal
}

func topAlertFingerprint(signal topAlertSignal) string {
	return strings.Join([]string{signal.component, signal.kind, signal.message}, "|")
}

func topAlertSeverityFromLevel(level string) string {
	switch level {
	case "critical":
		return "critical"
	case "degraded":
		return "error"
	default:
		return "warn"
	}
}

func pressureAlertMessage(item accessapi.TopPressureItem) string {
	signal := pressureSignalLabel(item.Component, item.Pool, item.Queue, item.Priority)
	if signal == "" {
		signal = item.Component
	}
	return signal + " pressure is " + item.Level
}

func pressureAlertEvidence(item accessapi.TopPressureItem) map[string]string {
	evidence := map[string]string{
		"component":          item.Component,
		"level":              item.Level,
		"score":              topAlertEvidenceFloat(item.Score),
		"threshold.busy":     "0.60",
		"threshold.degraded": "0.80",
		"threshold.critical": "0.95",
	}
	if item.Pool != "" && item.Pool != "none" {
		evidence["pool"] = item.Pool
	}
	if item.Queue != "" && item.Queue != "none" {
		evidence["queue"] = item.Queue
	}
	if item.Priority != "" && item.Priority != "none" {
		evidence["priority"] = item.Priority
	}
	if item.Depth > 0 || item.Capacity > 0 {
		evidence["depth"] = topAlertEvidenceInt(item.Depth)
		evidence["capacity"] = topAlertEvidenceInt(item.Capacity)
	}
	if item.Inflight > 0 || item.Workers > 0 {
		evidence["inflight"] = topAlertEvidenceInt(item.Inflight)
		evidence["workers"] = topAlertEvidenceInt(item.Workers)
	}
	if item.WaitP99MS > 0 {
		evidence["wait_p99_ms"] = topAlertEvidenceFloat(item.WaitP99MS)
	}
	if item.TaskP99MS > 0 {
		evidence["task_p99_ms"] = topAlertEvidenceFloat(item.TaskP99MS)
	}
	if item.AdmissionErrorPerSec > 0 {
		evidence["admission_error_per_sec"] = topAlertEvidenceFloat(item.AdmissionErrorPerSec)
	}
	return evidence
}

func topAlertEvidenceFloat(value float64) string {
	return fmt.Sprintf("%.2f", value)
}

func topAlertEvidenceInt(value int64) string {
	return fmt.Sprintf("%d", value)
}

func pressureSignalLabel(component, pool, queue, priority string) string {
	var parts []string
	if component != "" {
		parts = append(parts, component)
	}
	var details []string
	for _, value := range []string{pool, queue} {
		if value != "" && value != "none" {
			details = append(details, value)
		}
	}
	if priority != "" && priority != "none" {
		details = append(details, priority)
	}
	if len(details) > 0 {
		parts = append(parts, strings.Join(details, "/"))
	}
	return strings.Join(parts, " ")
}

func topAlertLess(a, b accessapi.TopAlert) bool {
	if a.Active != b.Active {
		return a.Active
	}
	if topAlertSeverityRank(a.Severity) != topAlertSeverityRank(b.Severity) {
		return topAlertSeverityRank(a.Severity) > topAlertSeverityRank(b.Severity)
	}
	if !a.LastSeen.Equal(b.LastSeen) {
		return a.LastSeen.After(b.LastSeen)
	}
	if a.NodeName != b.NodeName {
		return a.NodeName < b.NodeName
	}
	if a.Component != b.Component {
		return a.Component < b.Component
	}
	return a.Message < b.Message
}

func topAlertSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 3
	case "error":
		return 2
	case "warn":
		return 1
	default:
		return 0
	}
}

func severityRank(level string) int {
	switch level {
	case "critical":
		return 3
	case "degraded":
		return 2
	case "busy":
		return 1
	default:
		return 0
	}
}

func includeTraffic(view accessapi.TopView) bool {
	return view == accessapi.TopViewOverview || view == accessapi.TopViewTraffic || view == accessapi.TopViewAll
}

func includeClients(view accessapi.TopView) bool {
	return view == accessapi.TopViewOverview || view == accessapi.TopViewRuntime || view == accessapi.TopViewAll
}

func includePressure(view accessapi.TopView) bool {
	return view == accessapi.TopViewOverview || view == accessapi.TopViewRuntime || view == accessapi.TopViewAll
}

func includeChannelV2(view accessapi.TopView) bool {
	return view == accessapi.TopViewChannel || view == accessapi.TopViewAll
}

func includeStorage(view accessapi.TopView) bool {
	return view == accessapi.TopViewStorage || view == accessapi.TopViewAll
}

func includeDelivery(view accessapi.TopView) bool {
	return view == accessapi.TopViewDelivery || view == accessapi.TopViewAll
}

func cloneTopSample(sample topSample) topSample {
	return topSample{
		at:       sample.at,
		counters: cloneUint64Map(sample.counters),
		gauges:   cloneInt64Map(sample.gauges),
		histos:   cloneHistos(sample.histos),
		cluster:  sample.cluster,
		resource: sample.resource,
	}
}

func cloneTopAlertStates(in map[string]*topAlertState) map[string]*topAlertState {
	out := make(map[string]*topAlertState, len(in))
	for fingerprint, state := range in {
		if state == nil {
			continue
		}
		out[fingerprint] = &topAlertState{alert: cloneTopAlert(state.alert)}
	}
	return out
}

func cloneTopAlert(in accessapi.TopAlert) accessapi.TopAlert {
	out := in
	if in.ResolvedAt != nil {
		resolvedAt := *in.ResolvedAt
		out.ResolvedAt = &resolvedAt
	}
	out.Evidence = cloneStringMap(in.Evidence)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneUint64Map(in map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneInt64Map(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneHistos(in map[string][]float64) map[string][]float64 {
	out := make(map[string][]float64, len(in))
	for k, v := range in {
		out[k] = append([]float64(nil), v...)
	}
	return out
}
