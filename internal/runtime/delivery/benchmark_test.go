package delivery

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
)

func BenchmarkPlannerPartition100K(b *testing.B) {
	planner := NewPlanner(PlannerOptions{Partitioner: benchmarkPartitions(64)})
	env := Envelope{ChannelID: "g1", ChannelType: 2, MessageID: 1, MessageSeq: 1}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tasks, err := planner.Plan(context.Background(), env)
		if err != nil {
			b.Fatalf("Plan() error = %v", err)
		}
		if len(tasks) != 64 {
			b.Fatalf("Plan() tasks = %d, want 64", len(tasks))
		}
	}
}

func BenchmarkAckTrackerUniqueBindFinishAck(b *testing.B) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 64, Now: func() int64 { return 100 }})

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sessionID := uint64(i%10000 + 1)
		messageID := uint64(i + 1)
		uid := "u" + strconv.Itoa(i%10000)
		pending := PendingRecvAck{UID: uid, SessionID: sessionID, MessageID: messageID}
		bound := tracker.BindResult(pending)
		if !bound.Bound || !tracker.FinishBind(pending, bound.Token) {
			b.Fatal("unique bind/finish failed")
		}
		_, _ = tracker.Ack(Recvack{UID: uid, SessionID: sessionID, MessageID: messageID})
	}
}

func BenchmarkManagerCommittedRefresh55RoutesParallel(b *testing.B) {
	pending := make([]PendingRecvAck, 55)
	indexes := make([]int, 55)
	for i := range pending {
		pending[i] = PendingRecvAck{
			UID:       "u" + strconv.Itoa(i),
			SessionID: uint64(i + 1),
			MessageID: 1001,
		}
		indexes[i] = i
	}

	b.Run("single_refresh", func(b *testing.B) {
		manager := NewManager(ManagerOptions{
			Acks:        NewAckTracker(AckTrackerOptions{ShardCount: 32}),
			AckObserver: &benchmarkAckObserver{},
		})
		for _, item := range pending {
			if !manager.BindPendingAck(item) {
				b.Fatal("warm BindPendingAck() = false")
			}
		}
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				for _, item := range pending {
					if !manager.BindPendingAck(item) {
						panic("BindPendingAck() = false")
					}
				}
			}
		})
	})
	b.Run("batch_refresh", func(b *testing.B) {
		manager := NewManager(ManagerOptions{
			Acks:        NewAckTracker(AckTrackerOptions{ShardCount: 32}),
			AckObserver: &benchmarkAckObserver{},
		})
		warm := manager.BindPendingAcks(pending)
		if warm.Added != len(pending) {
			b.Fatalf("warm BindPendingAcks() Added = %d, want %d", warm.Added, len(pending))
		}
		if finished := manager.FinishPendingAcks(pending, warm.Tokens, indexes, 0); finished != len(pending) {
			b.Fatalf("warm FinishPendingAcks() = %d, want %d", finished, len(pending))
		}
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				result := manager.BindPendingAcks(pending)
				if len(result.Tokens) != len(pending) || !result.Tokens[len(result.Tokens)-1].Valid() {
					panic("BindPendingAcks() returned unaligned result")
				}
				if finished := manager.FinishPendingAcks(pending, result.Tokens, indexes, 0); finished != len(pending) {
					panic("FinishPendingAcks() returned incomplete result")
				}
			}
		})
	})
}

func BenchmarkFanoutWorkerLocalPresence(b *testing.B) {
	uids := benchmarkUIDs(512)
	worker := NewFanoutWorker(FanoutWorkerOptions{
		Subscribers: benchmarkSubscriber{uids: uids},
		Presence:    benchmarkPresence{ownerNodeID: 1},
		Push:        &benchmarkPusher{},
		PageSize:    len(uids),
	})
	task := FanoutTask{
		Envelope:  Envelope{MessageID: 1, MessageSeq: 1, ChannelID: "g1", ChannelType: 2},
		Partition: Partition{ID: 1, LeaderNodeID: 1},
		Attempt:   1,
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := worker.RunTask(context.Background(), task); err != nil {
			b.Fatalf("RunTask() error = %v", err)
		}
	}
}

func BenchmarkFanoutWorkerRemotePushBatch(b *testing.B) {
	uids := benchmarkUIDs(512)
	worker := NewFanoutWorker(FanoutWorkerOptions{
		Subscribers:   benchmarkSubscriber{uids: uids},
		Presence:      benchmarkPresence{ownerNodeID: 2},
		Push:          &benchmarkPusher{},
		PageSize:      len(uids),
		PushBatchSize: 128,
	})
	task := FanoutTask{
		Envelope:  Envelope{MessageID: 1, MessageSeq: 1, ChannelID: "g1", ChannelType: 2},
		Partition: Partition{ID: 1, LeaderNodeID: 1},
		Attempt:   1,
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := worker.RunTask(context.Background(), task); err != nil {
			b.Fatalf("RunTask() error = %v", err)
		}
	}
}

func BenchmarkChannelSubscriberPlannerScan100K(b *testing.B) {
	uids := benchmarkUIDs(100000)
	planner := NewChannelSubscriberPlanner(ChannelSubscriberPlannerOptions{
		Source: benchmarkChannelSubscriberSource{uids: uids},
	})
	task := FanoutTask{
		Envelope:  Envelope{MessageID: 1, MessageSeq: 1, ChannelID: "g1", ChannelType: 2},
		Partition: Partition{ID: 1, LeaderNodeID: 1},
		Attempt:   1,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cursor := ""
		total := 0
		for {
			page, err := planner.NextPartitionPage(context.Background(), task, cursor, 1024)
			if err != nil {
				b.Fatalf("NextPartitionPage() error = %v", err)
			}
			total += len(page.UIDs)
			if page.Done {
				break
			}
			cursor = page.NextCursor
		}
		if total != len(uids) {
			b.Fatalf("scanned UIDs = %d, want %d", total, len(uids))
		}
	}
}

func BenchmarkFanoutTaskRouterLocal(b *testing.B) {
	router := NewFanoutTaskRouter(FanoutTaskRouterOptions{
		LocalNodeID: 1,
		Local:       benchmarkFanoutTaskRunner{},
		Remote:      benchmarkFanoutForwarder{},
	})
	task := FanoutTask{Envelope: Envelope{MessageID: 1}, Partition: Partition{ID: 1, LeaderNodeID: 1}}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := router.RunTask(context.Background(), task); err != nil {
			b.Fatalf("RunTask() error = %v", err)
		}
	}
}

func BenchmarkFanoutTaskRouterRemote(b *testing.B) {
	router := NewFanoutTaskRouter(FanoutTaskRouterOptions{
		LocalNodeID: 1,
		Local:       benchmarkFanoutTaskRunner{},
		Remote:      benchmarkFanoutForwarder{},
	})
	task := FanoutTask{Envelope: Envelope{MessageID: 1}, Partition: Partition{ID: 1, LeaderNodeID: 2}}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := router.RunTask(context.Background(), task); err != nil {
			b.Fatalf("RunTask() error = %v", err)
		}
	}
}

func BenchmarkRetrySchedulerEnqueue(b *testing.B) {
	scheduler := NewRetryScheduler(RetrySchedulerOptions{
		Runner:      benchmarkRetryableFanoutTaskRunner{},
		Capacity:    1024,
		MaxAttempts: 3,
	})
	task := FanoutTask{Envelope: Envelope{MessageID: 1}, Partition: Partition{ID: 1}, Attempt: 1}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := scheduler.RunTask(context.Background(), task); err != nil {
			b.Fatalf("RunTask() error = %v", err)
		}
		select {
		case <-scheduler.queue:
		default:
			b.Fatal("retry queue was empty")
		}
	}
}

func BenchmarkManagerAsyncSubmitCommitted(b *testing.B) {
	runner := &benchmarkCountingFanoutTaskRunner{}
	manager := NewManager(ManagerOptions{
		Planner:        NewPlanner(PlannerOptions{}),
		Runner:         runner,
		AsyncQueueSize: defaultManagerAsyncQueueSize,
		AsyncWorkers:   defaultManagerAsyncWorkers,
	})
	if err := manager.Start(context.Background()); err != nil {
		b.Fatalf("Start() error = %v", err)
	}
	event := messageevents.MessageCommitted{
		MessageID:         1,
		MessageSeq:        1,
		ChannelID:         "g1",
		ChannelType:       2,
		FromUID:           "sender",
		MessageScopedUIDs: []string{"u1"},
		Payload:           []byte("payload"),
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event.MessageID = uint64(i + 1)
		event.MessageSeq = uint64(i + 1)
		if err := manager.SubmitCommitted(ctx, event); err != nil {
			b.Fatalf("SubmitCommitted() error = %v", err)
		}
	}
	b.StopTimer()
	if err := manager.Stop(context.Background()); err != nil {
		b.Fatalf("Stop() error = %v", err)
	}
	if got := runner.count.Load(); got != uint64(b.N) {
		b.Fatalf("runner count = %d, want %d", got, b.N)
	}
}

func BenchmarkDeliveryEndToEndNoCluster(b *testing.B) {
	uids := benchmarkUIDs(256)
	manager := NewManager(ManagerOptions{
		Planner: NewPlanner(PlannerOptions{}),
		Runner: NewFanoutWorker(FanoutWorkerOptions{
			Presence:      benchmarkPresence{ownerNodeID: 1},
			Push:          &benchmarkPusher{},
			PushBatchSize: 128,
		}),
		Acks: NewAckTracker(AckTrackerOptions{ShardCount: 64, Now: func() int64 { return 100 }}),
	})
	event := messageevents.MessageCommitted{
		MessageID:         1,
		MessageSeq:        1,
		ChannelID:         "g1",
		ChannelType:       2,
		FromUID:           "sender",
		MessageScopedUIDs: uids,
		Payload:           []byte("payload"),
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		event.MessageID = uint64(i + 1)
		event.MessageSeq = uint64(i + 1)
		if err := manager.runEnvelope(context.Background(), envelopeFromEvent(event)); err != nil {
			b.Fatalf("runEnvelope() error = %v", err)
		}
	}
}

type benchmarkPartitionList []Partition

func (p benchmarkPartitionList) Partitions(context.Context) ([]Partition, error) {
	return append([]Partition(nil), p...), nil
}

func benchmarkPartitions(count int) benchmarkPartitionList {
	partitions := make([]Partition, 0, count)
	for i := 0; i < count; i++ {
		partitions = append(partitions, Partition{
			ID:            uint32(i + 1),
			LeaderNodeID:  uint64(i%3 + 1),
			HashSlotStart: uint16(i * 16),
			HashSlotEnd:   uint16(i*16 + 15),
		})
	}
	return benchmarkPartitionList(partitions)
}

type benchmarkSubscriber struct {
	uids []string
}

func (s benchmarkSubscriber) NextPartitionPage(context.Context, FanoutTask, string, int) (UIDPage, error) {
	return UIDPage{UIDs: s.uids, Done: true}, nil
}

type benchmarkChannelSubscriberSource struct {
	uids []string
}

func (s benchmarkChannelSubscriberSource) ListSubscribers(_ context.Context, req SubscriberPageRequest) (UIDPage, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultFanoutPageSize
	}
	start := 0
	if req.Cursor != "" {
		parsed, err := strconv.Atoi(req.Cursor)
		if err != nil {
			return UIDPage{}, err
		}
		start = parsed
	}
	if start >= len(s.uids) {
		return UIDPage{Done: true}, nil
	}
	end := start + limit
	if end > len(s.uids) {
		end = len(s.uids)
	}
	page := UIDPage{
		UIDs: s.uids[start:end],
		Done: end == len(s.uids),
	}
	if !page.Done {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

type benchmarkFanoutTaskRunner struct{}

func (benchmarkFanoutTaskRunner) RunTask(context.Context, FanoutTask) error {
	return nil
}

type benchmarkRetryableFanoutTaskRunner struct{}

func (benchmarkRetryableFanoutTaskRunner) RunTask(context.Context, FanoutTask) error {
	return ErrRetryableFanoutTask
}

type benchmarkCountingFanoutTaskRunner struct {
	count atomic.Uint64
}

type benchmarkAckObserver struct {
	pending atomic.Int64
}

func (o *benchmarkAckObserver) ObserveAck(event AckEvent) {
	o.pending.Store(int64(event.PendingCount))
}

func (r *benchmarkCountingFanoutTaskRunner) RunTask(context.Context, FanoutTask) error {
	r.count.Add(1)
	return nil
}

type benchmarkFanoutForwarder struct{}

func (benchmarkFanoutForwarder) ForwardFanoutTask(context.Context, uint64, FanoutTask) error {
	return nil
}

type benchmarkPresence struct {
	ownerNodeID uint64
}

func (p benchmarkPresence) EndpointsByUIDs(_ context.Context, uids []string) (map[string][]Route, error) {
	out := make(map[string][]Route, len(uids))
	for i, uid := range uids {
		out[uid] = []Route{{
			UID:         uid,
			OwnerNodeID: p.ownerNodeID,
			OwnerBootID: 1,
			OwnerSeq:    uint64(i + 1),
			SessionID:   uint64(i + 1),
		}}
	}
	return out, nil
}

type benchmarkPusher struct{}

func (*benchmarkPusher) Push(_ context.Context, cmd PushCommand) (PushResult, error) {
	return PushResult{Accepted: append([]Route(nil), cmd.Routes...)}, nil
}

func benchmarkUIDs(count int) []string {
	uids := make([]string, 0, count)
	for i := 0; i < count; i++ {
		uids = append(uids, "u"+strconv.Itoa(i))
	}
	return uids
}
