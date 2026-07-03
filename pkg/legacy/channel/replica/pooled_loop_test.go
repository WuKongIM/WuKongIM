package replica

import (
	"context"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
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

	result, reusable := awaitPooledLoopCommandResult(context.Background(), reply, done, stop)

	if result.Err != nil {
		t.Fatalf("awaitPooledLoopCommandResult() error = %v", result.Err)
	}
	if !reusable {
		t.Fatal("awaitPooledLoopCommandResult() reusable = false, want true after receiving a reply")
	}
}

func TestPooledLoopCommandWaitReturnsNotLeaderWhenDoneWinsWithoutReply(t *testing.T) {
	reply := make(chan machineResult, 1)
	done := make(chan struct{})
	stop := make(chan struct{})
	close(done)

	result, reusable := awaitPooledLoopCommandResult(context.Background(), reply, done, stop)

	if result.Err != channel.ErrNotLeader {
		t.Fatalf("awaitPooledLoopCommandResult() error = %v, want %v", result.Err, channel.ErrNotLeader)
	}
	if reusable {
		t.Fatal("awaitPooledLoopCommandResult() reusable = true, want false when no reply was received")
	}
}

func TestPooledLoopReplyPoolReusesReadyReplies(t *testing.T) {
	reply := acquirePooledLoopReply()
	reply <- machineResult{}
	result, reusable := awaitPooledLoopCommandResult(context.Background(), reply, make(chan struct{}), make(chan struct{}))
	if result.Err != nil {
		t.Fatalf("awaitPooledLoopCommandResult() error = %v", result.Err)
	}
	if !reusable {
		t.Fatal("awaitPooledLoopCommandResult() reusable = false, want true")
	}
	releasePooledLoopReply(reply)

	done := make(chan struct{})
	stop := make(chan struct{})
	allocs := testing.AllocsPerRun(100, func() {
		reply := acquirePooledLoopReply()
		reply <- machineResult{}
		_, reusable := awaitPooledLoopCommandResult(context.Background(), reply, done, stop)
		if reusable {
			releasePooledLoopReply(reply)
		}
	})
	if allocs > 0 {
		t.Fatalf("pooled ready reply allocations = %v, want 0", allocs)
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

func TestPooledLoopDriverDoesNotPreallocateFullMailbox(t *testing.T) {
	pool := &ExecutionPool{
		cfg: ExecutionPoolConfig{
			Now:         time.Now,
			MailboxSize: 2048,
			TurnBudget:  64,
		},
		ready:  make(chan *pooledLoopDriver, 4),
		stopCh: make(chan struct{}),
	}
	r := &replica{loopDone: make(chan struct{})}
	driver := newPooledLoopDriver(r, ExecutionConfig{Mode: ExecutionModePooled, Pool: pool, MailboxSize: 2048, TurnBudget: 64})

	if got := cap(driver.queue); got > 16 {
		t.Fatalf("initial mailbox capacity = %d, want <= 16 to avoid per-replica 2048-slot allocation", got)
	}
	if !driver.pushLocked(pooledLoopMessage{event: testMailboxEvent{id: 1}}) {
		t.Fatal("first pushLocked() = false, want true")
	}
	if got := cap(driver.queue); got > 16 {
		t.Fatalf("mailbox capacity after first push = %d, want <= 16", got)
	}
}

type testMailboxEvent struct{ id int }

func (testMailboxEvent) isMachineEvent() {}

func TestPooledLoopMailboxRingWrapsWithoutDropping(t *testing.T) {
	pool := &ExecutionPool{
		cfg: ExecutionPoolConfig{
			Now:         time.Now,
			MailboxSize: 4,
			TurnBudget:  4,
		},
		ready:  make(chan *pooledLoopDriver, 4),
		stopCh: make(chan struct{}),
	}
	r := &replica{loopDone: make(chan struct{})}
	driver := newPooledLoopDriver(r, ExecutionConfig{Mode: ExecutionModePooled, Pool: pool, MailboxSize: 4, TurnBudget: 4})

	driver.mu.Lock()
	for i := 0; i < 4; i++ {
		if !driver.pushLocked(pooledLoopMessage{event: testMailboxEvent{id: i}}) {
			t.Fatalf("pushLocked(%d) = false, want true", i)
		}
	}
	if driver.pushLocked(pooledLoopMessage{event: testMailboxEvent{id: 99}}) {
		t.Fatal("pushLocked() accepted a full mailbox")
	}
	for i := 0; i < 2; i++ {
		msg, ok := driver.popLocked()
		if !ok {
			t.Fatalf("popLocked(%d) = false, want true", i)
		}
		if got := msg.event.(testMailboxEvent).id; got != i {
			t.Fatalf("popLocked(%d) id = %d, want %d", i, got, i)
		}
	}
	for i := 4; i < 6; i++ {
		if !driver.pushLocked(pooledLoopMessage{event: testMailboxEvent{id: i}}) {
			t.Fatalf("wrap pushLocked(%d) = false, want true", i)
		}
	}

	var got []int
	for driver.queueLenLocked() > 0 {
		msg, ok := driver.popLocked()
		if !ok {
			t.Fatal("popLocked() returned false before queueLenLocked reached zero")
		}
		got = append(got, msg.event.(testMailboxEvent).id)
	}
	driver.mu.Unlock()

	want := []int{2, 3, 4, 5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("remaining pop order = %v, want %v", got, want)
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
