//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNodeDefaultChannelsUseDurableMessageDBStore(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.HealthReport.Interval = 500 * time.Millisecond
	cfg.HealthReport.TTL = 2 * time.Second
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := WaitNodeReady(readyCtx, node); err != nil {
		readyCancel()
		t.Fatalf("WaitNodeReady() error = %v", err)
	}
	readyCancel()
	waitChannelDataNode(t, node, 1)
	channelID := channelruntime.ChannelID{ID: "durable", Type: 1}
	applyDefaultChannelMeta(t, node, channelID)
	first, err := node.AppendChannel(context.Background(), channelruntime.AppendRequest{
		ChannelID: channelID,
		Message:   channelruntime.Message{MessageID: 100, Payload: []byte("persisted")},
	})
	if err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if first.MessageSeq != 2 {
		t.Fatalf("AppendChannel() MessageSeq = %d, want 2 after the authority barrier", first.MessageSeq)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("restart Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	restartReadyCtx, restartReadyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := WaitNodeReady(restartReadyCtx, node); err != nil {
		restartReadyCancel()
		t.Fatalf("restart WaitNodeReady() error = %v", err)
	}
	restartReadyCancel()
	waitChannelDataNode(t, node, 1)
	applyDefaultChannelMeta(t, node, channelID)
	second, err := node.AppendChannel(context.Background(), channelruntime.AppendRequest{
		ChannelID: channelID,
		Message:   channelruntime.Message{MessageID: 101, Payload: []byte("after-restart")},
	})
	if err != nil {
		t.Fatalf("restart AppendChannel() error = %v", err)
	}
	if second.MessageSeq != 3 {
		t.Fatalf("restart AppendChannel() MessageSeq = %d, want 3 from durable message DB LEO", second.MessageSeq)
	}
}

func TestNodeReadChannelCommittedHonorsRetentionThroughSeq(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	waitChannelDataNode(t, node, 1)

	id := channelruntime.ChannelID{ID: "retained-read", Type: 1}
	route := waitRouteKeyLeaderReady(t, node, id.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 4; i++ {
		_, err := node.AppendChannel(ctx, channelruntime.AppendRequest{
			ChannelID: id,
			Message:   channelruntime.Message{MessageID: uint64(100 + i), Payload: []byte{byte('a' + i)}},
		})
		if err != nil {
			t.Fatalf("AppendChannel(%d) error = %v", i, err)
		}
	}
	meta, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if err := node.AdvanceChannelRetentionThroughSeq(ctx, metadb.ChannelRetentionAdvance{
		ChannelID:            id.ID,
		ChannelType:          int64(id.Type),
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedLeaseUntilMS: meta.LeaseUntilMS,
		RetentionThroughSeq:  2,
		RetentionUpdatedAtMS: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("AdvanceChannelRetentionThroughSeq() error = %v", err)
	}
	tracking := newNodeTrackingStoreFactory(node.defaultChannelStore)
	node.channelStoreFactory = tracking

	forward, err := node.ReadChannelCommitted(ctx, id, channelstore.ReadCommittedRequest{FromSeq: 1, MaxSeq: 4, Limit: 10, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("ReadChannelCommitted(forward) error = %v", err)
	}
	if got := nodeMessageSeqs(forward.Messages); !equalNodeMessageSeqs(got, []uint64{3, 4}) {
		t.Fatalf("forward seqs = %v, want [3 4]", got)
	}
	reverse, err := node.ReadChannelCommitted(ctx, id, channelstore.ReadCommittedRequest{FromSeq: 4, MaxSeq: 4, Limit: 10, MaxBytes: 1024, Reverse: true})
	if err != nil {
		t.Fatalf("ReadChannelCommitted(reverse) error = %v", err)
	}
	if got := nodeMessageSeqs(reverse.Messages); !equalNodeMessageSeqs(got, []uint64{4, 3}) {
		t.Fatalf("reverse seqs = %v, want [4 3]", got)
	}
	if got := tracking.Acquired(); got != 2 {
		t.Fatalf("ChannelStore acquisitions = %d, want 2", got)
	}
	if got := tracking.Closed(); got != 2 {
		t.Fatalf("ChannelStore closes = %d, want 2", got)
	}
}

func TestNodeLookupChannelIdempotencyUsesDefaultStore(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	waitChannelDataNode(t, node, 1)

	id := channelruntime.ChannelID{ID: "idempotency-read", Type: 1}
	applyDefaultChannelMeta(t, node, id)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := node.AppendChannel(ctx, channelruntime.AppendRequest{
		ChannelID: id,
		Message: channelruntime.Message{
			MessageID:   501,
			FromUID:     "u1",
			ClientMsgNo: "client-1",
			Payload:     []byte("payload"),
		},
	}); err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	tracking := newNodeTrackingStoreFactory(node.defaultChannelStore)
	node.channelStoreFactory = tracking

	hit, ok, err := node.LookupChannelIdempotency(ctx, id, "u1", "client-1")
	if err != nil {
		t.Fatalf("LookupChannelIdempotency() error = %v", err)
	}
	if !ok {
		t.Fatal("LookupChannelIdempotency() ok = false, want true")
	}
	if hit.Message.MessageID != 501 || hit.Message.MessageSeq != 2 || hit.Message.FromUID != "u1" || hit.Message.ClientMsgNo != "client-1" {
		t.Fatalf("LookupChannelIdempotency() hit = %#v, want committed message", hit)
	}
	if hit.PayloadHash == 0 {
		t.Fatalf("LookupChannelIdempotency() payload hash = 0, want persisted hash")
	}
	if got := tracking.Acquired(); got != 1 {
		t.Fatalf("ChannelStore acquisitions = %d, want 1", got)
	}
	if got := tracking.Closed(); got != 1 {
		t.Fatalf("ChannelStore closes = %d, want 1", got)
	}
}
