package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagementMessageRetentionOperatorAdvancesLocalLeaderBoundary(t *testing.T) {
	now := time.UnixMilli(1713859200123)
	node := &recordingRetentionNode{
		nodeID: 7,
		meta: metadb.ChannelRuntimeMeta{
			ChannelID: "room-1", ChannelType: 2,
			ChannelEpoch: 4, LeaderEpoch: 5, Leader: 7, LeaseUntilMS: 1713859300000,
			Replicas: []uint64{7}, ISR: []uint64{7}, MinISR: 1, Status: uint8(channelv2.StatusActive),
		},
		readResult: channelstore.ReadCommittedResult{Messages: []channelv2.Message{{MessageSeq: 3}}},
	}
	operator := NewManagementMessageRetentionOperator(node)
	operator.now = func() time.Time { return now }

	got, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 2,
	})

	if err != nil {
		t.Fatalf("AdvanceMessageRetention() error = %v", err)
	}
	if got.Status != managementusecase.MessageRetentionStatusAdvanced || got.AdvancedThroughSeq != 2 || got.MinAvailableSeq != 3 {
		t.Fatalf("response = %+v, want advanced through 2 min 3", got)
	}
	if !node.advanceCalled {
		t.Fatalf("AdvanceChannelRetentionThroughSeq was not called")
	}
	if node.advance.RetentionThroughSeq != 2 || node.advance.RetentionUpdatedAtMS != now.UnixMilli() {
		t.Fatalf("advance = %+v, want through 2 at now", node.advance)
	}
	if node.advance.ExpectedChannelEpoch != node.meta.ChannelEpoch ||
		node.advance.ExpectedLeaderEpoch != node.meta.LeaderEpoch ||
		node.advance.ExpectedLeader != node.meta.Leader ||
		node.advance.ExpectedLeaseUntilMS != node.meta.LeaseUntilMS {
		t.Fatalf("advance = %+v, want fence from meta %+v", node.advance, node.meta)
	}
	if node.lastReadReq.MinSeq != 1 || !node.lastReadReq.Reverse || node.lastReadReq.Limit != 1 {
		t.Fatalf("read request = %+v, want latest visible committed read", node.lastReadReq)
	}
}

func TestManagementMessageRetentionOperatorDryRunDoesNotAdvance(t *testing.T) {
	node := &recordingRetentionNode{
		nodeID: 7,
		meta: metadb.ChannelRuntimeMeta{
			ChannelID: "room-1", ChannelType: 2,
			ChannelEpoch: 4, LeaderEpoch: 5, Leader: 7,
			Replicas: []uint64{7}, ISR: []uint64{7}, MinISR: 1, Status: uint8(channelv2.StatusActive),
		},
		readResult: channelstore.ReadCommittedResult{Messages: []channelv2.Message{{MessageSeq: 3}}},
	}
	operator := NewManagementMessageRetentionOperator(node)

	got, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 2, DryRun: true,
	})

	if err != nil {
		t.Fatalf("AdvanceMessageRetention() error = %v", err)
	}
	if got.Status != managementusecase.MessageRetentionStatusWouldAdvance || got.AdvancedThroughSeq != 2 || got.MinAvailableSeq != 3 {
		t.Fatalf("response = %+v, want dry-run advance through 2 min 3", got)
	}
	if node.advanceCalled {
		t.Fatalf("AdvanceChannelRetentionThroughSeq called during dry-run")
	}
}

func TestManagementMessageRetentionOperatorRejectsNonLeader(t *testing.T) {
	node := &recordingRetentionNode{
		nodeID: 7,
		meta: metadb.ChannelRuntimeMeta{
			ChannelID: "room-1", ChannelType: 2,
			ChannelEpoch: 4, LeaderEpoch: 5, Leader: 8,
			Replicas: []uint64{7, 8}, ISR: []uint64{7, 8}, MinISR: 2, Status: uint8(channelv2.StatusActive),
		},
	}
	operator := NewManagementMessageRetentionOperator(node)

	_, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 2,
	})

	if !errors.Is(err, channelappend.ErrNotLeader) {
		t.Fatalf("AdvanceMessageRetention() error = %v, want ErrNotLeader", err)
	}
	if node.advanceCalled {
		t.Fatalf("AdvanceChannelRetentionThroughSeq called on non-leader")
	}
}

func TestManagementMessageRetentionOperatorMapsStaleFenceToStaleRoute(t *testing.T) {
	node := &recordingRetentionNode{
		nodeID: 7,
		meta: metadb.ChannelRuntimeMeta{
			ChannelID: "room-1", ChannelType: 2,
			ChannelEpoch: 4, LeaderEpoch: 5, Leader: 7,
			Replicas: []uint64{7}, ISR: []uint64{7}, MinISR: 1, Status: uint8(channelv2.StatusActive),
		},
		readResult: channelstore.ReadCommittedResult{Messages: []channelv2.Message{{MessageSeq: 3}}},
		advanceErr: metadb.ErrStaleMeta,
	}
	operator := NewManagementMessageRetentionOperator(node)

	_, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 2,
	})

	if !errors.Is(err, channelappend.ErrStaleRoute) {
		t.Fatalf("AdvanceMessageRetention() error = %v, want ErrStaleRoute", err)
	}
}

type recordingRetentionNode struct {
	nodeID        uint64
	meta          metadb.ChannelRuntimeMeta
	metaErr       error
	readResult    channelstore.ReadCommittedResult
	readErr       error
	lastReadReq   channelstore.ReadCommittedRequest
	advance       metadb.ChannelRetentionAdvance
	advanceCalled bool
	advanceErr    error
}

func (n *recordingRetentionNode) NodeID() uint64 {
	return n.nodeID
}

func (n *recordingRetentionNode) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return n.meta, n.metaErr
}

func (n *recordingRetentionNode) ReadChannelCommitted(_ context.Context, _ channelv2.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	n.lastReadReq = req
	return n.readResult, n.readErr
}

func (n *recordingRetentionNode) AdvanceChannelRetentionThroughSeq(_ context.Context, req metadb.ChannelRetentionAdvance) error {
	n.advance = req
	n.advanceCalled = true
	return n.advanceErr
}
