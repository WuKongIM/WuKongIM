package meta

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// MQTTSourceKind identifies the durable log supplying a replay obligation.
type MQTTSourceKind uint8

const MQTTSourceChannel MQTTSourceKind = 1

// MQTTDeliveryCursorKey scopes progress to one subscription and source lifetime.
// SourceID encodes the complete Channel identity; SourceGeneration must survive
// leadership changes but change after destruction/recreation of that log.
type MQTTDeliveryCursorKey struct {
	Namespace              string         `json:"broker_namespace"`
	ClientID               string         `json:"client_id"`
	SessionGeneration      uint64         `json:"session_generation"`
	SubscriptionGeneration uint64         `json:"subscription_generation"`
	SourceKind             MQTTSourceKind `json:"source_kind"`
	SourceID               string         `json:"source_id"`
	SourceGeneration       string         `json:"source_generation"`
}

// MQTTDeliveryCursor separates source coverage, window admission and contiguous
// completion. Pending counters include qualifying backlog outside the window.
// Accounting alone never proves that replay content may be reclaimed.
type MQTTDeliveryCursor struct {
	Key                  MQTTDeliveryCursorKey `json:"key"`
	Topic                string                `json:"topic"`
	AuthorizationVersion uint64                `json:"authorization_version"`
	StartAfter           uint64                `json:"start_after"`
	AccountedThrough     uint64                `json:"accounted_through"`
	WindowThrough        uint64                `json:"window_through"`
	CompletedThrough     uint64                `json:"completed_through"`
	PendingMessages      uint64                `json:"pending_messages"`
	PendingBytes         uint64                `json:"pending_bytes"`
	// Revision and digest witness this exact request, including no-op coverage.
	Revision           uint64 `json:"revision"`
	LastMutationDigest string `json:"last_mutation_digest"`
	UpdatedAtMS        int64  `json:"updated_at_ms"`
	// Only window mutations maintain outstanding count/bytes and linked endpoints.
	InflightCount uint16 `json:"inflight_count"`
	InflightBytes uint64 `json:"inflight_bytes"`
	HeadPacketID  uint16 `json:"head_packet_id"`
	TailPacketID  uint16 `json:"tail_packet_id"`
	// The receipt survives ACK deletion so exact retries return the same exchange.
	LastWindowPacketID      uint16 `json:"last_window_packet_id"`
	LastWindowDeliveryOrder uint64 `json:"last_window_delivery_order"`
}

// MQTTDeliveryCursorOp identifies an atomic accounting operation.
type MQTTDeliveryCursorOp uint8

const (
	MQTTCursorInit    MQTTDeliveryCursorOp = 1
	MQTTCursorAccount MQTTDeliveryCursorOp = 2
)

// MQTTDeliveryCursorMutation carries authority and qualified source coverage.
// The caller proves coverage against protected committed records. The FSM only
// checks deterministic identities, progress, arithmetic and quota transitions.
type MQTTDeliveryCursorMutation struct {
	Key                  MQTTDeliveryCursorKey `json:"key"`
	ExpectedRevision     uint64                `json:"expected_revision"`
	OwnerGeneration      uint64                `json:"owner_generation"`
	OwnerNodeID          uint64                `json:"owner_node_id"`
	OwnerBootID          string                `json:"owner_boot_id"`
	ConnectionID         uint64                `json:"connection_id"`
	Op                   MQTTDeliveryCursorOp  `json:"op"`
	Topic                string                `json:"topic"`
	AuthorizationVersion uint64                `json:"authorization_version"`
	// Through is the protected initial boundary or newly accounted source tail.
	Through       uint64 `json:"through"`
	AddedMessages uint64 `json:"added_messages"`
	AddedBytes    uint64 `json:"added_bytes"`
	UpdatedAtMS   int64  `json:"updated_at_ms"`
}

// MQTTDeliveryCursorResult is meaningful only after its enclosing batch commits.
// Quota termination is a durable successful operation, not a rejected increment.
type MQTTDeliveryCursorResult struct {
	Status            MQTTSessionCASStatus
	CurrentRevision   uint64
	SessionState      MQTTSessionState
	TerminationReason MQTTSessionEndReason
}

func validateMQTTDeliveryCursorKey(k MQTTDeliveryCursorKey) error {
	if validateMQTTIdentity(k.Namespace, 1024) != nil || validateMQTTIdentity(k.ClientID, 1024) != nil ||
		k.SessionGeneration == 0 || k.SubscriptionGeneration == 0 || k.SourceKind != MQTTSourceChannel ||
		validateMQTTIdentity(k.SourceID, 4096) != nil || validateMQTTIdentity(k.SourceGeneration, 128) != nil {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ValidateMQTTDeliveryCursor checks storage invariants, not source completeness.
func ValidateMQTTDeliveryCursor(r MQTTDeliveryCursor) error {
	if validateMQTTDeliveryCursorKey(r.Key) != nil || validateMQTTIdentity(r.Topic, 2048) != nil || r.Revision == 0 || r.UpdatedAtMS <= 0 ||
		r.StartAfter > r.CompletedThrough || r.CompletedThrough > r.WindowThrough || r.WindowThrough > r.AccountedThrough ||
		r.PendingMessages > r.AccountedThrough-r.CompletedThrough || (r.PendingMessages == 0 && r.PendingBytes != 0) || len(r.LastMutationDigest) != 64 {
		return dberrors.ErrInvalidArgument
	}
	if r.InflightCount > MQTTMaxInflight || uint64(r.InflightCount) > r.PendingMessages || r.InflightBytes > r.PendingBytes ||
		r.PendingMessages-uint64(r.InflightCount) > r.AccountedThrough-r.WindowThrough ||
		(r.LastWindowPacketID == 0) != (r.LastWindowDeliveryOrder == 0) {
		return dberrors.ErrInvalidArgument
	}
	if r.InflightCount == 0 {
		if r.InflightBytes != 0 || r.HeadPacketID != 0 || r.TailPacketID != 0 || r.CompletedThrough != r.WindowThrough {
			return dberrors.ErrInvalidArgument
		}
	} else if r.HeadPacketID == 0 || r.TailPacketID == 0 || (r.InflightCount == 1) != (r.HeadPacketID == r.TailPacketID) ||
		r.CompletedThrough >= r.WindowThrough || uint64(r.InflightCount) > r.WindowThrough-r.CompletedThrough {
		return dberrors.ErrInvalidArgument
	}
	if _, err := hex.DecodeString(r.LastMutationDigest); err != nil {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ValidateMQTTDeliveryCursorMutation bounds the independent owner/row fences.
func ValidateMQTTDeliveryCursorMutation(m MQTTDeliveryCursorMutation) error {
	if validateMQTTDeliveryCursorKey(m.Key) != nil || validateMQTTIdentity(m.Topic, 2048) != nil ||
		m.ExpectedRevision == 0 || m.ExpectedRevision == math.MaxUint64 || m.OwnerGeneration == 0 || m.OwnerNodeID == 0 ||
		validateMQTTIdentity(m.OwnerBootID, 128) != nil || m.ConnectionID == 0 || m.UpdatedAtMS <= 0 {
		return dberrors.ErrInvalidArgument
	}
	switch m.Op {
	case MQTTCursorInit:
		if m.AddedMessages != 0 || m.AddedBytes != 0 {
			return dberrors.ErrInvalidArgument
		}
	case MQTTCursorAccount:
		if m.AddedMessages == 0 && m.AddedBytes != 0 {
			return dberrors.ErrInvalidArgument
		}
	default:
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// MutateMQTTDeliveryCursor atomically accounts one protected source range and its
// session aggregate. It neither admits packets nor advances completed progress.
// No caller may infer source protection or owner isolation from this primitive.
func (b *Batch) MutateMQTTDeliveryCursor(slot HashSlot, m MQTTDeliveryCursorMutation) (*MQTTDeliveryCursorResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if err := ValidateMQTTDeliveryCursorMutation(m); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	digest := hex.EncodeToString(sum[:])
	result := &MQTTDeliveryCursorResult{}
	b.addOp(slot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTDeliveryCursorResult{Status: MQTTSessionCASConflict}
		session, found, err := loadUpdateRow(mqttSessionTable, state, slot, mqttSessionPrimaryKey(m.Key.Namespace, m.Key.ClientID))
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		*result = mqttDeliveryCursorResult(MQTTSessionCASConflict, session)
		if session.OwnerGeneration != m.OwnerGeneration || session.OwnerNodeID != m.OwnerNodeID ||
			session.OwnerBootID != m.OwnerBootID || session.ConnectionID != m.ConnectionID || session.Generation != m.Key.SessionGeneration {
			return nil
		}
		row, exists, err := loadUpdateRow(mqttDeliveryCursorTable, state, slot, mqttDeliveryCursorPrimaryKey(m.Key))
		if err != nil {
			return err
		}
		if exists && row.Revision == m.ExpectedRevision+1 && session.Revision == row.Revision && row.LastMutationDigest == digest {
			result.Status = MQTTSessionCASUnchanged
			return nil
		}
		if session.Revision != m.ExpectedRevision || session.State == MQTTSessionEnded {
			return nil
		}
		sub, found, err := loadUpdateRow(mqttSubscriptionTable, state, slot, mqttSubscriptionPrimaryKey(m.Key.Namespace, m.Key.ClientID, m.Key.SessionGeneration, m.Topic))
		if err != nil {
			return err
		}
		if !found || sub.Generation != m.Key.SubscriptionGeneration || sub.AuthorizationVersion != m.AuthorizationVersion ||
			(sub.Stage != MQTTSubscriptionPreparing && sub.Stage != MQTTSubscriptionActive) {
			return nil
		}
		previousSession := session
		switch m.Op {
		case MQTTCursorInit:
			if exists {
				return nil
			}
			row = MQTTDeliveryCursor{Key: m.Key, Topic: m.Topic, AuthorizationVersion: m.AuthorizationVersion,
				StartAfter: m.Through, AccountedThrough: m.Through, WindowThrough: m.Through, CompletedThrough: m.Through}
		case MQTTCursorAccount:
			if !exists || row.Topic != m.Topic || row.AuthorizationVersion != m.AuthorizationVersion || m.Through <= row.AccountedThrough ||
				m.AddedMessages > m.Through-row.AccountedThrough || m.AddedMessages > math.MaxUint64-row.PendingMessages ||
				m.AddedBytes > math.MaxUint64-row.PendingBytes || m.AddedMessages > math.MaxUint64-session.PendingMessages ||
				m.AddedBytes > math.MaxUint64-session.PendingBytes {
				return nil
			}
			row.AccountedThrough = m.Through
			row.PendingMessages += m.AddedMessages
			row.PendingBytes += m.AddedBytes
			session.PendingMessages += m.AddedMessages
			session.PendingBytes += m.AddedBytes
			if session.PendingMessages > session.QuotaMessages || session.PendingBytes > session.QuotaBytes {
				session.State, session.TerminationReason = MQTTSessionEnded, MQTTSessionQuota
				session.LeaseUntilMS, session.OfflineExpiresAtMS = 0, 0
			}
		}
		session.Revision++
		session.UpdatedAtMS = m.UpdatedAtMS
		var resolvedWill *MQTTWill
		if session.State == MQTTSessionEnded && previousSession.WillGeneration != 0 {
			live, err := loadMQTTReferencedWill(state, slot, previousSession)
			if err != nil {
				return err
			}
			var ok bool
			resolvedWill, ok = resolveMQTTLifecycleWill(previousSession, session, MQTTLifecycleEnd, live)
			if !ok {
				return nil
			}
			session.WillGeneration = 0
		}
		row.Revision, row.LastMutationDigest, row.UpdatedAtMS = session.Revision, digest, m.UpdatedAtMS
		if resolvedWill != nil {
			if err := stageUpdateRow(mqttWillTable, state, batch, slot, *resolvedWill); err != nil {
				return err
			}
		}
		if err := stageUpdateRow(mqttDeliveryCursorTable, state, batch, slot, row); err != nil {
			return err
		}
		if err := stageUpdateRow(mqttSessionTable, state, batch, slot, session); err != nil {
			return err
		}
		*result = mqttDeliveryCursorResult(MQTTSessionCASApplied, session)
		return nil
	})
	return result, nil
}

func mqttDeliveryCursorResult(status MQTTSessionCASStatus, session MQTTSession) MQTTDeliveryCursorResult {
	return MQTTDeliveryCursorResult{Status: status, CurrentRevision: session.Revision, SessionState: session.State, TerminationReason: session.TerminationReason}
}

func mqttDeliveryCursorPrimaryKey(k MQTTDeliveryCursorKey) KeyParts {
	return KeyParts{String(k.Namespace), String(k.ClientID), Uint64(k.SessionGeneration), Uint64(k.SubscriptionGeneration), Uint8(uint8(k.SourceKind)), String(k.SourceID), String(k.SourceGeneration)}
}

// GetMQTTDeliveryCursor reads node storage; absence is not proof of empty backlog.
func (s *Shard) GetMQTTDeliveryCursor(ctx context.Context, key MQTTDeliveryCursorKey) (MQTTDeliveryCursor, bool, error) {
	if err := validateMQTTDeliveryCursorKey(key); err != nil {
		return MQTTDeliveryCursor{}, false, err
	}
	return mqttDeliveryCursorTable.Get(ctx, s, mqttDeliveryCursorPrimaryKey(key))
}

// ListMQTTDeliveryCursors pages one session generation; subscriptionGeneration
// zero includes every subscription. The complete cursor prevents tied source
// identities or incarnations from being skipped. These are node-storage reads.
func (s *Shard) ListMQTTDeliveryCursors(ctx context.Context, namespace, clientID string, sessionGeneration, subscriptionGeneration uint64, after MQTTDeliveryCursorKey, limit int) ([]MQTTDeliveryCursor, MQTTDeliveryCursorKey, bool, error) {
	if validateMQTTIdentity(namespace, 1024) != nil || validateMQTTIdentity(clientID, 1024) != nil || sessionGeneration == 0 || limit < 1 || limit > 256 {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	prefix := KeyParts{String(namespace), String(clientID), Uint64(sessionGeneration)}
	if subscriptionGeneration != 0 {
		prefix = append(prefix, Uint64(subscriptionGeneration))
	}
	var cursor KeyParts
	if after != (MQTTDeliveryCursorKey{}) {
		if validateMQTTDeliveryCursorKey(after) != nil || after.Namespace != namespace || after.ClientID != clientID ||
			after.SessionGeneration != sessionGeneration || (subscriptionGeneration != 0 && after.SubscriptionGeneration != subscriptionGeneration) {
			return nil, after, false, dberrors.ErrInvalidArgument
		}
		cursor = mqttDeliveryCursorPrimaryKey(after)
	}
	rows, next, done, err := mqttDeliveryCursorTable.scanPrimaryPrefixStrict(ctx, s, prefix, cursor, limit)
	if err != nil {
		return nil, after, false, err
	}
	if len(next) > 0 {
		after = mqttDeliveryCursorKeyFromParts(next)
	}
	return rows, after, done, nil
}

func mqttDeliveryCursorKeyFromParts(pk KeyParts) MQTTDeliveryCursorKey {
	return MQTTDeliveryCursorKey{Namespace: pk[0].S, ClientID: pk[1].S, SessionGeneration: pk[2].U64, SubscriptionGeneration: pk[3].U64,
		SourceKind: MQTTSourceKind(pk[4].U8), SourceID: pk[5].S, SourceGeneration: pk[6].S}
}
