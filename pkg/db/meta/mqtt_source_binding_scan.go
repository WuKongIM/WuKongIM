package meta

import (
	"context"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// MQTTSourceBindingRecoveryCursor includes every recovery-index tie breaker.
type MQTTSourceBindingRecoveryCursor struct {
	RecoveryAtMS int64
	Key          MQTTSourceBindingKey
}

// MQTTSourceBindingRetentionCursor resumes conservative source-obligation scans.
// A zero floor with a nonempty Key is a valid cursor for an unknown boundary.
type MQTTSourceBindingRetentionCursor struct {
	CompletedThrough uint64
	Key              MQTTSourceBindingKey
}

func mqttSourceBindingRetentionParts(k MQTTSourceBindingKey, floor uint64) KeyParts {
	return append(mqttBindingOwnerParts(k.Owner), Uint64(floor), String(k.Namespace), String(k.ClientID), Uint64(k.SessionGeneration), Uint64(k.SubscriptionGeneration))
}

// ListMQTTSourceBindingCandidates discovers preparing/active subscriptions. It
// grants no delivery permission; callers revalidate Session and authorization.
func (s *Shard) ListMQTTSourceBindingCandidates(ctx context.Context, owner MQTTBindingOwner, after MQTTSourceBindingKey, limit int) ([]MQTTSourceBinding, MQTTSourceBindingKey, bool, error) {
	if limit < 1 || limit > 256 || validateMQTTBindingOwner(owner) != nil {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	var cursor KeyParts
	if after != (MQTTSourceBindingKey{}) {
		if after.Owner != owner || validateMQTTSourceBindingKey(after) != nil {
			return nil, after, false, dberrors.ErrInvalidArgument
		}
		cursor = mqttSourceBindingPrimaryKey(after)
	}
	rows, next, done, err := mqttSourceBindingTable.ScanIndex(ctx, s, 2, mqttBindingOwnerParts(owner), cursor, limit)
	if err != nil {
		return nil, after, false, err
	}
	if len(next) > 0 {
		after = mqttSourceBindingKeyFromParts(next)
	}
	return rows, after, done, nil
}

// ListMQTTSourceBindingRecovery includes active reconciliation and unfinished
// cleanup. Callers must establish source/UID Slot authority before using a page.
func (s *Shard) ListMQTTSourceBindingRecovery(ctx context.Context, after MQTTSourceBindingRecoveryCursor, limit int) ([]MQTTSourceBinding, MQTTSourceBindingRecoveryCursor, bool, error) {
	if limit < 1 || limit > 256 {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	var cursor KeyParts
	if after != (MQTTSourceBindingRecoveryCursor{}) {
		if after.RecoveryAtMS <= 0 || validateMQTTSourceBindingKey(after.Key) != nil {
			return nil, after, false, dberrors.ErrInvalidArgument
		}
		cursor = append(KeyParts{Int64Ordered(after.RecoveryAtMS)}, mqttSourceBindingPrimaryKey(after.Key)...)
	}
	rows, next, done, err := mqttSourceBindingTable.ScanIndex(ctx, s, 3, nil, cursor, limit)
	if err != nil {
		return nil, after, false, err
	}
	if len(next) > 0 {
		after = MQTTSourceBindingRecoveryCursor{RecoveryAtMS: next[0].I64, Key: mqttSourceBindingKeyFromParts(next[1:])}
	}
	return rows, after, done, nil
}

// ListMQTTSourceBindingRetention returns conservative Channel-source floors.
// Preparing without a boundary yields floor zero, never permission to reclaim.
// An empty local result does not establish authoritative absence of consumers.
func (s *Shard) ListMQTTSourceBindingRetention(ctx context.Context, owner MQTTBindingOwner, after MQTTSourceBindingRetentionCursor, limit int) ([]MQTTSourceBinding, MQTTSourceBindingRetentionCursor, bool, error) {
	if limit < 1 || limit > 256 || owner.Kind != MQTTBindingChannel || validateMQTTBindingOwner(owner) != nil {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	var cursor KeyParts
	if after != (MQTTSourceBindingRetentionCursor{}) {
		if after.Key.Owner != owner || validateMQTTSourceBindingKey(after.Key) != nil {
			return nil, after, false, dberrors.ErrInvalidArgument
		}
		cursor = mqttSourceBindingRetentionParts(after.Key, after.CompletedThrough)
	}
	rows, next, done, err := mqttSourceBindingTable.ScanIndex(ctx, s, 4, mqttBindingOwnerParts(owner), cursor, limit)
	if err != nil {
		return nil, after, false, err
	}
	if len(next) > 0 {
		after = MQTTSourceBindingRetentionCursor{CompletedThrough: next[3].U64, Key: MQTTSourceBindingKey{Owner: owner, Namespace: next[4].S, ClientID: next[5].S, SessionGeneration: next[6].U64, SubscriptionGeneration: next[7].U64}}
	}
	return rows, after, done, nil
}
