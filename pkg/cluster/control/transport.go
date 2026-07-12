package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	defaultControlRPCTimeout               = 200 * time.Millisecond
	defaultRaftTransportWorkers            = 4
	defaultRaftTransportQueueSizePerWorker = 256
)

type raftStepper interface {
	Step(context.Context, raftpb.Message) error
}

// RaftTransport sends Controller Raft messages over cluster typed messages.
type RaftTransport struct {
	sender   clusternet.Sender
	timeout  time.Duration
	observer transport.Observer
	queues   []chan raftTransportBatch
	done     chan struct{}

	mu       sync.RWMutex
	stopped  bool
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// RaftTransportOptions bounds Controller Raft send concurrency and observation.
type RaftTransportOptions struct {
	// WorkerCount is the fixed number of destination-preserving send shards.
	WorkerCount int
	// QueueSizePerWorker is the bounded queue capacity for each send shard.
	QueueSizePerWorker int
	// Timeout bounds one underlying network send.
	Timeout time.Duration
	// Observer receives low-cardinality queue, admission, and task events.
	Observer transport.Observer
}

type raftTransportBatch struct {
	nodeID  uint64
	payload []byte
	items   int
}

// NewRaftTransport creates a Controller Raft transport backed by sender.
func NewRaftTransport(sender clusternet.Sender) *RaftTransport {
	return NewRaftTransportWithOptions(sender, RaftTransportOptions{})
}

// NewRaftTransportWithOptions creates a bounded Controller Raft transport.
func NewRaftTransportWithOptions(sender clusternet.Sender, opts RaftTransportOptions) *RaftTransport {
	if opts.WorkerCount <= 0 {
		opts.WorkerCount = defaultRaftTransportWorkers
	}
	if opts.QueueSizePerWorker <= 0 {
		opts.QueueSizePerWorker = defaultRaftTransportQueueSizePerWorker
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultControlRPCTimeout
	}
	t := &RaftTransport{
		sender:   sender,
		timeout:  opts.Timeout,
		observer: opts.Observer,
		queues:   make([]chan raftTransportBatch, opts.WorkerCount),
		done:     make(chan struct{}),
	}
	for i := range t.queues {
		t.queues[i] = make(chan raftTransportBatch, opts.QueueSizePerWorker)
		t.wg.Add(1)
		go t.run(t.queues[i])
	}
	return t
}

// Send sends messages grouped by destination node without blocking indefinitely.
func (t *RaftTransport) Send(messages []raftpb.Message) {
	if t == nil || t.sender == nil {
		return
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.stopped {
		t.observeAdmission("stopped", 0, 0)
		return
	}
	byNode := make(map[uint64][]raftpb.Message)
	for _, msg := range messages {
		if msg.To == 0 {
			continue
		}
		byNode[msg.To] = append(byNode[msg.To], msg)
	}
	for nodeID, batch := range byNode {
		payload, err := EncodeRaftBatch(batch)
		if err != nil {
			t.observeTask("invalid", len(batch), 0)
			continue
		}
		queue := t.queues[nodeID%uint64(len(t.queues))]
		select {
		case queue <- raftTransportBatch{nodeID: nodeID, payload: payload, items: len(batch)}:
			t.observeQueue("ok")
		default:
			depth, capacity := t.queueUsage()
			t.observeAdmission("full", depth, capacity)
		}
	}
}

// Stop rejects new sends, drops queued batches, and waits for bounded workers.
func (t *RaftTransport) Stop() {
	if t == nil {
		return
	}
	t.stopOnce.Do(func() {
		t.mu.Lock()
		t.stopped = true
		close(t.done)
		t.mu.Unlock()
		t.wg.Wait()
	})
}

func (t *RaftTransport) run(queue chan raftTransportBatch) {
	defer t.wg.Done()
	for {
		select {
		case <-t.done:
			t.dropQueued(queue)
			return
		case batch := <-queue:
			select {
			case <-t.done:
				t.observeTask("stopped", batch.items, 0)
				t.dropQueued(queue)
				return
			default:
			}
			t.observeQueue("ok")
			t.sendBatch(batch)
		}
	}
}

func (t *RaftTransport) dropQueued(queue chan raftTransportBatch) {
	for {
		select {
		case batch := <-queue:
			t.observeTask("stopped", batch.items, 0)
		default:
			t.observeQueue("stopped")
			return
		}
	}
}

func (t *RaftTransport) sendBatch(batch raftTransportBatch) {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	err := clusternet.SendOwnedPayload(ctx, t.sender, batch.nodeID, clusternet.RPCControlRaft, batch.payload)
	cancel()
	t.observeTask(raftTransportResult(err), batch.items, time.Since(startedAt))
}

func (t *RaftTransport) observeQueue(result string) {
	if t.observer == nil {
		return
	}
	depth, capacity := t.queueUsage()
	t.observer.ObserveTransport(transport.Event{Name: "controller_raft_queue", Priority: transport.PriorityRaft, Result: result, Items: depth, Capacity: capacity})
}

func (t *RaftTransport) queueUsage() (int, int) {
	depth := 0
	capacity := 0
	for _, queue := range t.queues {
		depth += len(queue)
		capacity += cap(queue)
	}
	return depth, capacity
}

func (t *RaftTransport) observeAdmission(result string, depth, capacity int) {
	if t.observer == nil {
		return
	}
	t.observer.ObserveTransport(transport.Event{Name: "controller_raft_admission", Priority: transport.PriorityRaft, Result: result, Items: depth, Capacity: capacity})
}

func (t *RaftTransport) observeTask(result string, items int, duration time.Duration) {
	if t.observer == nil {
		return
	}
	t.observer.ObserveTransport(transport.Event{Name: "controller_raft_task", Priority: transport.PriorityRaft, Result: result, Items: items, Duration: duration})
}

func raftTransportResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "err"
	}
}

// NewRaftHandler creates an RPC handler that steps decoded Controller Raft messages.
func NewRaftHandler(stepper raftStepper) clusternet.Handler {
	return newRaftHandler(stepper, defaultControlRPCTimeout)
}

func newRaftHandler(stepper raftStepper, stepTimeout time.Duration) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		messages, err := DecodeRaftBatch(payload)
		if err != nil {
			return nil, err
		}
		for _, msg := range messages {
			if stepper == nil {
				continue
			}
			if err := stepWithTimeout(ctx, stepper, msg, stepTimeout); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

func stepWithTimeout(ctx context.Context, stepper raftStepper, msg raftpb.Message, timeout time.Duration) error {
	if timeout <= 0 {
		return stepper.Step(ctx, msg)
	}
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := stepper.Step(stepCtx, msg)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		// Raft transport is allowed to drop messages; dropping here prevents
		// one-way RPC notify goroutines from piling up behind a saturated local
		// Step queue.
		return nil
	}
	return err
}

// StateSyncEndpoint adapts cluster RPC to controller/sync.Endpoint.
type StateSyncEndpoint struct {
	caller clusternet.Caller
	nodeID uint64
}

// NewStateSyncEndpoint creates an endpoint for one remote Controller state peer.
func NewStateSyncEndpoint(caller clusternet.Caller, nodeID uint64) *StateSyncEndpoint {
	return &StateSyncEndpoint{caller: caller, nodeID: nodeID}
}

// GetState sends a Controller state sync request to the remote node.
func (e *StateSyncEndpoint) GetState(ctx context.Context, req controller.GetStateRequest) (controller.GetStateResponse, error) {
	payload, err := EncodeStateSyncRequest(req)
	if err != nil {
		return controller.GetStateResponse{}, err
	}
	resp, err := clusternet.CallOwnedPayload(ctx, e.caller, e.nodeID, clusternet.RPCControlStateSync, payload)
	if err != nil {
		return controller.GetStateResponse{}, err
	}
	return DecodeStateSyncResponse(resp)
}

// NewStateSyncHandler creates an RPC handler for a Controller state sync endpoint.
func NewStateSyncHandler(endpoint controller.Endpoint) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := DecodeStateSyncRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := endpoint.GetState(ctx, req)
		if err != nil {
			return nil, err
		}
		return EncodeStateSyncResponse(resp)
	})
}

// TaskApplier applies Controller task writes.
type TaskApplier interface {
	// CompleteTask submits a fenced global task completion result.
	CompleteTask(context.Context, TaskResult) error
	// FailTask submits a fenced global task failure result.
	FailTask(context.Context, TaskResult) error
	// ReportTaskProgress submits one participant's fenced progress report.
	ReportTaskProgress(context.Context, TaskProgress) error
	// RequestSlotLeaderTransfer submits a Controller-backed Slot leader transfer intent.
	RequestSlotLeaderTransfer(context.Context, SlotLeaderTransferRequest) (SlotLeaderTransferResult, error)
	// AdvanceSlotReplicaMovePhase submits a fenced Slot replica move phase update.
	AdvanceSlotReplicaMovePhase(context.Context, SlotReplicaMovePhaseAdvance) error
	// CommitSlotReplicaMove submits the final fenced Slot replica move commit.
	CommitSlotReplicaMove(context.Context, SlotReplicaMoveCommit) error
}

// TaskClient forwards Controller task writes to a remote node.
type TaskClient struct {
	caller clusternet.Caller
}

// NewTaskClient creates a task write RPC client.
func NewTaskClient(caller clusternet.Caller) *TaskClient {
	return &TaskClient{caller: caller}
}

// SubmitTask sends one task write request to nodeID.
func (c *TaskClient) SubmitTask(ctx context.Context, nodeID uint64, req TaskRequest) error {
	payload, err := EncodeTaskRequest(req)
	if err != nil {
		return err
	}
	_, err = clusternet.CallOwnedPayload(ctx, c.caller, nodeID, clusternet.RPCControlTaskResult, payload)
	return err
}

// NewTaskHandler creates an RPC handler for Controller task writes.
func NewTaskHandler(applier TaskApplier) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := DecodeTaskRequest(payload)
		if err != nil {
			return nil, err
		}
		switch req.Action {
		case TaskActionComplete:
			return nil, applier.CompleteTask(ctx, req.Result)
		case TaskActionFail:
			return nil, applier.FailTask(ctx, req.Result)
		case TaskActionProgress:
			return nil, applier.ReportTaskProgress(ctx, req.Progress)
		case TaskActionLeaderTransfer:
			_, err := applier.RequestSlotLeaderTransfer(ctx, req.LeaderTransfer)
			return nil, err
		case TaskActionReplicaMovePhase:
			return nil, applier.AdvanceSlotReplicaMovePhase(ctx, req.ReplicaMovePhase)
		case TaskActionReplicaMoveCommit:
			return nil, applier.CommitSlotReplicaMove(ctx, req.ReplicaMoveCommit)
		default:
			return nil, fmt.Errorf("control task: unknown action %q", req.Action)
		}
	})
}

// StaticPeerPicker resolves a fixed Controller voter set to cluster sync endpoints.
type StaticPeerPicker struct {
	endpoints map[uint64]controller.Endpoint
	ids       []uint64
}

// NewStaticPeerPicker creates a fixed peer picker backed by caller.
func NewStaticPeerPicker(caller clusternet.Caller, voters []RuntimeVoter) *StaticPeerPicker {
	picker := &StaticPeerPicker{
		endpoints: make(map[uint64]controller.Endpoint, len(voters)),
		ids:       make([]uint64, 0, len(voters)),
	}
	for _, voter := range voters {
		picker.ids = append(picker.ids, voter.NodeID)
		picker.endpoints[voter.NodeID] = NewStateSyncEndpoint(caller, voter.NodeID)
	}
	return picker
}

// Endpoint returns the sync endpoint for nodeID.
func (p *StaticPeerPicker) Endpoint(nodeID uint64) (controller.Endpoint, bool) {
	if p == nil {
		return nil, false
	}
	endpoint, ok := p.endpoints[nodeID]
	return endpoint, ok
}

// PeerIDs returns the fixed Controller voter IDs.
func (p *StaticPeerPicker) PeerIDs() []uint64 {
	if p == nil {
		return nil
	}
	return append([]uint64(nil), p.ids...)
}
