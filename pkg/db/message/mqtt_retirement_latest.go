package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// LoadLatestMQTTReplayRetirement pins source, HW and one reverse journal seek.
// Pending controls are excluded; the returned decision is independently proved
// from this replica's native identities, without reading or deleting bodies.
func (l *ChannelLog) LoadLatestMQTTReplayRetirement(ctx context.Context, generation string) (MQTTReplayRetirementProof, bool, error) {
	var empty MQTTReplayRetirementProof
	if !(MQTTSourceState{Generation: generation, Revision: 1}).valid() {
		return empty, false, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return empty, false, err
	}
	defer l.endUse()
	if err := ctxErr(ctx); err != nil {
		return empty, false, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return empty, false, err
	}
	defer view.Close()
	evidence, err := readMQTTActivationEvidence(view, l.key)
	if err != nil {
		return empty, false, err
	}
	if !evidence.present || !evidence.sourcePresent || evidence.source.Generation != generation {
		return empty, false, dberrors.ErrConflict
	}
	hw := evidence.checkpoint.HW
	_, activation, err := mqttReplayCommittedEntry(view, l.key, evidence.manifest.LastOffset, hw)
	if err != nil {
		return empty, false, err
	}
	if activation != evidence.manifest {
		return empty, false, dberrors.ErrCorruptState
	}
	// An already published baseline cannot disappear from the selected history.
	base, hasBase, err := loadMQTTReplayRetired(view, l.key)
	if err != nil {
		return empty, false, err
	}
	if hasBase && base.position > hw {
		return empty, false, dberrors.ErrCorruptState
	}
	// Also detect an omitted journal at the committed native tail instead of
	// quietly falling back to an earlier decision or reporting absence.
	_, _, err = loadMQTTReplayRetirementFrom(view, l.key, hw)
	if err != nil {
		return empty, false, err
	}
	span := keycodec.NewPrefixSpan(mqttReplayRetirementPrefix(l.key))
	if hw != ^uint64(0) {
		span.End = mqttReplayRetirementKey(l.key, hw+1)
	}
	iter, err := view.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return empty, false, err
	}
	defer iter.Close()
	var proof MQTTReplayRetirementProof
	found := iter.Last()
	if found {
		position, ok := mqttReplayRetirementPosition(l.key, iter.Key())
		if !ok || (hasBase && position < base.position) {
			return empty, false, dberrors.ErrCorruptState
		}
		var present bool
		proof, present, err = loadMQTTReplayRetirementFrom(view, l.key, position)
		if err != nil {
			return empty, false, err
		}
		if !present || (hasBase && !validMQTTRetirementAdvance(base.proof.Retirement, proof.Retirement)) {
			return empty, false, dberrors.ErrCorruptState
		}
	} else if hasBase {
		return empty, false, dberrors.ErrCorruptState
	}
	if err = iter.Error(); err != nil {
		return empty, false, err
	}
	if err = ctxErr(ctx); err != nil {
		return empty, false, err
	}
	return proof, found, nil
}

// LoadLatestMQTTReplayRetirement retains the compatibility lease through the
// pinned lookup and preserves absence separately from failed proof.
func (s *ChannelStore) LoadLatestMQTTReplayRetirement(ctx context.Context, generation string) (MQTTReplayRetirementProof, bool, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayRetirementProof{}, false, err
	}
	defer s.endUse()
	p, found, err := s.log.LoadLatestMQTTReplayRetirement(ctx, generation)
	return p, found, toChannelError(err)
}
