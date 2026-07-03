package replica

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

func BenchmarkReplicaAppendHotPath(b *testing.B) {
	for _, mode := range []channel.CommitMode{channel.CommitModeLocal, channel.CommitModeQuorum} {
		b.Run(commitModeBenchmarkName(mode), func(b *testing.B) {
			for _, batchSize := range []int{1, 16, 64} {
				b.Run(fmt.Sprintf("batch=%d", batchSize), func(b *testing.B) {
					env := newTestEnv(b)
					r := newReplicaFromEnvWithGroupCommit(b, env, time.Hour, 1, 1<<30)
					defer func() { _ = r.Close() }()
					meta := activeMetaWithMinISR(7, 1, 1)
					r.mustApplyMeta(b, meta)
					if err := r.BecomeLeader(meta); err != nil {
						b.Fatalf("BecomeLeader() error = %v", err)
					}
					records := benchmarkRecords(batchSize, 128)
					ctx := channel.WithCommitMode(context.Background(), mode)

					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if _, err := r.Append(ctx, records); err != nil {
							b.Fatalf("Append() error = %v", err)
						}
					}
				})
			}
		})
	}
}

func BenchmarkPooledMailboxSubmitResult(b *testing.B) {
	pool := &ExecutionPool{
		cfg: ExecutionPoolConfig{
			Now:         time.Now,
			MailboxSize: 1024,
			TurnBudget:  64,
		},
		ready:  make(chan *pooledLoopDriver, 1024),
		stopCh: make(chan struct{}),
	}
	r := &replica{
		now:      time.Now,
		stopCh:   make(chan struct{}),
		loopDone: make(chan struct{}),
		state: channel.ReplicaState{
			Role:        channel.ReplicaRoleFollower,
			CommitReady: true,
		},
	}
	r.publishStateLocked()
	driver := newPooledLoopDriver(r, ExecutionConfig{Mode: ExecutionModePooled, Pool: pool, MailboxSize: 1024, TurnBudget: 64})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := driver.submitResult(context.Background(), machineAdvanceHWEvent{}); err != nil {
			b.Fatalf("submitResult() error = %v", err)
		}
		ready := <-pool.ready
		ready.drain()
	}
}

func BenchmarkQuorumProgressCandidate(b *testing.B) {
	for _, replicas := range []int{3, 5, 9, 16, 32, 64} {
		b.Run(fmt.Sprintf("isr=%d", replicas), func(b *testing.B) {
			isr := make([]channel.NodeID, replicas)
			progress := make(map[channel.NodeID]uint64, replicas)
			for i := range isr {
				id := channel.NodeID(i + 1)
				isr[i] = id
				progress[id] = uint64(1000 + i)
			}
			minISR := replicas/2 + 1

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, ok, err := quorumProgressCandidate(isr, progress, minISR, 1000, 2000); err != nil || !ok {
					b.Fatalf("quorumProgressCandidate() ok=%v err=%v", ok, err)
				}
			}
		})
	}
}

func commitModeBenchmarkName(mode channel.CommitMode) string {
	switch mode {
	case channel.CommitModeLocal:
		return "local"
	case channel.CommitModeQuorum:
		return "quorum"
	default:
		return "unknown"
	}
}

func benchmarkRecords(n int, payloadBytes int) []channel.Record {
	records := make([]channel.Record, n)
	payload := make([]byte, payloadBytes)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	for i := range records {
		records[i] = channel.Record{Payload: append([]byte(nil), payload...), SizeBytes: payloadBytes}
	}
	return records
}

func BenchmarkReplicaApplyFollowerCursor(b *testing.B) {
	r := newLeaderReplica(b)
	defer func() { _ = r.Close() }()

	r.mu.Lock()
	r.state.LEO = 1 << 40
	r.setReplicaProgressLocked(r.localNode, r.state.LEO)
	r.publishStateLocked()
	epoch := r.state.Epoch
	channelKey := r.state.ChannelKey
	r.mu.Unlock()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := channel.ReplicaFollowerCursorUpdate{
			ChannelKey:  channelKey,
			Epoch:       epoch,
			ReplicaID:   2,
			MatchOffset: uint64(i + 1),
			OffsetEpoch: epoch,
		}
		if err := r.ApplyFollowerCursor(context.Background(), req); err != nil {
			b.Fatalf("ApplyFollowerCursor() error = %v", err)
		}
	}
}

func BenchmarkDurableLaneAcquireRelease(b *testing.B) {
	r := &replica{}
	r.initDurableLane()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		release, err := r.acquireDurableLane(context.Background())
		if err != nil {
			b.Fatalf("acquireDurableLane() error = %v", err)
		}
		release()
	}
}

func BenchmarkReplicaAppendCloneVsOwned(b *testing.B) {
	for _, owned := range []bool{false, true} {
		name := "clone"
		if owned {
			name = "owned"
		}
		b.Run(name, func(b *testing.B) {
			env := newTestEnv(b)
			r := newReplicaFromEnvWithGroupCommit(b, env, time.Hour, 1, 1<<30)
			defer func() { _ = r.Close() }()
			meta := activeMetaWithMinISR(7, 1, 1)
			r.mustApplyMeta(b, meta)
			if err := r.BecomeLeader(meta); err != nil {
				b.Fatalf("BecomeLeader() error = %v", err)
			}
			records := benchmarkRecords(1, 1024)
			ctx := channel.WithCommitMode(context.Background(), channel.CommitModeLocal)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var err error
				if owned {
					_, err = r.AppendOwned(ctx, records)
				} else {
					_, err = r.Append(ctx, records)
				}
				if err != nil {
					b.Fatalf("append error = %v", err)
				}
			}
		})
	}
}
