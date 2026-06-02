package delivery

import (
	"context"
	"strconv"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
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

func BenchmarkRecipientAckTrackerRecvack(b *testing.B) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 64, Now: func() int64 { return 100 }})

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sessionID := uint64(i%10000 + 1)
		messageID := uint64(i + 1)
		uid := "u" + strconv.Itoa(i%10000)
		tracker.Bind(PendingRecvAck{UID: uid, SessionID: sessionID, MessageID: messageID})
		_, _ = tracker.Ack(Recvack{UID: uid, SessionID: sessionID, MessageID: messageID})
	}
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

func BenchmarkDeliveryEndToEndNoCluster(b *testing.B) {
	uids := benchmarkUIDs(256)
	manager := NewManager(ManagerOptions{
		Planner: NewPlanner(PlannerOptions{}),
		Worker: NewFanoutWorker(FanoutWorkerOptions{
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
		if err := manager.SubmitCommitted(context.Background(), event); err != nil {
			b.Fatalf("SubmitCommitted() error = %v", err)
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
