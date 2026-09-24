package mqttsession

import (
	"context"
	"math"
	"slices"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// AccountingMetadata supplies authoritative Session snapshots and an atomic
// qualification commit. It must never substitute replica-local metadata.
type AccountingMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	MutateMQTTDeliveryCursor(context.Context, meta.MQTTDeliveryCursorMutation) (meta.MQTTDeliveryCursorResult, error)
}

// AccountingChannels supplies committed anchors and typed original messages
// through current Channel authority. Ordinary history cannot implement this port.
type AccountingChannels interface {
	ReplayRetentionChannels
	ReadChannelMQTTReplay(context.Context, ch.MQTTReplayConsumerRequest) (ch.MQTTReplayConsumerPage, error)
}

type AccountingOptions struct {
	Store         AccountingMetadata
	Metadata      ReplayMetadata
	Channels      AccountingChannels
	Authorization SubscriptionAuthorizer
	// Now evaluates one whole page at a fixed wall time. No lifetime clock resets.
	Now func() time.Time
	// Timeout bounds all authority, content and commit calls in one turn.
	Timeout time.Duration
	// PageSize/MaxBytes bound work independently of the Session's inflight window.
	PageSize, MaxBytes int
}

// AccountingResult reports durable responsibility, never permission to send or
// proof of owner isolation. Ended identifies quota termination for lifecycle
// cleanup of this exact observed Owner, including after an offline accounting turn.
type AccountingResult struct {
	Owner                              contract.Owner
	Through, AddedMessages, AddedBytes uint64
	Changed, Ended                     bool
	// Idle means the captured accepted prefix was already accounted, not that
	// the source has no newer publication waiting for shared-replay confirmation.
	Idle bool
}

// Accounting performs stateless bounded maintenance for online/offline Sessions.
// It owns no connection, worker, retries, inflight admission or message sending.
type Accounting struct{ options AccountingOptions }

func NewAccounting(o AccountingOptions) (*Accounting, error) {
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
	if o.Store == nil || o.Metadata == nil || o.Channels == nil || o.Authorization == nil || o.Timeout <= 0 || o.Timeout > time.Minute || o.PageSize < 1 || o.PageSize > 256 || o.MaxBytes < 1 || o.MaxBytes > 16<<20 {
		return nil, ErrInvalid
	}
	return &Accounting{options: o}, nil
}

// Account obtains counts from one protected original-content page. The captured
// parent/owner and child revisions fence concurrent takeover, options or removal;
// a conflict yields without rebasing the same work onto a successor's identity.
func (a *Accounting) Account(parent context.Context, key meta.MQTTDeliveryCursorKey) (out AccountingResult, err error) {
	if a == nil || parent == nil || meta.ValidateMQTTRead(meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: key}) != nil {
		return out, ErrInvalid
	}
	defer func() {
		if recover() != nil {
			out = AccountingResult{}
			err = ErrSubscriptionCallback
		}
	}()
	ctx, cancel := context.WithTimeout(parent, a.options.Timeout)
	defer cancel()
	r, err := a.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: key})
	if err != nil {
		return out, err
	}
	if r.Session == nil || len(r.DeliveryCursors) != 1 {
		return out, ErrEvidence
	}
	session, cursor := *r.Session, r.DeliveryCursors[0]
	if meta.ValidateMQTTSession(session) != nil || session.Namespace != key.Namespace || session.ClientID != key.ClientID || session.Generation != key.SessionGeneration || meta.ValidateMQTTDeliveryCursor(cursor) != nil || cursor.Key != key || cursor.Revision > session.Revision ||
		cursor.PendingMessages > session.PendingMessages || cursor.PendingBytes > session.PendingBytes || cursor.InflightCount > session.OutboundInflight {
		return out, ErrEvidence
	}
	startedAt := a.options.Now().UnixMilli()
	if err = a.live(session, startedAt); err != nil {
		return out, err
	}
	if cursor.AccountingVersion == 0 && cursor.WindowThrough != cursor.AccountedThrough {
		return out, ErrConflict
	}
	r, err = a.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: key.SessionGeneration, Topic: cursor.Topic})
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
	r, err = a.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: bindingKey})
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
	if err = a.authorize(ctx, session.UID, sub); err != nil {
		return out, err
	}
	placement, err := a.options.Metadata.ResolveChannelMetaFresh(ctx, request.ChannelID)
	if err != nil {
		return out, err
	}
	if !validReplayPlacement(placement, request.ChannelID) {
		return out, ErrEvidence
	}
	placement.Replicas, placement.ISR = slices.Clone(placement.Replicas), slices.Clone(placement.ISR)
	request.ExpectedChannelEpoch, request.ExpectedLeaderEpoch, request.ExpectedRouteGeneration = placement.Epoch, placement.LeaderEpoch, placement.RouteGeneration
	if err = ctx.Err(); err != nil {
		return out, err
	}
	plan, err := a.options.Channels.PlanChannelMQTTReplay(ctx, request)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !plan.ValidFor(request) || plan.Source.StartAfter > cursor.StartAfter || plan.Source.CommittedThrough < cursor.AccountedThrough {
		return out, ErrEvidence
	}
	if !plan.HasAnchor {
		return out, ch.ErrNotReady
	}
	if plan.Anchor.Anchor.Through < cursor.AccountedThrough {
		return out, ErrEvidence
	}
	if plan.Anchor.Anchor.Through == cursor.AccountedThrough {
		return AccountingResult{Owner: sessionOwner(session), Through: cursor.AccountedThrough, Idle: true}, nil
	}
	through := cursor.AccountedThrough + min(plan.Anchor.Anchor.Through-cursor.AccountedThrough, uint64(a.options.PageSize))
	read := ch.MQTTReplayConsumerRequest{AnchorPosition: plan.Anchor.Manifest.LastOffset, Request: ch.MQTTReplayRequest{ChannelID: request.ChannelID, ExpectedChannelEpoch: request.ExpectedChannelEpoch, ExpectedLeaderEpoch: request.ExpectedLeaderEpoch, ExpectedRouteGeneration: request.ExpectedRouteGeneration, Range: ch.MQTTReplayRange{Generation: key.SourceGeneration, From: cursor.AccountedThrough + 1, Through: through, Limit: a.options.PageSize, MaxBytes: a.options.MaxBytes}}}
	page, err := a.options.Channels.ReadChannelMQTTReplay(ctx, read)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !page.ValidFor(request.ChannelID, read.Request.Range) || page.Before.StartAfter != plan.Source.StartAfter || (page.After.Through == plan.Anchor.Anchor.Through && page.After != plan.Anchor.Prefix()) {
		return out, ErrEvidence
	}
	current, err := a.options.Metadata.ResolveChannelMetaFresh(ctx, request.ChannelID)
	if err != nil {
		return out, err
	}
	if !validReplayPlacement(current, request.ChannelID) || ch.MQTTReplayCopyAuthority(current) != ch.MQTTReplayCopyAuthority(placement) || current.WriteFence != placement.WriteFence {
		return out, ErrEvidence
	}
	if err = a.authorize(ctx, session.UID, sub); err != nil {
		return out, err
	}
	at := a.options.Now().UnixMilli()
	if err = a.live(session, at); err != nil {
		return out, err
	}
	if at < startedAt || at < cursor.UpdatedAtMS || at < sub.UpdatedAtMS || at < binding.UpdatedAtMS {
		return out, ErrClock
	}
	mutation := meta.MQTTDeliveryCursorMutation{Key: key, ExpectedRevision: session.Revision, OwnerGeneration: session.OwnerGeneration, OwnerNodeID: session.OwnerNodeID, OwnerBootID: session.OwnerBootID, ConnectionID: session.ConnectionID, Op: meta.MQTTCursorAccountQualified, Topic: sub.Topic, AuthorizationVersion: sub.AuthorizationVersion, Through: page.After.Through, UpdatedAtMS: at, Qualified: &meta.MQTTQualifiedAccounting{From: read.Request.Range.From, SubscriptionRevision: sub.Revision}}
	for _, entry := range page.Records {
		charged, e := qualifiesForBacklog(sub, entry, at)
		if e != nil {
			return out, e
		}
		if !charged {
			continue
		}
		if math.MaxUint64-mutation.AddedBytes < entry.AccountedBytes {
			return out, ErrEvidence
		}
		mutation.AddedMessages++
		mutation.AddedBytes += entry.AccountedBytes
		mutation.Qualified.Items = append(mutation.Qualified.Items, meta.MQTTAccountingItem{Position: entry.Message.MessageSeq, Bytes: entry.AccountedBytes})
	}
	if math.MaxUint64-session.PendingMessages < mutation.AddedMessages || math.MaxUint64-session.PendingBytes < mutation.AddedBytes || meta.ValidateMQTTDeliveryCursorMutation(mutation) != nil {
		return out, ErrEvidence
	}
	ended := session.PendingMessages+mutation.AddedMessages > session.QuotaMessages || session.PendingBytes+mutation.AddedBytes > session.QuotaBytes
	beforeCommit := a.options.Now().UnixMilli()
	if beforeCommit < at {
		return out, ErrClock
	}
	if err = a.live(session, beforeCommit); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	receipt, err := a.options.Store.MutateMQTTDeliveryCursor(ctx, mutation)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if receipt.Status == meta.MQTTSessionCASConflict {
		return out, ErrConflict
	}
	state, reason := session.State, meta.MQTTSessionEndReason(0)
	if ended {
		state, reason = meta.MQTTSessionEnded, meta.MQTTSessionQuota
	}
	if (receipt.Status != meta.MQTTSessionCASApplied && receipt.Status != meta.MQTTSessionCASUnchanged) || receipt.CurrentRevision != session.Revision+1 || receipt.SessionState != state || receipt.TerminationReason != reason {
		return out, ErrEvidence
	}
	return AccountingResult{Owner: sessionOwner(session), Through: mutation.Through, AddedMessages: mutation.AddedMessages, AddedBytes: mutation.AddedBytes, Changed: receipt.Status == meta.MQTTSessionCASApplied, Ended: ended}, nil
}

func (a *Accounting) live(s meta.MQTTSession, at int64) error {
	if at <= 0 || at < s.UpdatedAtMS || s.Revision == math.MaxUint64 {
		return ErrClock
	}
	switch s.State {
	case meta.MQTTSessionActive:
		if at >= s.LeaseUntilMS {
			return ErrFenced
		}
	case meta.MQTTSessionOffline:
		if at >= s.OfflineExpiresAtMS {
			return ErrFenced
		}
	default:
		return ErrFenced
	}
	return nil
}
func (a *Accounting) authorize(ctx context.Context, uid string, sub meta.MQTTSubscription) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	version, err := a.options.Authorization.AuthorizeSubscription(ctx, uid, subscriptionRequestFromRow(sub))
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if version != sub.AuthorizationVersion {
		return ErrSubscriptionRevoked
	}
	return nil
}
func (a *Accounting) read(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if err := ctx.Err(); err != nil {
		return meta.MQTTReadResult{}, err
	}
	r, err := a.options.Store.ReadMQTT(ctx, q)
	if err != nil {
		return meta.MQTTReadResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return meta.MQTTReadResult{}, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || r.Accounting != nil || len(r.SourceOwners) != 0 || len(r.Sessions) != 0 || len(r.Inflight) != 0 || len(r.Wills) != 0 ||
		(q.Kind != meta.MQTTReadDeliveryCursor && len(r.DeliveryCursors) != 0) || (q.Kind != meta.MQTTReadSubscription && len(r.Subscriptions) != 0) || (q.Kind != meta.MQTTReadSourceBinding && len(r.Bindings) != 0) || (q.Kind == meta.MQTTReadSourceBinding && r.Session != nil) {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}

// qualifiesForBacklog preserves immutable publisher/QoS/expiry semantics. The
// typed reader already proved content and control identity; this grants no send.
func qualifiesForBacklog(sub meta.MQTTSubscription, e ch.MQTTReplayPublication, at int64) (bool, error) {
	m := e.Message
	eligible := !e.Internal && sub.GrantedQoS == 1
	if m.Expire != 0 {
		duration := int64(m.Expire) * 1000
		if m.ServerTimestampMS <= 0 || m.ServerTimestampMS > math.MaxInt64-duration {
			return false, ErrEvidence
		}
		if at >= m.ServerTimestampMS+duration {
			eligible = false
		}
	}
	if len(m.PublicationMetadata) != 0 {
		md, err := publication.Decode(m.PublicationMetadata)
		if err != nil {
			return false, ErrEvidence
		}
		deadline, expires, err := md.ExpiryDeadlineMS(m.ServerTimestampMS)
		if err != nil {
			return false, ErrEvidence
		}
		if md.QoS == 0 || (expires && at >= deadline) || (sub.NoLocal && md.PublisherNamespace == sub.Namespace && md.PublisherClientID == sub.ClientID) {
			eligible = false
		}
	}
	return eligible, nil
}
