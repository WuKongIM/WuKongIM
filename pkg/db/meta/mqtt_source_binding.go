package meta

import (
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// MQTTBindingOwnerKind distinguishes concrete message sources from inbox discovery.
type MQTTBindingOwnerKind uint8

const (
	MQTTBindingChannel MQTTBindingOwnerKind = 1
	MQTTBindingUID     MQTTBindingOwnerKind = 2
)

// MQTTBindingOwner identifies the source/UID authority, independently of Session
// ownership. Generation is a durable source incarnation, never a leader epoch.
type MQTTBindingOwner struct {
	Kind       MQTTBindingOwnerKind `json:"kind"`
	ID         string               `json:"id"`
	Generation string               `json:"generation"`
}

// MQTTSourceBindingKey keeps each subscription lifetime separately fenceable.
type MQTTSourceBindingKey struct {
	Owner                  MQTTBindingOwner `json:"owner"`
	Namespace              string           `json:"broker_namespace"`
	ClientID               string           `json:"client_id"`
	SessionGeneration      uint64           `json:"session_generation"`
	SubscriptionGeneration uint64           `json:"subscription_generation"`
}

// MQTTBindingStage is a recoverable, monotonic source-projection lifecycle.
type MQTTBindingStage uint8

const (
	MQTTBindingPreparing MQTTBindingStage = 1
	MQTTBindingActive    MQTTBindingStage = 2
	MQTTBindingRemoving  MQTTBindingStage = 3
	MQTTBindingRemoved   MQTTBindingStage = 4
)

// MQTTBindingReleaseReason explains why a source obligation may be released.
type MQTTBindingReleaseReason uint8

const (
	MQTTBindingDrained      MQTTBindingReleaseReason = 1
	MQTTBindingSessionEnded MQTTBindingReleaseReason = 2
)

// MQTTSourceBinding projects one authoritative subscription onto a source Slot.
// It contains references and conservative progress, never tokens or bodies.
// Its proof revisions are assertions by the caller; storage cannot authenticate
// remote authorities or establish source protection by persisting this row.
type MQTTSourceBinding struct {
	Key   MQTTSourceBindingKey `json:"key"`
	UID   string               `json:"uid"`
	Topic string               `json:"topic"`
	// Revision is the source-owned CAS version; source operations use it as a fence.
	Revision uint64 `json:"revision"`
	// IntentRevision fences delayed projections from the Session subscription.
	IntentRevision uint64 `json:"intent_revision"`
	// ProgressRevision witnesses committed cursor progress or Session termination.
	ProgressRevision     uint64           `json:"progress_revision"`
	AuthorizationVersion uint64           `json:"authorization_version"`
	OperationID          string           `json:"operation_id"`
	Stage                MQTTBindingStage `json:"stage"`
	// BoundaryKnown distinguishes a protected empty source from an unprotected one.
	BoundaryKnown    bool   `json:"boundary_known"`
	StartAfter       uint64 `json:"start_after"`
	CompletedThrough uint64 `json:"completed_through"`
	// EndKnown seals a normal removal boundary exactly once.
	EndKnown      bool                     `json:"end_known"`
	EndThrough    uint64                   `json:"end_through"`
	ReleaseReason MQTTBindingReleaseReason `json:"release_reason"`
	// Discovery uses native directory ID/type bounds and stable UID primary order.
	DiscoveryAfterChannelID   string `json:"discovery_after_channel_id"`
	DiscoveryAfterChannelType uint8  `json:"discovery_after_channel_type"`
	DiscoveryDone             bool   `json:"discovery_done"`
	// Drain version 1 retains UID-only removal progress in the closed Session's
	// cursor prefix. It cannot reset initial discovery or prove source release.
	DrainVersion               uint8  `json:"drain_version,omitempty"`
	DrainAfterSourceID         string `json:"drain_after_source_id,omitempty"`
	DrainAfterSourceGeneration string `json:"drain_after_source_generation,omitempty"`
	DrainDone                  bool   `json:"drain_done,omitempty"`
	// RecoveryAtMS also schedules active projections for bounded reconciliation.
	RecoveryAtMS int64 `json:"recovery_at_ms"`
	UpdatedAtMS  int64 `json:"updated_at_ms"`
	// ProtectionRevision acknowledges a replicated source install/release request.
	ProtectionRevision uint64 `json:"protection_revision"`
}

// MQTTSourceBindingResult becomes meaningful only after the batch commits.
type MQTTSourceBindingResult struct {
	Status          MQTTSessionCASStatus
	CurrentRevision uint64
}

func validateMQTTBindingOwner(o MQTTBindingOwner) error {
	switch o.Kind {
	case MQTTBindingChannel:
		if validateMQTTIdentity(o.ID, 4096) != nil || validateMQTTIdentity(o.Generation, 128) != nil {
			return dberrors.ErrInvalidArgument
		}
	case MQTTBindingUID:
		if validateMQTTIdentity(o.ID, 1024) != nil || o.Generation != "" {
			return dberrors.ErrInvalidArgument
		}
	default:
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ValidateMQTTSourceBindingKey checks one binding key shape for command codecs.
// ValidateMQTTBindingOwner exposes owner shape checks to command codecs.
func ValidateMQTTBindingOwner(o MQTTBindingOwner) error { return validateMQTTBindingOwner(o) }

func ValidateMQTTSourceBindingKey(k MQTTSourceBindingKey) error {
	return validateMQTTSourceBindingKey(k)
}

func validateMQTTSourceBindingKey(k MQTTSourceBindingKey) error {
	if validateMQTTBindingOwner(k.Owner) != nil || validateMQTTIdentity(k.Namespace, 1024) != nil || validateMQTTIdentity(k.ClientID, 1024) != nil || k.SessionGeneration == 0 || k.SubscriptionGeneration == 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ValidateMQTTSourceBinding checks deterministic row shape, without inferring
// permission, source coverage, or freshness from the absence of local state.
func ValidateMQTTSourceBinding(r MQTTSourceBinding) error {
	if validateMQTTSourceBindingKey(r.Key) != nil || validateMQTTIdentity(r.UID, 1024) != nil || validateMQTTIdentity(r.Topic, 2048) != nil || validateMQTTIdentity(r.OperationID, 128) != nil || r.Revision == 0 || r.IntentRevision == 0 || r.UpdatedAtMS <= 0 || r.ProtectionRevision > r.Revision {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage < MQTTBindingPreparing || r.Stage > MQTTBindingRemoved || (r.Stage == MQTTBindingRemoved && r.RecoveryAtMS != 0) || (r.Stage != MQTTBindingRemoved && r.RecoveryAtMS <= 0) {
		return dberrors.ErrInvalidArgument
	}
	if r.ReleaseReason > MQTTBindingSessionEnded || (r.Stage < MQTTBindingRemoving && r.ReleaseReason != 0) || (r.ReleaseReason == MQTTBindingDrained && r.Stage != MQTTBindingRemoved) || (r.ReleaseReason == MQTTBindingSessionEnded && r.ProgressRevision == 0) || (r.Stage == MQTTBindingRemoved && r.ReleaseReason == 0) {
		return dberrors.ErrInvalidArgument
	}
	if err := validateMQTTInboxDrain(r); err != nil {
		return err
	}
	if r.Key.Owner.Kind == MQTTBindingUID {
		if r.UID != r.Key.Owner.ID || r.BoundaryKnown || r.StartAfter != 0 || r.CompletedThrough != 0 || r.EndKnown || r.EndThrough != 0 || r.ProtectionRevision != 0 || (r.Stage == MQTTBindingActive && !r.DiscoveryDone) {
			return dberrors.ErrInvalidArgument
		}
		if r.DiscoveryAfterChannelID == "" {
			if r.DiscoveryAfterChannelType != 0 {
				return dberrors.ErrInvalidArgument
			}
		} else if validateMQTTDirectoryKey(ChannelKey{ChannelID: r.DiscoveryAfterChannelID, ChannelType: int64(r.DiscoveryAfterChannelType)}) != nil {
			return dberrors.ErrInvalidArgument
		}
		return nil
	}
	if r.DiscoveryAfterChannelID != "" || r.DiscoveryAfterChannelType != 0 || r.DiscoveryDone {
		return dberrors.ErrInvalidArgument
	}
	if !r.BoundaryKnown {
		if r.StartAfter != 0 || r.CompletedThrough != 0 || r.EndKnown || r.EndThrough != 0 {
			return dberrors.ErrInvalidArgument
		}
	} else if r.CompletedThrough < r.StartAfter || r.ProtectionRevision == 0 {
		return dberrors.ErrInvalidArgument
	}
	if !r.EndKnown && r.EndThrough != 0 || r.EndKnown && (r.Stage < MQTTBindingRemoving || r.EndThrough < r.CompletedThrough) {
		return dberrors.ErrInvalidArgument
	}
	if r.Stage == MQTTBindingActive && (!r.BoundaryKnown || r.ProgressRevision == 0) {
		return dberrors.ErrInvalidArgument
	}
	if r.ReleaseReason == MQTTBindingDrained && (!r.EndKnown || r.CompletedThrough != r.EndThrough || r.ProgressRevision == 0) {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// CompareAndSwapMQTTSourceBinding applies one source-owned transition. It does
// not read a Session row in this Slot; the caller supplies remote authority proof.
// Removed tombstones are retained so delayed prepares cannot resurrect a binding.
func (b *Batch) CompareAndSwapMQTTSourceBinding(slot HashSlot, expected uint64, row MQTTSourceBinding) (*MQTTSourceBindingResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if expected == math.MaxUint64 || row.Revision != expected+1 {
		return nil, dberrors.ErrInvalidArgument
	}
	if err := ValidateMQTTSourceBinding(row); err != nil {
		return nil, err
	}
	result := &MQTTSourceBindingResult{}
	b.addOp(slot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTSourceBindingResult{Status: MQTTSessionCASConflict}
		old, found, err := loadUpdateRow(mqttSourceBindingTable, state, slot, mqttSourceBindingPrimaryKey(row.Key))
		if err != nil {
			return err
		}
		if found {
			result.CurrentRevision = old.Revision
			if old == row {
				result.Status = MQTTSessionCASUnchanged
				return nil
			}
			if old.Revision != expected || !validMQTTSourceBindingTransition(old, row) {
				return nil
			}
		} else {
			if expected != 0 {
				return nil
			}
			// A retired lifetime stays fenced after its tombstone row is deleted.
			fence, err := loadMQTTBindingFence(state, slot, row.Key)
			if err != nil {
				return err
			}
			if row.Key.SessionGeneration <= fence {
				return nil
			}
			switch row.Stage {
			case MQTTBindingPreparing:
				if row.BoundaryKnown || row.ProgressRevision != 0 || row.ProtectionRevision != 0 || row.DiscoveryAfterChannelID != "" || row.DiscoveryDone {
					return nil
				}
			case MQTTBindingRemoved:
				if row.ReleaseReason != MQTTBindingSessionEnded || row.BoundaryKnown || row.ProtectionRevision != 0 || row.DiscoveryAfterChannelID != "" || row.DiscoveryDone {
					return nil
				}
			default:
				return nil
			}
		}
		if err := stageUpdateRow(mqttSourceBindingTable, state, batch, slot, row); err != nil {
			return err
		}
		*result = MQTTSourceBindingResult{Status: MQTTSessionCASApplied, CurrentRevision: row.Revision}
		return nil
	})
	return result, nil
}

func validMQTTSourceBindingTransition(old, next MQTTSourceBinding) bool {
	if old.Stage == MQTTBindingRemoved || next.Stage < old.Stage || next.Stage > old.Stage+1 && !(old.Stage == MQTTBindingPreparing && next.Stage == MQTTBindingRemoving) {
		return false
	}
	if old.UID != next.UID || old.Topic != next.Topic || old.AuthorizationVersion != next.AuthorizationVersion || old.OperationID != next.OperationID || next.IntentRevision < old.IntentRevision || next.ProgressRevision < old.ProgressRevision || next.ProtectionRevision < old.ProtectionRevision {
		return false
	}
	if old.ReleaseReason != 0 && next.ReleaseReason != old.ReleaseReason {
		return false
	}
	if next.ReleaseReason == MQTTBindingSessionEnded && old.ReleaseReason != MQTTBindingSessionEnded && next.ProgressRevision <= old.ProgressRevision {
		return false
	}
	if old.Stage < MQTTBindingRemoving && next.Stage == MQTTBindingRemoving && next.ReleaseReason != MQTTBindingSessionEnded && next.IntentRevision <= old.IntentRevision {
		return false
	}
	if next.Key.Owner.Kind == MQTTBindingUID {
		if !validMQTTInboxDrainTransition(old, next) {
			return false
		}
		if old.DiscoveryDone && (!next.DiscoveryDone || next.DiscoveryAfterChannelID != old.DiscoveryAfterChannelID || next.DiscoveryAfterChannelType != old.DiscoveryAfterChannelType) {
			return false
		}
		if len(next.DiscoveryAfterChannelID) < len(old.DiscoveryAfterChannelID) || len(next.DiscoveryAfterChannelID) == len(old.DiscoveryAfterChannelID) && (next.DiscoveryAfterChannelID < old.DiscoveryAfterChannelID || next.DiscoveryAfterChannelID == old.DiscoveryAfterChannelID && next.DiscoveryAfterChannelType < old.DiscoveryAfterChannelType) {
			return false
		}
		return true
	}
	if old.BoundaryKnown {
		if !next.BoundaryKnown || next.StartAfter != old.StartAfter || next.CompletedThrough < old.CompletedThrough || next.CompletedThrough != old.CompletedThrough && next.ProgressRevision <= old.ProgressRevision {
			return false
		}
	} else if next.BoundaryKnown && next.CompletedThrough != next.StartAfter {
		return false
	}
	if old.EndKnown && (!next.EndKnown || next.EndThrough != old.EndThrough) {
		return false
	}
	if next.Stage == MQTTBindingRemoved && next.ProtectionRevision < old.Revision {
		return false
	}
	return true
}

func mqttBindingOwnerParts(o MQTTBindingOwner) KeyParts {
	generation := o.Generation
	// Table keys reject empty strings. UID has no incarnation, represented by a
	// reserved NUL key component that cannot be a valid Channel generation.
	if o.Kind == MQTTBindingUID && generation == "" {
		generation = "\x00"
	}
	return KeyParts{Uint8(uint8(o.Kind)), String(o.ID), String(generation)}
}
func mqttSourceBindingPrimaryKey(k MQTTSourceBindingKey) KeyParts {
	return append(mqttBindingOwnerParts(k.Owner), String(k.Namespace), String(k.ClientID), Uint64(k.SessionGeneration), Uint64(k.SubscriptionGeneration))
}
func mqttSourceBindingKeyFromParts(p KeyParts) MQTTSourceBindingKey {
	generation := p[2].S
	if p[0].U8 == uint8(MQTTBindingUID) && generation == "\x00" {
		generation = ""
	}
	return MQTTSourceBindingKey{Owner: MQTTBindingOwner{Kind: MQTTBindingOwnerKind(p[0].U8), ID: p[1].S, Generation: generation}, Namespace: p[3].S, ClientID: p[4].S, SessionGeneration: p[5].U64, SubscriptionGeneration: p[6].U64}
}

// GetMQTTSourceBinding reads node storage after the caller establishes current
// source/UID Slot authority and its durable-apply barrier.
func (s *Shard) GetMQTTSourceBinding(ctx context.Context, key MQTTSourceBindingKey) (MQTTSourceBinding, bool, error) {
	if err := validateMQTTSourceBindingKey(key); err != nil {
		return MQTTSourceBinding{}, false, err
	}
	return mqttSourceBindingTable.Get(ctx, s, mqttSourceBindingPrimaryKey(key))
}
