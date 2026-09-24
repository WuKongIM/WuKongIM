package mqttsession

import (
	"context"
	"errors"
	"sync"
	"time"

	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	ErrDeliveryClosed   = errors.New("mqttsession: delivery stream closed")
	ErrDeliveryCallback = errors.New("mqttsession: delivery dependency failed")
)

// DeliveryDisposition distinguishes confirmed enqueue from definite non-writes.
// Any error/unknown result is ambiguous and terminates this connection's stream.
type DeliveryDisposition uint8

const (
	DeliveryQueued  DeliveryDisposition = 1
	DeliveryBusy    DeliveryDisposition = 2
	DeliveryExpired DeliveryDisposition = 3
)

// DeliverySink is bound to one physical connection. Enqueue is synchronous and
// bounded by ctx; Queued proves enqueue only. Busy/Expired guarantee no write.
// Close accepts a terminal reason (zero for transport failure); it grants no
// isolation proof. Neither method may retain untracked asynchronous effects.
type DeliverySink interface {
	Enqueue(context.Context, PreparedDelivery, bool) (DeliveryDisposition, error)
	Close(context.Context, meta.MQTTSessionEndReason) error
}

// SessionEnder proves exact-owner isolation and commits the durable end decision;
// the sender calls it only after releasing its execution scope.
type SessionEnder interface {
	End(context.Context, EndCommand) error
}
type SenderOptions struct {
	// Window supplies shared authority, original-content, permission and Owners
	// ports for both new admission and existing-exchange recovery. Owners needs
	// at least two operation slots; app budgets additional concurrent control work.
	Window WindowAdmissionOptions
	Ender  SessionEnder
	// CleanupTimeout bounds joined terminal cleanup independently of canceled work.
	CleanupTimeout time.Duration
}

// Sender composes ordered recovery, new admission and final receive permission.
// App/runtime owns one stream per connection, source discovery and fair scheduling.
type Sender struct {
	window         *WindowAdmission
	recovery       *ExchangeRecovery
	ender          SessionEnder
	cleanupTimeout time.Duration
}

// DeliveryTurn reports observed work. Idle covers the supplied source/accounted
// prefix only; it is not complete subscription discovery or a delivery receipt.
type DeliveryTurn struct {
	Enqueued, Advanced, Busy, Idle bool
	// Ending retains required cleanup after a failed End. Ended confirms End
	// succeeded, never that source responsibility has already been reclaimed.
	Ending, Ended bool
}

// DeliveryStream retains only a sent-order cursor and one private body-free
// completion token. Its nonblocking gate forbids concurrent/reentrant sends.
type DeliveryStream struct {
	gate       sync.Mutex
	sender     *Sender
	connection Connection
	sink       DeliverySink
	// initialOrder freezes which exchanges predate this connection's sends.
	initialOrder uint64
	// after advances only for a confirmed enqueue; pending retains no payload.
	after          meta.MQTTInflightCursor
	pending        *qos0Completion
	closed, ending bool
	// endReason preserves the first trusted terminal decision through retries.
	endReason meta.MQTTSessionEndReason
}

// deliveryPermission is minted by original-content preparation, not by callers
// presenting public delivery fields. Begun QoS 1 ignores replacement options.
type deliveryPermission struct {
	request              SubscriptionRequest
	version              uint64
	subscriptionRevision uint64
}

func NewSender(o SenderOptions) (*Sender, error) {
	if o.CleanupTimeout == 0 {
		o.CleanupTimeout = time.Second
	}
	if o.Ender == nil || o.CleanupTimeout <= 0 || o.CleanupTimeout > 5*time.Second {
		return nil, ErrInvalid
	}
	w, err := NewWindowAdmission(o.Window)
	if err != nil {
		return nil, err
	}
	v := w.options
	r, err := NewExchangeRecovery(ExchangeRecoveryOptions{Store: v.Store, Owners: v.Owners, Metadata: v.Metadata, Channels: v.Channels, Authorization: v.Authorization, Now: v.Now, Timeout: v.Timeout, MaxBytes: v.MaxBytes})
	if err != nil {
		return nil, err
	}
	return &Sender{window: w, recovery: r, ender: o.Ender, cleanupTimeout: o.CleanupTimeout}, nil
}

// Open must run exactly once for an activated connection before new admission.
// SessionPresent comes from Connect, never a peer claim or current row heuristic.
func (s *Sender) Open(parent context.Context, c Connection, sink DeliverySink) (stream *DeliveryStream, err error) {
	if s == nil || sink == nil || c.UID == "" {
		return nil, ErrInvalid
	}
	op, ctx, cancel, err := s.window.guard.begin(parent, c.Owner)
	if err != nil {
		return nil, err
	}
	defer finishSubscription(op, cancel, &err)
	r, err := s.window.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: c.Owner.Key.Namespace, ClientID: c.Owner.Key.ClientID})
	if err != nil {
		return nil, err
	}
	if err = s.window.guard.checkSession(ctx, op, c.Owner, r.Session); err != nil {
		return nil, err
	}
	if op.UID() != c.UID || (!c.SessionPresent && r.Session.NextDeliveryOrder != 1) {
		return nil, ErrEvidence
	}
	return &DeliveryStream{sender: s, connection: c, sink: sink, initialOrder: r.Session.NextDeliveryOrder - 1}, nil
}

// Turn performs at most one enqueue or pending completion. A zero key only
// recovers existing exchanges. Busy adds no queue; callers schedule another turn.
func (s *DeliveryStream) Turn(parent context.Context, key meta.MQTTDeliveryCursorKey) (out DeliveryTurn, err error) {
	if s == nil || parent == nil {
		return out, ErrInvalid
	}
	if !s.gate.TryLock() {
		return DeliveryTurn{Busy: true}, nil
	}
	defer s.gate.Unlock()
	if s.ending {
		return s.finishEnding(parent)
	}
	if s.closed {
		return out, ErrDeliveryClosed
	}
	if key != (meta.MQTTDeliveryCursorKey{}) && (meta.ValidateMQTTRead(meta.MQTTRead{Kind: meta.MQTTReadAccounting, CursorKey: key}) != nil || key.Namespace != s.connection.Owner.Key.Namespace || key.ClientID != s.connection.Owner.Key.ClientID || key.SessionGeneration != s.connection.Owner.SessionGeneration) {
		return out, ErrInvalid
	}
	w := s.sender.window
	op, ctx, cancel, err := w.guard.begin(parent, s.connection.Owner)
	if err != nil {
		if errors.Is(err, runtime.ErrOwnerLimit) {
			return DeliveryTurn{Busy: true}, nil
		}
		return out, err
	}
	defer func() {
		if recover() != nil {
			err = ErrDeliveryCallback
			s.closed = true
		}
		if !s.closed && errors.Is(err, runtime.ErrOwnerLimit) {
			out.Busy, err = true, nil
		}
		if errors.Is(err, ErrSubscriptionDenied) || errors.Is(err, ErrSubscriptionRevoked) {
			s.closed, s.ending = true, true
			s.endReason = meta.MQTTSessionRevoked
		}
		if errors.Is(err, ErrFenced) || errors.Is(err, ErrClock) || errors.Is(err, runtime.ErrOwnerFenced) {
			s.closed = true
		}
		if s.closed {
			_ = w.options.Owners.Fence(s.connection.Owner)
		}
		cancel()
		op.Done() // Joined cleanup must never wait for this turn itself.
		if s.closed {
			cleanup, e := s.finishEnding(parent)
			out.Ending, out.Ended = cleanup.Ending, cleanup.Ended
			err = errors.Join(err, e)
		}
	}()
	if s.pending != nil {
		if err = s.completeEnqueued(ctx, op); err != nil {
			return out, err
		}
		return DeliveryTurn{Advanced: true}, nil
	}
	r, err := s.sender.recovery.Next(ctx, s.connection.Owner, s.after)
	if err != nil {
		return out, err
	}
	d := r.Delivery
	if d == nil {
		if key == (meta.MQTTDeliveryCursorKey{}) {
			return DeliveryTurn{Idle: true}, nil
		}
		prepared, e := w.Prepare(ctx, s.connection.Owner, key)
		if e != nil {
			return out, e
		}
		out.Advanced, out.Busy, out.Idle = prepared.Advanced, prepared.Full, prepared.Idle
		d = prepared.Delivery
		if d == nil {
			return out, nil
		}
	}
	if err = s.authorizeEnqueue(ctx, op, *d); err != nil {
		return out, err
	}
	dup := d.QoS == 1 && d.Exchange.DeliveryOrder <= s.initialOrder
	if dup && !s.connection.SessionPresent {
		return out, ErrEvidence
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return out, err
	}
	disposition, err := s.sink.Enqueue(ctx, *d, dup)
	if err != nil {
		s.closed = true
		return out, err
	}
	switch disposition {
	case DeliveryQueued:
		out.Enqueued = true
	case DeliveryBusy:
		out.Busy = true
	case DeliveryExpired:
		if d.QoS != 0 {
			s.closed = true
			return out, ErrEvidence
		}
	default:
		s.closed = true
		return out, ErrEvidence
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		s.closed = true
		return out, err
	}
	if disposition != DeliveryQueued {
		return out, nil
	}
	if d.QoS == 1 {
		s.after = meta.MQTTInflightCursor{PacketID: d.Exchange.PacketID, DeliveryOrder: d.Exchange.DeliveryOrder}
		return out, nil
	}
	if d.completion == nil || d.completion.issuer != w {
		s.closed = true
		return out, ErrEvidence
	}
	if !d.completion.preclaimed {
		s.pending = d.completion
		if err = s.completeEnqueued(ctx, op); err != nil {
			return out, err
		}
		out.Advanced = true
	}
	return out, nil
}

// finishEnding runs without an admitted scope, retaining failed end decisions
// for a bounded future turn. Superseded owners are not followed or reported ended.
func (s *DeliveryStream) finishEnding(parent context.Context) (out DeliveryTurn, err error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), s.sender.cleanupTimeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = ErrDeliveryCallback
			out.Ending = s.ending
		}
	}()
	reason := meta.MQTTSessionEndReason(0)
	if s.ending {
		reason = s.endReason
	}
	err = closeDeliverySink(ctx, s.sink, reason)
	if s.ending {
		e := s.sender.ender.End(ctx, EndCommand{Owner: s.connection.Owner, Reason: reason})
		if e == nil {
			s.ending = false
			out.Ended = true
		} else if errors.Is(e, ErrFenced) {
			s.ending = false
		}
		out.Ending = s.ending
		return out, errors.Join(err, e)
	}
	return out, errors.Join(err, s.sender.window.options.Owners.Quiesce(ctx, s.connection.Owner))
}
func closeDeliverySink(ctx context.Context, sink DeliverySink, reason meta.MQTTSessionEndReason) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrDeliveryCallback
		}
	}()
	return sink.Close(ctx, reason)
}
