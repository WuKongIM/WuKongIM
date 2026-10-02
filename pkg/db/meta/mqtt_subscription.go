package meta

import (
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// MQTTSubscriptionStage tracks recoverable cross-owner projection work.
type MQTTSubscriptionStage uint8

const (
	MQTTSubscriptionPreparing MQTTSubscriptionStage = 1
	MQTTSubscriptionActive    MQTTSubscriptionStage = 2
	MQTTSubscriptionRemoving  MQTTSubscriptionStage = 3
	MQTTSubscriptionRemoved   MQTTSubscriptionStage = 4
)

// MQTTSubscriptionTargetKind distinguishes one UID inbox from one group source.
type MQTTSubscriptionTargetKind uint8

const (
	MQTTSubscriptionUserInbox MQTTSubscriptionTargetKind = 1
	MQTTSubscriptionGroup     MQTTSubscriptionTargetKind = 2
)

// MQTTSubscription stores intent, not IM membership or a source protection proof.
// Its primary identity is namespace, ClientID, session generation and exact topic.
type MQTTSubscription struct {
	Namespace         string `json:"broker_namespace"`
	ClientID          string `json:"client_id"`
	SessionGeneration uint64 `json:"session_generation"`
	Topic             string `json:"topic"`
	// Generation remains stable across option replacement; cursors refer to it.
	Generation uint64 `json:"generation"`
	// Revision is the resulting session revision of this exact child mutation.
	// It distinguishes retries from unrelated session writes.
	Revision          uint64                     `json:"revision"`
	TargetKind        MQTTSubscriptionTargetKind `json:"target_kind"`
	TargetID          string                     `json:"target_id"`
	GrantedQoS        uint8                      `json:"granted_qos"`
	NoLocal           bool                       `json:"no_local"`
	RetainAsPublished bool                       `json:"retain_as_published"`
	RetainHandling    uint8                      `json:"retain_handling"`
	// SubscriptionIdentifier is zero when the client supplied no identifier.
	SubscriptionIdentifier uint32 `json:"subscription_identifier"`
	// AuthorizationVersion identifies the captured permission incarnation; zero
	// is allowed for authority providers without a versioned membership row.
	AuthorizationVersion uint64                `json:"authorization_version"`
	Stage                MQTTSubscriptionStage `json:"stage"`
	// OperationID stays stable throughout projection establishment and cleanup.
	OperationID string `json:"operation_id"`
	// RecoveryAtMS indexes pending work; active and removed rows have no entry.
	RecoveryAtMS int64 `json:"recovery_at_ms"`
	UpdatedAtMS  int64 `json:"updated_at_ms"`
}

// MQTTSubscriptionMutation must carry the initiating owner's identity, never an
// identity copied from a newer session to make a stale connection's write pass.
// The session and child share one Slot commit and one revision increment.
type MQTTSubscriptionMutation struct {
	ExpectedRevision uint64           `json:"expected_revision"`
	OwnerGeneration  uint64           `json:"owner_generation"`
	OwnerNodeID      uint64           `json:"owner_node_id"`
	OwnerBootID      string           `json:"owner_boot_id"`
	ConnectionID     uint64           `json:"connection_id"`
	Subscription     MQTTSubscription `json:"subscription"`
}

// ValidateMQTTSubscription checks deterministic storage invariants. Canonical
// topic mapping, authorization and source protection belong to the use case.
func ValidateMQTTSubscription(r MQTTSubscription) error {
	if validateMQTTSubscriptionKey(r.Namespace, r.ClientID, r.SessionGeneration, r.Topic) != nil ||
		validateMQTTIdentity(r.TargetID, 1024) != nil || validateMQTTIdentity(r.OperationID, 128) != nil ||
		r.Generation == 0 || r.Revision == 0 || r.GrantedQoS > 1 || r.RetainHandling > 2 || r.SubscriptionIdentifier > 268435455 ||
		r.TargetKind < MQTTSubscriptionUserInbox || r.TargetKind > MQTTSubscriptionGroup || r.UpdatedAtMS <= 0 {
		return dberrors.ErrInvalidArgument
	}
	switch r.Stage {
	case MQTTSubscriptionPreparing, MQTTSubscriptionRemoving:
		if r.RecoveryAtMS <= 0 {
			return dberrors.ErrInvalidArgument
		}
	case MQTTSubscriptionActive, MQTTSubscriptionRemoved:
		if r.RecoveryAtMS != 0 {
			return dberrors.ErrInvalidArgument
		}
	default:
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ValidateMQTTSubscriptionMutation also bounds the revision and owner fence.
func ValidateMQTTSubscriptionMutation(m MQTTSubscriptionMutation) error {
	if m.ExpectedRevision == 0 || m.ExpectedRevision == math.MaxUint64 || m.Subscription.Revision != m.ExpectedRevision+1 || m.OwnerGeneration == 0 ||
		m.OwnerNodeID == 0 || m.ConnectionID == 0 || validateMQTTIdentity(m.OwnerBootID, 128) != nil {
		return dberrors.ErrInvalidArgument
	}
	return ValidateMQTTSubscription(m.Subscription)
}

// MutateMQTTSubscription changes intent only while its owner and session revision
// are current. Results are meaningful only after the enclosing batch commits.
// This does not establish cross-Slot source bindings or permit sending SUBACK.
func (b *Batch) MutateMQTTSubscription(slot HashSlot, m MQTTSubscriptionMutation) (*MQTTSessionCASResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if err := ValidateMQTTSubscriptionMutation(m); err != nil {
		return nil, err
	}
	result := &MQTTSessionCASResult{}
	row := m.Subscription
	b.addOp(slot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTSessionCASResult{Status: MQTTSessionCASConflict}
		session, found, err := loadUpdateRow(mqttSessionTable, state, slot, mqttSessionPrimaryKey(row.Namespace, row.ClientID))
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		result.CurrentRevision = session.Revision
		if session.OwnerGeneration != m.OwnerGeneration || session.OwnerNodeID != m.OwnerNodeID ||
			session.OwnerBootID != m.OwnerBootID || session.ConnectionID != m.ConnectionID {
			return nil
		}
		old, found, err := loadUpdateRow(mqttSubscriptionTable, state, slot, mqttSubscriptionPrimaryKey(row.Namespace, row.ClientID, row.SessionGeneration, row.Topic))
		if err != nil {
			return err
		}
		if session.Revision == m.ExpectedRevision+1 && found && old == row {
			result.Status = MQTTSessionCASUnchanged
			return nil
		}
		if session.Revision != m.ExpectedRevision || !validMQTTSubscriptionTransition(session, old, found, row) {
			return nil
		}
		if err := stageUpdateRow(mqttSubscriptionTable, state, batch, slot, row); err != nil {
			return err
		}
		session.Revision++
		session.UpdatedAtMS = row.UpdatedAtMS
		if err := stageUpdateRow(mqttSessionTable, state, batch, slot, session); err != nil {
			return err
		}
		*result = MQTTSessionCASResult{Status: MQTTSessionCASApplied, CurrentRevision: session.Revision}
		return nil
	})
	return result, nil
}

func validMQTTSubscriptionTransition(session MQTTSession, old MQTTSubscription, found bool, next MQTTSubscription) bool {
	if next.SessionGeneration > session.Generation {
		return false
	}
	current := next.SessionGeneration == session.Generation
	if !found || old.Stage == MQTTSubscriptionRemoved && next.Stage == MQTTSubscriptionPreparing {
		return current && session.State == MQTTSessionActive && next.Stage == MQTTSubscriptionPreparing &&
			next.Generation == session.Revision+1 && (!found || next.Generation > old.Generation && next.OperationID != old.OperationID)
	}
	if (!current || session.State == MQTTSessionEnded) && next.Stage != MQTTSubscriptionRemoving && next.Stage != MQTTSubscriptionRemoved {
		return false
	}
	if old.Generation != next.Generation || old.TargetKind != next.TargetKind || old.TargetID != next.TargetID ||
		old.AuthorizationVersion != next.AuthorizationVersion || old.OperationID != next.OperationID {
		return false
	}
	// Only replacement of an already active subscription may change options.
	if old.Stage != MQTTSubscriptionActive || next.Stage != MQTTSubscriptionActive {
		if old.GrantedQoS != next.GrantedQoS || old.NoLocal != next.NoLocal || old.RetainAsPublished != next.RetainAsPublished ||
			old.RetainHandling != next.RetainHandling || old.SubscriptionIdentifier != next.SubscriptionIdentifier {
			return false
		}
	}
	switch old.Stage {
	case MQTTSubscriptionPreparing:
		return next.Stage == MQTTSubscriptionPreparing || next.Stage == MQTTSubscriptionActive || next.Stage == MQTTSubscriptionRemoving
	case MQTTSubscriptionActive:
		return (session.State == MQTTSessionActive && next.Stage == MQTTSubscriptionActive) || next.Stage == MQTTSubscriptionRemoving
	case MQTTSubscriptionRemoving:
		return next.Stage == MQTTSubscriptionRemoving || next.Stage == MQTTSubscriptionRemoved
	case MQTTSubscriptionRemoved:
		return next.Stage == MQTTSubscriptionRemoved
	}
	return false
}

func validateMQTTSubscriptionKey(namespace, clientID string, generation uint64, topic string) error {
	if validateMQTTIdentity(namespace, 1024) != nil || validateMQTTIdentity(clientID, 1024) != nil ||
		generation == 0 || validateMQTTIdentity(topic, 2048) != nil {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func mqttSubscriptionPrimaryKey(namespace, clientID string, generation uint64, topic string) KeyParts {
	return KeyParts{String(namespace), String(clientID), Uint64(generation), String(topic)}
}

// GetMQTTSubscription reads node storage after the caller establishes authority.
func (s *Shard) GetMQTTSubscription(ctx context.Context, namespace, clientID string, generation uint64, topic string) (MQTTSubscription, bool, error) {
	if err := validateMQTTSubscriptionKey(namespace, clientID, generation, topic); err != nil {
		return MQTTSubscription{}, false, err
	}
	return mqttSubscriptionTable.Get(ctx, s, mqttSubscriptionPrimaryKey(namespace, clientID, generation, topic))
}

// MQTTSubscriptionRecoveryCursor includes all tie-breakers of the recovery index.
type MQTTSubscriptionRecoveryCursor struct {
	RecoveryAtMS      int64
	Namespace         string
	ClientID          string
	SessionGeneration uint64
	Topic             string
}

// ListMQTTSubscriptions returns a bounded generation-scoped page including
// pending intents and tombstones. The cursor uses encoded topic order, not
// lexical string order. Callers still need an authoritative, coherent read view.
func (s *Shard) ListMQTTSubscriptions(ctx context.Context, namespace, clientID string, generation uint64, after string, limit int) ([]MQTTSubscription, string, bool, error) {
	if limit < 1 || limit > 256 || validateMQTTIdentity(namespace, 1024) != nil || validateMQTTIdentity(clientID, 1024) != nil || generation == 0 ||
		(after != "" && validateMQTTIdentity(after, 2048) != nil) {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	prefix := KeyParts{String(namespace), String(clientID), Uint64(generation)}
	var cursor KeyParts
	if after != "" {
		cursor = mqttSubscriptionPrimaryKey(namespace, clientID, generation, after)
	}
	rows, next, done, err := mqttSubscriptionTable.scanPrimaryPrefixStrict(ctx, s, prefix, cursor, limit)
	if err != nil {
		return nil, after, false, err
	}
	if len(next) > 0 {
		after = next[3].S
	}
	return rows, after, done, nil
}

// ListMQTTSubscriptionRecovery pages durable projection work. Candidates must be
// revalidated against their current session owner/revision before they are used.
func (s *Shard) ListMQTTSubscriptionRecovery(ctx context.Context, after MQTTSubscriptionRecoveryCursor, limit int) ([]MQTTSubscription, MQTTSubscriptionRecoveryCursor, bool, error) {
	if limit < 1 || limit > 256 {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	var cursor KeyParts
	if after != (MQTTSubscriptionRecoveryCursor{}) {
		if after.RecoveryAtMS <= 0 || validateMQTTSubscriptionKey(after.Namespace, after.ClientID, after.SessionGeneration, after.Topic) != nil {
			return nil, after, false, dberrors.ErrInvalidArgument
		}
		cursor = KeyParts{Int64Ordered(after.RecoveryAtMS), String(after.Namespace), String(after.ClientID), Uint64(after.SessionGeneration), String(after.Topic)}
	}
	rows, next, done, err := mqttSubscriptionTable.ScanIndex(ctx, s, 2, nil, cursor, limit)
	if err != nil {
		return nil, after, false, err
	}
	if len(next) > 0 {
		after = MQTTSubscriptionRecoveryCursor{RecoveryAtMS: next[0].I64, Namespace: next[1].S, ClientID: next[2].S, SessionGeneration: next[3].U64, Topic: next[4].S}
	}
	return rows, after, done, nil
}
