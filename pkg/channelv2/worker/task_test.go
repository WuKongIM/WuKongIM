package worker

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/stretchr/testify/require"
)

type workerTransportServer struct {
	lastPull   transport.PullRequest
	lastAck    transport.AckRequest
	lastNotify transport.NotifyRequest
}

func (s *workerTransportServer) HandlePull(ctx context.Context, req transport.PullRequest) (transport.PullResponse, error) {
	s.lastPull = req
	return transport.PullResponse{
		ChannelKey:  req.ChannelKey,
		Epoch:       req.Epoch,
		LeaderEpoch: req.LeaderEpoch,
		LeaderHW:    req.NextOffset,
		LeaderLEO:   req.NextOffset,
		Records:     []ch.Record{{ID: 99, Index: req.NextOffset, Payload: []byte("pulled"), SizeBytes: len("pulled")}},
	}, nil
}

func (s *workerTransportServer) HandleAck(ctx context.Context, req transport.AckRequest) error {
	s.lastAck = req
	return nil
}

func (s *workerTransportServer) HandleNotify(ctx context.Context, req transport.NotifyRequest) error {
	s.lastNotify = req
	return nil
}

func (s *workerTransportServer) HandlePullHint(ctx context.Context, req transport.PullHintRequest) error {
	return nil
}

type workerCaptureServer struct {
	pullHint transport.PullHintRequest
}

func (s *workerCaptureServer) HandlePull(ctx context.Context, req transport.PullRequest) (transport.PullResponse, error) {
	return transport.PullResponse{}, nil
}

func (s *workerCaptureServer) HandleAck(ctx context.Context, req transport.AckRequest) error {
	return nil
}

func (s *workerCaptureServer) HandleNotify(ctx context.Context, req transport.NotifyRequest) error {
	return nil
}

func (s *workerCaptureServer) HandlePullHint(ctx context.Context, req transport.PullHintRequest) error {
	s.pullHint = req
	return nil
}

func TestTaskRunStoreAppendUsesStoreDeps(t *testing.T) {
	key := ch.ChannelKey("1:a")
	id := ch.ChannelID{ID: "a", Type: 1}
	factory := store.NewMemoryFactory()
	deps := Deps{Stores: factory}
	fence := ch.Fence{ChannelKey: key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 10}

	res := Task{
		Kind:  TaskStoreAppend,
		Fence: fence,
		StoreAppend: &StoreAppendTask{
			ChannelID: id,
			Records:   []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}},
			Sync:      true,
		},
	}.Run(context.Background(), deps)

	require.NoError(t, res.Err)
	require.Equal(t, TaskStoreAppend, res.Kind)
	require.Equal(t, fence, res.Fence)
	require.NotNil(t, res.StoreAppend)
	require.Equal(t, uint64(1), res.StoreAppend.BaseOffset)
	require.Equal(t, uint64(1), res.StoreAppend.LastOffset)
}

func TestTaskRunStoreReadLogUsesStoreDeps(t *testing.T) {
	key := ch.ChannelKey("1:a")
	id := ch.ChannelID{ID: "a", Type: 1}
	factory := store.NewMemoryFactory()
	cs, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	_, err = cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}}})
	require.NoError(t, err)
	fence := ch.Fence{ChannelKey: key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 13}

	res := Task{
		Kind:  TaskStoreReadLog,
		Fence: fence,
		StoreReadLog: &StoreReadLogTask{
			ChannelID:  id,
			FromOffset: 1,
			MaxOffset:  1,
			MaxBytes:   1024,
		},
	}.Run(context.Background(), Deps{Stores: factory})

	require.NoError(t, res.Err)
	require.Equal(t, TaskStoreReadLog, res.Kind)
	require.NotNil(t, res.StoreReadLog)
	require.Len(t, res.StoreReadLog.Records, 1)
	require.Equal(t, uint64(1), res.StoreReadLog.Records[0].Index)
}

func TestTaskRunStoreApplyUsesStoreDeps(t *testing.T) {
	key := ch.ChannelKey("1:a")
	id := ch.ChannelID{ID: "a", Type: 1}
	factory := store.NewMemoryFactory()
	fence := ch.Fence{ChannelKey: key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 14}

	res := Task{
		Kind:  TaskStoreApply,
		Fence: fence,
		StoreApply: &StoreApplyTask{
			ChannelID: id,
			Records:   []ch.Record{{ID: 1, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
			LeaderHW:  1,
		},
	}.Run(context.Background(), Deps{Stores: factory})

	require.NoError(t, res.Err)
	require.Equal(t, TaskStoreApply, res.Kind)
	require.NotNil(t, res.StoreApply)
	require.Equal(t, uint64(1), res.StoreApply.LEO)
}

func TestTaskRunStoreCheckpoint(t *testing.T) {
	factory := store.NewMemoryFactory()
	id := ch.ChannelID{ID: "checkpoint", Type: 1}
	key := ch.ChannelKeyForID(id)
	cs, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	_, err = cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: []ch.Record{
		{ID: 1, Payload: []byte("a"), SizeBytes: 1},
		{ID: 2, Payload: []byte("b"), SizeBytes: 1},
		{ID: 3, Payload: []byte("c"), SizeBytes: 1},
		{ID: 4, Payload: []byte("d"), SizeBytes: 1},
	}})
	require.NoError(t, err)
	task := Task{Kind: TaskStoreCheckpoint, Fence: ch.Fence{ChannelKey: key, OpID: 1}, StoreCheckpoint: &StoreCheckpointTask{ChannelID: id, Checkpoint: ch.Checkpoint{HW: 4}}}

	res := task.Run(context.Background(), Deps{Stores: factory})
	require.NoError(t, res.Err)
	cs, err = factory.ChannelStore(key, id)
	require.NoError(t, err)
	loaded, err := cs.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(4), loaded.CheckpointHW)
}

func TestTaskRunRPCPullAndAckUseTransportDeps(t *testing.T) {
	net := transport.NewLocalNetwork()
	server := &workerTransportServer{}
	net.Register(2, server)
	deps := Deps{Transport: net.Client()}
	fence := ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 11}

	pull := Task{Kind: TaskRPCPull, Fence: fence, RPCPull: &RPCPullTask{Node: 2, Request: transport.PullRequest{ChannelKey: "1:a", NextOffset: 7}}}.Run(context.Background(), deps)
	require.NoError(t, pull.Err)
	require.NotNil(t, pull.RPCPull)
	require.Equal(t, uint64(7), server.lastPull.NextOffset)

	ack := Task{Kind: TaskRPCAck, Fence: fence, RPCAck: &RPCAckTask{Node: 2, Request: transport.AckRequest{ChannelKey: "1:a", MatchOffset: 9}}}.Run(context.Background(), deps)
	require.NoError(t, ack.Err)
	require.NotNil(t, ack.RPCAck)
	require.Equal(t, uint64(9), server.lastAck.MatchOffset)

	notify := Task{Kind: TaskRPCNotify, Fence: fence, RPCNotify: &RPCNotifyTask{Node: 2, Request: transport.NotifyRequest{ChannelKey: "1:a", LeaderLEO: 10}}}.Run(context.Background(), deps)
	require.NoError(t, notify.Err)
	require.NotNil(t, notify.RPCNotify)
	require.Equal(t, uint64(10), server.lastNotify.LeaderLEO)
}

func TestTaskRunRPCPullHint(t *testing.T) {
	net := transport.NewLocalNetwork()
	srv := &workerCaptureServer{}
	net.Register(2, srv)

	req := transport.PullHintRequest{ChannelKey: ch.ChannelKey("1:a"), ChannelID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, LeaderLEO: 3, ActivityVersion: 3, Reason: transport.PullHintReasonAppend}
	task := Task{Kind: TaskRPCPullHint, Fence: ch.Fence{ChannelKey: req.ChannelKey, OpID: 1}, RPCPullHint: &RPCPullHintTask{Node: 2, Request: req}}

	res := task.Run(context.Background(), Deps{Transport: net})
	require.NoError(t, res.Err)
	require.NotNil(t, res.RPCPullHint)
	require.Equal(t, req, srv.pullHint)
}

func TestTaskRunMissingDepsReturnsInvalidConfig(t *testing.T) {
	res := Task{Kind: TaskStoreReadLog}.Run(context.Background(), Deps{})
	require.ErrorIs(t, res.Err, ch.ErrInvalidConfig)
}

func TestTaskRunTaskContextDeadlineReportsDeadlineExceeded(t *testing.T) {
	deadlineCtx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()

	res := Task{
		Kind:    TaskFunc,
		Context: deadlineCtx,
		RunFunc: func(ctx context.Context) Result {
			<-ctx.Done()
			return Result{Err: ctx.Err()}
		},
	}.Run(context.Background(), Deps{})

	require.ErrorIs(t, res.Err, context.DeadlineExceeded)
}

func TestTaskRunTaskContextExplicitCancelReportsCanceled(t *testing.T) {
	taskCtx, cancel := context.WithCancel(context.Background())
	cancel()

	res := Task{
		Kind:    TaskFunc,
		Context: taskCtx,
		RunFunc: func(ctx context.Context) Result {
			<-ctx.Done()
			return Result{Err: ctx.Err()}
		},
	}.Run(context.Background(), Deps{})

	require.ErrorIs(t, res.Err, context.Canceled)
}

func TestTaskRunNilTypedPayloadReturnsInvalidConfig(t *testing.T) {
	factory := store.NewMemoryFactory()
	net := transport.NewLocalNetwork()
	deps := Deps{Stores: factory, Transport: net.Client()}
	fence := ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 20}
	cases := []struct {
		name string
		task Task
	}{
		{name: "store append", task: Task{Kind: TaskStoreAppend, Fence: fence}},
		{name: "store read log", task: Task{Kind: TaskStoreReadLog, Fence: fence}},
		{name: "store apply", task: Task{Kind: TaskStoreApply, Fence: fence}},
		{name: "store checkpoint", task: Task{Kind: TaskStoreCheckpoint, Fence: fence}},
		{name: "rpc pull", task: Task{Kind: TaskRPCPull, Fence: fence}},
		{name: "rpc ack", task: Task{Kind: TaskRPCAck, Fence: fence}},
		{name: "rpc notify", task: Task{Kind: TaskRPCNotify, Fence: fence}},
		{name: "rpc pull hint", task: Task{Kind: TaskRPCPullHint, Fence: fence}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.task.Run(context.Background(), deps)
			require.Equal(t, tc.task.Kind, res.Kind)
			require.Equal(t, fence, res.Fence)
			require.ErrorIs(t, res.Err, ch.ErrInvalidConfig)
		})
	}
}
