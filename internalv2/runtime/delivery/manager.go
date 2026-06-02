package delivery

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

// ManagerOptions configures the synchronous delivery runtime facade.
type ManagerOptions struct {
	// Planner creates fanout tasks from committed message events.
	Planner *Planner
	// Worker executes fanout tasks synchronously.
	Worker *FanoutWorker
	// Runner executes fanout tasks; when set it overrides Worker.
	Runner FanoutTaskRunner
	// Acks tracks pending recipient recvacks; nil creates a default tracker.
	Acks *AckTracker
	// AsyncQueueSize enables bounded asynchronous execution when greater than zero.
	AsyncQueueSize int
	// AsyncWorkers controls asynchronous fanout worker count; values <= 0 use one worker when async is enabled.
	AsyncWorkers int
	// ManagerObserver receives async manager admission and terminal observations.
	ManagerObserver ManagerObserver
}

// Manager is the synchronous delivery runtime facade used by app adapters.
type Manager struct {
	planner         *Planner
	runner          FanoutTaskRunner
	acks            *AckTracker
	asyncQueueSize  int
	asyncWorkers    int
	managerObserver ManagerObserver
}

// NewManager creates a delivery runtime facade.
func NewManager(opts ManagerOptions) *Manager {
	acks := opts.Acks
	if acks == nil {
		acks = NewAckTracker(AckTrackerOptions{})
	}
	runner := opts.Runner
	if runner == nil {
		runner = opts.Worker
	}
	asyncWorkers := opts.AsyncWorkers
	if opts.AsyncQueueSize > 0 && asyncWorkers <= 0 {
		asyncWorkers = defaultManagerAsyncWorkers
	}
	return &Manager{
		planner:         opts.Planner,
		runner:          runner,
		acks:            acks,
		asyncQueueSize:  opts.AsyncQueueSize,
		asyncWorkers:    asyncWorkers,
		managerObserver: opts.ManagerObserver,
	}
}

// Start prepares the manager lifecycle; async queue behavior is implemented by later slices.
func (m *Manager) Start(context.Context) error {
	return nil
}

// Stop closes manager lifecycle resources; async queue behavior is implemented by later slices.
func (m *Manager) Stop(context.Context) error {
	return nil
}

// SubmitCommitted plans and executes fanout for one committed message event.
func (m *Manager) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if m == nil || m.planner == nil || m.runner == nil {
		return nil
	}
	if m.asyncQueueSize > 0 {
		if m.managerObserver != nil {
			m.managerObserver.ObserveManagerAdmission(ManagerAdmissionEvent{
				Result:     "closed",
				QueueDepth: 0,
			})
		}
		return ErrManagerClosed
	}
	tasks, err := m.planner.Plan(ctx, envelopeFromEvent(event))
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := m.runner.RunTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

// Recvack clears a pending recipient recvack and ignores unknown acks.
func (m *Manager) Recvack(_ context.Context, cmd Recvack) error {
	if m == nil || m.acks == nil {
		return nil
	}
	m.acks.Ack(cmd)
	return nil
}

// SessionClosed clears pending recvacks for a closed recipient-owner session.
func (m *Manager) SessionClosed(_ context.Context, cmd SessionClosed) error {
	if m == nil || m.acks == nil {
		return nil
	}
	m.acks.SessionClosed(cmd.UID, cmd.SessionID)
	return nil
}

// BindPendingAck records one delivery waiting for a client recvack.
func (m *Manager) BindPendingAck(pending PendingRecvAck) bool {
	if m == nil || m.acks == nil {
		return false
	}
	return m.acks.Bind(pending)
}

// ExpirePendingAcks removes pending recvacks older than ttl.
func (m *Manager) ExpirePendingAcks(ttl time.Duration) []PendingRecvAck {
	if m == nil || m.acks == nil {
		return nil
	}
	return m.acks.Expire(ttl)
}

// PendingAckCount returns the current pending recvack count for tests and diagnostics.
func (m *Manager) PendingAckCount() int {
	if m == nil || m.acks == nil {
		return 0
	}
	return m.acks.PendingCount()
}

func envelopeFromEvent(event messageevents.MessageCommitted) Envelope {
	return Envelope{
		MessageID:         event.MessageID,
		MessageSeq:        event.MessageSeq,
		ChannelID:         event.ChannelID,
		ChannelType:       event.ChannelType,
		FromUID:           event.FromUID,
		SenderNodeID:      event.SenderNodeID,
		SenderSessionID:   event.SenderSessionID,
		ClientMsgNo:       event.ClientMsgNo,
		RedDot:            event.RedDot,
		Payload:           append([]byte(nil), event.Payload...),
		MessageScopedUIDs: append([]string(nil), event.MessageScopedUIDs...),
	}
}
