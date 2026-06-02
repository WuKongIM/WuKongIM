package delivery

import (
	"context"
	"errors"
	"fmt"
)

// ErrRouteNotReady reports that delivery fanout routing is not ready.
var ErrRouteNotReady = errors.New("internalv2/runtime/delivery: route not ready")

// ErrRetryableFanoutTask reports that a fanout task should be retried later.
var ErrRetryableFanoutTask = errors.New("internalv2/runtime/delivery: retryable fanout task")

// FanoutTaskRunner executes one planned fanout task.
type FanoutTaskRunner interface {
	RunTask(context.Context, FanoutTask) error
}

// FanoutTaskForwarder forwards one fanout task to a remote authority node.
type FanoutTaskForwarder interface {
	ForwardFanoutTask(context.Context, uint64, FanoutTask) error
}

// FanoutTaskRouterOptions configures partition-leader fanout routing.
type FanoutTaskRouterOptions struct {
	// LocalNodeID is this node's cluster identity; zero treats all tasks as local.
	LocalNodeID uint64
	// Local runs tasks owned by this node or by a leaderless default partition.
	Local FanoutTaskRunner
	// Remote forwards tasks whose partition leader is another node.
	Remote FanoutTaskForwarder
}

// FanoutTaskRouter routes fanout tasks by Partition.LeaderNodeID.
type FanoutTaskRouter struct {
	// localNodeID is this node's cluster identity.
	localNodeID uint64
	// local runs local authority fanout tasks.
	local FanoutTaskRunner
	// remote forwards non-local authority fanout tasks.
	remote FanoutTaskForwarder
}

// NewFanoutTaskRouter creates a partition-leader fanout router.
func NewFanoutTaskRouter(opts FanoutTaskRouterOptions) *FanoutTaskRouter {
	return &FanoutTaskRouter{localNodeID: opts.LocalNodeID, local: opts.Local, remote: opts.Remote}
}

// RunTask routes one fanout task to the local runner or a remote authority node.
func (r *FanoutTaskRouter) RunTask(ctx context.Context, task FanoutTask) error {
	if r == nil {
		return nil
	}
	leader := task.Partition.LeaderNodeID
	if leader == 0 || r.localNodeID == 0 || leader == r.localNodeID {
		if r.local == nil {
			return nil
		}
		return r.local.RunTask(ctx, task)
	}
	if r.remote == nil {
		return fmt.Errorf("%w: missing remote fanout forwarder for node %d", ErrRetryableFanoutTask, leader)
	}
	if err := r.remote.ForwardFanoutTask(ctx, leader, task); err != nil {
		return fmt.Errorf("%w: %w", ErrRetryableFanoutTask, err)
	}
	return nil
}
