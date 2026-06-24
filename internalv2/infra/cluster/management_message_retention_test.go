package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
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

func TestManagementMessageRetentionOperatorForwardsRemoteLeaderBoundary(t *testing.T) {
	remoteService := &recordingRemoteRetentionService{
		result: managementusecase.AdvanceMessageRetentionResponse{
			ChannelID: "room-1", ChannelType: 2,
			RequestedThroughSeq: 10, AdvancedThroughSeq: 8, MinAvailableSeq: 9,
			Status: managementusecase.MessageRetentionStatusAdvanced,
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerMessageRetention: remoteService})
	node := &recordingRetentionRPCNode{
		recordingRetentionNode: recordingRetentionNode{
			nodeID: 7,
			meta: metadb.ChannelRuntimeMeta{
				ChannelID: "room-1", ChannelType: 2,
				ChannelEpoch: 4, LeaderEpoch: 5, Leader: 8,
				Replicas: []uint64{7, 8}, ISR: []uint64{7, 8}, MinISR: 2, Status: uint8(channelv2.StatusActive),
			},
		},
		handler: adapter.HandleManagerMessageRetentionRPC,
	}
	operator := NewManagementMessageRetentionOperator(node)

	got, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 10,
	})

	if err != nil {
		t.Fatalf("AdvanceMessageRetention() error = %v", err)
	}
	if got.Status != managementusecase.MessageRetentionStatusAdvanced || got.AdvancedThroughSeq != 8 || got.MinAvailableSeq != 9 {
		t.Fatalf("response = %+v, want remote advanced through 8", got)
	}
	if node.calledNodeID != 8 || node.calledServiceID != accessnode.ManagerMessageRetentionRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 8 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerMessageRetentionRPCServiceID)
	}
	if remoteService.req.ChannelID != "room-1" || remoteService.req.ThroughSeq != 10 {
		t.Fatalf("remote request = %+v, want original retention request", remoteService.req)
	}
	if node.advanceCalled {
		t.Fatalf("local AdvanceChannelRetentionThroughSeq called for remote leader")
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

func TestMessageRetentionBlocksWhenHWBelowRequested(t *testing.T) {
	node := &recordingRetentionNode{
		nodeID:        7,
		meta:          localRetentionMeta("room-1", 2, 7),
		readResult:    channelstore.ReadCommittedResult{Messages: []channelv2.Message{{MessageSeq: 10}}},
		retentionView: channelv2.RetentionView{HW: 3, CheckpointHW: 10, MinISRMatchOffset: 10},
	}
	operator := NewManagementMessageRetentionOperator(node)

	got, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 5,
	})

	if err != nil {
		t.Fatalf("AdvanceMessageRetention() error = %v", err)
	}
	if got.Status != managementusecase.MessageRetentionStatusBlocked || got.BlockedReason != managementusecase.MessageRetentionBlockedReasonHW || got.AdvancedThroughSeq != 3 {
		t.Fatalf("response = %+v, want blocked by hw_lag at 3", got)
	}
	if node.advanceCalled {
		t.Fatalf("AdvanceChannelRetentionThroughSeq called for blocked HW")
	}
}

func TestMessageRetentionAdvancesLogicalBoundaryWhenCheckpointLags(t *testing.T) {
	node := &recordingRetentionNode{
		nodeID:        7,
		meta:          localRetentionMeta("room-1", 2, 7),
		readResult:    channelstore.ReadCommittedResult{Messages: []channelv2.Message{{MessageSeq: 10}}},
		retentionView: channelv2.RetentionView{HW: 10, CheckpointHW: 4, MinISRMatchOffset: 10},
	}
	operator := NewManagementMessageRetentionOperator(node)

	got, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 5,
	})

	if err != nil {
		t.Fatalf("AdvanceMessageRetention() error = %v", err)
	}
	if got.Status != managementusecase.MessageRetentionStatusAdvanced || got.AdvancedThroughSeq != 5 {
		t.Fatalf("response = %+v, want logical advance through requested boundary", got)
	}
	if !node.advanceCalled || node.advance.RetentionThroughSeq != 5 {
		t.Fatalf("advance = %+v called=%v, want through 5", node.advance, node.advanceCalled)
	}
}

func TestMessageRetentionBlocksWhenMinISRBelowRequested(t *testing.T) {
	node := &recordingRetentionNode{
		nodeID:        7,
		meta:          localRetentionMeta("room-1", 2, 7),
		readResult:    channelstore.ReadCommittedResult{Messages: []channelv2.Message{{MessageSeq: 10}}},
		retentionView: channelv2.RetentionView{HW: 10, CheckpointHW: 10, MinISRMatchOffset: 4},
	}
	operator := NewManagementMessageRetentionOperator(node)

	got, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 5,
	})

	if err != nil {
		t.Fatalf("AdvanceMessageRetention() error = %v", err)
	}
	if got.Status != managementusecase.MessageRetentionStatusBlocked || got.BlockedReason != managementusecase.MessageRetentionBlockedReasonMinISRMatchOffset || got.AdvancedThroughSeq != 4 {
		t.Fatalf("response = %+v, want blocked by min_isr_match_offset at 4", got)
	}
}

func TestMessageRetentionBlocksWhenNoCommittedMessage(t *testing.T) {
	node := &recordingRetentionNode{
		nodeID:        7,
		meta:          localRetentionMeta("room-1", 2, 7),
		retentionView: channelv2.RetentionView{HW: 10, CheckpointHW: 10, MinISRMatchOffset: 10},
	}
	operator := NewManagementMessageRetentionOperator(node)

	got, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 5,
	})

	if err != nil {
		t.Fatalf("AdvanceMessageRetention() error = %v", err)
	}
	if got.Status != managementusecase.MessageRetentionStatusBlocked || got.BlockedReason != managementusecase.MessageRetentionBlockedReasonNoCommittedMessage || got.AdvancedThroughSeq != 0 {
		t.Fatalf("response = %+v, want blocked by no_committed_message", got)
	}
}

func TestMessageRetentionAllowsSafeRequestedBoundary(t *testing.T) {
	node := &recordingRetentionNode{
		nodeID:        7,
		meta:          localRetentionMeta("room-1", 2, 7),
		readResult:    channelstore.ReadCommittedResult{Messages: []channelv2.Message{{MessageSeq: 10}}},
		retentionView: channelv2.RetentionView{HW: 10, CheckpointHW: 10, MinISRMatchOffset: 10},
	}
	operator := NewManagementMessageRetentionOperator(node)

	got, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 5,
	})

	if err != nil {
		t.Fatalf("AdvanceMessageRetention() error = %v", err)
	}
	if got.Status != managementusecase.MessageRetentionStatusAdvanced || got.AdvancedThroughSeq != 5 {
		t.Fatalf("response = %+v, want advanced through requested boundary", got)
	}
	if !node.advanceCalled || node.advance.RetentionThroughSeq != 5 {
		t.Fatalf("advance = %+v called=%v, want through 5", node.advance, node.advanceCalled)
	}
	if !node.retentionViewCalled {
		t.Fatalf("ChannelRetentionView was not called")
	}
}

func TestMessageRetentionForwardedLeaderUsesFreshRetentionView(t *testing.T) {
	remoteNode := &recordingRetentionNode{
		nodeID:        8,
		meta:          localRetentionMeta("room-1", 2, 8),
		readResult:    channelstore.ReadCommittedResult{Messages: []channelv2.Message{{MessageSeq: 10}}},
		retentionView: channelv2.RetentionView{HW: 10, CheckpointHW: 10, MinISRMatchOffset: 4},
	}
	adapter := accessnode.New(accessnode.Options{ManagerMessageRetention: NewLocalManagementMessageRetentionOperator(remoteNode)})
	originNode := &recordingRetentionRPCNode{
		recordingRetentionNode: recordingRetentionNode{
			nodeID: 7,
			meta: metadb.ChannelRuntimeMeta{
				ChannelID: "room-1", ChannelType: 2,
				ChannelEpoch: 4, LeaderEpoch: 5, Leader: 8,
				Replicas: []uint64{7, 8}, ISR: []uint64{7, 8}, MinISR: 2, Status: uint8(channelv2.StatusActive),
			},
			retentionView: channelv2.RetentionView{HW: 10, CheckpointHW: 10, MinISRMatchOffset: 10},
		},
		handler: adapter.HandleManagerMessageRetentionRPC,
	}
	operator := NewManagementMessageRetentionOperator(originNode)

	got, err := operator.AdvanceMessageRetention(context.Background(), managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 5,
	})

	if err != nil {
		t.Fatalf("AdvanceMessageRetention() error = %v", err)
	}
	if got.Status != managementusecase.MessageRetentionStatusBlocked || got.BlockedReason != managementusecase.MessageRetentionBlockedReasonMinISRMatchOffset || got.AdvancedThroughSeq != 4 {
		t.Fatalf("response = %+v, want remote leader blocked by fresh min ISR view", got)
	}
	if !remoteNode.retentionViewCalled {
		t.Fatalf("remote leader ChannelRetentionView was not called")
	}
	if originNode.retentionViewCalled {
		t.Fatalf("origin ChannelRetentionView called before forwarding")
	}
}

type recordingRetentionNode struct {
	nodeID              uint64
	meta                metadb.ChannelRuntimeMeta
	metaErr             error
	retentionView       channelv2.RetentionView
	retentionViewErr    error
	retentionViewCalled bool
	readResult          channelstore.ReadCommittedResult
	readErr             error
	lastReadReq         channelstore.ReadCommittedRequest
	advance             metadb.ChannelRetentionAdvance
	advanceCalled       bool
	advanceErr          error
}

func (n *recordingRetentionNode) NodeID() uint64 {
	return n.nodeID
}

func (n *recordingRetentionNode) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return n.meta, n.metaErr
}

func (n *recordingRetentionNode) ChannelRetentionView(context.Context, channelv2.ChannelID) (channelv2.RetentionView, error) {
	n.retentionViewCalled = true
	if n.retentionViewErr != nil {
		return channelv2.RetentionView{}, n.retentionViewErr
	}
	if n.retentionView.HW == 0 && n.retentionView.CheckpointHW == 0 && n.retentionView.MinISRMatchOffset == 0 {
		return channelv2.RetentionView{HW: maxUint64(), CheckpointHW: maxUint64(), MinISRMatchOffset: maxUint64()}, nil
	}
	return n.retentionView, nil
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

func localRetentionMeta(channelID string, channelType int64, leader uint64) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID: channelID, ChannelType: channelType,
		ChannelEpoch: 4, LeaderEpoch: 5, Leader: leader,
		Replicas: []uint64{leader}, ISR: []uint64{leader}, MinISR: 1, Status: uint8(channelv2.StatusActive),
	}
}

type recordingRetentionRPCNode struct {
	recordingRetentionNode
	handler         func(context.Context, []byte) ([]byte, error)
	calledNodeID    uint64
	calledServiceID uint8
}

func (n *recordingRetentionRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	n.calledNodeID = nodeID
	n.calledServiceID = serviceID
	return n.handler(ctx, payload)
}

type recordingRemoteRetentionService struct {
	req    managementusecase.AdvanceMessageRetentionRequest
	result managementusecase.AdvanceMessageRetentionResponse
	err    error
}

func (s *recordingRemoteRetentionService) AdvanceMessageRetention(_ context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error) {
	s.req = req
	return s.result, s.err
}
