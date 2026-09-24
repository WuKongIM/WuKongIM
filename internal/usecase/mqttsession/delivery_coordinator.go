package mqttsession

import (
	"context"
	"errors"
	"sync"

	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type DeliveryCoordinatorOptions struct {
	// Sender and Accounting share current cluster authority and receive policy.
	Sender     *Sender
	Accounting *Accounting
	// MaxSubscriptions bounds retained source continuations, matching subscription
	// admission's limit. Zero defaults to 128; the maximum is 1024.
	MaxSubscriptions int
}

// DeliveryCoordinator discovers current sources and composes bounded accounting
// and sending. It creates no worker; app registers each opened task exactly once.
type DeliveryCoordinator struct{ options DeliveryCoordinatorOptions }

// ConnectionDelivery owns one Sender stream and body-free pagination hints.
// Runtime may coalesce wakes, but cannot reset this stream on an idle result.
type ConnectionDelivery struct {
	gate          sync.Mutex
	coordinator   *DeliveryCoordinator
	stream        *DeliveryStream
	afterTopic    string
	pass          uint64
	sources       map[uint64]deliverySourceHint
	roundProgress bool
	done          bool
}

type deliverySourceHint struct {
	after meta.MQTTDeliveryCursorKey
	seen  uint64
}
type deliverySelection struct {
	key     meta.MQTTDeliveryCursorKey
	wrapped bool
}

func NewDeliveryCoordinator(o DeliveryCoordinatorOptions) (*DeliveryCoordinator, error) {
	if o.MaxSubscriptions == 0 {
		o.MaxSubscriptions = 128
	}
	if o.Sender == nil || o.Sender.window == nil || o.Sender.recovery == nil || o.Sender.ender == nil || o.Accounting == nil || o.Accounting.options.Store == nil || o.MaxSubscriptions < 1 || o.MaxSubscriptions > 1024 {
		return nil, ErrInvalid
	}
	return &DeliveryCoordinator{options: o}, nil
}

// Open freezes recovery order before any new exchange on this connection.
func (c *DeliveryCoordinator) Open(ctx context.Context, connection Connection, sink DeliverySink) (*ConnectionDelivery, error) {
	if c == nil {
		return nil, ErrInvalid
	}
	s, err := c.options.Sender.Open(ctx, connection, sink)
	if err != nil {
		return nil, err
	}
	return &ConnectionDelivery{coordinator: c, stream: s, pass: 1, sources: make(map[uint64]deliverySourceHint)}, nil
}

// Turn recovers first, then visits at most one subscription and source, accounts
// one protected page and attempts one enqueue. Busy/idle sources yield; no body
// or prepared publication survives the call. Sources are hints, never authority.
func (s *ConnectionDelivery) Turn(parent context.Context) (out runtime.DeliveryWork, err error) {
	if s == nil || parent == nil {
		return out, ErrInvalid
	}
	if !s.gate.TryLock() {
		return out, nil
	}
	defer s.gate.Unlock()
	defer func() {
		if recover() != nil {
			out, err = runtime.DeliveryWork{}, ErrDeliveryCallback
		}
	}()
	ctx, cancel := context.WithTimeout(parent, s.stream.sender.window.options.Timeout)
	defer cancel()
	if s.done {
		return runtime.DeliveryWork{Done: true}, nil
	}
	if s.stream.closed {
		return s.finishClosed(parent)
	}
	state, err := s.connectionState(ctx)
	if err != nil {
		return out, err
	}
	if state == nil || sessionOwner(*state) != s.stream.connection.Owner || state.State != meta.MQTTSessionActive {
		reason := meta.MQTTSessionEndReason(0)
		if state != nil && sessionOwner(*state) == s.stream.connection.Owner && state.State == meta.MQTTSessionEnded && state.TerminationReason != meta.MQTTSessionExpired && state.TerminationReason != meta.MQTTSessionCleanStart {
			reason = state.TerminationReason
		}
		return s.close(parent, reason)
	}
	recovered, err := s.stream.Turn(ctx, meta.MQTTDeliveryCursorKey{})
	if recovered.Ended {
		s.done = true
		return runtime.DeliveryWork{Done: true}, nil
	}
	if err != nil || !recovered.Idle {
		return s.resolveFailure(parent, runtime.DeliveryWork{Again: recovered.Enqueued || recovered.Advanced}, err)
	}
	selected, err := s.selectSource(ctx)
	if err != nil {
		return s.resolveFailure(parent, out, err)
	}
	if selected.key == (meta.MQTTDeliveryCursorKey{}) {
		return s.continueRotation(false, selected.wrapped), nil
	}
	accounted, err := s.account(ctx, selected.key)
	if err != nil {
		return s.resolveFailure(parent, out, err)
	}
	if accounted.Ended {
		return s.close(parent, meta.MQTTSessionQuota)
	}
	delivered, err := s.stream.Turn(ctx, selected.key)
	if delivered.Ended {
		s.done = true
		return runtime.DeliveryWork{Done: true}, nil
	}
	out = s.continueRotation(accounted.Changed || delivered.Enqueued || delivered.Advanced, selected.wrapped)
	return s.resolveFailure(parent, out, err)
}

// connectionState detects durable quota ending even after its commit reply was
// lost. This read grants no send authority and remains usable after local fencing.
func (s *ConnectionDelivery) connectionState(ctx context.Context) (*meta.MQTTSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := s.stream.connection.Owner
	r, err := s.stream.sender.window.options.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID})
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || r.Membership != nil || r.Accounting != nil || len(r.Sessions) != 0 || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) != 0 || len(r.Inflight) != 0 || len(r.SourceOwners) != 0 || len(r.Bindings) != 0 || len(r.Wills) != 0 {
		return nil, ErrEvidence
	}
	if r.Session != nil && (meta.ValidateMQTTSession(*r.Session) != nil || r.Session.Namespace != o.Key.Namespace || r.Session.ClientID != o.Key.ClientID || r.Session.UID != s.stream.connection.UID) {
		return nil, ErrEvidence
	}
	return r.Session, nil
}

func (s *ConnectionDelivery) resolveFailure(parent context.Context, out runtime.DeliveryWork, err error) (runtime.DeliveryWork, error) {
	if s.stream.closed {
		return out, err // Sender already attempted joined cleanup; retain any retry.
	}
	if errors.Is(err, ErrSubscriptionDenied) || errors.Is(err, ErrSubscriptionRevoked) {
		return s.close(parent, meta.MQTTSessionRevoked)
	}
	if errors.Is(err, ErrFenced) || errors.Is(err, ErrClock) || errors.Is(err, runtime.ErrOwnerFenced) || errors.Is(err, runtime.ErrOwnerUnknown) || errors.Is(err, runtime.ErrOwnerStopped) {
		return s.close(parent, 0)
	}
	return out, err
}

// close transfers a trusted terminal decision only after discovery/accounting
// scopes have exited. The exclusively owned Sender retains failed End cleanup.
func (s *ConnectionDelivery) close(parent context.Context, reason meta.MQTTSessionEndReason) (runtime.DeliveryWork, error) {
	s.stream.closed = true
	if reason != 0 && !s.stream.ending {
		s.stream.ending, s.stream.endReason = true, reason
	}
	_ = s.stream.sender.window.options.Owners.Fence(s.stream.connection.Owner)
	return s.finishClosed(parent)
}

func (s *ConnectionDelivery) finishClosed(parent context.Context) (runtime.DeliveryWork, error) {
	out, err := s.stream.finishEnding(parent)
	if out.Ended || !s.stream.ending && err == nil {
		s.done = true
		return runtime.DeliveryWork{Done: true}, nil
	}
	return runtime.DeliveryWork{}, err
}

func (s *ConnectionDelivery) continueRotation(progress, wrapped bool) runtime.DeliveryWork {
	s.roundProgress = s.roundProgress || progress
	again := true
	if wrapped {
		again = s.roundProgress || len(s.sources) != 0
		s.roundProgress = false
	}
	return runtime.DeliveryWork{Again: again}
}

// account holds exact local execution through stateless maintenance so takeover
// cannot turn this connection's accounting call into work for its successor.
func (s *ConnectionDelivery) account(parent context.Context, key meta.MQTTDeliveryCursorKey) (out AccountingResult, err error) {
	w := s.stream.sender.window
	op, ctx, cancel, err := w.guard.begin(parent, s.stream.connection.Owner)
	if err != nil {
		return out, err
	}
	defer finishSubscription(op, cancel, &err)
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return out, err
	}
	out, err = s.coordinator.options.Accounting.Account(ctx, key)
	if err != nil {
		return out, err
	}
	if out.Owner != s.stream.connection.Owner {
		return AccountingResult{}, ErrFenced
	}
	return out, checkSubscriptionScope(ctx, op)
}

// selectSource reads bounded authoritative pages; stale hints can cause a retry
// or extra visit, never grant send permission. Source-specific failures advance
// the subscription rotation after a valid subscription page was observed.
func (s *ConnectionDelivery) selectSource(parent context.Context) (out deliverySelection, err error) {
	w, owner := s.stream.sender.window, s.stream.connection.Owner
	op, ctx, cancel, err := w.guard.begin(parent, owner)
	if err != nil {
		return out, err
	}
	defer finishSubscription(op, cancel, &err)
	q := meta.MQTTRead{Kind: meta.MQTTReadSubscriptions, Namespace: owner.Key.Namespace, ClientID: owner.Key.ClientID, SessionGeneration: owner.SessionGeneration, Limit: 1, After: meta.MQTTReadCursor{Topic: s.afterTopic}}
	r, err := s.readPage(ctx, op, q)
	if err != nil {
		return out, err
	}
	if len(r.Subscriptions) > 1 || !r.Done && len(r.Subscriptions) != 1 {
		return out, ErrEvidence
	}
	after := s.afterTopic
	if len(r.Subscriptions) == 1 {
		row := r.Subscriptions[0]
		if !validSubscriptionEvidence(row, *r.Session) || after != "" && !subscriptionTopicAfter(row.Topic, after) {
			return out, ErrEvidence
		}
		after = row.Topic
	}
	wantAfter := q.After
	if !r.Done {
		wantAfter.Topic = after
	}
	if r.After != wantAfter {
		return out, ErrEvidence
	}
	out.wrapped = r.Done
	defer s.advanceSubscription(after, r.Done)
	if len(r.Subscriptions) == 0 {
		return out, nil
	}
	sub := r.Subscriptions[0]
	if sub.Stage != meta.MQTTSubscriptionActive {
		delete(s.sources, sub.Generation)
		return out, nil
	}
	hint, retained := s.sources[sub.Generation]
	if retained {
		hint.seen = s.pass
		s.sources[sub.Generation] = hint
	}
	q = meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: owner.Key.Namespace, ClientID: owner.Key.ClientID, SessionGeneration: owner.SessionGeneration, SubscriptionGeneration: sub.Generation, Limit: 1, After: meta.MQTTReadCursor{Delivery: hint.after}}
	r, err = s.readPage(ctx, op, q)
	if err != nil {
		return out, err
	}
	if len(r.DeliveryCursors) > 1 || !r.Done && len(r.DeliveryCursors) != 1 {
		return out, ErrEvidence
	}
	wantAfter = q.After
	if len(r.DeliveryCursors) == 1 {
		cursor := r.DeliveryCursors[0]
		key := cursor.Key
		if meta.ValidateMQTTDeliveryCursor(cursor) != nil || key.Namespace != owner.Key.Namespace || key.ClientID != owner.Key.ClientID || key.SessionGeneration != owner.SessionGeneration || key.SubscriptionGeneration != sub.Generation || cursor.Topic != sub.Topic || cursor.AuthorizationVersion != sub.AuthorizationVersion || cursor.Revision > r.Session.Revision || sub.Revision > r.Session.Revision || hint.after != (meta.MQTTDeliveryCursorKey{}) && !deliverySourceAfter(key, hint.after) {
			return out, ErrEvidence
		}
		out.key = key
		if !r.Done {
			wantAfter.Delivery = key
		}
	}
	if r.After != wantAfter {
		return deliverySelection{wrapped: out.wrapped}, ErrEvidence
	}
	if r.Done {
		delete(s.sources, sub.Generation)
	} else {
		if !retained && len(s.sources) >= s.coordinator.options.MaxSubscriptions {
			return deliverySelection{wrapped: out.wrapped}, ErrSubscriptionLimit
		}
		s.sources[sub.Generation] = deliverySourceHint{after: out.key, seen: s.pass}
	}
	return out, nil
}

func (s *ConnectionDelivery) advanceSubscription(topic string, wrapped bool) {
	s.afterTopic = topic
	if !wrapped {
		return
	}
	s.afterTopic = ""
	for generation, hint := range s.sources {
		if hint.seen != s.pass {
			delete(s.sources, generation)
		}
	}
	s.pass++
	if s.pass == 0 {
		clear(s.sources) // Scheduling hints are disposable even on counter wrap.
		s.pass = 1
	}
}

func (s *ConnectionDelivery) readPage(ctx context.Context, op *subscriptionOperation, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	w := s.stream.sender.window
	if err := checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTReadResult{}, err
	}
	r, err := w.options.Store.ReadMQTT(ctx, q)
	if err != nil {
		return meta.MQTTReadResult{}, err
	}
	if err = w.guard.checkSession(ctx, op, s.stream.connection.Owner, r.Session); err != nil {
		return meta.MQTTReadResult{}, err
	}
	if r.Membership != nil || r.Accounting != nil || len(r.Sessions) != 0 || len(r.SourceOwners) != 0 || len(r.Wills) != 0 || len(r.Bindings) != 0 || len(r.Inflight) != 0 || q.Kind != meta.MQTTReadSubscriptions && len(r.Subscriptions) != 0 || q.Kind != meta.MQTTReadDeliveryCursors && len(r.DeliveryCursors) != 0 {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}

// Source strings use the native length-prefixed key order, not lexical order.
func deliverySourceAfter(next, previous meta.MQTTDeliveryCursorKey) bool {
	if next.SourceKind != previous.SourceKind {
		return next.SourceKind > previous.SourceKind
	}
	if next.SourceID != previous.SourceID {
		return subscriptionTopicAfter(next.SourceID, previous.SourceID)
	}
	return subscriptionTopicAfter(next.SourceGeneration, previous.SourceGeneration)
}
