package meta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// MQTTWillKey identifies an obligation independently of the current Session.
type MQTTWillKey struct {
	Namespace         string `json:"broker_namespace"`
	ClientID          string `json:"client_id"`
	SessionGeneration uint64 `json:"session_generation"`
	// WillGeneration is allocated by the Session lifecycle, never by a client.
	WillGeneration uint64 `json:"will_generation"`
}

// MQTTWillStage separates cancellable configuration from committed publication work.
type MQTTWillStage uint8

const (
	MQTTWillArmed     MQTTWillStage = 1
	MQTTWillWaiting   MQTTWillStage = 2
	MQTTWillReady     MQTTWillStage = 3
	MQTTWillExecuting MQTTWillStage = 4
	MQTTWillPublished MQTTWillStage = 5
	MQTTWillCancelled MQTTWillStage = 6
	MQTTWillRejected  MQTTWillStage = 7
)

// MQTTWillCancelReason records the authoritative Session decision that cancelled it.
type MQTTWillCancelReason uint8

const (
	MQTTWillNormalDisconnect MQTTWillCancelReason = 1
	MQTTWillSessionResumed   MQTTWillCancelReason = 2
)

// MQTTWillRejectReason records permanent publication failure, never transient IO.
type MQTTWillRejectReason uint8

const (
	MQTTWillPermissionRevoked  MQTTWillRejectReason = 1
	MQTTWillTargetDeleted      MQTTWillRejectReason = 2
	MQTTWillPublicationInvalid MQTTWillRejectReason = 3
)

// MQTTWill owns bounded publication content and a durable execution receipt.
// Origin owner fields never grant authority to a stale connection. Session
// lifecycle decisions and current publication authorization remain caller proofs.
type MQTTWill struct {
	Key             MQTTWillKey `json:"key"`
	UID             string      `json:"uid"`
	OwnerGeneration uint64      `json:"owner_generation"`
	OwnerNodeID     uint64      `json:"owner_node_id"`
	OwnerBootID     string      `json:"owner_boot_id"`
	ConnectionID    uint64      `json:"connection_id"`
	Revision        uint64      `json:"revision"`
	// DecisionRevision witnesses the Session decision; it is not an execution lease.
	DecisionRevision uint64 `json:"decision_revision"`
	Topic            string `json:"topic"`
	TargetID         string `json:"target_id"`
	TargetType       uint8  `json:"target_type"`
	Payload          []byte `json:"payload"`
	// PublicationMetadata is a bounded versioned opaque contract, not a wire packet.
	// The publication adapter validates its contents; Will Delay is stored separately.
	PublicationMetadata []byte `json:"publication_metadata"`
	DelaySeconds        uint32 `json:"delay_seconds"`
	QoS                 uint8  `json:"qos"`
	// ClientMsgNo remains client metadata and cannot choose the server retry identity.
	ClientMsgNo string `json:"client_msg_no"`
	// IdempotencyKey must be used in the server Will domain, not the client key domain.
	IdempotencyKey   string        `json:"idempotency_key"`
	Stage            MQTTWillStage `json:"stage"`
	DisconnectedAtMS int64         `json:"disconnected_at_ms"`
	DueAtMS          int64         `json:"due_at_ms"`
	// ExecutionGeneration fences stale workers after lease reclamation.
	ExecutionGeneration uint64               `json:"execution_generation"`
	ExecutorNodeID      uint64               `json:"executor_node_id"`
	ExecutorBootID      string               `json:"executor_boot_id"`
	LeaseUntilMS        int64                `json:"lease_until_ms"`
	CancelReason        MQTTWillCancelReason `json:"cancel_reason"`
	RejectReason        MQTTWillRejectReason `json:"reject_reason"`
	MessageID           uint64               `json:"message_id"`
	MessageSeq          uint64               `json:"message_seq"`
	// PublishedAtMS is the committed publication time, not the Will's configuration time.
	PublishedAtMS int64 `json:"published_at_ms"`
	UpdatedAtMS   int64 `json:"updated_at_ms"`
}

// MQTTWillResult is valid only after the enclosing batch commits successfully.
type MQTTWillResult struct {
	Status          MQTTSessionCASStatus
	CurrentRevision uint64
}

func validateMQTTWillKey(k MQTTWillKey) error {
	if validateMQTTIdentity(k.Namespace, 1024) != nil || validateMQTTIdentity(k.ClientID, 1024) != nil || k.SessionGeneration == 0 || k.WillGeneration == 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// MQTTWillIdempotencyKey freezes a versioned canonical key identity. Callers must
// keep this identity in a server-owned idempotency domain when publishing.
func MQTTWillIdempotencyKey(k MQTTWillKey) (string, error) {
	if err := validateMQTTWillKey(k); err != nil {
		return "", err
	}
	data, _ := json.Marshal(struct {
		Version           uint8  `json:"version"`
		Namespace         string `json:"broker_namespace"`
		ClientID          string `json:"client_id"`
		SessionGeneration uint64 `json:"session_generation"`
		// WillGeneration is allocated by the Session lifecycle, never by a client.
		WillGeneration uint64 `json:"will_generation"`
	}{1, k.Namespace, k.ClientID, k.SessionGeneration, k.WillGeneration})
	digest := sha256.Sum256(data)
	return "mqtt-will-v1:" + hex.EncodeToString(digest[:]), nil
}

// ValidateMQTTWill checks storage shape; it cannot authenticate a remote Session
// decision, infer elapsed time, or authorize a publication from a local row.
func ValidateMQTTWill(r MQTTWill) error {
	id, err := MQTTWillIdempotencyKey(r.Key)
	if err != nil || r.IdempotencyKey != id || validateMQTTIdentity(r.UID, 1024) != nil || validateMQTTIdentity(r.OwnerBootID, 128) != nil || validateMQTTIdentity(r.Topic, 2048) != nil || validateMQTTIdentity(r.TargetID, 1024) != nil || validateMQTTIdentity(r.ClientMsgNo, 1024) != nil {
		return dberrors.ErrInvalidArgument
	}
	if r.OwnerGeneration == 0 || r.OwnerNodeID == 0 || r.ConnectionID == 0 || r.Revision == 0 || r.DecisionRevision == 0 || r.UpdatedAtMS <= 0 || r.QoS > 1 || (r.TargetType != 1 && r.TargetType != 2) || len(r.Payload) > 65535 || len(r.PublicationMetadata) > 32<<10 || len(r.PublicationMetadata) > 0 && r.PublicationMetadata[0] != 1 {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage < MQTTWillArmed || r.Stage > MQTTWillRejected || r.DisconnectedAtMS < 0 || r.DueAtMS < 0 || r.LeaseUntilMS < 0 {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage == MQTTWillArmed {
		if r.DisconnectedAtMS != 0 || r.DueAtMS != 0 {
			return dberrors.ErrInvalidArgument
		}
	} else if r.Stage == MQTTWillCancelled && r.DisconnectedAtMS == 0 {
		if r.DueAtMS != 0 {
			return dberrors.ErrInvalidArgument
		}
	} else if r.DisconnectedAtMS <= 0 || r.DueAtMS < r.DisconnectedAtMS || r.UpdatedAtMS < r.DisconnectedAtMS {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage == MQTTWillReady && r.UpdatedAtMS < r.DueAtMS {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage == MQTTWillWaiting && (r.DelaySeconds == 0 || r.DueAtMS <= r.DisconnectedAtMS) {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage == MQTTWillCancelled {
		if r.CancelReason < MQTTWillNormalDisconnect || r.CancelReason > MQTTWillSessionResumed || r.CancelReason == MQTTWillSessionResumed && r.DelaySeconds == 0 {
			return dberrors.ErrInvalidArgument
		}
	} else if r.CancelReason != 0 {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage == MQTTWillRejected {
		if r.RejectReason < MQTTWillPermissionRevoked || r.RejectReason > MQTTWillPublicationInvalid {
			return dberrors.ErrInvalidArgument
		}
	} else if r.RejectReason != 0 {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage == MQTTWillExecuting || r.Stage == MQTTWillPublished || r.Stage == MQTTWillRejected {
		if r.ExecutionGeneration == 0 || r.ExecutorNodeID == 0 || validateMQTTIdentity(r.ExecutorBootID, 128) != nil || r.UpdatedAtMS < r.DueAtMS {
			return dberrors.ErrInvalidArgument
		}
	} else if r.ExecutionGeneration != 0 || r.ExecutorNodeID != 0 || r.ExecutorBootID != "" {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage == MQTTWillExecuting {
		if r.LeaseUntilMS <= r.UpdatedAtMS {
			return dberrors.ErrInvalidArgument
		}
	} else if r.LeaseUntilMS != 0 {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage == MQTTWillPublished {
		if r.MessageID == 0 || r.MessageSeq == 0 || r.PublishedAtMS <= 0 {
			return dberrors.ErrInvalidArgument
		}
	} else if r.MessageID != 0 || r.MessageSeq != 0 || r.PublishedAtMS != 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// CompareAndSwapMQTTWill persists one bounded obligation or execution transition.
// It is not an atomic Session lifecycle operation. Product code must coordinate
// old-Will resolution, new configuration and Session state in one Slot command.
func (b *Batch) CompareAndSwapMQTTWill(slot HashSlot, expected uint64, row MQTTWill) (*MQTTWillResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if expected == math.MaxUint64 || row.Revision != expected+1 {
		return nil, dberrors.ErrInvalidArgument
	}
	if err := ValidateMQTTWill(row); err != nil {
		return nil, err
	}
	row.Payload = bytes.Clone(row.Payload)
	row.PublicationMetadata = bytes.Clone(row.PublicationMetadata)
	result := &MQTTWillResult{}
	b.addOp(slot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTWillResult{Status: MQTTSessionCASConflict}
		old, found, err := loadUpdateRow(mqttWillTable, state, slot, mqttWillPrimaryKey(row.Key))
		if err != nil {
			return err
		}
		if found {
			result.CurrentRevision = old.Revision
			if equalMQTTWill(old, row) {
				result.Status = MQTTSessionCASUnchanged
				return nil
			}
			if old.Revision != expected || !validMQTTWillTransition(old, row) {
				return nil
			}
		} else if expected != 0 || row.Stage != MQTTWillArmed {
			return nil
		}
		session, sessionFound, err := loadUpdateRow(mqttSessionTable, state, slot, mqttSessionPrimaryKey(row.Key.Namespace, row.Key.ClientID))
		if err != nil {
			return err
		}
		if sessionFound && session.Generation == row.Key.SessionGeneration && session.WillGeneration == row.Key.WillGeneration {
			return nil
		}
		if err := stageUpdateRow(mqttWillTable, state, batch, slot, row); err != nil {
			return err
		}
		*result = MQTTWillResult{Status: MQTTSessionCASApplied, CurrentRevision: row.Revision}
		return nil
	})
	return result, nil
}

func sameMQTTWillPublication(a, b MQTTWill) bool {
	return a.Key == b.Key && a.UID == b.UID &&
		a.OwnerGeneration == b.OwnerGeneration && a.OwnerNodeID == b.OwnerNodeID &&
		a.OwnerBootID == b.OwnerBootID && a.ConnectionID == b.ConnectionID &&
		a.Topic == b.Topic && a.TargetID == b.TargetID && a.TargetType == b.TargetType &&
		a.DelaySeconds == b.DelaySeconds && a.QoS == b.QoS &&
		a.ClientMsgNo == b.ClientMsgNo && a.IdempotencyKey == b.IdempotencyKey &&
		bytes.Equal(a.Payload, b.Payload) && bytes.Equal(a.PublicationMetadata, b.PublicationMetadata)
}
func sameMQTTWillExecutor(a, b MQTTWill) bool {
	return a.ExecutionGeneration == b.ExecutionGeneration &&
		a.ExecutorNodeID == b.ExecutorNodeID && a.ExecutorBootID == b.ExecutorBootID
}
func equalMQTTWill(a, b MQTTWill) bool {
	return sameMQTTWillPublication(a, b) && a.Revision == b.Revision &&
		a.DecisionRevision == b.DecisionRevision && a.Stage == b.Stage &&
		a.DisconnectedAtMS == b.DisconnectedAtMS && a.DueAtMS == b.DueAtMS &&
		sameMQTTWillExecutor(a, b) && a.LeaseUntilMS == b.LeaseUntilMS &&
		a.CancelReason == b.CancelReason && a.RejectReason == b.RejectReason &&
		a.MessageID == b.MessageID && a.MessageSeq == b.MessageSeq &&
		a.PublishedAtMS == b.PublishedAtMS && a.UpdatedAtMS == b.UpdatedAtMS
}

// validMQTTWillTransition fences decisions and executors without consulting a
// wall clock. The authority supplies the persisted decision/update times.
func validMQTTWillTransition(old, next MQTTWill) bool {
	if !sameMQTTWillPublication(old, next) || next.DecisionRevision < old.DecisionRevision || next.UpdatedAtMS < old.UpdatedAtMS {
		return false
	}
	switch old.Stage {
	case MQTTWillArmed:
		if next.DecisionRevision <= old.DecisionRevision {
			return false
		}
		if next.Stage == MQTTWillCancelled {
			return next.DisconnectedAtMS == 0
		}
		if next.Stage != MQTTWillWaiting && next.Stage != MQTTWillReady {
			return false
		}
		delay := int64(next.DelaySeconds) * 1000
		if next.DisconnectedAtMS > math.MaxInt64-delay || next.DueAtMS > next.DisconnectedAtMS+delay {
			return false
		}
		return next.Stage != MQTTWillReady || next.UpdatedAtMS >= next.DueAtMS
	case MQTTWillWaiting:
		if next.DecisionRevision <= old.DecisionRevision || next.DisconnectedAtMS != old.DisconnectedAtMS {
			return false
		}
		if next.Stage == MQTTWillCancelled {
			return next.CancelReason == MQTTWillSessionResumed && next.DueAtMS == old.DueAtMS && next.UpdatedAtMS < old.DueAtMS
		}
		return next.Stage == MQTTWillReady && next.DueAtMS <= old.DueAtMS && next.UpdatedAtMS >= next.DueAtMS
	case MQTTWillReady:
		return next.Stage == MQTTWillExecuting && next.ExecutionGeneration == 1 && next.DecisionRevision == old.DecisionRevision && next.DisconnectedAtMS == old.DisconnectedAtMS && next.DueAtMS == old.DueAtMS
	case MQTTWillExecuting:
		if next.DecisionRevision != old.DecisionRevision || next.DisconnectedAtMS != old.DisconnectedAtMS || next.DueAtMS != old.DueAtMS {
			return false
		}
		if next.Stage == MQTTWillExecuting {
			if sameMQTTWillExecutor(old, next) {
				return next.UpdatedAtMS < old.LeaseUntilMS && next.LeaseUntilMS >= old.LeaseUntilMS
			}
			return old.ExecutionGeneration != math.MaxUint64 && next.ExecutionGeneration == old.ExecutionGeneration+1 && next.UpdatedAtMS >= old.LeaseUntilMS
		}
		return (next.Stage == MQTTWillPublished || next.Stage == MQTTWillRejected) && sameMQTTWillExecutor(old, next) && next.UpdatedAtMS < old.LeaseUntilMS
	default:
		return false
	}
}

func mqttWillPrimaryKey(k MQTTWillKey) KeyParts {
	return KeyParts{String(k.Namespace), String(k.ClientID), Uint64(k.SessionGeneration), Uint64(k.WillGeneration)}
}
func mqttWillKeyFromParts(p KeyParts) MQTTWillKey {
	return MQTTWillKey{Namespace: p[0].S, ClientID: p[1].S, SessionGeneration: p[2].U64, WillGeneration: p[3].U64}
}
func mqttWillRecoveryAt(r MQTTWill) int64 {
	switch r.Stage {
	case MQTTWillWaiting, MQTTWillReady:
		return r.DueAtMS
	case MQTTWillExecuting:
		return r.LeaseUntilMS
	default:
		return 0
	}
}

// GetMQTTWill returns owned content after the caller establishes Slot authority.
func (s *Shard) GetMQTTWill(ctx context.Context, key MQTTWillKey) (MQTTWill, bool, error) {
	if err := validateMQTTWillKey(key); err != nil {
		return MQTTWill{}, false, err
	}
	return mqttWillTable.Get(ctx, s, mqttWillPrimaryKey(key))
}

// MQTTWillRecoveryCursor includes the whole key so equal deadlines page safely.
type MQTTWillRecoveryCursor struct {
	RecoveryAtMS int64
	Key          MQTTWillKey
}

// ListMQTTWillRecovery pages execution candidates, never authority to publish.
func (s *Shard) ListMQTTWillRecovery(ctx context.Context, after MQTTWillRecoveryCursor, limit int) ([]MQTTWill, MQTTWillRecoveryCursor, bool, error) {
	if limit < 1 || limit > 256 {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	var cursor KeyParts
	if after != (MQTTWillRecoveryCursor{}) {
		if after.RecoveryAtMS <= 0 || validateMQTTWillKey(after.Key) != nil {
			return nil, after, false, dberrors.ErrInvalidArgument
		}
		cursor = append(KeyParts{Int64Ordered(after.RecoveryAtMS)}, mqttWillPrimaryKey(after.Key)...)
	}
	rows, next, done, err := mqttWillTable.ScanIndex(ctx, s, 2, nil, cursor, limit)
	if err != nil {
		return nil, after, false, err
	}
	if len(next) > 0 {
		after = MQTTWillRecoveryCursor{RecoveryAtMS: next[0].I64, Key: mqttWillKeyFromParts(next[1:])}
	}
	return rows, after, done, nil
}
