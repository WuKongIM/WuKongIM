package channel_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelreplica "github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

func BenchmarkClusterAppend(b *testing.B) {
	for _, profile := range appendBenchmarkProfiles() {
		b.Run(profile.Name, func(b *testing.B) {
			for _, commitMode := range []channel.CommitMode{channel.CommitModeQuorum, channel.CommitModeLocal} {
				b.Run(commitModeName(commitMode), func(b *testing.B) {
					for _, channelCount := range []int{1, 64} {
						b.Run(fmt.Sprintf("channels=%d", channelCount), func(b *testing.B) {
							for _, workers := range []int{1, 4, 16, 64, 128, 256, 384, 448, 480, 512} {
								b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
									benchmarkClusterAppend(b, appendBenchmarkConfig{
										ChannelCount: channelCount,
										Workers:      workers,
										CommitMode:   commitMode,
										Profile:      profile,
									})
								})
							}
						})
					}
				})
			}
		})
	}
}

type appendBenchmarkConfig struct {
	ChannelCount int
	Workers      int
	CommitMode   channel.CommitMode
	Profile      appendBenchmarkProfile
}

type appendBenchmarkProfile struct {
	Name                         string
	AppendGroupCommitMaxWait     time.Duration
	AppendGroupCommitMaxRecords  int
	AppendGroupCommitMaxBytes    int
	CommitCoordinatorFlushWindow time.Duration
}

func appendBenchmarkProfiles() []appendBenchmarkProfile {
	return []appendBenchmarkProfile{
		{
			Name:                         "low_latency",
			AppendGroupCommitMaxWait:     time.Millisecond,
			AppendGroupCommitMaxRecords:  64,
			AppendGroupCommitMaxBytes:    64 * 1024,
			CommitCoordinatorFlushWindow: 200 * time.Microsecond,
		},
		{
			Name:                         "balanced",
			AppendGroupCommitMaxWait:     2 * time.Millisecond,
			AppendGroupCommitMaxRecords:  128,
			AppendGroupCommitMaxBytes:    256 * 1024,
			CommitCoordinatorFlushWindow: 500 * time.Microsecond,
		},
		{
			Name:                         "throughput_100ms_ack",
			AppendGroupCommitMaxWait:     10 * time.Millisecond,
			AppendGroupCommitMaxRecords:  512,
			AppendGroupCommitMaxBytes:    1024 * 1024,
			CommitCoordinatorFlushWindow: 2 * time.Millisecond,
		},
	}
}

func benchmarkClusterAppend(b *testing.B, cfg appendBenchmarkConfig) {
	if cfg.ChannelCount <= 0 || cfg.Workers <= 0 {
		b.Fatalf("invalid benchmark config: %+v", cfg)
	}
	env := newAppendBenchmarkEnv(b, cfg.ChannelCount, cfg.Profile)
	defer env.Close(b)

	payload := []byte("append benchmark payload")
	ackLatencyNS := make([]int64, b.N)
	var seq atomic.Uint64
	var wg sync.WaitGroup
	var failed atomic.Bool
	errCh := make(chan error, 1)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	startedAt := time.Now()
	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if failed.Load() {
					return
				}
				next := seq.Add(1)
				if next > uint64(b.N) {
					return
				}
				startedAt := time.Now()
				channelID := env.channels[int((next-1)%uint64(len(env.channels)))]
				_, err := env.cluster.Append(ctx, channel.AppendRequest{
					ChannelID:             channelID,
					SupportsMessageSeqU64: true,
					CommitMode:            cfg.CommitMode,
					Message: channel.Message{
						FromUID: "bench-user",
						Payload: payload,
					},
				})
				ackLatencyNS[next-1] = time.Since(startedAt).Nanoseconds()
				if err != nil {
					if failed.CompareAndSwap(false, true) {
						errCh <- err
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(startedAt)
	b.StopTimer()
	if failed.Load() {
		b.Fatalf("Append() error = %v", <-errCh)
	}
	if elapsed > 0 {
		b.ReportMetric(float64(b.N)/elapsed.Seconds(), "append_qps")
	}
	reportAppendAckLatency(b, ackLatencyNS)
}

func reportAppendAckLatency(b *testing.B, samples []int64) {
	b.Helper()

	latencies := samples[:0]
	var total int64
	var under100ms int
	for _, sample := range samples {
		if sample <= 0 {
			continue
		}
		latencies = append(latencies, sample)
		total += sample
		if sample <= int64(100*time.Millisecond) {
			under100ms++
		}
	}
	if len(latencies) == 0 {
		return
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})
	b.ReportMetric(float64(total)/float64(len(latencies))/float64(time.Millisecond), "ack_avg_ms")
	b.ReportMetric(float64(percentileAppendLatency(latencies, 95))/float64(time.Millisecond), "ack_p95_ms")
	b.ReportMetric(float64(percentileAppendLatency(latencies, 99))/float64(time.Millisecond), "ack_p99_ms")
	b.ReportMetric(float64(latencies[len(latencies)-1])/float64(time.Millisecond), "ack_max_ms")
	b.ReportMetric(float64(under100ms)*100/float64(len(latencies)), "ack_le_100ms_pct")
}

func percentileAppendLatency(sorted []int64, percentile int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if percentile <= 0 {
		return sorted[0]
	}
	if percentile >= 100 {
		return sorted[len(sorted)-1]
	}
	idx := (len(sorted)*percentile + 99) / 100
	if idx <= 0 {
		return sorted[0]
	}
	if idx > len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[idx-1]
}

type appendBenchmarkEnv struct {
	cluster  channel.Cluster
	store    *channelstore.Engine
	pool     *channelreplica.ExecutionPool
	channels []channel.ChannelID
}

func newAppendBenchmarkEnv(b *testing.B, channelCount int, profile appendBenchmarkProfile) *appendBenchmarkEnv {
	b.Helper()
	if profile.Name == "" {
		profile = appendBenchmarkProfiles()[0]
	}

	engine, err := channelstore.Open(b.TempDir())
	if err != nil {
		b.Fatalf("store.Open() error = %v", err)
	}
	engine.ConfigureCommitCoordinator(channelstore.CommitCoordinatorConfig{
		FlushWindow: profile.CommitCoordinatorFlushWindow,
	})

	pool, err := channelreplica.NewExecutionPool(channelreplica.ExecutionPoolConfig{})
	if err != nil {
		_ = engine.Close()
		b.Fatalf("NewExecutionPool() error = %v", err)
	}

	rtBuild := func(buildCfg channel.RuntimeBuildConfig) (channel.Runtime, channel.HandlerRuntime, error) {
		rt, err := channelruntime.New(channelruntime.Config{
			LocalNode: buildCfg.LocalNode,
			ReplicaFactory: appendBenchmarkReplicaFactory{
				store:   buildCfg.Store.(*channelstore.Engine),
				pool:    pool,
				now:     buildCfg.Now,
				profile: profile,
			},
			GenerationStore:                  buildCfg.GenerationStore.(channelruntime.GenerationStore),
			AutoRunScheduler:                 true,
			FollowerReplicationRetryInterval: 10 * time.Millisecond,
			Tombstones: channelruntime.TombstonePolicy{
				TombstoneTTL:    time.Minute,
				CleanupInterval: time.Minute,
			},
			Limits: channelruntime.Limits{
				MaxFetchInflightPeer: 1,
				MaxSnapshotInflight:  1,
			},
			Now: buildCfg.Now,
		})
		if err != nil {
			return nil, nil, err
		}
		return appendBenchmarkRuntimeControl{runtime: rt}, rt, nil
	}

	cluster, err := channel.New(channel.Config{
		LocalNode:       1,
		Store:           engine,
		GenerationStore: &appendBenchmarkGenerationStore{},
		MessageIDs:      &appendBenchmarkMessageIDs{},
		Transport: channel.TransportConfig{
			Build: func(channel.TransportBuildConfig) (any, error) {
				return appendBenchmarkTransport{}, nil
			},
			BindFetchService: func(any, channel.HandlerRuntime) error {
				return nil
			},
		},
		Runtime: channel.RuntimeConfig{
			Build: rtBuild,
		},
		Handler: channel.HandlerConfig{
			Build: func(buildCfg channel.HandlerBuildConfig) (channel.MetaRollbackService, error) {
				return channelhandler.New(channelhandler.Config{
					Runtime:    buildCfg.Runtime,
					Store:      buildCfg.Store.(*channelstore.Engine),
					MessageIDs: buildCfg.MessageIDs,
				})
			},
		},
		Now: time.Now,
	})
	if err != nil {
		_ = pool.Close()
		_ = engine.Close()
		b.Fatalf("channel.New() error = %v", err)
	}

	env := &appendBenchmarkEnv{
		cluster:  cluster,
		store:    engine,
		pool:     pool,
		channels: make([]channel.ChannelID, 0, channelCount),
	}
	for i := 0; i < channelCount; i++ {
		id := channel.ChannelID{ID: fmt.Sprintf("append-bench-%d", i), Type: 1}
		meta := channel.Meta{
			Key:         appendBenchmarkChannelKey(id),
			ID:          id,
			Epoch:       1,
			LeaderEpoch: 1,
			Leader:      1,
			Replicas:    []channel.NodeID{1},
			ISR:         []channel.NodeID{1},
			MinISR:      1,
			LeaseUntil:  time.Now().Add(24 * time.Hour),
			Status:      channel.StatusActive,
			Features:    channel.Features{MessageSeqFormat: channel.MessageSeqFormatU64},
		}
		if err := cluster.ApplyMeta(meta); err != nil {
			env.Close(b)
			b.Fatalf("ApplyMeta(%s) error = %v", id.ID, err)
		}
		env.channels = append(env.channels, id)
	}
	return env
}

func (e *appendBenchmarkEnv) Close(b *testing.B) {
	b.Helper()

	var err error
	if e.cluster != nil {
		err = e.cluster.Close()
	}
	if e.pool != nil {
		if closeErr := e.pool.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	if e.store != nil {
		if closeErr := e.store.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	if err != nil {
		b.Fatalf("append benchmark cleanup error = %v", err)
	}
}

type appendBenchmarkRuntimeControl struct {
	runtime channelruntime.Runtime
}

func (c appendBenchmarkRuntimeControl) UpsertMeta(meta channel.Meta) error {
	if err := c.runtime.EnsureChannel(meta); err != nil {
		if err == channelruntime.ErrChannelExists {
			return c.runtime.ApplyMeta(meta)
		}
		return err
	}
	return nil
}

func (c appendBenchmarkRuntimeControl) RemoveChannel(key channel.ChannelKey) error {
	err := c.runtime.RemoveChannel(key)
	if err == channel.ErrChannelNotFound {
		return nil
	}
	return err
}

func (c appendBenchmarkRuntimeControl) Close() error {
	return c.runtime.Close()
}

type appendBenchmarkReplicaFactory struct {
	store   *channelstore.Engine
	pool    *channelreplica.ExecutionPool
	now     func() time.Time
	profile appendBenchmarkProfile
}

func (f appendBenchmarkReplicaFactory) New(cfg channelruntime.ChannelConfig) (channelreplica.Replica, error) {
	store := f.store.ForChannel(cfg.ChannelKey, cfg.Meta.ID)
	return channelreplica.NewReplica(channelreplica.ReplicaConfig{
		LocalNode:                   1,
		LogStore:                    store,
		CheckpointStore:             appendBenchmarkCheckpointStore{store: store},
		ApplyFetchStore:             store,
		EpochHistoryStore:           appendBenchmarkEpochHistoryStore{store: store},
		SnapshotApplier:             appendBenchmarkSnapshotApplier{store: store},
		Now:                         f.now,
		AppendGroupCommitMaxWait:    f.profile.AppendGroupCommitMaxWait,
		AppendGroupCommitMaxRecords: f.profile.AppendGroupCommitMaxRecords,
		AppendGroupCommitMaxBytes:   f.profile.AppendGroupCommitMaxBytes,
		Execution: channelreplica.ExecutionConfig{
			Mode: channelreplica.ExecutionModePooled,
			Pool: f.pool,
		},
		OnStateChange: cfg.OnReplicaStateChange,
	})
}

type appendBenchmarkCheckpointStore struct {
	store *channelstore.ChannelStore
}

func (s appendBenchmarkCheckpointStore) Load() (channel.Checkpoint, error) {
	return s.store.LoadCheckpoint()
}

func (s appendBenchmarkCheckpointStore) Store(checkpoint channel.Checkpoint) error {
	return s.store.StoreCheckpoint(checkpoint)
}

type appendBenchmarkEpochHistoryStore struct {
	store *channelstore.ChannelStore
}

func (s appendBenchmarkEpochHistoryStore) Load() ([]channel.EpochPoint, error) {
	return s.store.LoadHistory()
}

func (s appendBenchmarkEpochHistoryStore) Append(point channel.EpochPoint) error {
	return s.store.AppendHistory(point)
}

func (s appendBenchmarkEpochHistoryStore) TruncateTo(leo uint64) error {
	return s.store.TruncateHistoryTo(leo)
}

type appendBenchmarkSnapshotApplier struct {
	store *channelstore.ChannelStore
}

func (s appendBenchmarkSnapshotApplier) InstallSnapshot(_ context.Context, snap channel.Snapshot) error {
	return s.store.StoreSnapshotPayload(snap.Payload)
}

type appendBenchmarkGenerationStore struct {
	values sync.Map
}

func (s *appendBenchmarkGenerationStore) Load(key channel.ChannelKey) (uint64, error) {
	if value, ok := s.values.Load(key); ok {
		return value.(uint64), nil
	}
	return 0, nil
}

func (s *appendBenchmarkGenerationStore) Store(key channel.ChannelKey, generation uint64) error {
	s.values.Store(key, generation)
	return nil
}

type appendBenchmarkMessageIDs struct {
	next atomic.Uint64
}

func (g *appendBenchmarkMessageIDs) Next() uint64 {
	return g.next.Add(1)
}

type appendBenchmarkTransport struct{}

func (appendBenchmarkTransport) Close() error {
	return nil
}

func appendBenchmarkChannelKey(id channel.ChannelID) channel.ChannelKey {
	encodedID := base64.RawURLEncoding.EncodeToString([]byte(id.ID))
	return channel.ChannelKey("channel/" + strconv.FormatUint(uint64(id.Type), 10) + "/" + encodedID)
}

func commitModeName(mode channel.CommitMode) string {
	switch mode {
	case channel.CommitModeLocal:
		return "local"
	default:
		return "quorum"
	}
}
