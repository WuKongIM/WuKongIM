package replica

import "testing"

func TestPooledLoopSerializesReplicaCommands(t *testing.T) {
	pool, err := NewExecutionPool(ExecutionPoolConfig{
		Workers:     2,
		MailboxSize: 64,
		TurnBudget:  4,
	})
	if err != nil {
		t.Fatalf("NewExecutionPool() error = %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	env := newTestEnv(t)
	cfg := env.config()
	cfg.Execution = ExecutionConfig{
		Mode:        ExecutionModePooled,
		Pool:        pool,
		MailboxSize: 64,
		TurnBudget:  4,
	}

	got, err := NewReplica(cfg)
	if err != nil {
		t.Fatalf("NewReplica() error = %v", err)
	}
	t.Cleanup(func() { _ = got.Close() })

	for i := 0; i < 100; i++ {
		if err := got.ApplyMeta(activeMetaWithMinISR(uint64(i+1), 1, 1)); err != nil {
			t.Fatalf("ApplyMeta(%d) error = %v", i+1, err)
		}
	}
}
