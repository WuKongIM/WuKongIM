package meta

import (
	"bytes"
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// MQTTInboxAdmission persists bounded future-person source preparation. The
// caller proves both UID registrations and each remote source before advancing.
// It is an auxiliary record in mqtt_source_binding System 1, not a consumer.
type MQTTInboxAdmission struct {
	ChannelID string `json:"channel_id"`
	// DirectoryGeneration binds the runtime incarnation. Zero is an internal
	// invalidation witness, retained across physical runtime deletion.
	DirectoryGeneration uint64 `json:"directory_generation"`
	// Revision never resets, including after invalidation or runtime recreation.
	Revision uint64 `json:"revision"`
	// Participant selects the decoded canonical UID (0 or 1); 2 means complete.
	Participant uint8                `json:"participant"`
	After       MQTTSourceBindingKey `json:"after"`
	UpdatedAtMS int64                `json:"updated_at_ms"`
}

// MQTTInboxAdmissionResult is meaningful only after its enclosing batch commits.
type MQTTInboxAdmissionResult struct {
	Status          MQTTSessionCASStatus
	CurrentRevision uint64
}

func mqttInboxParticipants(id string) ([2]string, error) {
	a, b, err := channelid.DecodePersonChannel(id)
	if err != nil || validateMQTTIdentity(a, 1024) != nil || validateMQTTIdentity(b, 1024) != nil || channelid.EncodePersonChannel(a, b) != id {
		return [2]string{}, dberrors.ErrInvalidArgument
	}
	return [2]string{a, b}, nil
}

// ValidateMQTTInboxAdmission accepts durable invalidations as well as live
// checkpoints. Only runtime deletion may write DirectoryGeneration zero.
func ValidateMQTTInboxAdmission(r MQTTInboxAdmission) error {
	uids, err := mqttInboxParticipants(r.ChannelID)
	if err != nil || r.Revision == 0 || r.UpdatedAtMS <= 0 || r.Participant > 2 {
		return dberrors.ErrInvalidArgument
	}
	if r.DirectoryGeneration == 0 && (r.Participant != 0 || r.After != (MQTTSourceBindingKey{})) {
		return dberrors.ErrInvalidArgument
	}
	if r.After != (MQTTSourceBindingKey{}) {
		if r.Participant == 2 || validateMQTTSourceBindingKey(r.After) != nil || r.After.Owner != (MQTTBindingOwner{Kind: MQTTBindingUID, ID: uids[r.Participant]}) {
			return dberrors.ErrInvalidArgument
		}
	}
	return nil
}

// CompareAndSwapMQTTInboxAdmission checks runtime incarnation and progress in
// the same Slot batch. Exact retry must still match the current incarnation.
func (b *Batch) CompareAndSwapMQTTInboxAdmission(slot HashSlot, expected uint64, row MQTTInboxAdmission) (*MQTTInboxAdmissionResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if expected == math.MaxUint64 || row.Revision != expected+1 || row.DirectoryGeneration == 0 || ValidateMQTTInboxAdmission(row) != nil {
		return nil, dberrors.ErrInvalidArgument
	}
	result := &MQTTInboxAdmissionResult{}
	b.addOp(slot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTInboxAdmissionResult{Status: MQTTSessionCASConflict}
		key := mqttInboxAdmissionKey(slot, row.ChannelID)
		old, found, err := loadMQTTInboxAdmission(state, key, row.ChannelID)
		if err != nil {
			return err
		}
		if found {
			result.CurrentRevision = old.Revision
		}
		runtimeKey := encodeChannelRuntimeMetaRowKey(slot, row.ChannelID, 1, channelRuntimeMetaPrimaryFamilyID)
		runtime, exists, err := state.loadRuntimeMeta(ctx, slot, runtimeKey, row.ChannelID, 1)
		if err != nil {
			return err
		}
		if !exists || runtime.DirectoryGeneration != row.DirectoryGeneration {
			return nil
		}
		if found {
			if old == row {
				result.Status = MQTTSessionCASUnchanged
				return nil
			}
			if old.Revision != expected || !validMQTTInboxAdmissionTransition(old, row) {
				return nil
			}
		} else if expected != 0 || row.Participant != 0 || row.After != (MQTTSourceBindingKey{}) {
			return nil
		}
		if err := stageMQTTInboxAdmission(state, batch, key, row); err != nil {
			return err
		}
		*result = MQTTInboxAdmissionResult{Status: MQTTSessionCASApplied, CurrentRevision: row.Revision}
		return nil
	})
	return result, nil
}

func validMQTTInboxAdmissionTransition(old, next MQTTInboxAdmission) bool {
	if next.UpdatedAtMS < old.UpdatedAtMS {
		return false
	}
	if old.DirectoryGeneration != next.DirectoryGeneration {
		return (old.DirectoryGeneration == 0 || next.DirectoryGeneration > old.DirectoryGeneration) && next.Participant == 0 && next.After == (MQTTSourceBindingKey{})
	}
	if old.Participant == 2 {
		return false
	}
	if next.Participant == old.Participant+1 {
		return next.After == (MQTTSourceBindingKey{})
	}
	if next.Participant != old.Participant || next.After == (MQTTSourceBindingKey{}) {
		return false
	}
	if old.After == (MQTTSourceBindingKey{}) {
		return true
	}
	a, _ := encodeKeyParts(nil, mqttSourceBindingPrimaryKey(old.After))
	z, _ := encodeKeyParts(nil, mqttSourceBindingPrimaryKey(next.After))
	return bytes.Compare(a, z) < 0
}

// invalidateMQTTInboxAdmission retains an ABA fence when a person runtime is
// removed. Repeated deletion leaves an already invalidated witness unchanged.
func invalidateMQTTInboxAdmission(state *batchCommitState, batch *engine.Batch, slot HashSlot, id string, typ int64) error {
	if typ != 1 {
		return nil
	}
	key := mqttInboxAdmissionKey(slot, id)
	old, found, err := loadMQTTInboxAdmission(state, key, id)
	if err != nil || !found || old.DirectoryGeneration == 0 {
		return err
	}
	if old.Revision == math.MaxUint64 {
		return dberrors.ErrInvalidArgument
	}
	old.Revision++
	old.DirectoryGeneration, old.Participant, old.After = 0, 0, MQTTSourceBindingKey{}
	return stageMQTTInboxAdmission(state, batch, key, old)
}

// CompareAndSwapMQTTInboxAdmission exposes the same fenced mutation to the FSM.
func (b *WriteBatch) CompareAndSwapMQTTInboxAdmission(slot uint16, expected uint64, row MQTTInboxAdmission) (*MQTTInboxAdmissionResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.CompareAndSwapMQTTInboxAdmission(HashSlot(slot), expected, row)
}
