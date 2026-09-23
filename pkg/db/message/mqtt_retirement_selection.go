package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

const mqttRetirementMaxScan = 64

// MQTTReplayRetirementSelection selects a whole accepted prefix. Done separates
// exhaustion or a candidate from continuation; it conveys no consumer authority.
type MQTTReplayRetirementSelection struct {
	Captured, Candidate MQTTReplayAnchorProof
	HasCandidate, Done  bool
	// BeforeAnchor is the last verified ineligible journal, excluded on resume.
	BeforeAnchor uint64
}

func mqttRetirementAnchorWithin(p, captured MQTTReplayAnchorProof) bool {
	a, c := p.Anchor, captured.Anchor
	return p.Manifest.LastOffset <= captured.Manifest.LastOffset && a.SourceCommand == c.SourceCommand && a.StartAfter == c.StartAfter &&
		a.Through <= c.Through && a.TotalBytes <= c.TotalBytes && a.TotalStoredBytes <= c.TotalStoredBytes &&
		(a.Through != c.Through || a == c)
}

// SelectMQTTReplayRetirementAnchor scans at most limit (1..64) committed journals
// in reverse, bounded by the anchor captured before the consumer-floor read.
// It pins all proof reads together and never reads or deletes shared bodies.
func (l *ChannelLog) SelectMQTTReplayRetirementAnchor(ctx context.Context, generation string, capturedPosition, through, beforePosition uint64, limit int) (MQTTReplayRetirementSelection, error) {
	var empty MQTTReplayRetirementSelection
	if capturedPosition == 0 || through >= capturedPosition || beforePosition > capturedPosition || limit < 1 || limit > mqttRetirementMaxScan || !(MQTTSourceState{Generation: generation, Revision: 1}).valid() {
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
	captured, found, err := loadMQTTReplayAnchorFrom(view, l.key, capturedPosition)
	if err != nil {
		return empty, err
	}
	if !found || mqttAnchorPrefix(captured).Generation != generation || through > captured.Anchor.Through {
		return empty, dberrors.ErrConflict
	}
	if beforePosition != 0 {
		cursor, found, err := loadMQTTReplayAnchorFrom(view, l.key, beforePosition)
		if err != nil {
			return empty, err
		}
		if !found || !mqttRetirementAnchorWithin(cursor, captured) || cursor.Anchor.Through <= through {
			return empty, dberrors.ErrConflict
		}
	}
	out := MQTTReplayRetirementSelection{Captured: captured}
	span := keycodec.NewPrefixSpan(mqttReplayAnchorPrefix(l.key))
	if beforePosition != 0 {
		span.End = mqttReplayAnchorKey(l.key, beforePosition)
	} else if capturedPosition != ^uint64(0) {
		span.End = mqttReplayAnchorKey(l.key, capturedPosition+1)
	}
	iter, err := view.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return empty, err
	}
	defer iter.Close()
	scanned := 0
	valid := iter.Last()
	for valid && scanned < limit {
		if err = ctxErr(ctx); err != nil {
			return empty, err
		}
		position, ok := mqttReplayAnchorPosition(l.key, iter.Key())
		if !ok {
			return empty, dberrors.ErrCorruptState
		}
		candidate, found, err := loadMQTTReplayAnchorFrom(view, l.key, position)
		if err != nil {
			return empty, err
		}
		if !found || !mqttRetirementAnchorWithin(candidate, captured) {
			return empty, dberrors.ErrCorruptState
		}
		if candidate.Anchor.Through <= through {
			if err = ctxErr(ctx); err != nil {
				return empty, err
			}
			out.Candidate, out.HasCandidate, out.Done, out.BeforeAnchor = candidate, true, true, 0
			return out, nil
		}
		out.BeforeAnchor = position
		scanned++
		valid = iter.Prev()
	}
	if err = iter.Error(); err != nil {
		return empty, err
	}
	if err = ctxErr(ctx); err != nil {
		return empty, err
	}
	if !valid {
		out.Done, out.BeforeAnchor = true, 0
	}
	return out, nil
}

// SelectMQTTReplayRetirementAnchor retains the compatibility lease through the
// pinned selection and maps errors without turning missing proof into a result.
func (s *ChannelStore) SelectMQTTReplayRetirementAnchor(ctx context.Context, generation string, capturedPosition, through, beforePosition uint64, limit int) (MQTTReplayRetirementSelection, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayRetirementSelection{}, err
	}
	defer s.endUse()
	p, err := s.log.SelectMQTTReplayRetirementAnchor(ctx, generation, capturedPosition, through, beforePosition, limit)
	return p, toChannelError(err)
}
