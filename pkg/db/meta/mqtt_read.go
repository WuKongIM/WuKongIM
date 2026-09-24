package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// MQTTReadKind selects one closed, bounded metadata read operation.
type MQTTReadKind uint8

const (
	MQTTReadSession MQTTReadKind = 1 + iota
	MQTTReadSessionDeadlines
	MQTTReadSubscription
	MQTTReadSubscriptions
	MQTTReadSubscriptionRecovery
	MQTTReadDeliveryCursor
	MQTTReadDeliveryCursors
	MQTTReadInflight
	MQTTReadInflightPage
	MQTTReadSourceBinding
	MQTTReadSourceCandidates
	MQTTReadSourceRecovery
	MQTTReadSourceRetention
	MQTTReadWill
	MQTTReadWillRecovery
	// MQTTReadSourceOwners discovers distinct Channel sources through retention index 4.
	MQTTReadSourceOwners
	// MQTTReadReplaySources includes retained tombstones so cleanup stays discoverable.
	MQTTReadReplaySources
	// MQTTReadAccounting pins Session, cursor and its exact qualified range head.
	MQTTReadAccounting
	// MQTTReadMembership pins channel flags, ordinary membership and its allocator.
	MQTTReadMembership
)

// MQTTReadCursor contains exactly the cursor belonging to the selected read.
// Complete index tie-breakers survive RPC serialization and hash-Slot paging.
type MQTTReadCursor struct {
	// SourceOwner is omitted entirely for older kinds to preserve their JSON shape.
	SourceOwner    MQTTBindingOwner                 `json:"source_owner,omitzero"`
	Topic          string                           `json:"topic,omitempty"`
	Deadline       MQTTSessionDeadlineCursor        `json:"deadline,omitempty"`
	Subscription   MQTTSubscriptionRecoveryCursor   `json:"subscription,omitempty"`
	Delivery       MQTTDeliveryCursorKey            `json:"delivery,omitempty"`
	Inflight       MQTTInflightCursor               `json:"inflight,omitempty"`
	Binding        MQTTSourceBindingKey             `json:"binding,omitempty"`
	SourceRecovery MQTTSourceBindingRecoveryCursor  `json:"source_recovery,omitempty"`
	Retention      MQTTSourceBindingRetentionCursor `json:"retention,omitempty"`
	Will           MQTTWillRecoveryCursor           `json:"will,omitempty"`
}

// MQTTRead carries one operation. Irrelevant fields are rejected, not ignored.
// Logical/physical Slot routing is deliberately outside this storage request.
type MQTTRead struct {
	MembershipKey          SubscriberKey         `json:"membership_key,omitzero"`
	Kind                   MQTTReadKind          `json:"kind"`
	Namespace              string                `json:"namespace,omitempty"`
	ClientID               string                `json:"client_id,omitempty"`
	SessionGeneration      uint64                `json:"session_generation,omitempty"`
	SubscriptionGeneration uint64                `json:"subscription_generation,omitempty"`
	Topic                  string                `json:"topic,omitempty"`
	CursorKey              MQTTDeliveryCursorKey `json:"cursor_key,omitempty"`
	PacketID               uint16                `json:"packet_id,omitempty"`
	Owner                  MQTTBindingOwner      `json:"owner,omitempty"`
	BindingKey             MQTTSourceBindingKey  `json:"binding_key,omitempty"`
	WillKey                MQTTWillKey           `json:"will_key,omitempty"`
	Limit                  int                   `json:"limit,omitempty"`
	After                  MQTTReadCursor        `json:"after,omitempty"`
}

// MQTTReadResult owns a bounded result from one snapshot. Session is included
// with Session-owned child reads so callers can fence subsequent decisions.
type MQTTReadResult struct {
	Membership      *MQTTMembershipView  `json:"membership,omitempty"`
	Accounting      *MQTTAccountingRange `json:"accounting,omitempty"`
	SourceOwners    []MQTTBindingOwner   `json:"source_owners,omitempty"`
	Session         *MQTTSession         `json:"session,omitempty"`
	Sessions        []MQTTSession        `json:"sessions,omitempty"`
	Subscriptions   []MQTTSubscription   `json:"subscriptions,omitempty"`
	DeliveryCursors []MQTTDeliveryCursor `json:"delivery_cursors,omitempty"`
	Inflight        []MQTTInflight       `json:"inflight,omitempty"`
	Bindings        []MQTTSourceBinding  `json:"bindings,omitempty"`
	Wills           []MQTTWill           `json:"wills,omitempty"`
	After           MQTTReadCursor       `json:"after"`
	Done            bool                 `json:"done"`
}

// Recovery identifies reads that scan one explicitly selected logical hash Slot.
func (q MQTTRead) Recovery() bool {
	return q.Kind == MQTTReadSessionDeadlines || q.Kind == MQTTReadSubscriptionRecovery || q.Kind == MQTTReadSourceRecovery || q.Kind == MQTTReadWillRecovery || q.Kind == MQTTReadSourceOwners || q.Kind == MQTTReadReplaySources
}

// SessionIdentity reports the stable owner for Session-scoped reads, including
// detached Will lookup. It excludes source/UID-owned projections and scans.
func (q MQTTRead) SessionIdentity() (string, string, bool) {
	switch q.Kind {
	case MQTTReadSession, MQTTReadSubscription, MQTTReadSubscriptions, MQTTReadDeliveryCursors, MQTTReadInflight, MQTTReadInflightPage:
		return q.Namespace, q.ClientID, true
	case MQTTReadDeliveryCursor, MQTTReadAccounting:
		return q.CursorKey.Namespace, q.CursorKey.ClientID, true
	case MQTTReadWill:
		return q.WillKey.Namespace, q.WillKey.ClientID, true
	default:
		return "", "", false
	}
}

// ValidateMQTTRead rejects ambiguous fields and bounds all work before a caller
// pays for an authority barrier. Table methods additionally validate cursors.
func ValidateMQTTRead(q MQTTRead) error {
	want := MQTTRead{Kind: q.Kind}
	page := false
	switch q.Kind {
	case MQTTReadMembership:
		want.MembershipKey = q.MembershipKey
		if err := validateMQTTMembershipKey(q.MembershipKey); err != nil {
			return err
		}
	case MQTTReadSession:
		want.Namespace, want.ClientID = q.Namespace, q.ClientID
	case MQTTReadSessionDeadlines:
		page, want.After.Deadline = true, q.After.Deadline
	case MQTTReadSubscription, MQTTReadSubscriptions:
		want.Namespace, want.ClientID, want.SessionGeneration = q.Namespace, q.ClientID, q.SessionGeneration
		if q.SessionGeneration == 0 {
			return dberrors.ErrInvalidArgument
		}
		if q.Kind == MQTTReadSubscription {
			want.Topic = q.Topic
			if validateMQTTIdentity(q.Topic, 2048) != nil {
				return dberrors.ErrInvalidArgument
			}
		} else {
			page, want.After.Topic = true, q.After.Topic
		}
	case MQTTReadSubscriptionRecovery:
		page, want.After.Subscription = true, q.After.Subscription
	case MQTTReadDeliveryCursor, MQTTReadAccounting:
		want.CursorKey = q.CursorKey
		if validateMQTTDeliveryCursorKey(q.CursorKey) != nil {
			return dberrors.ErrInvalidArgument
		}
	case MQTTReadDeliveryCursors:
		want.Namespace, want.ClientID, want.SessionGeneration, want.SubscriptionGeneration = q.Namespace, q.ClientID, q.SessionGeneration, q.SubscriptionGeneration
		if q.SessionGeneration == 0 || q.SubscriptionGeneration == 0 {
			return dberrors.ErrInvalidArgument
		}
		page, want.After.Delivery = true, q.After.Delivery
	case MQTTReadInflight, MQTTReadInflightPage:
		want.Namespace, want.ClientID, want.SessionGeneration = q.Namespace, q.ClientID, q.SessionGeneration
		if q.SessionGeneration == 0 {
			return dberrors.ErrInvalidArgument
		}
		if q.Kind == MQTTReadInflight {
			want.PacketID = q.PacketID
			if q.PacketID == 0 {
				return dberrors.ErrInvalidArgument
			}
		} else {
			page, want.After.Inflight = true, q.After.Inflight
		}
	case MQTTReadSourceBinding:
		want.BindingKey = q.BindingKey
		if validateMQTTSourceBindingKey(q.BindingKey) != nil {
			return dberrors.ErrInvalidArgument
		}
	case MQTTReadSourceCandidates, MQTTReadSourceRetention:
		want.Owner = q.Owner
		if validateMQTTBindingOwner(q.Owner) != nil {
			return dberrors.ErrInvalidArgument
		}
		page = true
		if q.Kind == MQTTReadSourceCandidates {
			want.After.Binding = q.After.Binding
		} else {
			want.After.Retention = q.After.Retention
		}
	case MQTTReadSourceOwners, MQTTReadReplaySources:
		page, want.After.SourceOwner = true, q.After.SourceOwner
	case MQTTReadSourceRecovery:
		page, want.After.SourceRecovery = true, q.After.SourceRecovery
	case MQTTReadWill:
		want.WillKey = q.WillKey
		if validateMQTTWillKey(q.WillKey) != nil {
			return dberrors.ErrInvalidArgument
		}
	case MQTTReadWillRecovery:
		page, want.After.Will = true, q.After.Will
	default:
		return dberrors.ErrInvalidArgument
	}
	if namespace, client, ok := q.SessionIdentity(); ok && (validateMQTTIdentity(namespace, 1024) != nil || validateMQTTIdentity(client, 1024) != nil) {
		return dberrors.ErrInvalidArgument
	}
	if page {
		want.Limit = q.Limit
		if q.Limit < 1 || q.Limit > 64 || (q.Kind == MQTTReadWillRecovery && q.Limit > 16) {
			return dberrors.ErrInvalidArgument
		}
	}
	if q != want {
		return dberrors.ErrInvalidArgument
	}
	return validateMQTTReadCursor(q)
}

func validateMQTTReadCursor(q MQTTRead) error {
	a := q.After
	if a.SourceOwner != (MQTTBindingOwner{}) && (a.SourceOwner.Kind != MQTTBindingChannel || validateMQTTBindingOwner(a.SourceOwner) != nil) {
		return dberrors.ErrInvalidArgument
	}
	bad := a.Topic != "" && validateMQTTIdentity(a.Topic, 2048) != nil
	if a.Deadline != (MQTTSessionDeadlineCursor{}) {
		bad = bad || a.Deadline.DeadlineMS <= 0 || validateMQTTIdentity(a.Deadline.Namespace, 1024) != nil || validateMQTTIdentity(a.Deadline.ClientID, 1024) != nil
	}
	if a.Subscription != (MQTTSubscriptionRecoveryCursor{}) {
		bad = bad || a.Subscription.RecoveryAtMS <= 0 || validateMQTTSubscriptionKey(a.Subscription.Namespace, a.Subscription.ClientID, a.Subscription.SessionGeneration, a.Subscription.Topic) != nil
	}
	if a.Delivery != (MQTTDeliveryCursorKey{}) {
		bad = bad || validateMQTTDeliveryCursorKey(a.Delivery) != nil || a.Delivery.Namespace != q.Namespace || a.Delivery.ClientID != q.ClientID || a.Delivery.SessionGeneration != q.SessionGeneration || a.Delivery.SubscriptionGeneration != q.SubscriptionGeneration
	}
	if a.Inflight != (MQTTInflightCursor{}) {
		bad = bad || a.Inflight.DeliveryOrder == 0 || a.Inflight.PacketID == 0
	}
	if a.Binding != (MQTTSourceBindingKey{}) {
		bad = bad || validateMQTTSourceBindingKey(a.Binding) != nil || a.Binding.Owner != q.Owner
	}
	if a.SourceRecovery != (MQTTSourceBindingRecoveryCursor{}) {
		bad = bad || a.SourceRecovery.RecoveryAtMS <= 0 || validateMQTTSourceBindingKey(a.SourceRecovery.Key) != nil
	}
	if a.Retention != (MQTTSourceBindingRetentionCursor{}) {
		bad = bad || validateMQTTSourceBindingKey(a.Retention.Key) != nil || a.Retention.Key.Owner != q.Owner
	}
	if a.Will != (MQTTWillRecoveryCursor{}) {
		bad = bad || a.Will.RecoveryAtMS <= 0 || validateMQTTWillKey(a.Will.Key) != nil
	}
	if bad {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ReadMQTTState pins a complete local read only after the caller establishes
// authority. The snapshot cannot escape this call or be used to mutate a shard.
func (db *MetaDB) ReadMQTTState(ctx context.Context, hashSlot HashSlot, q MQTTRead) (MQTTReadResult, error) {
	if err := ValidateMQTTRead(q); err != nil {
		return MQTTReadResult{}, err
	}
	if err := contextErr(ctx); err != nil {
		return MQTTReadResult{}, err
	}
	if db == nil || db.engine == nil {
		return MQTTReadResult{}, dberrors.ErrClosed
	}
	snapshot, err := db.engine.NewSnapshot()
	if err != nil {
		return MQTTReadResult{}, err
	}
	defer snapshot.Close()
	view := &Shard{db: db, hashSlot: hashSlot, readSnapshot: snapshot}
	return view.readMQTTState(ctx, q)
}

// ReadMQTTState exposes the coherent local view to the authoritative Slot proxy.
func (db *DB) ReadMQTTState(ctx context.Context, hashSlot uint16, q MQTTRead) (MQTTReadResult, error) {
	if db == nil || db.meta == nil {
		return MQTTReadResult{}, dberrors.ErrClosed
	}
	return db.meta.ReadMQTTState(ctx, HashSlot(hashSlot), q)
}

func (s *Shard) readMQTTState(ctx context.Context, q MQTTRead) (MQTTReadResult, error) {
	out := MQTTReadResult{After: q.After, Done: true}
	if ns, client, ok := q.SessionIdentity(); ok {
		r, found, err := s.GetMQTTSession(ctx, ns, client)
		if err != nil {
			return MQTTReadResult{}, err
		}
		if found {
			out.Session = &r
		}
	}
	var err error
	switch q.Kind {
	case MQTTReadMembership:
		out.Membership, err = s.readMQTTMembership(ctx, q.MembershipKey)
	case MQTTReadSession:
	case MQTTReadSessionDeadlines:
		out.Sessions, out.After.Deadline, out.Done, err = s.ListMQTTSessionDeadlines(ctx, q.After.Deadline, q.Limit)
	case MQTTReadSubscription:
		var r MQTTSubscription
		var found bool
		r, found, err = s.GetMQTTSubscription(ctx, q.Namespace, q.ClientID, q.SessionGeneration, q.Topic)
		if found {
			out.Subscriptions = []MQTTSubscription{r}
		}
	case MQTTReadSubscriptions:
		out.Subscriptions, out.After.Topic, out.Done, err = s.ListMQTTSubscriptions(ctx, q.Namespace, q.ClientID, q.SessionGeneration, q.After.Topic, q.Limit)
	case MQTTReadSubscriptionRecovery:
		out.Subscriptions, out.After.Subscription, out.Done, err = s.ListMQTTSubscriptionRecovery(ctx, q.After.Subscription, q.Limit)
	case MQTTReadDeliveryCursor, MQTTReadAccounting:
		var r MQTTDeliveryCursor
		var found bool
		r, found, err = s.GetMQTTDeliveryCursor(ctx, q.CursorKey)
		if found {
			out.DeliveryCursors = []MQTTDeliveryCursor{r}
			if q.Kind == MQTTReadAccounting && err == nil {
				out.Accounting, err = s.readMQTTAccounting(r)
			}
		}
	case MQTTReadDeliveryCursors:
		out.DeliveryCursors, out.After.Delivery, out.Done, err = s.ListMQTTDeliveryCursors(ctx, q.Namespace, q.ClientID, q.SessionGeneration, q.SubscriptionGeneration, q.After.Delivery, q.Limit)
	case MQTTReadInflight:
		var r MQTTInflight
		var found bool
		r, found, err = s.GetMQTTInflight(ctx, q.Namespace, q.ClientID, q.SessionGeneration, MQTTOutbound, q.PacketID)
		if found {
			out.Inflight = []MQTTInflight{r}
		}
	case MQTTReadInflightPage:
		out.Inflight, out.After.Inflight, out.Done, err = s.ListMQTTInflight(ctx, q.Namespace, q.ClientID, q.SessionGeneration, MQTTOutbound, q.After.Inflight, q.Limit)
	case MQTTReadSourceBinding:
		var r MQTTSourceBinding
		var found bool
		r, found, err = s.GetMQTTSourceBinding(ctx, q.BindingKey)
		if found {
			out.Bindings = []MQTTSourceBinding{r}
		}
	case MQTTReadSourceCandidates:
		out.Bindings, out.After.Binding, out.Done, err = s.ListMQTTSourceBindingCandidates(ctx, q.Owner, q.After.Binding, q.Limit)
	case MQTTReadSourceOwners:
		out.SourceOwners, out.After.SourceOwner, out.Done, err = s.ListMQTTSourceOwners(ctx, q.After.SourceOwner, q.Limit)
	case MQTTReadReplaySources:
		out.SourceOwners, out.After.SourceOwner, out.Done, err = s.ListMQTTReplaySources(ctx, q.After.SourceOwner, q.Limit)
	case MQTTReadSourceRecovery:
		out.Bindings, out.After.SourceRecovery, out.Done, err = s.ListMQTTSourceBindingRecovery(ctx, q.After.SourceRecovery, q.Limit)
	case MQTTReadSourceRetention:
		out.Bindings, out.After.Retention, out.Done, err = s.ListMQTTSourceBindingRetention(ctx, q.Owner, q.After.Retention, q.Limit)
	case MQTTReadWill:
		var r MQTTWill
		var found bool
		r, found, err = s.GetMQTTWill(ctx, q.WillKey)
		if found {
			out.Wills = []MQTTWill{r}
		}
	case MQTTReadWillRecovery:
		out.Wills, out.After.Will, out.Done, err = s.ListMQTTWillRecovery(ctx, q.After.Will, q.Limit)
	default:
		err = dberrors.ErrInvalidArgument
	}
	if err != nil {
		return MQTTReadResult{}, err
	}
	return out, nil
}
