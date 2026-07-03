package delivery

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestFanoutTaskRouterRunsLocalLeaderTask(t *testing.T) {
	local := &recordingFanoutRunner{}
	remote := &recordingFanoutForwarder{}
	router := NewFanoutTaskRouter(FanoutTaskRouterOptions{
		LocalNodeID: 1,
		Local:       local,
		Remote:      remote,
	})
	task := FanoutTask{Envelope: Envelope{MessageID: 1}, Partition: Partition{ID: 7, LeaderNodeID: 1}}

	if err := router.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}

	if !reflect.DeepEqual(local.tasks, []FanoutTask{task}) {
		t.Fatalf("local tasks = %#v, want %#v", local.tasks, []FanoutTask{task})
	}
	if len(remote.calls) != 0 {
		t.Fatalf("remote calls = %#v, want none", remote.calls)
	}
}

func TestFanoutTaskRouterForwardsRemoteLeaderTask(t *testing.T) {
	local := &recordingFanoutRunner{}
	remote := &recordingFanoutForwarder{}
	router := NewFanoutTaskRouter(FanoutTaskRouterOptions{
		LocalNodeID: 1,
		Local:       local,
		Remote:      remote,
	})
	task := FanoutTask{Envelope: Envelope{MessageID: 1}, Partition: Partition{ID: 7, LeaderNodeID: 2}}

	if err := router.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}

	if len(local.tasks) != 0 {
		t.Fatalf("local tasks = %#v, want none", local.tasks)
	}
	if len(remote.calls) != 1 || remote.calls[0].nodeID != 2 || !reflect.DeepEqual(remote.calls[0].task, task) {
		t.Fatalf("remote calls = %#v, want node 2 task", remote.calls)
	}
}

func TestFanoutTaskRouterReturnsRetryableWhenRemoteUnavailable(t *testing.T) {
	router := NewFanoutTaskRouter(FanoutTaskRouterOptions{LocalNodeID: 1})

	err := router.RunTask(context.Background(), FanoutTask{Partition: Partition{LeaderNodeID: 2}})
	if !errors.Is(err, ErrRetryableFanoutTask) {
		t.Fatalf("RunTask() error = %v, want ErrRetryableFanoutTask", err)
	}
}

type recordingFanoutRunner struct {
	tasks []FanoutTask
	err   error
}

func (r *recordingFanoutRunner) RunTask(_ context.Context, task FanoutTask) error {
	r.tasks = append(r.tasks, task)
	return r.err
}

type recordingFanoutForwarder struct {
	calls []fanoutForwardCall
	err   error
}

func (f *recordingFanoutForwarder) ForwardFanoutTask(_ context.Context, nodeID uint64, task FanoutTask) error {
	f.calls = append(f.calls, fanoutForwardCall{nodeID: nodeID, task: task})
	return f.err
}

type fanoutForwardCall struct {
	nodeID uint64
	task   FanoutTask
}
