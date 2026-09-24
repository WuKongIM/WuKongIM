package mqttsession

import (
	"context"
	"encoding/hex"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ExchangeRecoveryMetadata supplies authoritative pinned reads only. Recovery
// preparation cannot complete an exchange or change any durable obligation.
type ExchangeRecoveryMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
}
type ExchangeRecoveryOptions struct {
	Store         ExchangeRecoveryMetadata
	Owners        *runtime.Owners
	Metadata      ReplayMetadata
	Channels      AccountingChannels
	Authorization SubscriptionAuthorizer
	// Now shares Owners' monotonic clock. Timeout and MaxBytes bound the whole turn.
	Now      func() time.Time
	Timeout  time.Duration
	MaxBytes int
}

// ExchangeRecovery prepares existing exchanges without consulting replacement
// subscription options. It owns no socket, cursor state, retries or scheduling.
type ExchangeRecovery struct {
	options ExchangeRecoveryOptions
	guard   *Subscriptions
}
type RecoveryPreparation struct {
	Delivery *PreparedDelivery
	// After advances only when the trusted sender has successfully processed this
	// exchange. Failure never authorizes a caller to skip its original order.
	After meta.MQTTInflightCursor
	// Done covers this captured query after the supplied cursor, not complete
	// connection recovery or permission to start an independently racing sender.
	Done bool
}

func NewExchangeRecovery(o ExchangeRecoveryOptions) (*ExchangeRecovery, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 16 << 20
	}
	if o.Store == nil || o.Owners == nil || o.Metadata == nil || o.Channels == nil || o.Authorization == nil || o.Timeout <= 0 || o.Timeout > time.Minute || o.MaxBytes < 1 || o.MaxBytes > 16<<20 {
		return nil, ErrInvalid
	}
	r := &ExchangeRecovery{options: o, guard: &Subscriptions{options: SubscriptionOptions{Owners: o.Owners, Authorization: o.Authorization, Now: o.Now, Timeout: o.Timeout}}}
	if _, err := r.guard.now(); err != nil {
		return nil, err
	}
	return r, nil
}

// Next returns at most one original exchange in send order. The caller must
// serialize recovery before new admission and recheck permission at enqueue.
// Expiry, No Local, current subscription QoS and Receive Maximum cannot erase a
// begun exchange. Denial returns an error without ACK or silent abandonment.
func (r *ExchangeRecovery) Next(parent context.Context, o contract.Owner, after meta.MQTTInflightCursor) (out RecoveryPreparation, err error) {
	q := meta.MQTTRead{Kind: meta.MQTTReadInflightPage, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Limit: 1, After: meta.MQTTReadCursor{Inflight: after}}
	if r == nil || meta.ValidateMQTTRead(q) != nil {
		return out, ErrInvalid
	}
	op, ctx, cancel, err := r.guard.begin(parent, o)
	if err != nil {
		return out, err
	}
	defer finishSubscription(op, cancel, &err)
	started, err := r.guard.now()
	if err != nil {
		return out, err
	}
	page, err := r.read(ctx, op, q)
	if err != nil {
		return out, err
	}
	if err = r.guard.checkSession(ctx, op, o, page.Session); err != nil {
		return out, err
	}
	session := *page.Session
	if after.DeliveryOrder >= session.NextDeliveryOrder {
		return out, ErrConflict
	}
	if len(page.Inflight) > 1 {
		return out, ErrEvidence
	}
	if len(page.Inflight) == 0 {
		if !page.Done || page.After.Inflight != after || (after == (meta.MQTTInflightCursor{}) && session.OutboundInflight != 0) {
			return out, ErrEvidence
		}
		now, e := r.guard.now()
		if e != nil {
			return out, e
		}
		if now.UnixMilli() < started.UnixMilli() || now.UnixMilli() >= session.LeaseUntilMS {
			return out, ErrClock
		}
		if e = checkSubscriptionScope(ctx, op); e != nil {
			return out, e
		}
		return RecoveryPreparation{After: after, Done: true}, nil
	}
	e := page.Inflight[0]
	next := meta.MQTTInflightCursor{DeliveryOrder: e.DeliveryOrder, PacketID: e.PacketID}
	if meta.ValidateMQTTInflight(e) != nil || e.Key.Namespace != o.Key.Namespace || e.Key.ClientID != o.Key.ClientID || e.Key.SessionGeneration != o.SessionGeneration || e.DeliveryOrder <= after.DeliveryOrder || e.DeliveryOrder >= session.NextDeliveryOrder || e.UpdatedAtMS > session.UpdatedAtMS || session.OutboundInflight == 0 ||
		(!page.Done && page.After.Inflight != next) || (page.Done && page.After.Inflight != after) {
		return out, ErrEvidence
	}
	view, err := r.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: e.Key})
	if err != nil {
		return out, err
	}
	if view.Session == nil || *view.Session != session || len(view.DeliveryCursors) != 1 {
		return out, ErrConflict
	}
	cursor := view.DeliveryCursors[0]
	if meta.ValidateMQTTDeliveryCursor(cursor) != nil || cursor.Key != e.Key || cursor.Topic != e.Topic || cursor.Revision > session.Revision || cursor.PendingMessages > session.PendingMessages || cursor.PendingBytes > session.PendingBytes || cursor.InflightCount == 0 || cursor.InflightCount > session.OutboundInflight || cursor.InflightBytes < e.Publication.Bytes || e.Publication.Position <= cursor.CompletedThrough || e.Publication.Position > cursor.WindowThrough {
		return out, ErrEvidence
	}
	source := meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: e.Key.SourceID, Generation: e.Key.SourceGeneration}
	sourceRequest, valid := replaySourceRequest(source)
	if !valid {
		return out, ErrEvidence
	}
	bindingKey := meta.MQTTSourceBindingKey{Owner: source, Namespace: e.Key.Namespace, ClientID: e.Key.ClientID, SessionGeneration: e.Key.SessionGeneration, SubscriptionGeneration: e.Key.SubscriptionGeneration}
	view, err = r.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: bindingKey})
	if err != nil {
		return out, err
	}
	if len(view.Bindings) != 1 {
		return out, ErrEvidence
	}
	b := view.Bindings[0]
	if meta.ValidateMQTTSourceBinding(b) != nil || b.Key != bindingKey || b.UID != session.UID || b.Topic != cursor.Topic || b.AuthorizationVersion != cursor.AuthorizationVersion || !b.BoundaryKnown || b.StartAfter != cursor.StartAfter || b.ProgressRevision > cursor.Revision || b.CompletedThrough > cursor.CompletedThrough || b.ReleaseReason != 0 ||
		(b.Stage != meta.MQTTBindingActive && b.Stage != meta.MQTTBindingRemoving) || (b.EndKnown && b.EndThrough < e.Publication.Position) {
		return out, ErrEvidence
	}
	permission := SubscriptionRequest{Topic: e.Topic, RequestedQoS: 1, SubscriptionIdentifier: e.Publication.SubscriptionIdentifier}
	switch sourceRequest.ChannelID.Type {
	case 2:
		permission.TargetKind, permission.TargetID = meta.MQTTSubscriptionGroup, sourceRequest.ChannelID.ID
	case 1:
		permission.TargetKind, permission.TargetID = meta.MQTTSubscriptionUserInbox, session.UID
	default:
		return out, ErrEvidence
	}
	authorize := func() error {
		version, e := r.guard.authorize(ctx, op, permission)
		if e != nil {
			return e
		}
		if version != cursor.AuthorizationVersion {
			return ErrSubscriptionRevoked
		}
		return nil
	}
	if err = authorize(); err != nil {
		return out, err
	}
	content, err := readAnchoredOriginals(ctx, r.options.Metadata, r.options.Channels, cursor, e.Publication.Position, e.Publication.Position, 1, r.options.MaxBytes)
	if err != nil {
		return out, err
	}
	if err = authorize(); err != nil {
		return out, err
	}
	original := content.Records[0]
	if original.Internal || original.Message.MessageID != e.Publication.MessageID || original.Message.MessageSeq != e.Publication.MessageSeq || original.ContentVersion != e.Publication.ContentVersion || hex.EncodeToString(original.ContentHash[:]) != e.Publication.ContentHash || original.AccountedBytes != e.Publication.Bytes {
		return out, ErrEvidence
	}
	// Read back the immutable exchange, allowing unrelated ACKs to update links.
	view, err = r.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadInflight, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, PacketID: e.PacketID})
	if err != nil {
		return out, err
	}
	if err = r.guard.checkSession(ctx, op, o, view.Session); err != nil {
		return out, err
	}
	if view.Session.Revision < session.Revision || view.Session.NextDeliveryOrder != session.NextDeliveryOrder || len(view.Inflight) != 1 {
		return out, ErrConflict
	}
	current := view.Inflight[0]
	if view.Session.OutboundInflight == 0 || view.Session.PendingMessages == 0 || view.Session.PendingBytes < current.Publication.Bytes {
		return out, ErrEvidence
	}
	if meta.ValidateMQTTInflight(current) != nil || current.Key != e.Key || current.PacketID != e.PacketID || current.DeliveryOrder != e.DeliveryOrder || current.Publication != e.Publication || current.Topic != e.Topic || current.UpdatedAtMS < e.UpdatedAtMS || current.UpdatedAtMS > view.Session.UpdatedAtMS {
		return out, ErrConflict
	}
	now, err := r.guard.now()
	if err != nil {
		return out, err
	}
	if now.UnixMilli() < max(started.UnixMilli(), cursor.UpdatedAtMS, b.UpdatedAtMS, view.Session.UpdatedAtMS) || now.UnixMilli() >= view.Session.LeaseUntilMS {
		return out, ErrClock
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return out, err
	}
	return RecoveryPreparation{Delivery: &PreparedDelivery{Owner: o, Topic: e.Topic, QoS: 1, SubscriptionIdentifier: e.Publication.SubscriptionIdentifier, Exchange: current, Publication: original}, After: next, Done: page.Done}, nil
}
func (r *ExchangeRecovery) read(ctx context.Context, op *subscriptionOperation, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if err := checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTReadResult{}, err
	}
	v, err := r.options.Store.ReadMQTT(ctx, q)
	if err != nil {
		return meta.MQTTReadResult{}, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTReadResult{}, err
	}
	after := meta.MQTTReadCursor{}
	if q.Kind == meta.MQTTReadInflightPage {
		after.Inflight = v.After.Inflight
	}
	if v.After != after || (q.Kind != meta.MQTTReadInflightPage && !v.Done) || v.Accounting != nil || len(v.SourceOwners) != 0 || len(v.Sessions) != 0 || len(v.Subscriptions) != 0 || len(v.Wills) != 0 ||
		(q.Kind != meta.MQTTReadDeliveryCursor && len(v.DeliveryCursors) != 0) || (q.Kind != meta.MQTTReadSourceBinding && len(v.Bindings) != 0) || (q.Kind == meta.MQTTReadSourceBinding && v.Session != nil) ||
		(q.Kind != meta.MQTTReadInflightPage && q.Kind != meta.MQTTReadInflight && len(v.Inflight) != 0) {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return v, nil
}
