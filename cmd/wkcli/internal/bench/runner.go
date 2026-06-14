package bench

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type sendPool interface {
	Connect(context.Context, []wkclient.Identity) error
	SendBatch(context.Context, []wkclient.RoutedMessage) ([]wkclient.SendResult, error)
	Close() error
}

type sendPoolConfig struct {
	Addrs       []string
	Token       string
	AckTimeout  time.Duration
	ConnectRate int
}

var newSendPool = newClientSendPool

func runSend(ctx context.Context, cfg sendConfig) (sendResult, error) {
	normalized, err := normalizeSendConfig(cfg)
	if err != nil {
		return sendResult{}, err
	}
	target, err := resolveTarget(ctx, normalized)
	if err != nil {
		return sendResult{}, err
	}
	planner, err := newChannelPlanner(normalized)
	if err != nil {
		return sendResult{}, err
	}
	pool, err := newSendPool(sendPoolConfig{
		Addrs:       target.GatewayAddrs,
		Token:       normalized.Token,
		AckTimeout:  normalized.AckTimeout,
		ConnectRate: normalized.ConnectRate,
	})
	if err != nil {
		return sendResult{}, err
	}
	defer pool.Close()

	identities := buildIdentities(normalized)
	if err := pool.Connect(ctx, identities); err != nil {
		return sendResult{}, err
	}

	payload := buildPayload(normalized)
	stats := newSendStats(normalized, len(target.GatewayAddrs), len(planner.channels))
	start := time.Now()
	stopProgress := startProgress(normalized, stats, start)
	defer stopProgress()
	var next atomic.Int64
	var wg sync.WaitGroup
	for _, identity := range identities {
		identity := identity
		wg.Add(1)
		go func() {
			defer wg.Done()
			runSendWorker(ctx, pool, normalized, planner, payload, identity, &next, stats, start)
		}()
	}
	wg.Wait()
	return stats.finish(time.Since(start)), nil
}

func newClientSendPool(cfg sendPoolConfig) (sendPool, error) {
	return wkclient.NewPool(wkclient.PoolConfig{
		Addrs: cfg.Addrs,
		Client: wkclient.Config{
			Token:       cfg.Token,
			AckTimeout:  cfg.AckTimeout,
			AutoRecvAck: true,
		},
		ConnectRatePerSecond: cfg.ConnectRate,
	})
}

func runSendWorker(ctx context.Context, pool sendPool, cfg sendConfig, planner *channelPlanner, payload []byte, identity wkclient.Identity, next *atomic.Int64, stats *sendStats, start time.Time) {
	for {
		batch := make([]wkclient.RoutedMessage, 0, cfg.BatchSize)
		offsets := make([]int, 0, cfg.BatchSize)
		for len(batch) < cfg.BatchSize {
			offset := int(next.Add(1) - 1)
			if offset >= cfg.Messages {
				break
			}
			throttle(ctx, cfg, offset, start)
			channel := planner.Pick(offset)
			msg := wkclient.RoutedMessage{
				UID: identity.UID,
				Message: wkclient.Message{
					ClientSeq:   uint64(offset + 1),
					ClientMsgNo: formatClientMsgNo(cfg, offset+1),
					ChannelID:   channel.ID,
					ChannelType: channel.Type,
					Payload:     payload,
				},
			}
			batch = append(batch, msg)
			offsets = append(offsets, offset)
			stats.recordScheduled(channel.ID)
		}
		if len(batch) == 0 {
			return
		}
		sendStart := time.Now()
		results, err := pool.SendBatch(ctx, batch)
		elapsed := time.Since(sendStart)
		if err != nil {
			stats.recordSendError(len(batch), classifyError(err))
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if len(results) != len(batch) {
			stats.recordSendError(len(batch), "result_mismatch")
			continue
		}
		for i, result := range results {
			_ = offsets[i]
			stats.recordSendack(result.ReasonCode, elapsed)
		}
		if cfg.Sleep > 0 {
			select {
			case <-time.After(cfg.Sleep):
			case <-ctx.Done():
				return
			}
		}
	}
}

// formatClientMsgNo keeps message numbers sortable while avoiding collisions across runs.
func formatClientMsgNo(cfg sendConfig, seq int) string {
	return fmt.Sprintf("%s-%s-%012d", cfg.ClientMsgNoPrefix, cfg.RunID, seq)
}

func throttle(ctx context.Context, cfg sendConfig, offset int, start time.Time) {
	if cfg.Throughput <= 0 {
		return
	}
	expected := time.Duration(float64(offset+1) / float64(cfg.Throughput) * float64(time.Second))
	wait := start.Add(expected).Sub(time.Now())
	if wait <= 0 {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

func buildIdentities(cfg sendConfig) []wkclient.Identity {
	identities := make([]wkclient.Identity, cfg.Clients)
	for i := range identities {
		identities[i] = wkclient.Identity{
			UID:      fmt.Sprintf("%s-%06d", cfg.UIDPrefix, i+1),
			DeviceID: fmt.Sprintf("%s-%06d", cfg.DevicePrefix, i+1),
			Token:    cfg.Token,
		}
	}
	return identities
}

func buildPayload(cfg sendConfig) []byte {
	if cfg.Payload != "" {
		return []byte(cfg.Payload)
	}
	payload := make([]byte, cfg.PayloadBytes)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	return payload
}

func startProgress(cfg sendConfig, stats *sendStats, start time.Time) func() {
	if cfg.ProgressWriter == nil {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		lastLen := 0
		writeLine := func() {
			line := formatProgressLine(cfg, stats.snapshot(time.Since(start)))
			padding := ""
			if len(line) < lastLen {
				padding = strings.Repeat(" ", lastLen-len(line))
			}
			fmt.Fprintf(cfg.ProgressWriter, "\r%s%s", line, padding)
			lastLen = len(line)
		}
		writeLine()
		for {
			select {
			case <-ticker.C:
				writeLine()
			case <-done:
				writeLine()
				fmt.Fprintln(cfg.ProgressWriter)
				return
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

type sendStats struct {
	cfg         sendConfig
	gateways    int
	channels    int
	mu          sync.Mutex
	success     uint64
	errors      uint64
	reasons     map[string]uint64
	errorCounts map[string]uint64
	latencies   []time.Duration
	byChannel   map[string]int
}

func newSendStats(cfg sendConfig, gateways, channels int) *sendStats {
	return &sendStats{
		cfg:         cfg,
		gateways:    gateways,
		channels:    channels,
		reasons:     make(map[string]uint64),
		errorCounts: make(map[string]uint64),
		latencies:   make([]time.Duration, 0, cfg.Messages),
		byChannel:   make(map[string]int, channels),
	}
}

func (s *sendStats) recordScheduled(channelID string) {
	s.mu.Lock()
	s.byChannel[channelID]++
	s.mu.Unlock()
}

func (s *sendStats) recordSendError(count int, key string) {
	s.mu.Lock()
	s.errors += uint64(count)
	s.errorCounts[key] += uint64(count)
	s.mu.Unlock()
}

func (s *sendStats) recordSendack(reason frame.ReasonCode, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reasons[reasonKey(reason)]++
	s.latencies = append(s.latencies, latency)
	if reason == frame.ReasonSuccess {
		s.success++
		return
	}
	s.errors++
}

func (s *sendStats) finish(duration time.Duration) sendResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := resultPass
	if s.errors > 0 {
		result = resultFail
	}
	minPerChannel, avgPerChannel, maxPerChannel := channelDistribution(s.byChannel)
	latency := summarizeLatencies(s.latencies)
	seconds := duration.Seconds()
	var throughput, bandwidth float64
	if seconds > 0 {
		throughput = float64(s.success) / seconds
		bandwidth = float64(s.success) * float64(s.cfg.PayloadBytes) / seconds
	}
	return sendResult{
		Result:                result,
		Duration:              duration,
		Success:               s.success,
		Errors:                s.errors,
		Throughput:            throughput,
		BandwidthBytes:        bandwidth,
		Clients:               s.cfg.Clients,
		Gateways:              s.gateways,
		Channels:              s.channels,
		ChannelPick:           s.cfg.ChannelPick,
		Messages:              s.cfg.Messages,
		PayloadBytes:          s.cfg.PayloadBytes,
		BatchSize:             s.cfg.BatchSize,
		MinMessagesPerChannel: minPerChannel,
		AvgMessagesPerChannel: avgPerChannel,
		MaxMessagesPerChannel: maxPerChannel,
		Latency:               latency,
		SendackReasons:        copyCounts(s.reasons),
		ErrorCounts:           copyCounts(s.errorCounts),
	}
}

type progressSnapshot struct {
	Done           uint64
	Errors         uint64
	Percent        float64
	Throughput     float64
	BandwidthBytes float64
	P99            time.Duration
}

func (s *sendStats) snapshot(duration time.Duration) progressSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	done := s.success + s.errors
	percent := 0.0
	if s.cfg.Messages > 0 {
		percent = float64(done) / float64(s.cfg.Messages) * 100
	}
	throughput := 0.0
	bandwidth := 0.0
	if duration > 0 {
		throughput = float64(s.success) / duration.Seconds()
		bandwidth = float64(s.success) * float64(s.cfg.PayloadBytes) / duration.Seconds()
	}
	return progressSnapshot{
		Done:           done,
		Errors:         s.errors,
		Percent:        percent,
		Throughput:     throughput,
		BandwidthBytes: bandwidth,
		P99:            summarizeLatencies(s.latencies).P99,
	}
}

func channelDistribution(counts map[string]int) (int, float64, int) {
	if len(counts) == 0 {
		return 0, 0, 0
	}
	min := 0
	max := 0
	total := 0
	for _, count := range counts {
		if min == 0 || count < min {
			min = count
		}
		if count > max {
			max = count
		}
		total += count
	}
	return min, float64(total) / float64(len(counts)), max
}

func summarizeLatencies(latencies []time.Duration) latencySummary {
	if len(latencies) == 0 {
		return latencySummary{}
	}
	sorted := append([]time.Duration(nil), latencies...)
	sortDurations(sorted)
	var total time.Duration
	for _, latency := range sorted {
		total += latency
	}
	return latencySummary{
		Average: total / time.Duration(len(sorted)),
		P50:     percentileDuration(sorted, 0.50),
		P95:     percentileDuration(sorted, 0.95),
		P99:     percentileDuration(sorted, 0.99),
		Max:     sorted[len(sorted)-1],
	}
}

func sortDurations(items []time.Duration) {
	sort.Slice(items, func(i, j int) bool { return items[i] < items[j] })
}

func percentileDuration(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * p)
	return sorted[index]
}

func copyCounts(in map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func reasonKey(reason frame.ReasonCode) string {
	if reason == frame.ReasonSuccess {
		return "success"
	}
	name := reason.String()
	name = strings.TrimPrefix(name, "Reason")
	return camelToSnake(name)
}

func camelToSnake(value string) string {
	var b strings.Builder
	for i, r := range value {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func classifyError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}
