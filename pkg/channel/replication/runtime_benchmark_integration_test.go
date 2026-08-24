//go:build integration

package replication_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/service"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	channelworker "github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	gonruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

const (
	threeNodeBenchmarkChannels   = 1000
	threeNodeBenchmarkRate       = 1000
	threeNodeHostedBenchmarkRate = 500
	threeNodeBenchmarkWorkers    = 256
)

// BenchmarkThreeNodeDurableQuorumCommit1000QPS measures the public durable-log
// seam against three physical MessageDB engines. Run it with a fixed benchtime,
// for example -benchtime=5000x, so the latency distribution covers five seconds.
func BenchmarkThreeNodeDurableQuorumCommit1000QPS(b *testing.B) {
	benchmarkThreeNodeDurableQuorumCommit1000QPS(b, 1)
}

// BenchmarkThreeNodeDurableQuorumCommitShardMatrix1000QPS compares bounded
// per-channel commit lanes at the exact durable quorum seam.
func BenchmarkThreeNodeDurableQuorumCommitShardMatrix1000QPS(b *testing.B) {
	for _, shards := range []int{1, 2, 4} {
		b.Run(fmt.Sprintf("shards_%d", shards), func(b *testing.B) {
			benchmarkThreeNodeDurableQuorumCommit1000QPS(b, shards)
		})
	}
}

// BenchmarkThreeNodeDurableQuorumHedgeMatrix1000QPS measures the foreground
// quorum tail when one voter intermittently pauses. The pause is injected at
// the peer-link boundary while all three voters still use physical MessageDB
// engines, so the matrix also exposes the extra storage work caused by an
// overly eager hedge.
func BenchmarkThreeNodeDurableQuorumHedgeMatrix1000QPS(b *testing.B) {
	for _, delay := range []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond} {
		b.Run(delay.String(), func(b *testing.B) {
			cluster := newDurableQuorumBenchmarkClusterWithOptions(b, threeNodeBenchmarkChannels, durableQuorumBenchmarkOptions{
				commitShards: 1, replicaHedgeDelay: delay,
				delayedTarget: 2, delayedEvery: 10, delayedBy: 300 * time.Millisecond,
			})
			benchmarkDurableQuorumCluster1000QPS(b, cluster, "hedge")
		})
	}
}

// BenchmarkThreeNodeDurableQuorumNaturalHedgeMatrix1000QPS checks that a
// shorter hedge does not amplify physical commits when all voters are healthy.
func BenchmarkThreeNodeDurableQuorumNaturalHedgeMatrix1000QPS(b *testing.B) {
	for _, delay := range []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond} {
		b.Run(delay.String(), func(b *testing.B) {
			cluster := newDurableQuorumBenchmarkClusterWithOptions(b, threeNodeBenchmarkChannels, durableQuorumBenchmarkOptions{
				commitShards: 1, replicaHedgeDelay: delay,
			})
			benchmarkDurableQuorumCluster1000QPS(b, cluster, "natural")
		})
	}
}

func benchmarkThreeNodeDurableQuorumCommit1000QPS(b *testing.B, commitShards int) {
	cluster := newDurableQuorumBenchmarkClusterWithShards(b, threeNodeBenchmarkChannels, commitShards)
	benchmarkDurableQuorumCluster1000QPS(b, cluster, "durable")
}

func benchmarkDurableQuorumCluster1000QPS(b *testing.B, cluster *durableQuorumBenchmarkCluster, metricPrefix string) {
	defer cluster.close(b)

	latencies := make([]time.Duration, b.N)
	jobs := make(chan int, threeNodeBenchmarkWorkers)
	var workers sync.WaitGroup
	workers.Add(threeNodeBenchmarkWorkers)
	for range threeNodeBenchmarkWorkers {
		go func() {
			defer workers.Done()
			for index := range jobs {
				channelIndex := index % len(cluster.channels)
				channel := cluster.channels[channelIndex]
				sequence := uint64(index/len(cluster.channels) + 1)
				proposal := durableQuorumBenchmarkProposal(channel, index, sequence)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				started := time.Now()
				_, err := cluster.runtimes[channel.authority.Leader].Log().Commit(ctx, proposal)
				latencies[index] = time.Since(started)
				cancel()
				if err != nil {
					cluster.recordError(fmt.Errorf("commit channel %d sequence %d: %w", channelIndex, sequence, err))
				}
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for index := 0; index < b.N; index++ {
		waitUntil(started.Add(time.Duration(index) * time.Second / threeNodeBenchmarkRate))
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()

	if err := cluster.firstError(); err != nil {
		b.Fatal(err)
	}
	reportDurableQuorumBenchmark(b, metricPrefix, latencies, cluster.commitObserver.snapshot())
}

// BenchmarkThreeNodeChannelAppend1000QPS adds the public Channel service,
// reactors, mailboxes, and worker pools around the same physical quorum seam.
func BenchmarkThreeNodeChannelAppend1000QPS(b *testing.B) {
	cluster := newChannelAppendBenchmarkCluster(b, threeNodeBenchmarkChannels)
	benchmarkThreeNodeChannelAppendCluster1000QPS(b, cluster, "append")
}

// BenchmarkThreeNodeChannelAppend500QPS is the hosted-runner regression seam.
// The 1000 QPS variant remains the dedicated capacity-environment benchmark.
func BenchmarkThreeNodeChannelAppend500QPS(b *testing.B) {
	cluster := newChannelAppendBenchmarkCluster(b, threeNodeBenchmarkChannels)
	benchmarkThreeNodeChannelAppendClusterAtRate(b, cluster, "append", threeNodeHostedBenchmarkRate)
}

// BenchmarkThreeNodeChannelAppendPayloadMixWithGatewayDeliveryPressure1000QPS
// uses the formal chat-lifecycle payload distribution together with 2,500
// sessions and synchronous receive acknowledgements.
func BenchmarkThreeNodeChannelAppendPayloadMixWithGatewayDeliveryPressure1000QPS(b *testing.B) {
	cluster := newChannelAppendBenchmarkClusterWithOptions(b, threeNodeBenchmarkChannels, durableQuorumBenchmarkOptions{
		useNetwork: true, commitShards: 1, payload: chatLifecycleBenchmarkPayload,
	})
	pressure := newChannelAppendGatewayDeliveryPressure(b, channelAppendDeliveryPressureSessions, channelAppendDeliverySynchronousAck)
	benchmarkThreeNodeChannelAppendClusterWithLoad1000QPS(b, cluster, "append-payload-mix", pressure)
	b.ReportMetric(float64(pressure.writes.Load())/float64(b.N), "delivery-writes/op")
	b.ReportMetric(float64(pressure.handler.recvAcks.Load())/float64(b.N), "recvacks/op")
}

// BenchmarkThreeNodeChannelAppendStoreWorkerMatrix1000QPS isolates whether the
// bounded quorum-commit worker pool creates the 1000 QPS append tail.
func BenchmarkThreeNodeChannelAppendStoreWorkerMatrix1000QPS(b *testing.B) {
	for _, workers := range []int{20, 40, 80, 128} {
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			cluster := newChannelAppendBenchmarkClusterWithOptions(b, threeNodeBenchmarkChannels, durableQuorumBenchmarkOptions{
				useNetwork: true, commitShards: 1, storeAppendWorkers: workers,
			})
			benchmarkThreeNodeChannelAppendCluster1000QPS(b, cluster, "append")
		})
	}
}

// BenchmarkThreeNodeChannelAppendCommitFlushMatrix1000QPS checks whether a
// larger physical-commit collection window shrinks the quorum-worker tail.
func BenchmarkThreeNodeChannelAppendCommitFlushMatrix1000QPS(b *testing.B) {
	for _, wait := range []time.Duration{time.Millisecond, 2 * time.Millisecond, 5 * time.Millisecond} {
		b.Run(wait.String(), func(b *testing.B) {
			cluster := newChannelAppendBenchmarkClusterWithOptions(b, threeNodeBenchmarkChannels, durableQuorumBenchmarkOptions{
				useNetwork: true, commitShards: 1, commitFlushWindow: wait,
			})
			benchmarkThreeNodeChannelAppendCluster1000QPS(b, cluster, "append")
		})
	}
}

// BenchmarkThreeNodeChannelAppendHedgeMatrix1000QPS includes real TCP peer
// exchange, foreground quorum RPCs, deferred convergence, physical MessageDB
// durability, and the public Channel service around the hedge policy.
func BenchmarkThreeNodeChannelAppendHedgeMatrix1000QPS(b *testing.B) {
	for _, delay := range []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond} {
		b.Run(delay.String(), func(b *testing.B) {
			cluster := newChannelAppendBenchmarkClusterWithOptions(b, threeNodeBenchmarkChannels, durableQuorumBenchmarkOptions{
				useNetwork: true, commitShards: 1, replicaHedgeDelay: delay,
			})
			benchmarkThreeNodeChannelAppendCluster1000QPS(b, cluster, "append")
		})
	}
}

// BenchmarkThreeNodeChannelAppendTransportWriteWaitMatrix1000QPS measures
// whether a wider cluster-RPC coalescing window reduces fragmented writev calls
// without moving the public Channel append tail beyond its 200ms budget.
func BenchmarkThreeNodeChannelAppendTransportWriteWaitMatrix1000QPS(b *testing.B) {
	for _, wait := range []time.Duration{100 * time.Microsecond, 250 * time.Microsecond, 500 * time.Microsecond} {
		b.Run(wait.String(), func(b *testing.B) {
			cluster := newChannelAppendBenchmarkClusterWithOptions(b, threeNodeBenchmarkChannels, durableQuorumBenchmarkOptions{
				useNetwork: true, commitShards: 1, transportWriteBatchMaxWait: wait,
			})
			benchmarkThreeNodeChannelAppendCluster1000QPS(b, cluster, "append")
		})
	}
}

// BenchmarkThreeNodeChannelAppendPeerFlightMatrix1000QPS measures whether
// fewer same-target RPC flights create materially larger quorum batches
// without serializing independent channels into a worse append tail.
func BenchmarkThreeNodeChannelAppendPeerFlightMatrix1000QPS(b *testing.B) {
	for _, flights := range []int{4, 8, 16} {
		b.Run(fmt.Sprintf("flights_%d", flights), func(b *testing.B) {
			cluster := newChannelAppendBenchmarkClusterWithOptions(b, threeNodeBenchmarkChannels, durableQuorumBenchmarkOptions{
				useNetwork: true, commitShards: 1, peerTargetFlight: flights,
			})
			benchmarkThreeNodeChannelAppendCluster1000QPS(b, cluster, "append")
		})
	}
}

// BenchmarkThreeNodeChannelAppendMixedCold1000QPS reproduces the retained
// three-node run's approximate 25% hot / 75% first-create mix. Hot latency is
// measured while cold operations install a fresh quorum authority and append
// its first record through the same TCP, workers, and MessageDB engines.
func BenchmarkThreeNodeChannelAppendMixedCold1000QPS(b *testing.B) {
	const hotChannels = 100
	cluster := newChannelAppendBenchmarkCluster(b, hotChannels)
	defer cluster.close(b)

	hotLatencies := make([]time.Duration, (b.N+3)/4)
	jobs := make(chan int, threeNodeBenchmarkWorkers)
	var workers sync.WaitGroup
	workers.Add(threeNodeBenchmarkWorkers)
	for range threeNodeBenchmarkWorkers {
		go func() {
			defer workers.Done()
			for index := range jobs {
				hot := index%4 == 0
				channel := durableQuorumBenchmarkChannel{}
				if hot {
					channel = cluster.channels[(index/4)%hotChannels]
				} else {
					var err error
					channel, err = cluster.installChannel(context.Background(), hotChannels+index)
					if err != nil {
						cluster.recordError(fmt.Errorf("install cold channel %d: %w", index, err))
						continue
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				started := time.Now()
				result, err := cluster.nodes[channel.authority.Leader].AppendBatch(ctx, ch.AppendBatchRequest{
					ChannelID: channel.authority.ChannelID,
					Messages: []ch.Message{{
						MessageID: uint64(index + 1), ChannelID: channel.authority.ChannelID.ID,
						ChannelType: channel.authority.ChannelID.Type, FromUID: "benchmark-sender",
						ClientMsgNo: fmt.Sprintf("mixed-%d", index+1), Payload: durableQuorumBenchmarkPayload(index),
					}},
					CommitMode: ch.CommitModeQuorum, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1,
					OmitResultPayload: true, ServerAllocatedMessageIDs: true,
				})
				latency := time.Since(started)
				cancel()
				if err == nil && (len(result.Items) != 1 || result.Items[0].Err != nil) {
					if len(result.Items) == 1 {
						err = result.Items[0].Err
					} else {
						err = ch.ErrInvalidConfig
					}
				}
				if err != nil {
					cluster.recordError(fmt.Errorf("mixed append %d: %w", index, err))
					continue
				}
				if hot {
					hotLatencies[index/4] = latency
				}
			}
		}()
	}

	b.ResetTimer()
	started := time.Now()
	for index := range b.N {
		waitUntil(started.Add(time.Duration(index) * time.Second / threeNodeBenchmarkRate))
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()
	if err := cluster.firstError(); err != nil {
		b.Fatal(err)
	}
	reportDurableQuorumBenchmark(b, "mixed-hot", hotLatencies, cluster.commitObserver.snapshot())
}

func benchmarkThreeNodeChannelAppendCluster1000QPS(b *testing.B, cluster *durableQuorumBenchmarkCluster, metricPrefix string) {
	benchmarkThreeNodeChannelAppendClusterAtRate(b, cluster, metricPrefix, threeNodeBenchmarkRate)
}

func benchmarkThreeNodeChannelAppendClusterAtRate(b *testing.B, cluster *durableQuorumBenchmarkCluster, metricPrefix string, rate int) {
	benchmarkThreeNodeChannelAppendClusterWithLoadAtRate(b, cluster, metricPrefix, nil, rate)
}

type channelAppendBenchmarkLoad interface {
	Start()
	Stop() error
}

func benchmarkThreeNodeChannelAppendClusterWithLoad1000QPS(b *testing.B, cluster *durableQuorumBenchmarkCluster, metricPrefix string, load channelAppendBenchmarkLoad) {
	benchmarkThreeNodeChannelAppendClusterWithLoadAtRate(b, cluster, metricPrefix, load, threeNodeBenchmarkRate)
}

func benchmarkThreeNodeChannelAppendClusterWithLoadAtRate(b *testing.B, cluster *durableQuorumBenchmarkCluster, metricPrefix string, load channelAppendBenchmarkLoad, rate int) {
	if rate <= 0 {
		b.Fatal("channel append benchmark rate must be positive")
	}
	defer cluster.close(b)
	if load != nil {
		defer func() {
			if load == nil {
				return
			}
			if err := load.Stop(); err != nil {
				b.Errorf("background load Stop(): %v", err)
			}
		}()
	}

	latencies := make([]time.Duration, b.N)
	jobs := make(chan int, threeNodeBenchmarkWorkers)
	var workers sync.WaitGroup
	workers.Add(threeNodeBenchmarkWorkers)
	for range threeNodeBenchmarkWorkers {
		go func() {
			defer workers.Done()
			for index := range jobs {
				channelIndex := index % len(cluster.channels)
				channel := cluster.channels[channelIndex]
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				started := time.Now()
				result, err := cluster.nodes[channel.authority.Leader].AppendBatch(ctx, ch.AppendBatchRequest{
					ChannelID: channel.authority.ChannelID,
					Messages: []ch.Message{{
						MessageID: uint64(index + 1), ChannelID: channel.authority.ChannelID.ID,
						ChannelType: channel.authority.ChannelID.Type, FromUID: "benchmark-sender",
						ClientMsgNo: fmt.Sprintf("bench-%d", index+1), Payload: cluster.payload(index),
					}},
					CommitMode: ch.CommitModeQuorum, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1,
					OmitResultPayload: true, ServerAllocatedMessageIDs: true,
				})
				latencies[index] = time.Since(started)
				cancel()
				if err == nil && (len(result.Items) != 1 || result.Items[0].Err != nil) {
					if len(result.Items) == 1 {
						err = result.Items[0].Err
					} else {
						err = ch.ErrInvalidConfig
					}
				}
				if err != nil {
					cluster.recordError(fmt.Errorf("append channel %d: %w", channelIndex, err))
				}
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	if load != nil {
		load.Start()
	}
	started := time.Now()
	for index := 0; index < b.N; index++ {
		waitUntil(started.Add(time.Duration(index) * time.Second / time.Duration(rate)))
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()
	if load != nil {
		if err := load.Stop(); err != nil {
			b.Errorf("background load Stop(): %v", err)
		}
		load = nil
	}

	if err := cluster.firstError(); err != nil {
		b.Fatal(err)
	}
	b.Logf("channel stages: %s", cluster.channelObserver.summary())
	cluster.transportObserver.report(b)
	cluster.exchangeObserver.report(b)
	reportDurableQuorumBenchmark(b, metricPrefix, latencies, cluster.commitObserver.snapshot())
}

type durableQuorumBenchmarkChannel struct {
	authority replication.Authority
}

type durableQuorumBenchmarkCluster struct {
	runtimes          map[ch.NodeID]*replication.Runtime
	factories         map[ch.NodeID]*channelstore.MessageDBFactory
	nodes             map[ch.NodeID]ch.Cluster
	channels          []durableQuorumBenchmarkChannel
	commitObserver    *durableQuorumCommitObserver
	channelObserver   *durableQuorumChannelObserver
	tickStop          chan struct{}
	tickWG            sync.WaitGroup
	transportClients  map[ch.NodeID]*clusternet.TransportClient
	transportServers  map[ch.NodeID]*clusternet.TransportServer
	transportObserver *durableQuorumTransportObserver
	exchangeObserver  *durableQuorumExchangeObserver
	payload           func(int) []byte

	errMu sync.Mutex
	err   error
}

type durableQuorumTransportObserver struct {
	batches atomic.Uint64
	frames  atomic.Uint64
	singles atomic.Uint64
}

func (o *durableQuorumTransportObserver) ObserveTransport(event transport.Event) {
	if o == nil || event.Name != "write_batch" || event.Items <= 0 {
		return
	}
	o.batches.Add(1)
	o.frames.Add(uint64(event.Items))
	if event.Items == 1 {
		o.singles.Add(1)
	}
}

func (o *durableQuorumTransportObserver) report(b *testing.B) {
	b.Helper()
	if o == nil {
		return
	}
	// Transport observers drain asynchronously. Once foreground work finishes,
	// wait for the batch count to remain stable before reporting its shape.
	last := o.batches.Load()
	stable := 0
	for stable < 3 {
		time.Sleep(10 * time.Millisecond)
		current := o.batches.Load()
		if current == last {
			stable++
			continue
		}
		last = current
		stable = 0
	}
	batches := o.batches.Load()
	if batches == 0 {
		return
	}
	b.ReportMetric(float64(o.frames.Load())/float64(batches), "transport-frames/batch")
	b.ReportMetric(100*float64(o.singles.Load())/float64(batches), "transport-single-batch-pct")
}

type durableQuorumExchangeObserver struct {
	foregroundBatches atomic.Uint64
	foregroundItems   atomic.Uint64
	backgroundBatches atomic.Uint64
	backgroundItems   atomic.Uint64
}

func (o *durableQuorumExchangeObserver) observe(batch replication.ExchangeBatch) {
	if o == nil || len(batch.Items) == 0 {
		return
	}
	switch batch.Priority {
	case replication.ExchangePriorityForeground:
		o.foregroundBatches.Add(1)
		o.foregroundItems.Add(uint64(len(batch.Items)))
	case replication.ExchangePriorityBackground:
		o.backgroundBatches.Add(1)
		o.backgroundItems.Add(uint64(len(batch.Items)))
	}
}

func (o *durableQuorumExchangeObserver) report(b *testing.B) {
	b.Helper()
	if o == nil {
		return
	}
	if batches := o.foregroundBatches.Load(); batches > 0 {
		b.ReportMetric(float64(o.foregroundItems.Load())/float64(batches), "foreground-items/exchange")
	}
	if batches := o.backgroundBatches.Load(); batches > 0 {
		b.ReportMetric(float64(o.backgroundItems.Load())/float64(batches), "background-items/exchange")
	}
}

type observedDurableQuorumBenchmarkLink struct {
	base     replication.PeerLink
	observer *durableQuorumExchangeObserver
}

func (l *observedDurableQuorumBenchmarkLink) Exchange(ctx context.Context, target ch.NodeID, batch replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	l.observer.observe(batch)
	return l.base.Exchange(ctx, target, batch)
}

func newChannelAppendBenchmarkCluster(b *testing.B, channelCount int) *durableQuorumBenchmarkCluster {
	b.Helper()
	return newChannelAppendBenchmarkClusterWithOptions(b, channelCount, durableQuorumBenchmarkOptions{useNetwork: true, commitShards: 1})
}

func newChannelAppendBenchmarkClusterWithOptions(b *testing.B, channelCount int, options durableQuorumBenchmarkOptions) *durableQuorumBenchmarkCluster {
	b.Helper()
	cluster := newDurableQuorumBenchmarkClusterWithOptions(b, channelCount, options)
	network := channeltransport.NewLocalNetwork()
	cluster.nodes = make(map[ch.NodeID]ch.Cluster, 3)
	cluster.channelObserver = newDurableQuorumChannelObserver()
	cluster.tickStop = make(chan struct{})
	for _, node := range []ch.NodeID{1, 2, 3} {
		channelCluster, err := service.New(service.Config{
			LocalNode: node, ReactorCount: max(4, runtime.GOMAXPROCS(0)),
			Store: cluster.factories[node], Transport: network.Client(),
			QuorumLog: cluster.runtimes[node].Log(), RPCWorkers: 160, RPCBatchMaxItems: 16,
			Observer: cluster.channelObserver, StoreAppendWorkers: options.storeAppendWorkers,
		})
		if err != nil {
			cluster.close(b)
			b.Fatalf("service.New(node=%d) error = %v", node, err)
		}
		cluster.nodes[node] = channelCluster
		if server, ok := channelCluster.(channeltransport.Server); ok {
			network.Register(node, server)
		}
	}
	for _, channel := range cluster.channels {
		authority := channel.authority
		meta := ch.Meta{
			Key: authority.Key, ID: authority.ChannelID,
			Epoch: authority.ID.ChannelEpoch, LeaderEpoch: authority.ID.LeaderTerm, RouteGeneration: authority.ID.FenceVersion,
			Leader: authority.Leader, Replicas: append([]ch.NodeID(nil), authority.Voters...),
			ISR: append([]ch.NodeID(nil), authority.Voters...), MinISR: authority.WriteQuorum, Status: ch.StatusActive,
		}
		for _, node := range []ch.NodeID{1, 2, 3} {
			if err := cluster.nodes[node].ApplyMeta(meta); err != nil {
				cluster.close(b)
				b.Fatalf("ApplyMeta(node=%d, channel=%s) error = %v", node, authority.Key, err)
			}
		}
	}
	for _, node := range []ch.NodeID{1, 2, 3} {
		tickStop := cluster.tickStop
		cluster.tickWG.Add(1)
		go func(channelCluster ch.Cluster) {
			defer cluster.tickWG.Done()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-tickStop:
					return
				case <-ticker.C:
					_ = channelCluster.Tick(context.Background())
				}
			}
		}(cluster.nodes[node])
	}
	cluster.commitObserver.reset()
	return cluster
}

func newDurableQuorumBenchmarkCluster(b *testing.B, channelCount int) *durableQuorumBenchmarkCluster {
	b.Helper()
	return newDurableQuorumBenchmarkClusterWithShards(b, channelCount, 1)
}

func newDurableQuorumBenchmarkClusterWithShards(b *testing.B, channelCount, commitShards int) *durableQuorumBenchmarkCluster {
	b.Helper()
	return newDurableQuorumBenchmarkClusterWithTransportAndShards(b, channelCount, false, commitShards)
}

func newDurableQuorumBenchmarkClusterWithTransport(b *testing.B, channelCount int, useNetwork bool) *durableQuorumBenchmarkCluster {
	b.Helper()
	return newDurableQuorumBenchmarkClusterWithTransportAndShards(b, channelCount, useNetwork, 1)
}

func newDurableQuorumBenchmarkClusterWithTransportAndShards(b *testing.B, channelCount int, useNetwork bool, commitShards int) *durableQuorumBenchmarkCluster {
	b.Helper()
	return newDurableQuorumBenchmarkClusterWithOptions(b, channelCount, durableQuorumBenchmarkOptions{
		useNetwork: useNetwork, commitShards: commitShards,
	})
}

type durableQuorumBenchmarkOptions struct {
	useNetwork                 bool
	commitShards               int
	commitFlushWindow          time.Duration
	storeAppendWorkers         int
	payload                    func(int) []byte
	replicaHedgeDelay          time.Duration
	peerTargetFlight           int
	transportPoolSize          int
	transportWriteBatchMaxWait time.Duration
	delayedTarget              ch.NodeID
	delayedEvery               uint64
	delayedBy                  time.Duration
}

func newDurableQuorumBenchmarkClusterWithOptions(b *testing.B, channelCount int, options durableQuorumBenchmarkOptions) *durableQuorumBenchmarkCluster {
	b.Helper()
	if options.commitShards == 0 {
		options.commitShards = 1
	}
	if options.commitFlushWindow == 0 {
		options.commitFlushWindow = time.Millisecond
	}
	if options.payload == nil {
		options.payload = durableQuorumBenchmarkPayload
	}
	if options.transportPoolSize == 0 {
		options.transportPoolSize = 16
	}
	router := &durableQuorumBenchmarkRouter{}
	cluster := &durableQuorumBenchmarkCluster{
		runtimes:          make(map[ch.NodeID]*replication.Runtime, 3),
		factories:         make(map[ch.NodeID]*channelstore.MessageDBFactory, 3),
		channels:          make([]durableQuorumBenchmarkChannel, channelCount),
		commitObserver:    &durableQuorumCommitObserver{},
		transportObserver: &durableQuorumTransportObserver{},
		exchangeObserver:  &durableQuorumExchangeObserver{},
		transportClients:  make(map[ch.NodeID]*clusternet.TransportClient, 3),
		transportServers:  make(map[ch.NodeID]*clusternet.TransportServer, 3),
		payload:           options.payload,
	}
	gateways := make(map[ch.NodeID]*clusterchannels.QuorumExchangeGateway, 3)
	links := make(map[ch.NodeID]replication.PeerLink, 3)
	if options.useNetwork {
		limits := transport.DefaultLimits()
		limits.WriteBatchMaxWait = options.transportWriteBatchMaxWait
		discovery := clusternet.NewDiscovery()
		addresses := make([]clusternet.NodeAddress, 0, 3)
		for _, node := range []ch.NodeID{1, 2, 3} {
			gateway := clusterchannels.NewQuorumExchangeGateway(nil)
			server := clusternet.NewTransportServer(clusternet.TransportServerConfig{
				NodeID: uint64(node), Limits: limits, Observer: cluster.transportObserver,
			})
			clusterchannels.RegisterQuorumExchangeHandlerOn(server, gateway)
			if err := server.Start("127.0.0.1:0"); err != nil {
				cluster.close(b)
				b.Fatalf("TransportServer.Start(node=%d) error = %v", node, err)
			}
			gateways[node] = gateway
			cluster.transportServers[node] = server
			addresses = append(addresses, clusternet.NodeAddress{NodeID: uint64(node), Addr: server.Addr()})
		}
		discovery.Update(addresses)
		for _, node := range []ch.NodeID{1, 2, 3} {
			client := clusternet.NewTransportClient(clusternet.TransportClientConfig{
				Discovery: discovery, NodeID: uint64(node), PoolSize: options.transportPoolSize, Limits: limits, Observer: cluster.transportObserver,
			})
			link, err := clusterchannels.NewQuorumPeerLink(node, client)
			if err != nil {
				cluster.close(b)
				b.Fatalf("NewQuorumPeerLink(node=%d) error = %v", node, err)
			}
			cluster.transportClients[node] = client
			links[node] = link
		}
	}
	for _, node := range []ch.NodeID{1, 2, 3} {
		factory := channelstore.NewMessageDBFactoryWithOptions(
			b.TempDir(),
			channelstore.MessageDBFactoryOptions{
				CommitFlushWindow: options.commitFlushWindow,
				CommitShards:      options.commitShards,
				CommitObserver:    cluster.commitObserver,
			},
		)
		store, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
			Factory: factory, MaxBatchItems: replication.MaxExchangeBatchItems, MaxBatchBytes: replication.MaxExchangeBatchBytes,
		})
		if err != nil {
			cluster.close(b)
			b.Fatalf("NewStoreAdapter(node=%d) error = %v", node, err)
		}
		link := links[node]
		if link == nil {
			link = durableQuorumBenchmarkLink{from: node, router: router}
		}
		if options.delayedTarget != 0 && options.delayedEvery > 0 && options.delayedBy > 0 {
			link = &delayedDurableQuorumBenchmarkLink{
				base: link, target: options.delayedTarget, every: options.delayedEvery, delay: options.delayedBy,
			}
		}
		link = &observedDurableQuorumBenchmarkLink{base: link, observer: cluster.exchangeObserver}
		runtime, err := replication.NewRuntime(replication.RuntimeConfig{
			LocalNode: node, Store: store, Link: link,
			Goroutines: gonruntimeregistry.New(), ReplicaHedgeDelay: options.replicaHedgeDelay,
			PeerTargetFlight: options.peerTargetFlight,
		})
		if err != nil {
			cluster.close(b)
			b.Fatalf("NewRuntime(node=%d) error = %v", node, err)
		}
		cluster.factories[node] = factory
		cluster.runtimes[node] = runtime
		if options.useNetwork {
			gateways[node].Replace(runtime.ExchangeServer())
		} else {
			router.register(node, runtime.ExchangeServer())
		}
	}

	installJobs := make(chan int, 64)
	var installWorkers sync.WaitGroup
	installWorkers.Add(64)
	for range 64 {
		go func() {
			defer installWorkers.Done()
			for index := range installJobs {
				id := ch.ChannelID{ID: fmt.Sprintf("durable-quorum-%04d", index), Type: 1}
				authority := replication.Authority{
					Key: ch.ChannelKeyForID(id), ChannelID: id,
					ID:     replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1},
					Leader: ch.NodeID(index%3 + 1), Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
				}
				if _, err := cluster.runtimes[authority.Leader].Log().Install(context.Background(), authority); err != nil {
					cluster.recordError(fmt.Errorf("install channel %d: %w", index, err))
					continue
				}
				cluster.channels[index] = durableQuorumBenchmarkChannel{authority: authority}
			}
		}()
	}
	for index := range channelCount {
		installJobs <- index
	}
	close(installJobs)
	installWorkers.Wait()
	if err := cluster.firstError(); err != nil {
		cluster.close(b)
		b.Fatal(err)
	}
	cluster.commitObserver.reset()
	return cluster
}

func (c *durableQuorumBenchmarkCluster) installChannel(ctx context.Context, index int) (durableQuorumBenchmarkChannel, error) {
	id := ch.ChannelID{ID: fmt.Sprintf("durable-quorum-%06d", index), Type: 1}
	authority := replication.Authority{
		Key: ch.ChannelKeyForID(id), ChannelID: id,
		ID:     replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1},
		Leader: ch.NodeID(index%3 + 1), Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
	if _, err := c.runtimes[authority.Leader].Log().Install(ctx, authority); err != nil {
		return durableQuorumBenchmarkChannel{}, err
	}
	meta := ch.Meta{
		Key: authority.Key, ID: authority.ChannelID,
		Epoch: authority.ID.ChannelEpoch, LeaderEpoch: authority.ID.LeaderTerm, RouteGeneration: authority.ID.FenceVersion,
		Leader: authority.Leader, Replicas: append([]ch.NodeID(nil), authority.Voters...),
		ISR: append([]ch.NodeID(nil), authority.Voters...), MinISR: authority.WriteQuorum, Status: ch.StatusActive,
	}
	for _, node := range []ch.NodeID{1, 2, 3} {
		if err := c.nodes[node].ApplyMeta(meta); err != nil {
			return durableQuorumBenchmarkChannel{}, err
		}
	}
	return durableQuorumBenchmarkChannel{authority: authority}, nil
}

type delayedDurableQuorumBenchmarkLink struct {
	base   replication.PeerLink
	target ch.NodeID
	every  uint64
	delay  time.Duration
	calls  atomic.Uint64
}

func (l *delayedDurableQuorumBenchmarkLink) Exchange(ctx context.Context, target ch.NodeID, batch replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	if target == l.target && batch.Priority == replication.ExchangePriorityForeground && l.calls.Add(1)%l.every == 0 {
		timer := time.NewTimer(l.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return replication.ExchangeBatchResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	return l.base.Exchange(ctx, target, batch)
}

func (c *durableQuorumBenchmarkCluster) recordError(err error) {
	if err == nil {
		return
	}
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.err == nil {
		c.err = err
	}
}

func (c *durableQuorumBenchmarkCluster) firstError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

func (c *durableQuorumBenchmarkCluster) close(tb testing.TB) {
	tb.Helper()
	if c.tickStop != nil {
		close(c.tickStop)
		c.tickWG.Wait()
		c.tickStop = nil
	}
	for _, node := range []ch.NodeID{1, 2, 3} {
		if channelCluster := c.nodes[node]; channelCluster != nil {
			if err := channelCluster.Close(); err != nil {
				tb.Errorf("Channel.Close(node=%d) error = %v", node, err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, node := range []ch.NodeID{1, 2, 3} {
		if runtime := c.runtimes[node]; runtime != nil {
			if err := runtime.Close(ctx); err != nil {
				tb.Errorf("Runtime.Close(node=%d) error = %v", node, err)
			}
		}
	}
	for _, node := range []ch.NodeID{1, 2, 3} {
		if client := c.transportClients[node]; client != nil {
			client.Stop()
		}
		if server := c.transportServers[node]; server != nil {
			server.Stop()
		}
	}
	for _, node := range []ch.NodeID{1, 2, 3} {
		if factory := c.factories[node]; factory != nil {
			if err := factory.Close(); err != nil {
				tb.Errorf("MessageDBFactory.Close(node=%d) error = %v", node, err)
			}
		}
	}
}

func durableQuorumBenchmarkProposal(channel durableQuorumBenchmarkChannel, index int, sequence uint64) replication.Proposal {
	var commandID ch.CommandID
	binary.BigEndian.PutUint64(commandID[0:8], uint64(index+1))
	binary.BigEndian.PutUint64(commandID[8:16], sequence)
	payload := durableQuorumBenchmarkPayload(index)
	return replication.Proposal{
		Key: channel.authority.Key, Expected: channel.authority.ID, CommandID: commandID,
		ServerAllocatedMessageIDs: true,
		Records: []ch.Record{{
			ID: uint64(index + 1), Epoch: channel.authority.ID.ChannelEpoch,
			FromUID: "benchmark-sender", ClientMsgNo: fmt.Sprintf("bench-%d", index+1),
			ServerTimestampMS: time.Now().UnixMilli(), Payload: payload, SizeBytes: len(payload),
		}},
	}
}

func durableQuorumBenchmarkPayload(index int) []byte {
	payload := make([]byte, 1024)
	binary.BigEndian.PutUint64(payload[:8], uint64(index+1))
	return payload
}

func chatLifecycleBenchmarkPayload(index int) []byte {
	size := 256
	switch index % 100 {
	case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94:
		size = 1024
	case 95, 96, 97, 98:
		size = 4 * 1024
	case 99:
		size = 16 * 1024
	}
	payload := make([]byte, size)
	binary.BigEndian.PutUint64(payload[:8], uint64(index+1))
	return payload
}

func waitUntil(deadline time.Time) {
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		timer := time.NewTimer(remaining)
		<-timer.C
	}
}

type durableQuorumBenchmarkRouter struct {
	mu      sync.RWMutex
	servers [4]*replication.ExchangeServer
}

func (r *durableQuorumBenchmarkRouter) register(node ch.NodeID, server *replication.ExchangeServer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers[int(node)] = server
}

func (r *durableQuorumBenchmarkRouter) exchange(ctx context.Context, from, target ch.NodeID, batch replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	r.mu.RLock()
	server := r.servers[int(target)]
	r.mu.RUnlock()
	if server == nil {
		return replication.ExchangeBatchResult{}, ch.ErrNotReady
	}
	return server.Handle(ctx, from, batch)
}

type durableQuorumBenchmarkLink struct {
	from   ch.NodeID
	router *durableQuorumBenchmarkRouter
}

func (l durableQuorumBenchmarkLink) Exchange(ctx context.Context, target ch.NodeID, batch replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	return l.router.exchange(ctx, l.from, target, batch)
}

type durableQuorumCommitSnapshot struct {
	batches int
	records int
	commit  time.Duration
}

type durableQuorumCommitObserver struct {
	mu      sync.Mutex
	batches int
	records int
	commit  time.Duration
}

func (o *durableQuorumCommitObserver) SetCommitCoordinatorQueueDepth(int) {}

func (o *durableQuorumCommitObserver) ObserveCommitCoordinatorBatch(event messagedb.CommitCoordinatorBatchEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.batches++
	o.records += event.Records
	o.commit += event.CommitDuration
}

func (o *durableQuorumCommitObserver) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.batches = 0
	o.records = 0
	o.commit = 0
}

func (o *durableQuorumCommitObserver) snapshot() durableQuorumCommitSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return durableQuorumCommitSnapshot{batches: o.batches, records: o.records, commit: o.commit}
}

type durableQuorumChannelObserver struct {
	mu sync.Mutex

	appendStages map[string][]time.Duration
	workerTasks  map[channelworker.TaskKind][]time.Duration
	workerWaits  map[channelworker.TaskKind][]time.Duration
	appendTotal  []time.Duration
	mailboxMax   int
	workerMax    int
	inflightPeak int
}

func newDurableQuorumChannelObserver() *durableQuorumChannelObserver {
	return &durableQuorumChannelObserver{
		appendStages: make(map[string][]time.Duration),
		workerTasks:  make(map[channelworker.TaskKind][]time.Duration),
		workerWaits:  make(map[channelworker.TaskKind][]time.Duration),
	}
}

func (o *durableQuorumChannelObserver) SetReactorMailboxDepth(_ int, _ string, depth int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.mailboxMax = max(o.mailboxMax, depth)
}

func (o *durableQuorumChannelObserver) SetWorkerQueueDepth(_ string, depth int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workerMax = max(o.workerMax, depth)
}

func (o *durableQuorumChannelObserver) ObserveAppendBatch(int, int, time.Duration) {}

func (o *durableQuorumChannelObserver) ObserveAppendLatency(_ ch.CommitMode, duration time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.appendTotal = append(o.appendTotal, duration)
}

func (o *durableQuorumChannelObserver) ObserveWorkerResult(kind channelworker.TaskKind, _ error, duration time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workerTasks[kind] = append(o.workerTasks[kind], duration)
}

func (o *durableQuorumChannelObserver) ObserveWorkerWait(_ string, kind channelworker.TaskKind, duration time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workerWaits[kind] = append(o.workerWaits[kind], duration)
}

func (o *durableQuorumChannelObserver) SetWorkerInflight(_ string, _ int) {}

func (o *durableQuorumChannelObserver) SetWorkerInflightPeak(_ string, peak int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.inflightPeak = max(o.inflightPeak, peak)
}

func (o *durableQuorumChannelObserver) ObserveChannelAppendStage(stage, result string, duration time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.appendStages[stage+"/"+result] = append(o.appendStages[stage+"/"+result], duration)
}

func (o *durableQuorumChannelObserver) ObserveAppendWaitStage(stage string, _ ch.CommitMode, result string, duration time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.appendStages["wait_"+stage+"/"+result] = append(o.appendStages["wait_"+stage+"/"+result], duration)
}

func (o *durableQuorumChannelObserver) summary() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	parts := make([]string, 0, len(o.appendStages)+2*len(o.workerTasks)+3)
	parts = append(parts, fmt.Sprintf("mailbox_max=%d worker_queue_max=%d inflight_peak=%d append_p99=%s", o.mailboxMax, o.workerMax, o.inflightPeak, durationP99(o.appendTotal)))
	stageNames := make([]string, 0, len(o.appendStages))
	for stage := range o.appendStages {
		stageNames = append(stageNames, stage)
	}
	sort.Strings(stageNames)
	for _, stage := range stageNames {
		parts = append(parts, fmt.Sprintf("%s_p99=%s", stage, durationP99(o.appendStages[stage])))
	}
	taskKinds := make([]int, 0, len(o.workerTasks))
	for kind := range o.workerTasks {
		taskKinds = append(taskKinds, int(kind))
	}
	sort.Ints(taskKinds)
	for _, kind := range taskKinds {
		parts = append(parts, fmt.Sprintf("worker_%d_wait_p99=%s worker_%d_run_p99=%s", kind,
			durationP99(o.workerWaits[channelworker.TaskKind(kind)]), kind, durationP99(o.workerTasks[channelworker.TaskKind(kind)])))
	}
	return fmt.Sprint(parts)
}

func durationP99(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(len(sorted)*99-1)/100]
}

func reportDurableQuorumBenchmark(b *testing.B, prefix string, latencies []time.Duration, commits durableQuorumCommitSnapshot) {
	b.Helper()
	if len(latencies) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p99 := sorted[(len(sorted)*99-1)/100]
	aboveBudget := 0
	for _, latency := range latencies {
		if latency > 200*time.Millisecond {
			aboveBudget++
		}
	}
	b.ReportMetric(float64(p99)/float64(time.Millisecond), prefix+"-p99-ms")
	b.ReportMetric(100*float64(aboveBudget)/float64(len(latencies)), prefix+"-over-200ms-pct")
	if commits.batches > 0 {
		b.ReportMetric(float64(commits.batches)/float64(len(latencies)), "physical-commits/op")
		b.ReportMetric(float64(commits.records)/float64(commits.batches), "records/physical-commit")
		b.ReportMetric(float64(commits.commit)/float64(commits.batches)/float64(time.Millisecond), "physical-commit-ms")
	}
	if len(latencies) >= 1000 && float64(aboveBudget)/float64(len(latencies)) > 0.01 {
		b.Errorf("%s operations above 200ms = %.3f%%, p99=%s, physical batches=%d, records=%d, mean physical commit=%s; want <= 1%%",
			prefix, 100*float64(aboveBudget)/float64(len(latencies)), p99, commits.batches, commits.records,
			commits.commit/time.Duration(max(commits.batches, 1)))
	}
}
