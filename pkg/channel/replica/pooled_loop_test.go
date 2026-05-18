package replica

import (
	"context"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

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

func TestPooledLoopCommandWaitPrefersReadyReplyOverDone(t *testing.T) {
	reply := make(chan machineResult, 1)
	done := make(chan struct{})
	stop := make(chan struct{})
	reply <- machineResult{}
	close(done)
	close(stop)

	result := awaitPooledLoopCommandResult(context.Background(), reply, done, stop)

	if result.Err != nil {
		t.Fatalf("awaitPooledLoopCommandResult() error = %v", result.Err)
	}
}

func TestPooledLoopCommandWaitReturnsNotLeaderWhenDoneWinsWithoutReply(t *testing.T) {
	reply := make(chan machineResult, 1)
	done := make(chan struct{})
	stop := make(chan struct{})
	close(done)

	result := awaitPooledLoopCommandResult(context.Background(), reply, done, stop)

	if result.Err != channel.ErrNotLeader {
		t.Fatalf("awaitPooledLoopCommandResult() error = %v, want %v", result.Err, channel.ErrNotLeader)
	}
}

func TestPooledLoopMessageStoresCommandInline(t *testing.T) {
	field, ok := reflect.TypeOf(pooledLoopMessage{}).FieldByName("command")
	if !ok {
		t.Fatal("pooledLoopMessage.command field missing")
	}
	if field.Type.Kind() == reflect.Pointer {
		t.Fatalf("pooledLoopMessage.command is %s; store commands inline to avoid per-submit pointer allocation", field.Type)
	}
}

func TestPooledAppendFlushDoesNotSpawnTimerGoroutinePerReplica(t *testing.T) {
	pool, err := NewExecutionPool(ExecutionPoolConfig{
		Workers:     4,
		MailboxSize: 64,
		TurnBudget:  4,
	})
	if err != nil {
		t.Fatalf("NewExecutionPool() error = %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	reps := make([]*replica, 0, 200)
	for i := 0; i < 200; i++ {
		env := newTestEnv(t)
		cfg := env.config()
		cfg.AppendGroupCommitMaxWait = time.Second
		cfg.Execution = ExecutionConfig{
			Mode:        ExecutionModePooled,
			Pool:        pool,
			MailboxSize: 64,
			TurnBudget:  4,
		}
		got, err := NewReplica(cfg)
		if err != nil {
			t.Fatalf("NewReplica(%d) error = %v", i, err)
		}
		rep := got.(*replica)
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

	afterCreate := runtime.NumGoroutine()
	for i, rep := range reps {
		ctx := channel.WithCommitMode(context.Background(), channel.CommitModeLocal)
		req := &appendRequest{
			ctx:        ctx,
			batch:      []channel.Record{{Payload: []byte("x"), SizeBytes: 1}},
			byteCount:  1,
			commitMode: channel.CommitModeLocal,
			waiter:     &appendWaiter{ch: make(chan appendCompletion, 1), enqueuedAt: rep.now()},
			enqueuedAt: rep.now(),
		}
		result := rep.submitLoopCommand(ctx, machineAppendRequestCommand{Request: req, Now: rep.now()})
		if result.Err != nil {
			t.Fatalf("append request %d error = %v", i, result.Err)
		}
	}
	time.Sleep(20 * time.Millisecond)

	if delta := runtime.NumGoroutine() - afterCreate; delta > 60 {
		t.Fatalf("goroutines delta after scheduling append flushes = %d, want <= 60", delta)
	}
}

func TestPooledExecutionDoesNotStartDurableWorkerPerReplica(t *testing.T) {
	before := runtime.NumGoroutine()
	pool, err := NewExecutionPool(ExecutionPoolConfig{
		Workers:           4,
		AppendWorkers:     4,
		CheckpointWorkers: 2,
	})
	if err != nil {
		t.Fatalf("NewExecutionPool() error = %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	reps := make([]Replica, 0, 200)
	for i := 0; i < 200; i++ {
		env := newTestEnv(t)
		cfg := env.config()
		cfg.Execution = ExecutionConfig{Mode: ExecutionModePooled, Pool: pool}
		rep, err := NewReplica(cfg)
		if err != nil {
			t.Fatalf("NewReplica(%d) error = %v", i, err)
		}
		reps = append(reps, rep)
	}
	t.Cleanup(func() {
		for _, rep := range reps {
			_ = rep.Close()
		}
	})

	if delta := runtime.NumGoroutine() - before; delta > 80 {
		t.Fatalf("goroutines delta = %d, want <= 80", delta)
	}
}
