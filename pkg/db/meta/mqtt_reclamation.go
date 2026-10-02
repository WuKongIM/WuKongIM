package meta

import (
	"bytes"
	"context"
	"math"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// MQTTSessionReclamation discards only lifetimes that the retained parent has
// ended or superseded. It grants no owner-isolation or shared-content GC proof.
// ThroughGeneration is fixed across pages; each partial page needs a fresh
// ExpectedRevision. The current live lifetime and detached Will stay untouched.
type MQTTSessionReclamation struct {
	Namespace         string `json:"broker_namespace"`
	ClientID          string `json:"client_id"`
	ExpectedRevision  uint64 `json:"expected_revision"`
	ThroughGeneration uint64 `json:"through_generation"`
	UpdatedAtMS       int64  `json:"updated_at_ms"`
}

// MQTTSessionReclamationResult is valid only after a successful batch commit.
// Done proves the requested local child range is absent, not remote cleanup.
// Unchanged is a durable completion witness, not an exact mutation receipt.
type MQTTSessionReclamationResult struct {
	Status                     MQTTSessionCASStatus
	CurrentRevision            uint64
	ReclaimedThroughGeneration uint64
	RemovedSubscriptions       int
	Done                       bool
}

// ValidateMQTTSessionReclamation bounds deterministic cleanup input.
func ValidateMQTTSessionReclamation(m MQTTSessionReclamation) error {
	if validateMQTTIdentity(m.Namespace, 1024) != nil || validateMQTTIdentity(m.ClientID, 1024) != nil || m.ExpectedRevision == 0 || m.ExpectedRevision == math.MaxUint64 || m.ThroughGeneration == 0 || m.UpdatedAtMS <= 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ReclaimMQTTSession removes at most 64 subscription intents and their time
// indexes. Once no intents remain in the range, four range tombstones remove
// cursor rows, qualified charges, inflight rows and send-order indexes atomically
// with the parent's monotonic completion marker. Selection retains at most 65
// visible primary candidates; same-batch masks are skipped with bounded seeks.
func (b *Batch) ReclaimMQTTSession(slot HashSlot, m MQTTSessionReclamation) (*MQTTSessionReclamationResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if err := ValidateMQTTSessionReclamation(m); err != nil {
		return nil, err
	}
	result := &MQTTSessionReclamationResult{}
	b.addOp(slot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTSessionReclamationResult{Status: MQTTSessionCASConflict}
		s, found, err := loadUpdateRow(mqttSessionTable, state, slot, mqttSessionPrimaryKey(m.Namespace, m.ClientID))
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		result.CurrentRevision = s.Revision
		result.ReclaimedThroughGeneration = s.ReclaimedThroughGeneration
		if m.ThroughGeneration <= s.ReclaimedThroughGeneration {
			result.Status = MQTTSessionCASUnchanged
			result.Done = true
			return nil
		}
		if s.Revision != m.ExpectedRevision || m.UpdatedAtMS < s.UpdatedAtMS || m.ThroughGeneration > s.Generation || m.ThroughGeneration == s.Generation && s.State != MQTTSessionEnded {
			return nil
		}
		span, err := mqttReclamationSpan(encodeRowPrefix(slot, TableIDMQTTSubscription), m)
		if err != nil {
			return err
		}
		keys, done, err := mqttReclamationBatchKeys(ctx, state, span)
		if err != nil {
			return err
		}
		base := encodeRowPrefix(slot, TableIDMQTTSubscription)
		for _, key := range keys {
			pk, ok := mqttSubscriptionTable.decodePrimaryRowKey(base, key)
			if !ok {
				return dberrors.ErrCorruptValue
			}
			_, exists, err := mqttSubscriptionTable.loadBatchRow(state, slot, pk, key)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
			if err := deleteUpdateRow(mqttSubscriptionTable, state, batch, slot, pk); err != nil {
				return err
			}
			result.RemovedSubscriptions++
		}
		// Retain the deleted primary prefix in the batch overlay. A later
		// cleanup command must seek beyond it, producing the same page as if
		// the earlier command had already committed on its own.
		deleted := span
		if !done {
			deleted.End = keycodec.PrefixEnd(keys[len(keys)-1])
		}
		if err := stageMQTTReclamationRange(state, batch, deleted); err != nil {
			return err
		}
		if done {
			var builder keycodec.Builder
			accounting := builder.Reset().Domain(keycodec.DomainMeta).Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(slot)).System(TableIDMQTTDeliveryCursor, 1).Key()
			for _, prefix := range [][]byte{encodeRowPrefix(slot, TableIDMQTTDeliveryCursor), accounting, encodeRowPrefix(slot, TableIDMQTTInflight), encodeIndexPrefix(slot, TableIDMQTTInflight, 2)} {
				span, err := mqttReclamationSpan(prefix, m)
				if err != nil {
					return err
				}
				if err := stageMQTTReclamationRange(state, batch, span); err != nil {
					return err
				}
			}
			s.ReclaimedThroughGeneration = m.ThroughGeneration
			if m.ThroughGeneration == s.Generation {
				s.PendingMessages = 0
				s.PendingBytes = 0
				s.OutboundInflight = 0
			}
		}
		s.Revision++
		s.UpdatedAtMS = m.UpdatedAtMS
		if err := stageUpdateRow(mqttSessionTable, state, batch, slot, s); err != nil {
			return err
		}
		result.Status = MQTTSessionCASApplied
		result.CurrentRevision = s.Revision
		result.ReclaimedThroughGeneration = s.ReclaimedThroughGeneration
		result.Done = done
		return nil
	})
	return result, nil
}

// mqttReclamationSpan includes every historical lifetime through the requested
// generation under one exact namespace/ClientID. PrefixEnd handles MaxUint64
// without overflowing into another client's range.
func mqttReclamationSpan(base []byte, m MQTTSessionReclamation) (engine.Span, error) {
	start, err := encodeKeyParts(base, KeyParts{String(m.Namespace), String(m.ClientID)})
	if err != nil {
		return engine.Span{}, err
	}
	last, err := encodeKeyParts(bytes.Clone(start), KeyParts{Uint64(m.ThroughGeneration)})
	if err != nil {
		return engine.Span{}, err
	}
	return engine.Span{Start: start, End: keycodec.PrefixEnd(last)}, nil
}

// mqttReclamationBatchKeys keeps at most 65 ordered candidates while
// merging prior operations in the atomic apply batch. Deleted primary prefixes
// are skipped with seeks, keeping page results independent of apply batching.
func mqttReclamationBatchKeys(ctx context.Context, state *batchCommitState, span engine.Span) ([][]byte, bool, error) {
	iter, err := state.db.engine.NewIter(span, engine.IterOptions{})
	if err != nil {
		return nil, false, err
	}
	defer iter.Close()
	keys := make([][]byte, 0, 65)
	for ok := iter.First(); ok && len(keys) < 65; {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		var skip []byte
		for _, deleted := range state.tableDeletes {
			if mqttReclamationContains(deleted, iter.Key()) && bytes.Compare(deleted.End, skip) > 0 {
				skip = deleted.End
			}
		}
		if skip != nil {
			ok = iter.SeekGE(skip)
			continue
		}
		keys = append(keys, bytes.Clone(iter.Key()))
		ok = iter.Next()
	}

	if err := iter.Error(); err != nil {
		return nil, false, err
	}
	for key, row := range state.tableRows {
		if !row.exists || !mqttReclamationContains(span, []byte(key)) {
			continue
		}
		at := sort.Search(len(keys), func(i int) bool { return bytes.Compare(keys[i], []byte(key)) >= 0 })
		if at < len(keys) && bytes.Equal(keys[at], []byte(key)) {
			continue
		}
		if at == 65 {
			continue
		}
		if len(keys) < 65 {
			keys = append(keys, nil)
		}
		copy(keys[at+1:], keys[at:len(keys)-1])
		keys[at] = []byte(key)
	}
	if len(keys) == 65 {
		return keys[:64], false, nil
	}
	return keys, true, nil
}
func mqttReclamationContains(span engine.Span, key []byte) bool {
	return bytes.Compare(key, span.Start) >= 0 && bytes.Compare(key, span.End) < 0
}

// stageMQTTReclamationRange masks disk state and earlier point writes in the
// same apply batch. Later permitted point writes take precedence over the mask.
func stageMQTTReclamationRange(state *batchCommitState, b *engine.Batch, span engine.Span) error {
	if err := b.DeleteRange(span); err != nil {
		return err
	}
	state.tableDeletes = append(state.tableDeletes, span)
	for key := range state.tableRows {
		if mqttReclamationContains(span, []byte(key)) {
			delete(state.tableRows, key)
		}
	}
	return nil
}

// ReclaimMQTTSession exposes the same atomic operation to the Slot FSM adapter.
func (b *WriteBatch) ReclaimMQTTSession(slot uint16, m MQTTSessionReclamation) (*MQTTSessionReclamationResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.ReclaimMQTTSession(HashSlot(slot), m)
}
