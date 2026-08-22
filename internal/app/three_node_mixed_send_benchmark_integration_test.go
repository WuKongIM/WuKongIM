//go:build integration

package app

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	dto "github.com/prometheus/client_model/go"
)

const (
	threeNodeMixedSendRate          = 1000
	threeNodeMixedHostedSendRate    = 500
	threeNodeMixedSendWorkers       = 256
	threeNodeMixedSendUsers         = 2500
	threeNodeMixedPersonChannels    = 2000
	threeNodeMixedHotPersonChannels = 100
	threeNodeMixedGroupChannels     = 500
	threeNodeMixedGroupMembers      = 10
	threeNodeMixedPersonPercent     = 90
	threeNodeMixedMinimumIterations = 3000
)

// BenchmarkThreeNodeMixedSendPath1000QPS is the short feedback loop for the
// steady-state reviewed ingress shape after a bounded channel warmup. The
// separate cold-wave benchmark owns first-message and projector pressure.
func BenchmarkThreeNodeMixedSendPath1000QPS(b *testing.B) {
	benchmarkThreeNodeMixedSendPathAtRate(b, threeNodeMixedHotPersonChannels, true, threeNodeMixedSendRate)
}

// BenchmarkThreeNodeMixedSendPath500QPS is the hosted-runner regression seam.
// The 1000 QPS variant remains the dedicated capacity-environment benchmark.
func BenchmarkThreeNodeMixedSendPath500QPS(b *testing.B) {
	benchmarkThreeNodeMixedSendPathAtRate(b, threeNodeMixedHotPersonChannels, true, threeNodeMixedHostedSendRate)
}

// BenchmarkThreeNodeMixedSendColdDirectoryWave1000QPS deliberately creates
// all 2,000 person channels in the short run. It is a projector catch-up and
// foreground-interference stress benchmark, not the formal p99 distribution.
func BenchmarkThreeNodeMixedSendColdDirectoryWave1000QPS(b *testing.B) {
	benchmarkThreeNodeMixedSendPathAtRate(b, threeNodeMixedPersonChannels, false, threeNodeMixedSendRate)
}

func benchmarkThreeNodeMixedSendPathAtRate(b *testing.B, personChannels int, prewarm bool, rate int) {
	if b.N < threeNodeMixedMinimumIterations {
		return
	}
	if rate <= 0 {
		b.Fatal("mixed SEND benchmark rate must be positive")
	}
	apps, nodes := newThreeNodeMixedSendApps(b)
	seedThreeNodeMixedGroups(b, nodes[0])

	sink := &threeNodeMixedAckSink{}
	sessions := newThreeNodeMixedSessions(apps, sink)
	if prewarm {
		warmThreeNodeMixedSendChannels(b, apps, nodes, sessions, sink, personChannels)
	}
	latencies := make([]time.Duration, b.N)
	cold := make([]bool, b.N)
	jobs := make(chan int, threeNodeMixedSendWorkers)
	var workers sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}
	workers.Add(threeNodeMixedSendWorkers)
	for range threeNodeMixedSendWorkers {
		go func() {
			defer workers.Done()
			for index := range jobs {
				input := threeNodeMixedSendInputAt(index, personChannels, !prewarm)
				appIndex := index % len(apps)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				started := time.Now()
				err := apps[appIndex].Handler().OnFrame(coregateway.Context{
					Session: sessions[appIndex][input.senderIndex], RequestContext: ctx,
				}, &frame.SendPacket{
					ClientSeq: uint64(1_000_000 + index + 1), ClientMsgNo: fmt.Sprintf("mixed-%d", index+1),
					ChannelID: input.channelID, ChannelType: input.channelType,
					Payload: threeNodeMixedPayload(index),
				})
				latencies[index] = time.Since(started)
				cold[index] = input.cold
				cancel()
				if err != nil {
					recordErr(fmt.Errorf("send index=%d: %w", index, err))
				}
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for index := 0; index < b.N; index++ {
		waitThreeNodeMixedSendUntil(started.Add(time.Duration(index) * time.Second / time.Duration(rate)))
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()

	if firstErr != nil {
		b.Fatal(firstErr)
	}
	if got := sink.failures.Load(); got != 0 {
		b.Fatalf("non-success SENDACKs = %d", got)
	}
	if got := sink.successes.Load(); got != uint64(b.N) {
		b.Fatalf("success SENDACKs = %d, want %d", got, b.N)
	}
	projectionDrain := waitThreeNodeMixedPersonDirectoryDrain(b, nodes, 10*time.Second)
	b.ReportMetric(float64(projectionDrain)/float64(time.Millisecond), "person-directory-drain-ms")
	reportThreeNodeMixedSendLatencies(b, latencies, cold)
	reportThreeNodeMixedSendStages(b, apps)
	reportThreeNodeMixedChannelStages(b, apps)
}

func waitThreeNodeMixedPersonDirectoryDrain(b *testing.B, nodes []*clusterpkg.Node, timeout time.Duration) time.Duration {
	b.Helper()
	started := time.Now()
	deadline := started.Add(timeout)
	lastPending := -1
	var lastErr error
	for time.Now().Before(deadline) {
		pending, err := countThreeNodeMixedPersonDirectoryTasks(nodes)
		lastPending, lastErr = pending, err
		if err == nil && pending == 0 {
			return time.Since(started)
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.Fatalf("person-directory tasks did not drain: pending=%d err=%v", lastPending, lastErr)
	return 0
}

func countThreeNodeMixedPersonDirectoryTasks(nodes []*clusterpkg.Node) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	total := 0
	for _, node := range nodes {
		hashSlots, err := node.LocalLeaderHashSlots(ctx)
		if err != nil {
			return 0, err
		}
		for _, hashSlot := range hashSlots {
			cursor := metadb.PersonDirectoryTaskCursor{}
			for {
				rows, next, done, err := node.ListPersonDirectoryTaskPage(ctx, hashSlot, cursor, 64)
				if err != nil {
					return 0, err
				}
				total += len(rows)
				if done {
					break
				}
				if next == cursor {
					return 0, fmt.Errorf("person-directory scan did not advance for hash slot %d", hashSlot)
				}
				cursor = next
			}
		}
	}
	return total, nil
}

type threeNodeMixedSendInput struct {
	senderIndex int
	channelID   string
	channelType uint8
	cold        bool
}

func threeNodeMixedSendInputAt(index, personChannels int, markFirstCold bool) threeNodeMixedSendInput {
	cycle := index % 100
	if cycle >= threeNodeMixedPersonPercent {
		groupOrdinal := index / 10
		channelIndex := groupOrdinal % threeNodeMixedGroupChannels
		memberIndex := groupOrdinal % threeNodeMixedGroupMembers
		return threeNodeMixedSendInput{
			senderIndex: (channelIndex*threeNodeMixedGroupMembers + memberIndex) % threeNodeMixedSendUsers,
			channelID:   fmt.Sprintf("mixed-group-%04d", channelIndex), channelType: frame.ChannelTypeGroup,
		}
	}
	personOrdinal := index - index/10
	channelIndex := personOrdinal % personChannels
	senderIndex := channelIndex % threeNodeMixedSendUsers
	receiverIndex := (channelIndex*37 + 1) % threeNodeMixedSendUsers
	if receiverIndex == senderIndex {
		receiverIndex = (receiverIndex + 1) % threeNodeMixedSendUsers
	}
	return threeNodeMixedSendInput{
		senderIndex: senderIndex, channelID: fmt.Sprintf("mixed-user-%04d", receiverIndex),
		channelType: frame.ChannelTypePerson, cold: markFirstCold && personOrdinal < personChannels,
	}
}

func warmThreeNodeMixedSendChannels(b *testing.B, apps []*App, nodes []*clusterpkg.Node, sessions [][]session.Session, sink *threeNodeMixedAckSink, personChannels int) {
	b.Helper()
	total := personChannels + threeNodeMixedGroupChannels
	jobs := make(chan int, threeNodeMixedSendWorkers)
	var workers sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	workers.Add(threeNodeMixedSendWorkers)
	for range threeNodeMixedSendWorkers {
		go func() {
			defer workers.Done()
			for ordinal := range jobs {
				input := threeNodeMixedWarmInput(ordinal, personChannels)
				appIndex := ordinal % len(apps)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := apps[appIndex].Handler().OnFrame(coregateway.Context{
					Session: sessions[appIndex][input.senderIndex], RequestContext: ctx,
				}, &frame.SendPacket{
					ClientSeq: uint64(ordinal + 1), ClientMsgNo: fmt.Sprintf("mixed-warm-%d", ordinal+1),
					ChannelID: input.channelID, ChannelType: input.channelType, Payload: threeNodeMixedPayload(ordinal),
				})
				cancel()
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("warm channel ordinal=%d: %w", ordinal, err)
					}
					errMu.Unlock()
				}
			}
		}()
	}
	for ordinal := 0; ordinal < total; ordinal++ {
		jobs <- ordinal
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		b.Fatal(firstErr)
	}
	if got := sink.failures.Load(); got != 0 {
		b.Fatalf("warmup non-success SENDACKs = %d", got)
	}
	if got := sink.successes.Load(); got != uint64(total) {
		b.Fatalf("warmup success SENDACKs = %d, want %d", got, total)
	}
	waitThreeNodeMixedPersonDirectoryDrain(b, nodes, 10*time.Second)
	sink.successes.Store(0)
	sink.failures.Store(0)
}

func threeNodeMixedWarmInput(ordinal, personChannels int) threeNodeMixedSendInput {
	if ordinal >= personChannels {
		channelIndex := ordinal - personChannels
		return threeNodeMixedSendInput{
			senderIndex: (channelIndex * threeNodeMixedGroupMembers) % threeNodeMixedSendUsers,
			channelID:   fmt.Sprintf("mixed-group-%04d", channelIndex), channelType: frame.ChannelTypeGroup,
		}
	}
	senderIndex := ordinal % threeNodeMixedSendUsers
	receiverIndex := (ordinal*37 + 1) % threeNodeMixedSendUsers
	if receiverIndex == senderIndex {
		receiverIndex = (receiverIndex + 1) % threeNodeMixedSendUsers
	}
	return threeNodeMixedSendInput{
		senderIndex: senderIndex, channelID: fmt.Sprintf("mixed-user-%04d", receiverIndex), channelType: frame.ChannelTypePerson,
	}
}

func newThreeNodeMixedSendApps(b *testing.B) ([]*App, []*clusterpkg.Node) {
	b.Helper()
	addrs := []string{threeNodeMixedFreeAddr(b), threeNodeMixedFreeAddr(b), threeNodeMixedFreeAddr(b)}
	voters := make([]clusterpkg.ControlVoter, len(addrs))
	for index, addr := range addrs {
		voters[index] = clusterpkg.ControlVoter{NodeID: uint64(index + 1), Addr: addr}
	}
	apps := make([]*App, 0, len(voters))
	for index, voter := range voters {
		plugin := PluginConfig{}
		plugin.SetEnableExplicit(true)
		plugin.SetExplicitFlags(true)
		dataDir := b.TempDir()
		cfg := Config{
			NodeID: voter.NodeID, DataDir: dataDir, Plugin: plugin,
			Observability: ObservabilityConfig{MetricsEnabled: true},
			Cluster: clusterpkg.Config{
				NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: dataDir,
				Control: clusterpkg.ControlConfig{ClusterID: "three-node-mixed-send", Voters: voters, AllowBootstrap: true},
				Slots:   clusterpkg.SlotConfig{InitialSlotCount: 12, HashSlotCount: 256, ReplicaCount: 3},
				Channel: clusterpkg.ChannelConfig{
					ReplicaCount: 3, TickInterval: time.Millisecond, StoreAppendWorkers: 500,
					RPCWorkers: 160, MaxChannels: 50000,
				},
				Storage:  clusterpkg.StorageConfig{CommitFlushWindow: time.Millisecond, CommitShards: 1},
				Timeouts: clusterpkg.TimeoutConfig{Start: 15 * time.Second, Stop: 10 * time.Second},
			},
		}
		app, err := New(cfg, WithLogger(wklog.NewNop()), WithGateway(nil))
		if err != nil {
			b.Fatalf("New(node=%d,index=%d): %v", voter.NodeID, index, err)
		}
		apps = append(apps, app)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer startCancel()
	startErrs := make(chan error, len(apps))
	for _, app := range apps {
		app := app
		go func() { startErrs <- app.Start(startCtx) }()
	}
	for range apps {
		if err := <-startErrs; err != nil {
			stopThreeNodeMixedSendApps(apps)
			b.Fatalf("Start(): %v", err)
		}
	}
	b.Cleanup(func() { stopThreeNodeMixedSendApps(apps) })

	nodes := make([]*clusterpkg.Node, 0, len(apps))
	for _, app := range apps {
		node, ok := app.cluster.(*clusterpkg.Node)
		if !ok {
			b.Fatalf("cluster runtime = %T, want *cluster.Node", app.cluster)
		}
		nodes = append(nodes, node)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		snapshots := make([]clusterpkg.Snapshot, len(nodes))
		for index, node := range nodes {
			snapshots[index] = node.Snapshot()
		}
		if appClusterSnapshotsConverged(snapshots) {
			return apps, nodes
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.Fatalf("three-node cluster snapshots did not converge")
	return nil, nil
}

type threeNodeMixedHistogram struct {
	count   uint64
	sum     float64
	buckets map[float64]uint64
}

func reportThreeNodeMixedSendStages(b *testing.B, apps []*App) {
	b.Helper()
	const familyName = "wukongim_message_send_batch_stage_item_duration_seconds"
	byStage := make(map[string]*threeNodeMixedHistogram)
	for _, app := range apps {
		families, err := app.metrics.PrometheusRegistry().Gather()
		if err != nil {
			b.Fatalf("gather benchmark metrics: %v", err)
		}
		for _, family := range families {
			if family.GetName() != familyName {
				continue
			}
			for _, metric := range family.Metric {
				stage := threeNodeMixedMetricLabel(metric, "stage")
				if stage == "" || metric.Histogram == nil {
					continue
				}
				aggregate := byStage[stage]
				if aggregate == nil {
					aggregate = &threeNodeMixedHistogram{buckets: make(map[float64]uint64)}
					byStage[stage] = aggregate
				}
				aggregate.count += metric.Histogram.GetSampleCount()
				aggregate.sum += metric.Histogram.GetSampleSum()
				for _, bucket := range metric.Histogram.Bucket {
					aggregate.buckets[bucket.GetUpperBound()] += bucket.GetCumulativeCount()
				}
			}
		}
	}
	for stage, histogram := range byStage {
		if histogram.count == 0 {
			continue
		}
		bounds := make([]float64, 0, len(histogram.buckets))
		for bound := range histogram.buckets {
			bounds = append(bounds, bound)
		}
		sort.Float64s(bounds)
		threshold := (histogram.count*99 + 99) / 100
		p99 := bounds[len(bounds)-1]
		for _, bound := range bounds {
			if histogram.buckets[bound] >= threshold {
				p99 = bound
				break
			}
		}
		b.ReportMetric(histogram.sum*1000/float64(histogram.count), "stage-"+stage+"-avg-ms")
		b.ReportMetric(p99*1000, "stage-"+stage+"-p99-upper-ms")
	}
}

func reportThreeNodeMixedChannelStages(b *testing.B, apps []*App) {
	b.Helper()
	const familyName = "wukongim_channelv2_append_stage_duration_seconds"
	byStage := make(map[string]*threeNodeMixedHistogram)
	for _, app := range apps {
		families, err := app.metrics.PrometheusRegistry().Gather()
		if err != nil {
			b.Fatalf("gather benchmark channel metrics: %v", err)
		}
		for _, family := range families {
			if family.GetName() != familyName {
				continue
			}
			for _, metric := range family.Metric {
				if threeNodeMixedMetricLabel(metric, "result") != "ok" || metric.Histogram == nil {
					continue
				}
				stage := threeNodeMixedMetricLabel(metric, "stage")
				if stage == "" {
					continue
				}
				aggregate := byStage[stage]
				if aggregate == nil {
					aggregate = &threeNodeMixedHistogram{buckets: make(map[float64]uint64)}
					byStage[stage] = aggregate
				}
				aggregate.count += metric.Histogram.GetSampleCount()
				aggregate.sum += metric.Histogram.GetSampleSum()
				for _, bucket := range metric.Histogram.Bucket {
					aggregate.buckets[bucket.GetUpperBound()] += bucket.GetCumulativeCount()
				}
			}
		}
	}
	for stage, histogram := range byStage {
		if histogram.count == 0 {
			continue
		}
		bounds := make([]float64, 0, len(histogram.buckets))
		for bound := range histogram.buckets {
			bounds = append(bounds, bound)
		}
		sort.Float64s(bounds)
		threshold := (histogram.count*99 + 99) / 100
		p99 := bounds[len(bounds)-1]
		for _, bound := range bounds {
			if histogram.buckets[bound] >= threshold {
				p99 = bound
				break
			}
		}
		b.ReportMetric(histogram.sum*1000/float64(histogram.count), "channel-"+stage+"-avg-ms")
		b.ReportMetric(p99*1000, "channel-"+stage+"-p99-upper-ms")
	}
}

func threeNodeMixedMetricLabel(metric *dto.Metric, name string) string {
	for _, label := range metric.Label {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

func stopThreeNodeMixedSendApps(apps []*App) {
	stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, app := range apps {
		if app == nil {
			continue
		}
		wg.Add(1)
		go func(app *App) {
			defer wg.Done()
			_ = app.Stop(stopCtx)
		}(app)
	}
	wg.Wait()
}

func seedThreeNodeMixedGroups(b *testing.B, node *clusterpkg.Node) {
	b.Helper()
	jobs := make(chan int, 32)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	wg.Add(32)
	for range 32 {
		go func() {
			defer wg.Done()
			for channelIndex := range jobs {
				channelID := fmt.Sprintf("mixed-group-%04d", channelIndex)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := node.UpsertChannelMetadata(ctx, metadb.Channel{ChannelID: channelID, ChannelType: int64(frame.ChannelTypeGroup)})
				if err == nil {
					members := make([]string, threeNodeMixedGroupMembers)
					for memberIndex := range members {
						members[memberIndex] = fmt.Sprintf("mixed-user-%04d", (channelIndex*threeNodeMixedGroupMembers+memberIndex)%threeNodeMixedSendUsers)
					}
					err = node.AddChannelSubscribers(ctx, channelID, int64(frame.ChannelTypeGroup), members, 1)
				}
				cancel()
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("seed group %d: %w", channelIndex, err)
					}
					errMu.Unlock()
				}
			}
		}()
	}
	for channelIndex := 0; channelIndex < threeNodeMixedGroupChannels; channelIndex++ {
		jobs <- channelIndex
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		b.Fatal(firstErr)
	}
}

type threeNodeMixedAckSink struct {
	successes atomic.Uint64
	failures  atomic.Uint64
}

func newThreeNodeMixedSessions(apps []*App, sink *threeNodeMixedAckSink) [][]session.Session {
	sessions := make([][]session.Session, len(apps))
	for appIndex := range apps {
		sessions[appIndex] = make([]session.Session, threeNodeMixedSendUsers)
		for userIndex := 0; userIndex < threeNodeMixedSendUsers; userIndex++ {
			sess := session.New(session.Config{
				ID: uint64((appIndex+1)*10000 + userIndex + 1),
				WriteFrameFn: func(value frame.Frame, _ session.OutboundMeta) error {
					ack, ok := value.(*frame.SendackPacket)
					if !ok || ack.ReasonCode != frame.ReasonSuccess {
						sink.failures.Add(1)
						return nil
					}
					sink.successes.Add(1)
					return nil
				},
			})
			sess.SetValue(coregateway.SessionValueUID, fmt.Sprintf("mixed-user-%04d", userIndex))
			sess.SetValue(coregateway.SessionValueProtocolVersion, uint8(frame.LatestVersion))
			sessions[appIndex][userIndex] = sess
		}
	}
	return sessions
}

func threeNodeMixedPayload(index int) []byte {
	size := 256
	switch index % 100 {
	case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94:
		size = 1024
	case 95, 96, 97, 98:
		size = 4096
	case 99:
		size = 16384
	}
	return make([]byte, size)
}

func reportThreeNodeMixedSendLatencies(b *testing.B, latencies []time.Duration, cold []bool) {
	b.Helper()
	hotValues := make([]time.Duration, 0, len(latencies))
	coldValues := make([]time.Duration, 0, threeNodeMixedPersonChannels)
	for index, latency := range latencies {
		if cold[index] {
			coldValues = append(coldValues, latency)
		} else {
			hotValues = append(hotValues, latency)
		}
	}
	reportThreeNodeMixedLatencyClass(b, "all", latencies)
	reportThreeNodeMixedLatencyClass(b, "hot", hotValues)
	reportThreeNodeMixedLatencyClass(b, "cold", coldValues)
}

func reportThreeNodeMixedLatencyClass(b *testing.B, name string, values []time.Duration) {
	b.Helper()
	if len(values) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	over200 := 0
	for _, value := range values {
		if value > 200*time.Millisecond {
			over200++
		}
	}
	b.ReportMetric(float64(sorted[(len(sorted)*99-1)/100])/float64(time.Millisecond), name+"-p99-ms")
	b.ReportMetric(100*float64(over200)/float64(len(values)), name+"-over-200ms-pct")
}

func waitThreeNodeMixedSendUntil(deadline time.Time) {
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		if remaining > 200*time.Microsecond {
			time.Sleep(remaining - 100*time.Microsecond)
			continue
		}
	}
}

func threeNodeMixedFreeAddr(b *testing.B) string {
	b.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		b.Fatal(err)
	}
	return addr
}
