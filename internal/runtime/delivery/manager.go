package delivery

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
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
	// AckBatchObserver receives optional aggregate bind and finish stage observations.
	AckBatchObserver AckBatchObserver
}

// Manager is the delivery runtime facade used by app adapters.
// It admits committed work into a bounded queue when fanout ports are configured.
type Manager struct {
	planner          *Planner
	runner           FanoutTaskRunner
	acks             *AckTracker
	async            *managerAsync
	ackObserver      AckObserver
	ackBatchObserver AckBatchObserver
	ackMu            sync.Mutex
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
		planner:          opts.Planner,
		runner:           runner,
		acks:             acks,
		ackObserver:      opts.AckObserver,
		ackBatchObserver: opts.AckBatchObserver,
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
	m.ackMu.Lock()
	defer m.ackMu.Unlock()

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
	m.ackMu.Lock()
	defer m.ackMu.Unlock()

	removed := m.acks.SessionClosed(cmd.UID, cmd.SessionID)
	result := DeliveryAckResultNoop
	if len(removed) > 0 {
		result = DeliveryAckResultOK
	}
	m.observeAck(DeliveryAckActionSessionClosed, result, len(removed), m.acks.PendingCount())
	return nil
}

// BindPendingAck records one already-successful delivery waiting for a client recvack.
func (m *Manager) BindPendingAck(pending PendingRecvAck) bool {
	result := m.BindPendingAckResult(pending)
	if !result.Bound {
		return false
	}
	// A concurrent identity cleanup may consume the reservation first.
	m.FinishPendingAck(pending, result.Token)
	return true
}

// BindPendingAckResult records one delivery and returns a token that scopes a
// later success finish or failed-delivery rollback to this bind attempt.
func (m *Manager) BindPendingAckResult(pending PendingRecvAck) AckBindResult {
	if m == nil || m.acks == nil {
		return AckBindResult{}
	}
	m.ackMu.Lock()
	defer m.ackMu.Unlock()

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
	return result
}

// RollbackPendingAck cancels only the bind token owned by one failed delivery.
// It never clears a concurrent or earlier successful bind for the same key.
func (m *Manager) RollbackPendingAck(pending PendingRecvAck, token AckBindToken) bool {
	if m == nil || m.acks == nil {
		return false
	}
	m.ackMu.Lock()
	defer m.ackMu.Unlock()

	result := m.acks.CancelBind(pending, token)
	eventResult := DeliveryAckResultMiss
	changed := 0
	if result.Canceled {
		eventResult = DeliveryAckResultOK
		if result.Removed {
			changed = 1
		}
	}
	m.observeAck(DeliveryAckActionRollback, eventResult, changed, result.PendingCount)
	return result.Canceled
}

// FinishPendingAck commits one successful delivery reservation. It deliberately
// bypasses the Manager observation lock because finishing cannot change the
// pending identity count exposed by observers.
func (m *Manager) FinishPendingAck(pending PendingRecvAck, token AckBindToken) bool {
	if m == nil || m.acks == nil {
		return false
	}
	return m.acks.FinishBind(pending, token)
}

// FinishPendingAcks commits successful aligned batch indexes with one lock per
// affected tracker shard and without emitting a no-count-change observation.
// rollback is the number of bind reservations the caller actually canceled
// while processing this owner-push batch; omitted in-flight tokens are not
// inferred to have rolled back.
func (m *Manager) FinishPendingAcks(pending []PendingRecvAck, tokens []AckBindToken, indexes []int, rollback int) int {
	if m == nil || m.acks == nil {
		return 0
	}
	batchObserver := m.ackBatchObserver
	observeBatch := batchObserver != nil
	var started time.Time
	if observeBatch {
		started = time.Now()
	}
	finished, shards, selected := m.acks.finishBindBatch(pending, tokens, indexes)
	if observeBatch {
		bound := countBoundAckTokens(pending, tokens)
		rejected := len(pending) - bound
		if rollback < 0 {
			rollback = 0
		}
		batchObserver.ObserveAckBatch(AckBatchEvent{
			Phase:    DeliveryAckBatchPhaseFinish,
			Outcome:  finishAckBatchOutcome(finished, selected, rejected, rollback),
			Items:    len(pending),
			Shards:   shards,
			Rejected: rejected,
			Rollback: rollback,
			Duration: time.Since(started),
		})
	}
	return finished
}

// BindPendingAcks reserves an item-aligned delivery batch waiting for client
// recvacks. Every non-zero token must be finished or rolled back unless
// identity cleanup wins first. The batch holds the manager
// mutation/observation lock once and the tracker acquires each affected
// internal shard once.
func (m *Manager) BindPendingAcks(pending []PendingRecvAck) AckBindBatchResult {
	if m == nil || m.acks == nil {
		return AckBindBatchResult{Tokens: make([]AckBindToken, len(pending))}
	}
	if len(pending) == 0 {
		return AckBindBatchResult{PendingCount: m.acks.PendingCount()}
	}
	batchObserver := m.ackBatchObserver
	observeBatch := batchObserver != nil
	var started time.Time
	if observeBatch {
		started = time.Now()
	}
	result := func() AckBindBatchResult {
		m.ackMu.Lock()
		defer m.ackMu.Unlock()

		result := m.acks.BindBatch(pending)
		eventResult := DeliveryAckResultRejected
		for _, token := range result.Tokens {
			if token.Valid() {
				eventResult = DeliveryAckResultOK
				break
			}
		}
		m.observeAck(DeliveryAckActionBind, eventResult, result.Added, result.PendingCount)
		return result
	}()
	if observeBatch {
		rejected := len(pending) - result.Bound
		batchObserver.ObserveAckBatch(AckBatchEvent{
			Phase:    DeliveryAckBatchPhaseBind,
			Outcome:  bindAckBatchOutcome(result.Bound, rejected),
			Items:    len(pending),
			Shards:   result.Shards,
			Rejected: rejected,
			Duration: time.Since(started),
		})
	}
	return result
}

// ExpirePendingAcks removes pending recvacks older than ttl.
func (m *Manager) ExpirePendingAcks(ttl time.Duration) []PendingRecvAck {
	if m == nil || m.acks == nil {
		return nil
	}
	m.ackMu.Lock()
	defer m.ackMu.Unlock()

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

func countBoundAckTokens(pending []PendingRecvAck, tokens []AckBindToken) int {
	limit := len(pending)
	if len(tokens) < limit {
		limit = len(tokens)
	}
	bound := 0
	for i := 0; i < limit; i++ {
		if validPendingRecvAck(pending[i]) && tokens[i].Valid() {
			bound++
		}
	}
	return bound
}

func bindAckBatchOutcome(bound, rejected int) string {
	switch {
	case bound == 0:
		return DeliveryAckBatchOutcomeRejected
	case rejected > 0:
		return DeliveryAckBatchOutcomePartial
	default:
		return DeliveryAckBatchOutcomeOK
	}
}

func finishAckBatchOutcome(finished, selected, rejected, rollback int) string {
	switch {
	case finished == selected && selected > 0 && rejected == 0 && rollback == 0:
		return DeliveryAckBatchOutcomeOK
	case finished > 0:
		return DeliveryAckBatchOutcomePartial
	case rollback > 0:
		return DeliveryAckBatchOutcomeRolledBack
	case rejected > 0 && selected == 0:
		return DeliveryAckBatchOutcomeRejected
	default:
		return DeliveryAckBatchOutcomeMiss
	}
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
