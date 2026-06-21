package delivery

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

// ManagerOptions configures the delivery runtime facade.
type ManagerOptions struct {
	// Planner creates fanout tasks from committed message events.
	Planner *Planner
	// Runner executes planned fanout tasks for accepted commands.
	Runner FanoutTaskRunner
	// Acks tracks pending recipient recvacks; nil creates a default tracker.
	Acks *AckTracker
	// AsyncQueueSize bounds committed-message commands waiting for fanout.
	AsyncQueueSize int
	// AsyncWorkers controls fanout worker count; values <= 0 use one worker.
	AsyncWorkers int
	// ManagerObserver receives async manager admission and terminal observations.
	ManagerObserver ManagerObserver
	// AckObserver receives owner-local pending recvack state changes.
	AckObserver AckObserver
}

// Manager is the delivery runtime facade used by app adapters.
// It admits committed work into a bounded queue when fanout ports are configured.
type Manager struct {
	planner     *Planner
	runner      FanoutTaskRunner
	acks        *AckTracker
	async       *managerAsync
	ackObserver AckObserver
}

// NewManager creates a delivery runtime facade.
func NewManager(opts ManagerOptions) *Manager {
	acks := opts.Acks
	if acks == nil {
		acks = NewAckTracker(AckTrackerOptions{})
	}
	runner := opts.Runner
	asyncWorkers := opts.AsyncWorkers
	if asyncWorkers <= 0 {
		asyncWorkers = defaultManagerAsyncWorkers
	}
	manager := &Manager{
		planner:     opts.Planner,
		runner:      runner,
		acks:        acks,
		ackObserver: opts.AckObserver,
	}
	if opts.Planner != nil && runner != nil {
		manager.async = newManagerAsync(manager, opts.AsyncQueueSize, asyncWorkers, opts.ManagerObserver)
	}
	return manager
}

// Start prepares the manager lifecycle.
func (m *Manager) Start(ctx context.Context) error {
	if m == nil || m.async == nil {
		return nil
	}
	return m.async.start(ctx)
}

// Stop closes manager lifecycle resources.
func (m *Manager) Stop(ctx context.Context) error {
	if m == nil || m.async == nil {
		return nil
	}
	return m.async.stop(ctx)
}

// SubmitCommitted admits one committed message event into async fanout.
func (m *Manager) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if m == nil || m.planner == nil || m.runner == nil || m.async == nil {
		return nil
	}
	return m.async.submit(ctx, envelopeFromEvent(event))
}

func (m *Manager) runEnvelope(ctx context.Context, env Envelope) error {
	tasks, err := m.planner.Plan(ctx, env)
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
	_, ok := m.acks.Ack(cmd)
	result := DeliveryAckResultMiss
	changed := 0
	if ok {
		result = DeliveryAckResultOK
		changed = 1
	}
	m.observeAck(DeliveryAckActionAck, result, changed, m.acks.PendingCount())
	return nil
}

// SessionClosed clears pending recvacks for a closed recipient-owner session.
func (m *Manager) SessionClosed(_ context.Context, cmd SessionClosed) error {
	if m == nil || m.acks == nil {
		return nil
	}
	removed := m.acks.SessionClosed(cmd.UID, cmd.SessionID)
	result := DeliveryAckResultNoop
	if len(removed) > 0 {
		result = DeliveryAckResultOK
	}
	m.observeAck(DeliveryAckActionSessionClosed, result, len(removed), m.acks.PendingCount())
	return nil
}

// BindPendingAck records one delivery waiting for a client recvack.
func (m *Manager) BindPendingAck(pending PendingRecvAck) bool {
	if m == nil || m.acks == nil {
		return false
	}
	result := m.acks.BindResult(pending)
	eventResult := DeliveryAckResultRejected
	changed := 0
	if result.Bound {
		eventResult = DeliveryAckResultOK
		if result.Added {
			changed = 1
		}
	}
	m.observeAck(DeliveryAckActionBind, eventResult, changed, result.PendingCount)
	return result.Bound
}

// ExpirePendingAcks removes pending recvacks older than ttl.
func (m *Manager) ExpirePendingAcks(ttl time.Duration) []PendingRecvAck {
	if m == nil || m.acks == nil {
		return nil
	}
	removed := m.acks.Expire(ttl)
	result := DeliveryAckResultNoop
	if len(removed) > 0 {
		result = DeliveryAckResultOK
	}
	m.observeAck(DeliveryAckActionExpire, result, len(removed), m.acks.PendingCount())
	return removed
}

// PendingAckCount returns the current pending recvack count for tests and diagnostics.
func (m *Manager) PendingAckCount() int {
	if m == nil || m.acks == nil {
		return 0
	}
	return m.acks.PendingCount()
}

func (m *Manager) observeAck(action, result string, changed, pendingCount int) {
	if m == nil || m.ackObserver == nil {
		return
	}
	m.ackObserver.ObserveAck(AckEvent{
		Action:       action,
		Result:       result,
		Changed:      changed,
		PendingCount: pendingCount,
	})
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
