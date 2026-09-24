package mqttsession

import (
	"context"
	"encoding/hex"
	"math"
	"slices"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type WindowAdmissionOptions struct {
	Store         AcknowledgementMetadata
	Owners        *runtime.Owners
	Metadata      ReplayMetadata
	Channels      AccountingChannels
	Authorization SubscriptionAuthorizer
	// Now shares Owners' monotonic clock; Timeout bounds a complete preparation.
	Now     func() time.Time
	Timeout time.Duration
	// PageSize/MaxBytes bound original-content work independently of the window.
	PageSize, MaxBytes int
}

// PreparedDelivery contains original content and, for QoS 1, a proved durable
// exchange. It grants no network-send authority: the sender must recheck current
// receive permission, serialize preparation/enqueue/completion, order recovery
// and hold exact-owner execution across write.
type PreparedDelivery struct {
	Owner                  contract.Owner
	Topic                  string
	QoS                    uint8
	SubscriptionIdentifier uint32
	Exchange               meta.MQTTInflight
	Publication            ch.MQTTReplayPublication
	completion             *qos0Completion
}

type WindowPreparation struct {
	Delivery *PreparedDelivery
	Through  uint64
	// Advanced commits a skipped prefix. Full yields to ACK flow control. Idle
	// means only that this cursor's already-accounted prefix has been consumed.
	Advanced, Full, Idle bool
}

type qos0Completion struct {
	issuer   *WindowAdmission
	owner    contract.Owner
	mutation meta.MQTTWindowMutation
}

// WindowAdmission derives one action from authoritative accounting and original
// replay content. It owns no worker, socket, recovery ordering or retransmission.
type WindowAdmission struct {
	options WindowAdmissionOptions
	guard   *Subscriptions
}

func NewWindowAdmission(o WindowAdmissionOptions) (*WindowAdmission, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.PageSize == 0 {
		o.PageSize = 256
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 16 << 20
	}
	if o.Store == nil || o.Owners == nil || o.Metadata == nil || o.Channels == nil || o.Authorization == nil || o.Timeout <= 0 || o.Timeout > time.Minute || o.PageSize < 1 || o.PageSize > 256 || o.MaxBytes < 1 || o.MaxBytes > 16<<20 {
		return nil, ErrInvalid
	}
	w := &WindowAdmission{options: o, guard: &Subscriptions{options: SubscriptionOptions{Owners: o.Owners, Authorization: o.Authorization, Now: o.Now, Timeout: o.Timeout}}}
	if _, err := w.guard.now(); err != nil {
		return nil, err
	}
	return w, nil
}

// Prepare processes at most one bounded page and one original charge receipt.
// A skip and an admission are separate turns; a failed/ambiguous commit never
// exposes a delivery. QoS 0 remains uncommitted until successful packet enqueue.
func (w *WindowAdmission) Prepare(parent context.Context, o contract.Owner, key meta.MQTTDeliveryCursorKey) (out WindowPreparation, err error) {
	if w == nil || meta.ValidateMQTTRead(meta.MQTTRead{Kind: meta.MQTTReadAccounting, CursorKey: key}) != nil || key.Namespace != o.Key.Namespace || key.ClientID != o.Key.ClientID || key.SessionGeneration != o.SessionGeneration {
		return out, ErrInvalid
	}
	op, ctx, cancel, err := w.guard.begin(parent, o)
	if err != nil {
		return out, err
	}
	defer finishSubscription(op, cancel, &err)
	started, err := w.guard.now()
	if err != nil {
		return out, err
	}
	r, err := w.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadAccounting, CursorKey: key})
	if err != nil {
		return out, err
	}
	if err = w.guard.checkSession(ctx, op, o, r.Session); err != nil {
		return out, err
	}
	if len(r.DeliveryCursors) != 1 {
		return out, ErrEvidence
	}
	session, cursor, head := *r.Session, r.DeliveryCursors[0], r.Accounting
	if meta.ValidateMQTTDeliveryCursor(cursor) != nil || cursor.Key != key || cursor.Revision > session.Revision || cursor.PendingMessages > session.PendingMessages || cursor.PendingBytes > session.PendingBytes || cursor.InflightCount > session.OutboundInflight || meta.ValidateMQTTAccountingHead(cursor, head) != nil {
		return out, ErrEvidence
	}
	if cursor.AccountingVersion != 1 {
		return out, ErrConflict
	}
	r, err = w.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: key.SessionGeneration, Topic: cursor.Topic})
	if err != nil {
		return out, err
	}
	if r.Session == nil || *r.Session != session || len(r.Subscriptions) != 1 {
		return out, ErrEvidence
	}
	sub := r.Subscriptions[0]
	if !validSubscriptionEvidence(sub, session) || sub.Namespace != key.Namespace || sub.ClientID != key.ClientID || sub.SessionGeneration != key.SessionGeneration || sub.Generation != key.SubscriptionGeneration || sub.Topic != cursor.Topic || sub.AuthorizationVersion != cursor.AuthorizationVersion {
		return out, ErrEvidence
	}
	if sub.Stage != meta.MQTTSubscriptionActive {
		return out, ErrConflict
	}
	source := meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: key.SourceID, Generation: key.SourceGeneration}
	request, valid := replaySourceRequest(source)
	if !valid {
		return out, ErrInvalid
	}
	if sub.TargetKind == meta.MQTTSubscriptionGroup && (request.ChannelID.Type != 2 || request.ChannelID.ID != sub.TargetID) || sub.TargetKind == meta.MQTTSubscriptionUserInbox && (request.ChannelID.Type != 1 || sub.TargetID != session.UID) {
		return out, ErrEvidence
	}
	bindingKey := meta.MQTTSourceBindingKey{Owner: source, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: key.SessionGeneration, SubscriptionGeneration: key.SubscriptionGeneration}
	r, err = w.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: bindingKey})
	if err != nil {
		return out, err
	}
	if len(r.Bindings) != 1 {
		return out, ErrEvidence
	}
	binding := r.Bindings[0]
	if meta.ValidateMQTTSourceBinding(binding) != nil || binding.Key != bindingKey || binding.UID != session.UID || binding.Topic != sub.Topic || binding.AuthorizationVersion != sub.AuthorizationVersion || binding.OperationID != sub.OperationID || binding.IntentRevision > sub.Revision || !binding.BoundaryKnown || binding.StartAfter != cursor.StartAfter || binding.ProgressRevision > cursor.Revision {
		return out, ErrEvidence
	}
	if binding.Stage != meta.MQTTBindingActive || binding.EndKnown || binding.ReleaseReason != 0 {
		return out, ErrConflict
	}
	if err = w.check(ctx, op, o, session, started.UnixMilli()); err != nil {
		return out, err
	}
	if cursor.WindowThrough == cursor.AccountedThrough {
		return WindowPreparation{Idle: true, Through: cursor.WindowThrough}, nil
	}
	through := cursor.WindowThrough + min(cursor.AccountedThrough-cursor.WindowThrough, uint64(w.options.PageSize))
	if head != nil && head.NextFrom != 0 {
		through = min(through, head.NextFrom-1)
	}
	page, err := w.originals(ctx, op, request, cursor, sub, through)
	if err != nil {
		return out, err
	}
	at, err := w.guard.now()
	if err != nil {
		return out, err
	}
	if at.UnixMilli() < max(started.UnixMilli(), cursor.UpdatedAtMS, sub.UpdatedAtMS, binding.UpdatedAtMS) {
		return out, ErrClock
	}
	mutation := meta.MQTTWindowMutation{Key: key, ExpectedRevision: session.Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTWindowAdvance, UpdatedAtMS: at.UnixMilli()}
	var delivery *PreparedDelivery
	itemIndex := 0
	for _, entry := range page.Records {
		charged := false
		if head != nil {
			for itemIndex < len(head.Items) && head.Items[itemIndex].Position < entry.Message.MessageSeq {
				itemIndex++
			}
			if itemIndex < len(head.Items) && head.Items[itemIndex].Position == entry.Message.MessageSeq {
				charged = true
				if head.Items[itemIndex].Bytes != entry.AccountedBytes {
					return out, ErrEvidence
				}
			}
		}
		eligible, qos, e := publicationEligibility(sub, entry, at.UnixMilli())
		if e != nil {
			return out, e
		}
		if eligible && (qos == 0 || charged) {
			if mutation.Through != 0 {
				break
			}
			delivery = &PreparedDelivery{Owner: o, Topic: sub.Topic, QoS: qos, SubscriptionIdentifier: sub.SubscriptionIdentifier, Publication: entry}
			if qos == 1 {
				mutation.Op = meta.MQTTWindowAdmit
				mutation.Publication = meta.MQTTInflightPublication{Position: entry.Message.MessageSeq, MessageID: entry.Message.MessageID, MessageSeq: entry.Message.MessageSeq, ContentVersion: entry.ContentVersion, ContentHash: hex.EncodeToString(entry.ContentHash[:]), Bytes: entry.AccountedBytes, SubscriptionIdentifier: sub.SubscriptionIdentifier}
				break
			}
		}
		mutation.Through = entry.Message.MessageSeq
		if charged {
			mutation.ReleasedMessages++
			mutation.ReleasedBytes += entry.AccountedBytes
		}
		if delivery != nil {
			break
		}
	}
	if meta.ValidateMQTTWindowMutation(mutation) != nil {
		return out, ErrEvidence
	}
	if err = w.check(ctx, op, o, session, mutation.UpdatedAtMS); err != nil {
		return out, err
	}
	if delivery != nil && delivery.QoS == 0 {
		delivery.completion = &qos0Completion{issuer: w, owner: o, mutation: mutation}
		return WindowPreparation{Delivery: delivery}, nil
	}
	receipt, err := w.commit(ctx, op, mutation)
	if err != nil {
		return out, err
	}
	if err = w.check(ctx, op, o, session, mutation.UpdatedAtMS); err != nil {
		return out, err
	}
	if receipt.Status == meta.MQTTWindowFull {
		return WindowPreparation{Full: true}, nil
	}
	if delivery == nil {
		return WindowPreparation{Advanced: true, Through: mutation.Through}, nil
	}
	r, err = w.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadInflight, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: key.SessionGeneration, PacketID: receipt.PacketID})
	if err != nil {
		return out, err
	}
	if err = w.guard.checkSession(ctx, op, o, r.Session); err != nil {
		return out, err
	}
	if r.Session.Revision < receipt.CurrentRevision || len(r.Inflight) != 1 {
		return out, ErrEvidence
	}
	exchange := r.Inflight[0]
	if meta.ValidateMQTTInflight(exchange) != nil || exchange.Key != key || exchange.PacketID != receipt.PacketID || exchange.DeliveryOrder != receipt.DeliveryOrder || exchange.Publication != mutation.Publication || exchange.Topic != sub.Topic || exchange.UpdatedAtMS < mutation.UpdatedAtMS || exchange.UpdatedAtMS > r.Session.UpdatedAtMS {
		return out, ErrEvidence
	}
	if err = w.check(ctx, op, o, *r.Session, mutation.UpdatedAtMS); err != nil {
		return out, err
	}
	delivery.Exchange = exchange
	return WindowPreparation{Delivery: delivery}, nil
}

// CompleteQoS0 must be called only after successful packet enqueue by the trusted
// sender. Private captured identity/revision/debits cannot be redirected through
// public presentation fields; a changed parent fails without rebasing the work.
func (w *WindowAdmission) CompleteQoS0(parent context.Context, d PreparedDelivery) (changed bool, err error) {
	c := d.completion
	if w == nil || c == nil || c.issuer != w {
		return false, ErrInvalid
	}
	op, ctx, cancel, err := w.guard.begin(parent, c.owner)
	if err != nil {
		return false, err
	}
	defer finishSubscription(op, cancel, &err)
	r, err := w.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: c.owner.Key.Namespace, ClientID: c.owner.Key.ClientID})
	if err != nil {
		return false, err
	}
	if err = w.guard.checkSession(ctx, op, c.owner, r.Session); err != nil {
		return false, err
	}
	if r.Session.Revision != c.mutation.ExpectedRevision {
		return false, ErrConflict
	}
	if err = w.check(ctx, op, c.owner, *r.Session, c.mutation.UpdatedAtMS); err != nil {
		return false, err
	}
	receipt, err := w.commit(ctx, op, c.mutation)
	if err != nil {
		return false, err
	}
	if err = w.check(ctx, op, c.owner, *r.Session, c.mutation.UpdatedAtMS); err != nil {
		return false, err
	}
	return receipt.Status == meta.MQTTWindowApplied, nil
}

func (w *WindowAdmission) check(ctx context.Context, op *subscriptionOperation, o contract.Owner, s meta.MQTTSession, earliest int64) error {
	if err := w.guard.checkSession(ctx, op, o, &s); err != nil {
		return err
	}
	now, err := w.guard.now()
	if err != nil {
		return err
	}
	if now.UnixMilli() < earliest || now.UnixMilli() >= s.LeaseUntilMS || s.Revision == math.MaxUint64 {
		return ErrClock
	}
	return checkSubscriptionScope(ctx, op)
}
func (w *WindowAdmission) commit(ctx context.Context, op *subscriptionOperation, m meta.MQTTWindowMutation) (meta.MQTTWindowResult, error) {
	r, err := w.options.Store.MutateMQTTWindow(ctx, m)
	if err != nil {
		return meta.MQTTWindowResult{}, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTWindowResult{}, err
	}
	if r.Status == meta.MQTTWindowConflict {
		return meta.MQTTWindowResult{}, ErrConflict
	}
	if r.Status == meta.MQTTWindowFull {
		if m.Op != meta.MQTTWindowAdmit || r.CurrentRevision != m.ExpectedRevision || r.PacketID != 0 || r.DeliveryOrder != 0 {
			return meta.MQTTWindowResult{}, ErrEvidence
		}
		return r, nil
	}
	if (r.Status != meta.MQTTWindowApplied && r.Status != meta.MQTTWindowUnchanged) || r.CurrentRevision != m.ExpectedRevision+1 ||
		(m.Op == meta.MQTTWindowAdmit && (r.PacketID == 0 || r.DeliveryOrder == 0)) ||
		(m.Op == meta.MQTTWindowAdvance && (r.PacketID != 0 || r.DeliveryOrder != 0)) {
		return meta.MQTTWindowResult{}, ErrEvidence
	}
	return r, nil
}
func (w *WindowAdmission) read(ctx context.Context, op *subscriptionOperation, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if err := checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTReadResult{}, err
	}
	r, err := w.options.Store.ReadMQTT(ctx, q)
	if err != nil {
		return meta.MQTTReadResult{}, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTReadResult{}, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || len(r.SourceOwners) != 0 || len(r.Sessions) != 0 || len(r.Wills) != 0 ||
		(q.Kind != meta.MQTTReadAccounting && (r.Accounting != nil || len(r.DeliveryCursors) != 0)) ||
		(q.Kind != meta.MQTTReadSubscription && len(r.Subscriptions) != 0) ||
		(q.Kind != meta.MQTTReadInflight && len(r.Inflight) != 0) ||
		(q.Kind != meta.MQTTReadSourceBinding && len(r.Bindings) != 0) || (q.Kind == meta.MQTTReadSourceBinding && r.Session != nil) {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}

// originals pins a committed anchor and rechecks both placement and permission
// after reading. It cannot use mutable history or accept an unanchored suffix.
func (w *WindowAdmission) originals(ctx context.Context, op *subscriptionOperation, request ch.MQTTReplayPlanRequest, cursor meta.MQTTDeliveryCursor, sub meta.MQTTSubscription, through uint64) (ch.MQTTReplayConsumerPage, error) {
	var empty ch.MQTTReplayConsumerPage
	authorize := func() error {
		version, err := w.guard.authorize(ctx, op, subscriptionRequestFromRow(sub))
		if err != nil {
			return err
		}
		if version != sub.AuthorizationVersion {
			return ErrSubscriptionRevoked
		}
		return nil
	}
	if err := authorize(); err != nil {
		return empty, err
	}
	placement, err := w.options.Metadata.ResolveChannelMetaFresh(ctx, request.ChannelID)
	if err != nil {
		return empty, err
	}
	if !validReplayPlacement(placement, request.ChannelID) {
		return empty, ErrEvidence
	}
	placement.Replicas, placement.ISR = slices.Clone(placement.Replicas), slices.Clone(placement.ISR)
	request.ExpectedChannelEpoch, request.ExpectedLeaderEpoch, request.ExpectedRouteGeneration = placement.Epoch, placement.LeaderEpoch, placement.RouteGeneration
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return empty, err
	}
	plan, err := w.options.Channels.PlanChannelMQTTReplay(ctx, request)
	if err != nil {
		return empty, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return empty, err
	}
	if !plan.ValidFor(request) || plan.Source.StartAfter > cursor.StartAfter || plan.Source.CommittedThrough < cursor.AccountedThrough {
		return empty, ErrEvidence
	}
	if !plan.HasAnchor {
		return empty, ch.ErrNotReady
	}
	if plan.Anchor.Anchor.Through < cursor.AccountedThrough {
		return empty, ErrEvidence
	}
	q := ch.MQTTReplayConsumerRequest{AnchorPosition: plan.Anchor.Manifest.LastOffset, Request: ch.MQTTReplayRequest{ChannelID: request.ChannelID, ExpectedChannelEpoch: request.ExpectedChannelEpoch, ExpectedLeaderEpoch: request.ExpectedLeaderEpoch, ExpectedRouteGeneration: request.ExpectedRouteGeneration, Range: ch.MQTTReplayRange{Generation: cursor.Key.SourceGeneration, From: cursor.WindowThrough + 1, Through: through, Limit: w.options.PageSize, MaxBytes: w.options.MaxBytes}}}
	page, err := w.options.Channels.ReadChannelMQTTReplay(ctx, q)
	if err != nil {
		return empty, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return empty, err
	}
	if !page.ValidFor(request.ChannelID, q.Request.Range) || page.Before.StartAfter != plan.Source.StartAfter || (page.After.Through == plan.Anchor.Anchor.Through && page.After != plan.Anchor.Prefix()) {
		return empty, ErrEvidence
	}
	current, err := w.options.Metadata.ResolveChannelMetaFresh(ctx, request.ChannelID)
	if err != nil {
		return empty, err
	}
	if !validReplayPlacement(current, request.ChannelID) || ch.MQTTReplayCopyAuthority(current) != ch.MQTTReplayCopyAuthority(placement) || current.WriteFence != placement.WriteFence {
		return empty, ErrEvidence
	}
	if err = authorize(); err != nil {
		return empty, err
	}
	return page, nil
}
