package replica

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

func TestReplicaStressPooledLightActiveChannels(t *testing.T) {
	if os.Getenv("WK_REPLICA_POOLED_STRESS") != "1" {
		t.Skip("set WK_REPLICA_POOLED_STRESS=1 to enable")
	}

	channels := stressEnvInt("WK_REPLICA_POOLED_STRESS_CHANNELS", 1000)
	workers := stressEnvInt("WK_REPLICA_POOLED_STRESS_WORKERS", 8)
	pool, err := NewExecutionPool(ExecutionPoolConfig{
		Workers:           workers,
		AppendWorkers:     workers,
		CheckpointWorkers: workers / 2,
		MailboxSize:       1024,
		EffectQueueSize:   1024,
		TurnBudget:        8,
	})
	if err != nil {
		t.Fatalf("NewExecutionPool() error = %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	before := runtime.NumGoroutine()
	reps := make([]Replica, 0, channels)
	for i := 0; i < channels; i++ {
		env := newTestEnv(t)
		cfg := env.config()
		cfg.Execution = ExecutionConfig{Mode: ExecutionModePooled, Pool: pool, MailboxSize: 1024}
		rep, err := NewReplica(cfg)
		if err != nil {
			t.Fatalf("NewReplica(%d) error = %v", i, err)
		}
		meta := activeMetaWithMinISR(7, 1, 1)
		if err := rep.ApplyMeta(meta); err != nil {
			t.Fatalf("ApplyMeta(%d) error = %v", i, err)
		}
		if err := rep.BecomeLeader(meta); err != nil {
			t.Fatalf("BecomeLeader(%d) error = %v", i, err)
		}
		reps = append(reps, rep)
	}
	t.Cleanup(func() {
		for _, rep := range reps {
			_ = rep.Close()
		}
	})

	for i, rep := range reps {
		_, err := rep.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		if err != nil {
			t.Fatalf("Append(%d) error = %v", i, err)
		}
	}

	t.Logf("pooled_light_active channels=%d workers=%d goroutines_delta=%d", channels, workers, runtime.NumGoroutine()-before)
}

func stressEnvInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}
