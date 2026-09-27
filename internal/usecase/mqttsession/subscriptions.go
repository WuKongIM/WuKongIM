package mqttsession

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// Subscribe persists one filter at a time, granting QoS at most one. Success
// describes authoritative Active intent; entry still owns its SUBACK enqueue.
func (s *Subscriptions) Subscribe(parent context.Context, o contract.Owner, r SubscriptionRequest) (out meta.MQTTSubscription, err error) {
	if !validSubscriptionRequest(r) {
		return out, ErrInvalid
	}
	op, ctx, cancel, err := s.begin(parent, o)
	if err != nil {
		return out, err
	}
	unconfirmed := false
	defer subscriptionUnconfirmed(&unconfirmed, &err)
	defer finishSubscription(op, cancel, &err)
	if r.TargetKind == meta.MQTTSubscriptionUserInbox && r.TargetID != op.UID() {
		return out, ErrSubscriptionDenied
	}
	session, old, found, err := s.read(ctx, op, o, r.Topic)
	if err != nil {
		return out, err
	}
	unconfirmed = found && (old.Stage == meta.MQTTSubscriptionPreparing || old.Stage == meta.MQTTSubscriptionRemoving)
	version, err := s.authorize(ctx, op, r)
	if err != nil {
		return out, err
	}
	if found && old.Stage != meta.MQTTSubscriptionRemoved {
		if old.TargetKind != r.TargetKind || old.TargetID != r.TargetID {
			return out, ErrConflict
		}
		if old.AuthorizationVersion != version {
			return out, ErrSubscriptionRevoked
		}
		switch old.Stage {
		case meta.MQTTSubscriptionPreparing:
			if !subscriptionOptionsEqual(old, r) {
				return out, ErrConflict
			}
			return s.complete(ctx, op, o, old)
		case meta.MQTTSubscriptionActive:
			if subscriptionOptionsEqual(old, r) {
				return old, nil
			}
			next := old
			setSubscriptionOptions(&next, r)
			unconfirmed = true
			return s.mutate(ctx, op, o, session, next)
		default:
			return out, ErrConflict
		}
	}
	if err = s.checkCapacity(ctx, op, o, session); err != nil {
		return out, err
	}
	if session.Revision == math.MaxUint64 {
		return out, ErrClock
	}
	next := meta.MQTTSubscription{Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Topic: r.Topic, TargetKind: r.TargetKind, TargetID: r.TargetID, Generation: session.Revision + 1, AuthorizationVersion: version, Stage: meta.MQTTSubscriptionPreparing}
	next.OperationID = subscriptionOperationID(next)
	setSubscriptionOptions(&next, r)
	unconfirmed = true
	next, err = s.mutate(ctx, op, o, session, next)
	if err != nil {
		return out, err
	}
	return s.complete(ctx, op, o, next)
}

// Unsubscribe needs ownership, not receive permission. It never deletes inflight
// exchanges or resets their cursors. False means no non-removed intent existed.
func (s *Subscriptions) Unsubscribe(parent context.Context, o contract.Owner, topic string) (existed bool, err error) {
	if !validSubscriptionTopic(topic) {
		return false, ErrInvalid
	}
	op, ctx, cancel, err := s.begin(parent, o)
	if err != nil {
		return false, err
	}
	unconfirmed := false
	defer subscriptionUnconfirmed(&unconfirmed, &err)
	defer finishSubscription(op, cancel, &err)
	session, row, found, err := s.read(ctx, op, o, topic)
	if err != nil {
		return false, err
	}
	if !found || row.Stage == meta.MQTTSubscriptionRemoved {
		return false, nil
	}
	unconfirmed = true
	if row.Stage != meta.MQTTSubscriptionRemoving {
		row.Stage = meta.MQTTSubscriptionRemoving
		row, err = s.mutate(ctx, op, o, session, row)
		if err != nil {
			return false, err
		}
	}
	_, err = s.complete(ctx, op, o, row)
	return err == nil, err
}

// Preserve the original cause for bounded pending handling while distinguishing
// an unresolved intent from a rejection before any possible mutation.
func subscriptionUnconfirmed(possible *bool, err *error) {
	if *possible && *err != nil {
		*err = errors.Join(ErrSubscriptionUnconfirmed, *err)
	}
}

// Reconcile resumes exact pending intent under the current live owner. It does
// not create subscriptions or adopt another lifetime's cleanup responsibilities.
func (s *Subscriptions) Reconcile(parent context.Context, o contract.Owner, topic string) (out meta.MQTTSubscription, err error) {
	if !validSubscriptionTopic(topic) {
		return out, ErrInvalid
	}
	op, ctx, cancel, err := s.begin(parent, o)
	if err != nil {
		return out, err
	}
	defer finishSubscription(op, cancel, &err)
	_, row, found, err := s.read(ctx, op, o, topic)
	if err != nil {
		return out, err
	}
	if !found {
		return out, ErrConflict
	}
	if row.Stage == meta.MQTTSubscriptionRemoved {
		return row, nil
	}
	if row.Stage == meta.MQTTSubscriptionPreparing || row.Stage == meta.MQTTSubscriptionActive {
		version, err := s.authorize(ctx, op, subscriptionRequestFromRow(row))
		if err != nil {
			return out, err
		}
		if version != row.AuthorizationVersion {
			return out, ErrSubscriptionRevoked
		}
		if row.Stage == meta.MQTTSubscriptionActive {
			return row, nil
		}
	}
	return s.complete(ctx, op, o, row)
}

// complete accepts only projection evidence for the exact persisted child.
// A concurrent parent renewal is harmless; a changed child or owner is not.
func (s *Subscriptions) complete(ctx context.Context, op *subscriptionOperation, o contract.Owner, row meta.MQTTSubscription) (meta.MQTTSubscription, error) {
	if err := checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTSubscription{}, err
	}
	request := SubscriptionProjectionRequest{Owner: o, UID: op.UID(), Subscription: row}
	var receipt SubscriptionProjectionReceipt
	var err error
	switch row.Stage {
	case meta.MQTTSubscriptionPreparing:
		receipt, err = s.options.Projection.Establish(ctx, request)
	case meta.MQTTSubscriptionRemoving:
		receipt, err = s.options.Projection.Remove(ctx, request)
	default:
		return meta.MQTTSubscription{}, ErrInvalid
	}
	if err != nil {
		// A background worker may finish this exact closed intent while projection
		// yields. Confirm durable completion before treating that race as failure.
		if row.Stage == meta.MQTTSubscriptionRemoving && (errors.Is(err, ErrConflict) || errors.Is(err, ErrSourceDrainPending)) {
			_, current, found, readErr := s.read(ctx, op, o, row.Topic)
			if readErr == nil && found && sameRemovedIntent(row, current) {
				return current, nil
			}
		}
		return meta.MQTTSubscription{}, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTSubscription{}, err
	}
	if receipt != (SubscriptionProjectionReceipt{Namespace: row.Namespace, ClientID: row.ClientID, Topic: row.Topic, SessionGeneration: row.SessionGeneration, SubscriptionGeneration: row.Generation, IntentRevision: row.Revision, OperationID: row.OperationID}) {
		return meta.MQTTSubscription{}, ErrEvidence
	}
	session, current, found, err := s.read(ctx, op, o, row.Topic)
	if err != nil {
		return meta.MQTTSubscription{}, err
	}
	if found && sameRemovedIntent(row, current) {
		return current, nil
	}
	if !found || current != row {
		return meta.MQTTSubscription{}, ErrConflict
	}
	if row.Stage == meta.MQTTSubscriptionPreparing {
		version, err := s.authorize(ctx, op, subscriptionRequestFromRow(row))
		if err != nil {
			return meta.MQTTSubscription{}, err
		}
		if version != row.AuthorizationVersion {
			return meta.MQTTSubscription{}, ErrSubscriptionRevoked
		}
		row.Stage = meta.MQTTSubscriptionActive
	} else {
		row.Stage = meta.MQTTSubscriptionRemoved
	}
	return s.mutate(ctx, op, o, session, row)
}

// subscriptionOperation checks parent cancellation synchronously as well as the
// owner gate; context.AfterFunc propagation may be scheduled after cancellation.
type subscriptionOperation struct {
	*runtime.Operation
	parent context.Context
}

func (o *subscriptionOperation) Check() error {
	if err := o.parent.Err(); err != nil {
		return err
	}
	return o.Operation.Check()
}

func (s *Subscriptions) begin(parent context.Context, o contract.Owner) (*subscriptionOperation, context.Context, context.CancelFunc, error) {
	if s == nil || parent == nil || o.Validate() != nil {
		return nil, nil, nil, ErrInvalid
	}
	op, err := s.options.Owners.Begin(parent, o)
	if err != nil {
		return nil, nil, nil, err
	}
	deadline := time.Now().Add(s.options.Timeout)
	if d, ok := parent.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	ctx, cancel := context.WithDeadline(op.Context(), deadline)
	return &subscriptionOperation{Operation: op, parent: parent}, ctx, cancel, nil
}

func finishSubscription(op *subscriptionOperation, cancel context.CancelFunc, err *error) {
	defer op.Done()
	defer cancel()
	if recover() != nil {
		*err = ErrSubscriptionCallback
	}
}

func checkSubscriptionScope(ctx context.Context, op *subscriptionOperation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return op.Check()
}

func (s *Subscriptions) now() (time.Time, error) {
	now := s.options.Now()
	if now == now.Round(0) || now.UnixMilli() <= 0 || now.UnixMilli() > math.MaxInt64-int64(time.Minute/time.Millisecond) {
		return time.Time{}, ErrClock
	}
	return now, nil
}

func (s *Subscriptions) authorize(ctx context.Context, op *subscriptionOperation, r SubscriptionRequest) (uint64, error) {
	if r.TargetKind == meta.MQTTSubscriptionUserInbox && r.TargetID != op.UID() {
		return 0, ErrSubscriptionDenied
	}
	if err := checkSubscriptionScope(ctx, op); err != nil {
		return 0, err
	}
	version, err := s.options.Authorization.AuthorizeSubscription(ctx, op.UID(), r)
	if err != nil {
		return 0, err
	}
	return version, checkSubscriptionScope(ctx, op)
}

func (s *Subscriptions) checkSession(ctx context.Context, op *subscriptionOperation, o contract.Owner, row *meta.MQTTSession) error {
	if err := checkSubscriptionScope(ctx, op); err != nil {
		return err
	}
	if row == nil {
		return ErrFenced
	}
	if meta.ValidateMQTTSession(*row) != nil {
		return ErrEvidence
	}
	if sessionOwner(*row) != o || row.UID != op.UID() || row.State != meta.MQTTSessionActive {
		return ErrFenced
	}
	now, err := s.now()
	if err != nil {
		return err
	}
	if now.UnixMilli() < row.UpdatedAtMS || now.UnixMilli() >= row.LeaseUntilMS {
		return ErrClock
	}
	return nil
}

func (s *Subscriptions) read(ctx context.Context, op *subscriptionOperation, o contract.Owner, topic string) (meta.MQTTSession, meta.MQTTSubscription, bool, error) {
	var emptySession meta.MQTTSession
	var emptyRow meta.MQTTSubscription
	if err := checkSubscriptionScope(ctx, op); err != nil {
		return emptySession, emptyRow, false, err
	}
	r, err := s.options.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Topic: topic})
	if err != nil {
		return emptySession, emptyRow, false, err
	}
	if err = s.checkSession(ctx, op, o, r.Session); err != nil {
		return emptySession, emptyRow, false, err
	}
	if !r.Done || len(r.Subscriptions) > 1 {
		return emptySession, emptyRow, false, ErrEvidence
	}
	if len(r.Subscriptions) == 0 {
		return *r.Session, emptyRow, false, nil
	}
	row := r.Subscriptions[0]
	if !validSubscriptionEvidence(row, *r.Session) || row.Topic != topic {
		return emptySession, emptyRow, false, ErrEvidence
	}
	return *r.Session, row, true, nil
}

func (s *Subscriptions) mutate(ctx context.Context, op *subscriptionOperation, o contract.Owner, session meta.MQTTSession, row meta.MQTTSubscription) (meta.MQTTSubscription, error) {
	if err := s.checkSession(ctx, op, o, &session); err != nil {
		return meta.MQTTSubscription{}, err
	}
	now, err := s.now()
	if err != nil {
		return meta.MQTTSubscription{}, err
	}
	if session.Revision == math.MaxUint64 {
		return meta.MQTTSubscription{}, ErrClock
	}
	row.Revision = session.Revision + 1
	row.UpdatedAtMS = now.UnixMilli()
	row.RecoveryAtMS = 0
	if row.Stage == meta.MQTTSubscriptionPreparing || row.Stage == meta.MQTTSubscriptionRemoving {
		row.RecoveryAtMS = row.UpdatedAtMS
	}
	m := meta.MQTTSubscriptionMutation{ExpectedRevision: session.Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Subscription: row}
	if meta.ValidateMQTTSubscriptionMutation(m) != nil {
		return meta.MQTTSubscription{}, ErrInvalid
	}
	receipt, err := s.options.Store.MutateMQTTSubscription(ctx, m)
	if err != nil {
		return meta.MQTTSubscription{}, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTSubscription{}, err
	}
	if receipt.Status == meta.MQTTSessionCASConflict {
		return meta.MQTTSubscription{}, ErrConflict
	}
	if (receipt.Status != meta.MQTTSessionCASApplied && receipt.Status != meta.MQTTSessionCASUnchanged) || receipt.CurrentRevision != row.Revision {
		return meta.MQTTSubscription{}, ErrEvidence
	}
	return row, nil
}

func (s *Subscriptions) checkCapacity(ctx context.Context, op *subscriptionOperation, o contract.Owner, session meta.MQTTSession) error {
	after := ""
	retained := 0
	for page := 0; page < s.options.MaxScanPages; page++ {
		previous := after
		if err := checkSubscriptionScope(ctx, op); err != nil {
			return err
		}
		result, err := s.options.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscriptions, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Limit: 64, After: meta.MQTTReadCursor{Topic: after}})
		if err != nil {
			return err
		}
		if err = s.checkSession(ctx, op, o, result.Session); err != nil {
			return err
		}
		if result.Session.Revision != session.Revision {
			return ErrConflict
		}
		if len(result.Subscriptions) > 64 || !result.Done && len(result.Subscriptions) == 0 {
			return ErrEvidence
		}
		for _, row := range result.Subscriptions {
			if !validSubscriptionEvidence(row, session) || after != "" && !subscriptionTopicAfter(row.Topic, after) {
				return ErrEvidence
			}
			after = row.Topic
			if row.Stage != meta.MQTTSubscriptionRemoved {
				retained++
			}
		}
		// The metadata API leaves the input cursor unchanged on its final page.
		cursor := after
		if result.Done {
			cursor = previous
		}
		if result.After != (meta.MQTTReadCursor{Topic: cursor}) {
			return ErrEvidence
		}
		if retained >= s.options.MaxSubscriptions {
			return ErrSubscriptionLimit
		}
		if result.Done {
			return nil
		}
	}
	return ErrSubscriptionLimit
}

func validSubscriptionEvidence(row meta.MQTTSubscription, session meta.MQTTSession) bool {
	return meta.ValidateMQTTSubscription(row) == nil && row.Namespace == session.Namespace && row.ClientID == session.ClientID && row.SessionGeneration == session.Generation && row.Revision <= session.Revision
}
func subscriptionTopicAfter(next, old string) bool {
	return len(next) > len(old) || len(next) == len(old) && next > old
}
func validSubscriptionTopic(topic string) bool {
	return contract.ValidIdentity(topic, 2048) && !strings.ContainsAny(topic, "+#") && !strings.HasPrefix(topic, "$share/")
}
func validSubscriptionRequest(r SubscriptionRequest) bool {
	return validSubscriptionTopic(r.Topic) && contract.ValidIdentity(r.TargetID, 1024) && (r.TargetKind == meta.MQTTSubscriptionGroup || r.TargetKind == meta.MQTTSubscriptionUserInbox) && r.RequestedQoS <= 2 && r.RetainHandling <= 2 && r.SubscriptionIdentifier <= 268435455
}
func setSubscriptionOptions(row *meta.MQTTSubscription, r SubscriptionRequest) {
	row.GrantedQoS = min(r.RequestedQoS, 1)
	row.NoLocal = r.NoLocal
	row.RetainAsPublished = r.RetainAsPublished
	row.RetainHandling = r.RetainHandling
	row.SubscriptionIdentifier = r.SubscriptionIdentifier
}
func subscriptionOptionsEqual(row meta.MQTTSubscription, r SubscriptionRequest) bool {
	return row.GrantedQoS == min(r.RequestedQoS, 1) && row.NoLocal == r.NoLocal && row.RetainAsPublished == r.RetainAsPublished && row.RetainHandling == r.RetainHandling && row.SubscriptionIdentifier == r.SubscriptionIdentifier
}
func subscriptionRequestFromRow(row meta.MQTTSubscription) SubscriptionRequest {
	return SubscriptionRequest{Topic: row.Topic, TargetKind: row.TargetKind, TargetID: row.TargetID, RequestedQoS: row.GrantedQoS, NoLocal: row.NoLocal, RetainAsPublished: row.RetainAsPublished, RetainHandling: row.RetainHandling, SubscriptionIdentifier: row.SubscriptionIdentifier}
}
func subscriptionOperationID(row meta.MQTTSubscription) string {
	b := []byte("mqtt-sub-v1")
	for _, v := range []string{row.Namespace, row.ClientID, row.Topic} {
		b = binary.BigEndian.AppendUint64(b, uint64(len(v)))
		b = append(b, v...)
	}
	b = binary.BigEndian.AppendUint64(b, row.SessionGeneration)
	b = binary.BigEndian.AppendUint64(b, row.Generation)
	sum := sha256.Sum256(b)
	return "mqtt-sub-v1:" + hex.EncodeToString(sum[:])
}

// sameRemovedIntent accepts only monotonic completion of the full captured
// child. A replacement generation, operation or receive option never matches.
func sameRemovedIntent(before, after meta.MQTTSubscription) bool {
	if before.Stage != meta.MQTTSubscriptionRemoving || after.Stage != meta.MQTTSubscriptionRemoved || after.Revision <= before.Revision || after.UpdatedAtMS < before.UpdatedAtMS {
		return false
	}
	before.Stage, before.RecoveryAtMS = meta.MQTTSubscriptionRemoved, 0
	before.Revision, before.UpdatedAtMS = after.Revision, after.UpdatedAtMS
	return before == after
}
