package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

const mqttRepairMaxScan = 64

// MQTTReplayRepairPlan describes one pinned local recovery view. Completion is
// relative to Target only and proves neither current placement nor readiness.
type MQTTReplayRepairPlan struct {
	Current           MQTTReplayState
	Target, Next      MQTTReplayAnchorProof
	HasNext, Complete bool
	// ScanAfter advances only over verified covered journals, not missing content.
	ScanAfter uint64
}

func mqttAnchorPrefix(p MQTTReplayAnchorProof) MQTTReplayState {
	a := p.Anchor
	return MQTTReplayState{Generation: quorumlog.MQTTSourceGeneration(a.SourceCommand), StartAfter: a.StartAfter, Through: a.Through, TotalBytes: a.TotalBytes, TotalStoredBytes: a.TotalStoredBytes, Digest: a.Digest}
}

// verifyMQTTRepairCovered authenticates a skipped journal against this replica's
// retained cumulative meter. A caller's scan cursor is never trusted progress.
func verifyMQTTRepairCovered(view messageBackupReadView, key ChannelKey, current MQTTReplayState, p MQTTReplayAnchorProof) error {
	expected := mqttAnchorPrefix(p)
	if expected.Generation != current.Generation || expected.StartAfter != current.StartAfter || expected.Through > current.Through {
		return dberrors.ErrConflict
	}
	prefix, err := mqttReplayPrefix(view, key, current, expected.Through)
	if err != nil {
		return err
	}
	if prefix != expected {
		return dberrors.ErrCorruptState
	}
	return nil
}

// PlanMQTTReplayRepair selects the first uncovered committed anchor interval at
// or before targetPosition. It examines at most limit journals (1..64), verifies
// an optional covered continuation, and writes no checkpoint or replay progress.
// An absent shared frontier is valid after source release: independent anchors,
// not source copied-through, define what still needs recovery.
func (l *ChannelLog) PlanMQTTReplayRepair(ctx context.Context, generation string, targetPosition, afterPosition uint64, limit int) (MQTTReplayRepairPlan, error) {
	var empty MQTTReplayRepairPlan
	if targetPosition == 0 || afterPosition > targetPosition || limit <= 0 || limit > mqttRepairMaxScan || !(MQTTSourceState{Generation: generation, Revision: 1}).valid() {
		return empty, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return empty, err
	}
	defer l.endUse()
	if err := ctxErr(ctx); err != nil {
		return empty, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return empty, err
	}
	defer view.Close()
	target, found, err := loadMQTTReplayAnchorFrom(view, l.key, targetPosition)
	if err != nil {
		return empty, err
	}
	if !found {
		return empty, dberrors.ErrConflict
	}
	expected := mqttAnchorPrefix(target)
	if expected.Generation != generation {
		return empty, dberrors.ErrConflict
	}
	current, present, err := loadMQTTReplayState(view, l.key)
	if err != nil {
		return empty, err
	}
	if present {
		if current.Generation != generation || current.StartAfter != expected.StartAfter {
			return empty, dberrors.ErrCorruptState
		}
		if _, err = mqttReplayTransferEvidence(view, l.key, current); err != nil {
			return empty, err
		}
		if err = validateMQTTReplayTail(view, l.key, current); err != nil {
			return empty, err
		}
	} else {
		current = MQTTReplayState{Generation: generation, StartAfter: expected.StartAfter, Through: expected.StartAfter}
	}
	if afterPosition != 0 {
		cursor, found, err := loadMQTTReplayAnchorFrom(view, l.key, afterPosition)
		if err != nil {
			return empty, err
		}
		if !found {
			return empty, dberrors.ErrConflict
		}
		if err = verifyMQTTRepairCovered(view, l.key, current, cursor); err != nil {
			return empty, err
		}
	}
	out := MQTTReplayRepairPlan{Current: current, Target: target, ScanAfter: afterPosition}
	if current.Through >= expected.Through {
		if err = verifyMQTTRepairCovered(view, l.key, current, target); err != nil {
			return empty, err
		}
		if err = ctxErr(ctx); err != nil {
			return empty, err
		}
		out.Complete, out.ScanAfter = true, 0
		return out, nil
	}
	// Both positions are below the committed target here; adding one cannot wrap.
	if afterPosition >= targetPosition {
		return empty, dberrors.ErrConflict
	}
	span := keycodec.NewPrefixSpan(mqttReplayAnchorPrefix(l.key))
	span.Start = mqttReplayAnchorKey(l.key, max(current.Through, afterPosition)+1)
	if targetPosition != ^uint64(0) {
		span.End = mqttReplayAnchorKey(l.key, targetPosition+1)
	}
	iter, err := view.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return empty, err
	}
	defer iter.Close()
	scanned := 0
	for valid := iter.First(); valid; valid = iter.Next() {
		if err = ctxErr(ctx); err != nil {
			return empty, err
		}
		position, ok := mqttReplayAnchorPosition(l.key, iter.Key())
		if !ok {
			return empty, dberrors.ErrCorruptState
		}
		next, found, err := loadMQTTReplayAnchorFrom(view, l.key, position)
		if err != nil {
			return empty, err
		}
		if !found {
			return empty, dberrors.ErrCorruptState
		}
		prefix := mqttAnchorPrefix(next)
		if prefix.Generation != generation || prefix.StartAfter != current.StartAfter {
			return empty, dberrors.ErrCorruptState
		}
		scanned++
		if prefix.Through > current.Through {
			if prefix.Through > expected.Through || prefix.TotalBytes > expected.TotalBytes || prefix.TotalStoredBytes > expected.TotalStoredBytes ||
				(prefix.Through == expected.Through && prefix != expected) || prefix.Through-current.Through > mqttReplayMaxRows ||
				prefix.TotalBytes < current.TotalBytes || prefix.TotalStoredBytes <= current.TotalStoredBytes || prefix.TotalStoredBytes-current.TotalStoredBytes > mqttReplayMaxBytes ||
				prefix.TotalBytes-current.TotalBytes > prefix.TotalStoredBytes-current.TotalStoredBytes {
				return empty, dberrors.ErrCorruptState
			}
			if err = ctxErr(ctx); err != nil {
				return empty, err
			}
			out.Next, out.HasNext = next, true
			return out, nil
		}
		if err = verifyMQTTRepairCovered(view, l.key, current, next); err != nil {
			return empty, err
		}
		out.ScanAfter = position
		if scanned == limit {
			if err = iter.Error(); err != nil {
				return empty, err
			}
			if err = ctxErr(ctx); err != nil {
				return empty, err
			}
			return out, nil
		}
	}
	if err = iter.Error(); err != nil {
		return empty, err
	}
	// The independently verified target must still exist in this pinned range.
	return empty, dberrors.ErrCorruptState
}

// PlanMQTTReplayRepair retains the compatibility lease through the pinned read.
func (s *ChannelStore) PlanMQTTReplayRepair(ctx context.Context, generation string, targetPosition, afterPosition uint64, limit int) (MQTTReplayRepairPlan, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayRepairPlan{}, err
	}
	defer s.endUse()
	plan, err := s.log.PlanMQTTReplayRepair(ctx, generation, targetPosition, afterPosition, limit)
	return plan, toChannelError(err)
}
