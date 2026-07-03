package runtime

import (
	"context"
	"errors"
	"fmt"
	goruntime "runtime"
	"sync"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	channelreplica "github.com/WuKongIM/WuKongIM/pkg/legacy/channel/replica"
)

func TestRuntimeStressChannelActivationFootprint(t *testing.T) {
	cfg, err := loadActivationPressureConfig()
	if err != nil {
		t.Fatalf("loadActivationPressureConfig() error = %v", err)
	}
	if !cfg.stressEnabled {
		t.Skip("set MULTIISR_ACTIVATION_STRESS=1 to enable")
	}

	beforeGoroutines := goruntime.NumGoroutine()
	var beforeMem goruntime.MemStats
	goruntime.ReadMemStats(&beforeMem)

	var executionPool *channelreplica.ExecutionPool
	if cfg.executionMode == "pooled" {
		executionPool, err = channelreplica.NewExecutionPool(channelreplica.ExecutionPoolConfig{
			Workers:         cfg.executionWorkers,
			MailboxSize:     cfg.executionQueueSize,
			EffectQueueSize: cfg.executionQueueSize,
		})
		if err != nil {
			t.Fatalf("NewExecutionPool() error = %v", err)
		}
		defer func() {
			if err := executionPool.Close(); err != nil {
				t.Fatalf("execution pool Close() error = %v", err)
			}
		}()
	}

	rt, err := New(Config{
		LocalNode: 1,
		ReplicaFactory: activationReplicaFactory{
			localNode:      1,
			executionMode:  cfg.executionMode,
			executionPool:  executionPool,
			executionQueue: cfg.executionQueueSize,
		},
		GenerationStore:                  newSessionGenerationStore(),
		AutoRunScheduler:                 false,
		FollowerReplicationRetryInterval: time.Hour,
		Limits: Limits{
			MaxChannels: cfg.maxChannels,
		},
		Tombstones: TombstonePolicy{
			TombstoneTTL:    time.Hour,
			CleanupInterval: time.Hour,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	startedAt := time.Now()
	activated := 0
	for i := 0; i < cfg.channels; i++ {
		err := rt.EnsureChannel(testMetaLocal(uint64(i+1), 1, 2, []core.NodeID{1, 2}))
		if errors.Is(err, ErrTooManyChannels) {
			break
		}
		if err != nil {
			t.Fatalf("EnsureChannel(%d) error = %v", i+1, err)
		}
		activated++
		if cfg.reportEvery > 0 && activated%cfg.reportEvery == 0 {
			logActivationPressureSnapshot(t, "progress", startedAt, activated, cfg, beforeGoroutines, beforeMem)
		}
	}

	wantActivated := cfg.channels
	if cfg.maxChannels > 0 && cfg.maxChannels < wantActivated {
		wantActivated = cfg.maxChannels
	}
	if activated != wantActivated {
		t.Fatalf("activated channels = %d, want %d", activated, wantActivated)
	}
	logActivationPressureSnapshot(t, "final", startedAt, activated, cfg, beforeGoroutines, beforeMem)
}

func logActivationPressureSnapshot(t testing.TB, phase string, startedAt time.Time, activated int, cfg activationPressureConfig, beforeGoroutines int, beforeMem goruntime.MemStats) {
	t.Helper()

	var mem goruntime.MemStats
	goruntime.ReadMemStats(&mem)
	t.Logf(
		"activation_pressure phase=%s activated=%d requested=%d max_channels=%d elapsed=%s goroutines_delta=%d heap_alloc_delta=%s heap_sys_delta=%s",
		phase,
		activated,
		cfg.channels,
		cfg.maxChannels,
		time.Since(startedAt),
		goruntime.NumGoroutine()-beforeGoroutines,
		formatBytesDelta(mem.HeapAlloc, beforeMem.HeapAlloc),
		formatBytesDelta(mem.HeapSys, beforeMem.HeapSys),
	)
}

func formatBytesDelta(current, previous uint64) string {
	if current >= previous {
		return formatBytes(current - previous)
	}
	return "-" + formatBytes(previous-current)
}

func formatBytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%dB", v)
	}
	div, exp := uint64(unit), 0
	for n := v / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(v)/float64(div), "KMGTPE"[exp])
}

type activationReplicaFactory struct {
	localNode      core.NodeID
	executionMode  string
	executionPool  *channelreplica.ExecutionPool
	executionQueue int
}

func (f activationReplicaFactory) New(cfg ChannelConfig) (channelreplica.Replica, error) {
	state := &activationReplicaStoreState{}
	execution := channelreplica.ExecutionConfig{}
	if f.executionMode == "pooled" {
		execution = channelreplica.ExecutionConfig{
			Mode:        channelreplica.ExecutionModePooled,
			Pool:        f.executionPool,
			MailboxSize: f.executionQueue,
		}
	}
	return channelreplica.NewReplica(channelreplica.ReplicaConfig{
		LocalNode:         f.localNode,
		LogStore:          activationLogStore{state: state},
		CheckpointStore:   activationCheckpointStore{state: state},
		EpochHistoryStore: activationEpochHistoryStore{state: state},
		SnapshotApplier:   activationSnapshotStore{state: state},
		Execution:         execution,
	})
}

type activationReplicaStoreState struct {
	mu         sync.Mutex
	records    []core.Record
	checkpoint core.Checkpoint
	history    []core.EpochPoint
	snapshot   []byte
	retention  core.RetentionState
}

type activationLogStore struct {
	state *activationReplicaStoreState
}

func (s activationLogStore) LEO() uint64 {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	return uint64(len(s.state.records))
}

func (s activationLogStore) Append(records []core.Record) (uint64, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	base := uint64(len(s.state.records))
	cloned := make([]core.Record, len(records))
	copy(cloned, records)
	for i := range cloned {
		cloned[i].Index = base + uint64(i) + 1
	}
	s.state.records = append(s.state.records, cloned...)
	return base, nil
}

func (s activationLogStore) Read(from uint64, maxBytes int) ([]core.Record, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	if from == 0 || from > uint64(len(s.state.records)) {
		return nil, nil
	}
	out := append([]core.Record(nil), s.state.records[from-1:]...)
	if maxBytes <= 0 {
		return out, nil
	}
	bytes := 0
	limit := len(out)
	for i, record := range out {
		bytes += len(record.Payload)
		if bytes > maxBytes {
			limit = i
			break
		}
	}
	return out[:limit], nil
}

func (s activationLogStore) Truncate(to uint64) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	if to > uint64(len(s.state.records)) {
		return core.ErrInvalidArgument
	}
	s.state.records = append([]core.Record(nil), s.state.records[:to]...)
	return nil
}

func (s activationLogStore) Sync() error { return nil }

func (s activationLogStore) LoadRetentionState() (core.RetentionState, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	return s.state.retention, nil
}

func (s activationLogStore) AdoptRetentionBoundary(_ context.Context, throughSeq uint64, _ string) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	if throughSeq > s.state.retention.LocalRetentionThroughSeq {
		s.state.retention.LocalRetentionThroughSeq = throughSeq
	}
	if throughSeq > s.state.retention.RetainedMaxSeq {
		s.state.retention.RetainedMaxSeq = throughSeq
	}
	return nil
}

func (s activationLogStore) TrimMessagesThrough(_ context.Context, throughSeq uint64) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	if throughSeq > s.state.retention.PhysicalRetentionThroughSeq {
		s.state.retention.PhysicalRetentionThroughSeq = throughSeq
	}
	return nil
}

type activationCheckpointStore struct {
	state *activationReplicaStoreState
}

func (s activationCheckpointStore) Load() (core.Checkpoint, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	if s.state.checkpoint == (core.Checkpoint{}) {
		return core.Checkpoint{}, core.ErrEmptyState
	}
	return s.state.checkpoint, nil
}

func (s activationCheckpointStore) Store(checkpoint core.Checkpoint) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	s.state.checkpoint = checkpoint
	return nil
}

type activationEpochHistoryStore struct {
	state *activationReplicaStoreState
}

func (s activationEpochHistoryStore) Load() ([]core.EpochPoint, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	if len(s.state.history) == 0 {
		return nil, core.ErrEmptyState
	}
	return append([]core.EpochPoint(nil), s.state.history...), nil
}

func (s activationEpochHistoryStore) Append(point core.EpochPoint) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	s.state.history = append(s.state.history, point)
	return nil
}

func (s activationEpochHistoryStore) TruncateTo(leo uint64) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	keep := s.state.history[:0]
	for _, point := range s.state.history {
		if point.StartOffset <= leo {
			keep = append(keep, point)
		}
	}
	s.state.history = append([]core.EpochPoint(nil), keep...)
	return nil
}

type activationSnapshotStore struct {
	state *activationReplicaStoreState
}

func (s activationSnapshotStore) InstallSnapshot(_ context.Context, snap core.Snapshot) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	s.state.snapshot = append([]byte(nil), snap.Payload...)
	return nil
}

func (s activationSnapshotStore) LoadSnapshotPayload(context.Context) ([]byte, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	if s.state.snapshot == nil {
		return nil, core.ErrEmptyState
	}
	return append([]byte(nil), s.state.snapshot...), nil
}
