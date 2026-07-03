package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrRouteNotReady reports that delivery fanout routing is not ready.
var ErrRouteNotReady = errors.New("internal/runtime/delivery: route not ready")

// ErrRetryableFanoutTask reports that a fanout task should be retried later.
var ErrRetryableFanoutTask = errors.New("internal/runtime/delivery: retryable fanout task")

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
	// Observer receives bounded fanout task routing observations.
	Observer Observer
}

// FanoutTaskRouter routes fanout tasks by Partition.LeaderNodeID.
type FanoutTaskRouter struct {
	// localNodeID is this node's cluster identity.
	localNodeID uint64
	// local runs local authority fanout tasks.
	local FanoutTaskRunner
	// remote forwards non-local authority fanout tasks.
	remote FanoutTaskForwarder
	// observer receives fanout task routing observations.
	observer Observer
}

// NewFanoutTaskRouter creates a partition-leader fanout router.
func NewFanoutTaskRouter(opts FanoutTaskRouterOptions) *FanoutTaskRouter {
	return &FanoutTaskRouter{localNodeID: opts.LocalNodeID, local: opts.Local, remote: opts.Remote, observer: opts.Observer}
}

// RunTask routes one fanout task to the local runner or a remote authority node.
func (r *FanoutTaskRouter) RunTask(ctx context.Context, task FanoutTask) error {
	if r == nil {
		return nil
	}
	leader := task.Partition.LeaderNodeID
	if leader == 0 || r.localNodeID == 0 || leader == r.localNodeID {
		var started time.Time
		if r.observer != nil {
			started = time.Now()
		}
		targetNodeID := leader
		if targetNodeID == 0 {
			targetNodeID = r.localNodeID
		}
		if r.local == nil {
			if r.observer != nil {
				r.observeFanoutTask(task, targetNodeID, DeliveryFanoutPathLocal, time.Since(started), nil)
			}
			return nil
		}
		err := r.local.RunTask(ctx, task)
		if r.observer != nil {
			r.observeFanoutTask(task, targetNodeID, DeliveryFanoutPathLocal, time.Since(started), err)
		}
		return err
	}
	if r.remote == nil {
		err := fmt.Errorf("%w: missing remote fanout forwarder for node %d", ErrRetryableFanoutTask, leader)
		if r.observer != nil {
			r.observeFanoutTask(task, leader, DeliveryFanoutPathRemote, 0, err)
		}
		return err
	}
	var started time.Time
	if r.observer != nil {
		started = time.Now()
	}
	if err := r.remote.ForwardFanoutTask(ctx, leader, task); err != nil {
		err = fmt.Errorf("%w: %w", ErrRetryableFanoutTask, err)
		if r.observer != nil {
			r.observeFanoutTask(task, leader, DeliveryFanoutPathRemote, time.Since(started), err)
		}
		return err
	}
	if r.observer != nil {
		r.observeFanoutTask(task, leader, DeliveryFanoutPathRemote, time.Since(started), nil)
	}
	return nil
}

func (r *FanoutTaskRouter) observeFanoutTask(task FanoutTask, targetNodeID uint64, path string, dur time.Duration, err error) {
	if r == nil || r.observer == nil {
		return
	}
	r.observer.ObserveFanoutTask(FanoutTaskEvent{
		TargetNodeID: targetNodeID,
		PartitionID:  task.Partition.ID,
		Path:         path,
		Result:       deliveryResultForError(err),
		ErrorClass:   DeliveryErrorClass(err),
		Duration:     dur,
	})
}
