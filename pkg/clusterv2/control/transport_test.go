package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRaftTransportSendsBatchByDestination(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	stepper := newRecordingRaftStepper()
	network.Register(2, clusternet.RPCControlRaft, NewRaftHandler(stepper))

	transport := NewRaftTransport(network)
	transport.Send([]raftpb.Message{
		{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 4},
		{From: 1, To: 0, Type: raftpb.MsgBeat},
	})

	select {
	case <-stepper.received:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for raft message")
	}
	messages := stepper.Messages()
	if len(messages) != 1 || messages[0].To != 2 || messages[0].Term != 4 {
		t.Fatalf("stepped messages = %#v", messages)
	}
}

func TestRaftTransportSendReturnsWithoutWaitingForSender(t *testing.T) {
	network := &blockingRaftMessenger{entered: make(chan struct{}), release: make(chan struct{})}
	transport := NewRaftTransport(network)

	returned := make(chan struct{})
	go func() {
		transport.Send([]raftpb.Message{{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 4}})
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(50 * time.Millisecond):
		close(network.release)
		<-returned
		t.Fatal("RaftTransport.Send blocked on sender")
	}
	select {
	case <-network.entered:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for async sender")
	}
	close(network.release)
}

func TestRaftTransportUsesOneWaySend(t *testing.T) {
	network := &recordingRaftMessenger{
		sent:  make(chan sentControlMessage, 1),
		calls: make(chan struct{}, 1),
	}
	transport := NewRaftTransport(network)
	transport.Send([]raftpb.Message{{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 4}})

	select {
	case sent := <-network.sent:
		if sent.nodeID != 2 || sent.serviceID != clusternet.RPCControlRaft {
			t.Fatalf("sent target=(%d,%d), want node=2 service=%d", sent.nodeID, sent.serviceID, clusternet.RPCControlRaft)
		}
		messages, err := DecodeRaftBatch(sent.payload)
		if err != nil {
			t.Fatalf("DecodeRaftBatch() error = %v", err)
		}
		if len(messages) != 1 || messages[0].Term != 4 {
			t.Fatalf("messages = %#v, want one term=4 heartbeat", messages)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for one-way send")
	}

	select {
	case <-network.calls:
		t.Fatal("RaftTransport called synchronous Call, want one-way Send")
	default:
	}
}

func TestRaftTransportUsesOwnedSendWhenAvailable(t *testing.T) {
	network := &recordingOwnedRaftMessenger{
		recordingRaftMessenger: recordingRaftMessenger{
			sent:  make(chan sentControlMessage, 1),
			calls: make(chan struct{}, 1),
		},
		ownedSent: make(chan sentControlMessage, 1),
	}
	transport := NewRaftTransport(network)
	transport.Send([]raftpb.Message{{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 4}})

	select {
	case sent := <-network.ownedSent:
		if sent.nodeID != 2 || sent.serviceID != clusternet.RPCControlRaft {
			t.Fatalf("owned sent target=(%d,%d), want node=2 service=%d", sent.nodeID, sent.serviceID, clusternet.RPCControlRaft)
		}
		messages, err := DecodeRaftBatch(sent.payload)
		if err != nil {
			t.Fatalf("DecodeRaftBatch() error = %v", err)
		}
		if len(messages) != 1 || messages[0].Term != 4 {
			t.Fatalf("messages = %#v, want one term=4 heartbeat", messages)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for owned one-way send")
	}

	select {
	case <-network.sent:
		t.Fatal("RaftTransport used normal Send, want SendOwned")
	default:
	}
}

func TestRaftHandlerDoesNotBlockIndefinitelyWhenStepperBackpressures(t *testing.T) {
	stepper := &blockingRaftStepper{entered: make(chan struct{})}
	payload, err := EncodeRaftBatch([]raftpb.Message{{From: 2, To: 1, Type: raftpb.MsgHeartbeatResp, Term: 4}})
	if err != nil {
		t.Fatalf("EncodeRaftBatch() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := NewRaftHandler(stepper).HandleRPC(ctx, payload)
		done <- err
	}()

	select {
	case <-stepper.entered:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for stepper")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("HandleRPC() error = %v", err)
		}
	case <-time.After(defaultControlRPCTimeout + 100*time.Millisecond):
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatal("Raft handler blocked indefinitely on backpressured Step")
	}
}

func TestStateSyncClientCallsRemoteEndpoint(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	server := cv2.NewStateSyncServer(cv2.StateSyncServerConfig{
		NodeID:    1,
		ClusterID: "cluster-a",
		LeaderID:  func() uint64 { return 1 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (cv2.ClusterState, error) { return controllerV2State(), nil },
	})
	network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(server))

	endpoint := NewStateSyncEndpoint(network, 1)
	resp, err := endpoint.GetState(context.Background(), cv2.GetStateRequest{ClusterID: "cluster-a"})
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if resp.Revision == 0 || len(resp.Payload) == 0 {
		t.Fatalf("GetState() = %#v, want payload", resp)
	}
}

func TestTaskClientCallsRemoteHandler(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	applier := &recordingTaskApplier{}
	network.Register(1, clusternet.RPCControlTaskResult, NewTaskHandler(applier))

	client := NewTaskClient(network)
	err := client.SubmitTask(context.Background(), 1, TaskRequest{
		Action: TaskActionComplete,
		Result: cv2.TaskResult{
			TaskID:      "slot-1-bootstrap-1",
			SlotID:      1,
			TaskKind:    cv2.TaskKindBootstrap,
			ConfigEpoch: 1,
			Attempt:     0,
		},
	})

	if err != nil {
		t.Fatalf("SubmitTask() error = %v", err)
	}
	if len(applier.completed) != 1 || applier.completed[0].TaskID != "slot-1-bootstrap-1" {
		t.Fatalf("completed = %#v", applier.completed)
	}

	transfer := SlotLeaderTransferRequest{SlotID: 1, SourceNode: 1, TargetNode: 2, TargetPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, StateRevision: 9}
	err = client.SubmitTask(context.Background(), 1, TaskRequest{
		Action:         TaskActionLeaderTransfer,
		LeaderTransfer: transfer,
	})
	if err != nil {
		t.Fatalf("SubmitTask(leader transfer) error = %v", err)
	}
	if len(applier.leaderTransfers) != 1 || applier.leaderTransfers[0].TargetNode != 2 {
		t.Fatalf("leaderTransfers = %#v, want target node 2", applier.leaderTransfers)
	}
}

func TestControlWriteClientPreservesWrappedSemanticErrorIdentity(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	applier := &recordingControlWriteApplier{activateErr: fmt.Errorf("wrapped semantic error: %w", cv2.ErrProposalRejected)}
	network.Register(1, clusternet.RPCControlWrite, NewControlWriteHandler(applier))
	client := NewControlWriteClient(network)

	_, err := client.Submit(context.Background(), 1, ControlWriteRequest{
		Action:       ControlWriteActionActivateNode,
		ActivateNode: ActivateNodeRequest{NodeID: 4},
	})

	if !errors.Is(err, cv2.ErrProposalRejected) {
		t.Fatalf("Submit() error = %v, want errors.Is(ErrProposalRejected)", err)
	}
}

func TestNewControlWriteHandlerCallsMarkNodeLeaving(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	applier := &recordingControlWriteApplier{
		markNodeLeavingResult: MarkNodeLeavingResult{
			Changed: true,
			Node: Node{
				NodeID:         4,
				Addr:           "n4",
				Roles:          []Role{RoleData},
				JoinState:      NodeJoinStateLeaving,
				Status:         NodeAlive,
				CapacityWeight: 1,
			},
			Revision: 9,
		},
	}
	network.Register(1, clusternet.RPCControlWrite, NewControlWriteHandler(applier))
	client := NewControlWriteClient(network)

	result, err := client.Submit(context.Background(), 1, ControlWriteRequest{
		Action:          ControlWriteActionMarkNodeLeaving,
		MarkNodeLeaving: MarkNodeLeavingRequest{NodeID: 4},
	})
	if err != nil {
		t.Fatalf("Submit(mark node leaving) error = %v", err)
	}
	if len(applier.markNodeLeaving) != 1 || applier.markNodeLeaving[0].NodeID != 4 {
		t.Fatalf("markNodeLeaving = %#v, want node 4", applier.markNodeLeaving)
	}
	if !result.MarkNodeLeaving.Changed || result.MarkNodeLeaving.Node.JoinState != NodeJoinStateLeaving {
		t.Fatalf("Submit(mark node leaving) = %#v, want leaving result", result.MarkNodeLeaving)
	}
}

func TestNewControlWriteHandlerCallsMarkNodeRemoved(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	applier := &recordingControlWriteApplier{
		markNodeRemovedResult: MarkNodeRemovedResult{
			Changed: true,
			Node: Node{
				NodeID:         4,
				Addr:           "n4",
				Roles:          []Role{RoleData},
				JoinState:      NodeJoinStateRemoved,
				Status:         NodeDown,
				CapacityWeight: 1,
			},
			Revision: 10,
		},
	}
	network.Register(1, clusternet.RPCControlWrite, NewControlWriteHandler(applier))
	client := NewControlWriteClient(network)

	result, err := client.Submit(context.Background(), 1, ControlWriteRequest{
		Action:          ControlWriteActionMarkNodeRemoved,
		MarkNodeRemoved: MarkNodeRemovedRequest{NodeID: 4},
	})
	if err != nil {
		t.Fatalf("Submit(mark node removed) error = %v", err)
	}
	if len(applier.markNodeRemoved) != 1 || applier.markNodeRemoved[0].NodeID != 4 {
		t.Fatalf("markNodeRemoved = %#v, want node 4", applier.markNodeRemoved)
	}
	if !result.MarkNodeRemoved.Changed || result.MarkNodeRemoved.Node.JoinState != NodeJoinStateRemoved {
		t.Fatalf("Submit(mark node removed) = %#v, want removed result", result.MarkNodeRemoved)
	}
}

type recordingTaskApplier struct {
	completed       []TaskResult
	failed          []TaskResult
	progress        []TaskProgress
	leaderTransfers []SlotLeaderTransferRequest
	movePhases      []SlotReplicaMovePhaseAdvance
	moveCommits     []SlotReplicaMoveCommit
}

func (a *recordingTaskApplier) CompleteTask(ctx context.Context, result TaskResult) error {
	a.completed = append(a.completed, result)
	return nil
}

func (a *recordingTaskApplier) FailTask(ctx context.Context, result TaskResult) error {
	a.failed = append(a.failed, result)
	return nil
}

func (a *recordingTaskApplier) ReportTaskProgress(ctx context.Context, progress TaskProgress) error {
	a.progress = append(a.progress, progress)
	return nil
}

func (a *recordingTaskApplier) RequestSlotLeaderTransfer(ctx context.Context, req SlotLeaderTransferRequest) (SlotLeaderTransferResult, error) {
	a.leaderTransfers = append(a.leaderTransfers, req)
	return SlotLeaderTransferResult{Created: true}, nil
}

func (a *recordingTaskApplier) AdvanceSlotReplicaMovePhase(ctx context.Context, phase SlotReplicaMovePhaseAdvance) error {
	a.movePhases = append(a.movePhases, phase)
	return nil
}

func (a *recordingTaskApplier) CommitSlotReplicaMove(ctx context.Context, commit SlotReplicaMoveCommit) error {
	a.moveCommits = append(a.moveCommits, commit)
	return nil
}

type recordingControlWriteApplier struct {
	joinNodes             []JoinNodeRequest
	joinResult            JoinNodeResult
	joinErr               error
	activateNodes         []ActivateNodeRequest
	activateCalls         int
	activateResult        ActivateNodeResult
	activateErr           error
	markNodeLeaving       []MarkNodeLeavingRequest
	markNodeLeavingResult MarkNodeLeavingResult
	markNodeLeavingErr    error
	markNodeRemoved       []MarkNodeRemovedRequest
	markNodeRemovedResult MarkNodeRemovedResult
	markNodeRemovedErr    error
	slotReplicaMoves      []SlotReplicaMoveRequest
	slotReplicaMoveResult SlotReplicaMoveResult
	slotReplicaMoveErr    error
}

func (a *recordingControlWriteApplier) JoinNode(ctx context.Context, req JoinNodeRequest) (JoinNodeResult, error) {
	a.joinNodes = append(a.joinNodes, req)
	if a.joinErr != nil {
		return JoinNodeResult{}, a.joinErr
	}
	return a.joinResult, nil
}

func (a *recordingControlWriteApplier) ActivateNode(ctx context.Context, req ActivateNodeRequest) (ActivateNodeResult, error) {
	a.activateNodes = append(a.activateNodes, req)
	a.activateCalls++
	if a.activateErr != nil {
		return ActivateNodeResult{}, a.activateErr
	}
	return a.activateResult, nil
}

func (a *recordingControlWriteApplier) MarkNodeLeaving(ctx context.Context, req MarkNodeLeavingRequest) (MarkNodeLeavingResult, error) {
	a.markNodeLeaving = append(a.markNodeLeaving, req)
	if a.markNodeLeavingErr != nil {
		return MarkNodeLeavingResult{}, a.markNodeLeavingErr
	}
	return a.markNodeLeavingResult, nil
}

func (a *recordingControlWriteApplier) MarkNodeRemoved(ctx context.Context, req MarkNodeRemovedRequest) (MarkNodeRemovedResult, error) {
	a.markNodeRemoved = append(a.markNodeRemoved, req)
	if a.markNodeRemovedErr != nil {
		return MarkNodeRemovedResult{}, a.markNodeRemovedErr
	}
	return a.markNodeRemovedResult, nil
}

func (a *recordingControlWriteApplier) RequestSlotReplicaMove(ctx context.Context, req SlotReplicaMoveRequest) (SlotReplicaMoveResult, error) {
	a.slotReplicaMoves = append(a.slotReplicaMoves, req)
	if a.slotReplicaMoveErr != nil {
		return SlotReplicaMoveResult{}, a.slotReplicaMoveErr
	}
	return a.slotReplicaMoveResult, nil
}

type recordingRaftStepper struct {
	mu       sync.Mutex
	once     sync.Once
	received chan struct{}
	messages []raftpb.Message
}

func newRecordingRaftStepper() *recordingRaftStepper {
	return &recordingRaftStepper{received: make(chan struct{})}
}

func (s *recordingRaftStepper) Step(ctx context.Context, msg raftpb.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()
	s.once.Do(func() { close(s.received) })
	return nil
}

func (s *recordingRaftStepper) Messages() []raftpb.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]raftpb.Message(nil), s.messages...)
}

type sentControlMessage struct {
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

type recordingRaftMessenger struct {
	sent  chan sentControlMessage
	calls chan struct{}
}

func (m *recordingRaftMessenger) Send(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) error {
	m.sent <- sentControlMessage{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)}
	return nil
}

func (m *recordingRaftMessenger) Call(context.Context, uint64, uint8, []byte) ([]byte, error) {
	m.calls <- struct{}{}
	return nil, nil
}

type recordingOwnedRaftMessenger struct {
	recordingRaftMessenger
	ownedSent chan sentControlMessage
}

func (m *recordingOwnedRaftMessenger) SendOwned(_ context.Context, nodeID uint64, serviceID uint8, payload transportv2.OwnedBuffer) error {
	m.ownedSent <- sentControlMessage{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload.Bytes()...)}
	payload.Release()
	return nil
}

type blockingRaftMessenger struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *blockingRaftMessenger) Send(ctx context.Context, _ uint64, _ uint8, _ []byte) error {
	m.once.Do(func() { close(m.entered) })
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type blockingRaftStepper struct {
	entered chan struct{}
	once    sync.Once
}

func (s *blockingRaftStepper) Step(ctx context.Context, _ raftpb.Message) error {
	s.once.Do(func() { close(s.entered) })
	<-ctx.Done()
	return ctx.Err()
}
