package message

import (
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// mqttStorageMutation retains the conservative larger debt on unknown commit.
// Positive reservations are canceled only before physical submission; negative
// deltas become reusable only after a confirmed atomic suffix mutation.
type mqttStorageMutation struct {
	budget           *mqttStorageBudget
	reserved, refund uint64
	submitted        bool
}

func (m *mqttStorageMutation) finish(ctx context.Context) error {
	if m.budget == nil {
		return nil
	}
	return m.budget.release(ctx, m.refund)
}
func (m *mqttStorageMutation) cancel() {
	if !m.submitted {
		m.budget.cancelReservation(m.reserved)
	}
}

// stageMQTTStorageReplacement follows the existing recovery/truncation proof;
// it supplies no authority to discard committed source or replay content.
func (e *channelEntry) stageMQTTStorageReplacement(ctx context.Context, batch *engine.Batch, keep uint64, rows []messageRow, proposals []durableProposalRecord) (mqttStorageMutation, error) {
	change := mqttStorageMutation{budget: e.db.mqttStorage}
	if change.budget == nil {
		return change, nil
	}
	if err := change.budget.guardMQTTStorageReplacement(e.key); err != nil {
		return change, err
	}
	if keep == math.MaxUint64 {
		if len(rows) != 0 {
			return change, dberrors.ErrCorruptState
		}
		return change, nil
	}
	span := keycodec.NewPrefixSpan(encodeMessageSystemPrefix(e.key, messageSystemIDMQTTStorage))
	start := mqttStorageChargeKey(e.key, keep+1)
	it, err := e.db.engine.NewIter(engine.Span{Start: start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return change, err
	}
	defer it.Close()
	var oldTotal uint64
	for valid := it.First(); valid; valid = it.Next() {
		if err = ctxErr(ctx); err != nil {
			return change, err
		}
		v, err := it.Value()
		if err != nil {
			return change, err
		}
		n, err := decodeMQTTStorageCharge(it.Key(), v)
		if err != nil {
			return change, err
		}
		if n > math.MaxUint64-oldTotal {
			return change, dberrors.ErrCorruptState
		}
		oldTotal += n
	}
	if err = it.Error(); err != nil {
		return change, err
	}
	source, protected, err := e.mqttStorageSource(proposals)
	if err != nil {
		return change, err
	}
	if !protected {
		// A replacement can introduce its first activation before business rows.
		for _, p := range proposals {
			if p.manifest.Version == quorumlog.MQTTSourceProposalManifestVersion {
				source.Generation = quorumlog.MQTTSourceGeneration(p.manifest.CommandID)
				source.StartAfter = p.manifest.BaseOffset
				protected = true
				break
			}
		}
	}
	var newTotal uint64
	var charges []mqttStorageCharge
	if protected {
		for _, row := range rows {
			if row.MessageSeq <= source.StartAfter {
				continue
			}
			n, err := e.mqttStorageRowCharge(row, source.Generation, proposals)
			if err != nil {
				return change, err
			}
			if n == 0 {
				continue
			}
			if n > math.MaxUint64-newTotal {
				return change, dberrors.ErrCorruptState
			}
			newTotal += n
			charges = append(charges, mqttStorageCharge{row.MessageSeq, n})
		}
	}
	if newTotal > oldTotal {
		change.reserved = newTotal - oldTotal
		if err = change.budget.reserve(ctx, change.reserved); err != nil {
			return mqttStorageMutation{}, err
		}
	} else {
		change.refund = oldTotal - newTotal
	}
	failed := true
	defer func() {
		if failed {
			change.cancel()
		}
	}()
	if err = batch.DeleteRange(engine.Span{Start: start, End: span.End}); err != nil {
		return change, err
	}
	if err = stageMQTTStorageCharges(batch, e.key, charges); err != nil {
		return change, err
	}
	ticket, found, err := loadMQTTStorageTicket(e.db.engine, e.key)
	if err != nil {
		return change, err
	}
	if found && ticket.Manifest.LastOffset > keep {
		ticket.Phase = mqttStorageCanceled
		ticket.Charges = nil
		for _, p := range proposals {
			if p.manifest == ticket.Manifest {
				ticket.Phase = mqttStorageConsumed
			}
		}
		if err = stageMQTTStorageTicket(batch, e.key, ticket); err != nil {
			return change, err
		}
	}
	failed = false
	return change, nil
}
