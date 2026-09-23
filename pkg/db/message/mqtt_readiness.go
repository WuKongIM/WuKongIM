package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayReadiness is replica-local coverage at one caller-captured committed
// frontier. Only a separately fenced runtime may use it for migration admission.
type MQTTReplayReadiness struct {
	CommittedThrough                uint64
	AnchorPosition, RequiredThrough uint64
	Covered                         bool
}

// ReadMQTTReplayReadiness verifies source, anchor and shared coverage in one pinned
// view. It uses one reverse journal seek and bounded point reads, with no writes,
// caller-provided digest, consumer decision or source-release side effect.
func (l *ChannelLog) ReadMQTTReplayReadiness(ctx context.Context, through uint64) (MQTTReplayReadiness, error) {
	var empty MQTTReplayReadiness
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
	evidence, err := readMQTTActivationEvidence(view, l.key)
	if err != nil {
		return empty, err
	}
	if through > evidence.checkpoint.HW {
		return empty, dberrors.ErrConflict
	}
	if evidence.checkpointPresent && validateCheckpoint(evidence.checkpoint) != nil {
		return empty, dberrors.ErrCorruptState
	}
	if evidence.sourcePresent && !evidence.present {
		return empty, dberrors.ErrCorruptState
	}
	if evidence.sourcePresent && evidence.source.CopiedThrough > through {
		return empty, dberrors.ErrConflict
	}
	out := MQTTReplayReadiness{CommittedThrough: through, Covered: true}
	current, present, err := loadMQTTReplayState(view, l.key)
	if err != nil {
		return empty, err
	}
	retired, hasRetired, err := loadMQTTReplayRetired(view, l.key)
	if err != nil {
		return empty, err
	}
	if hasRetired && (!present || retired.position > through) {
		return empty, dberrors.ErrConflict
	}
	if !evidence.present || evidence.manifest.LastOffset > through {
		if present && (!evidence.sourcePresent || current.Generation != evidence.source.Generation) {
			return empty, dberrors.ErrCorruptState
		}
		if err = ctxErr(ctx); err != nil {
			return empty, err
		}
		return out, nil
	}
	if !evidence.sourcePresent {
		return empty, dberrors.ErrCorruptState
	}
	_, activation, err := mqttReplayCommittedEntry(view, l.key, evidence.manifest.LastOffset, through)
	if err != nil {
		return empty, err
	}
	if activation != evidence.manifest {
		return empty, dberrors.ErrCorruptState
	}
	if present {
		if current.Generation != evidence.source.Generation || current.StartAfter != evidence.source.StartAfter {
			return empty, dberrors.ErrCorruptState
		}
		if _, err = mqttReplayTransferEvidence(view, l.key, current); err != nil {
			return empty, err
		}
		if err = validateMQTTReplayTail(view, l.key, current); err != nil {
			return empty, err
		}
	}
	span := keycodec.NewPrefixSpan(mqttReplayAnchorPrefix(l.key))
	if through != ^uint64(0) {
		span.End = mqttReplayAnchorKey(l.key, through+1)
	}
	iter, err := view.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return empty, err
	}
	defer iter.Close()
	var anchor MQTTReplayAnchorProof
	if iter.Last() {
		position, valid := mqttReplayAnchorPosition(l.key, iter.Key())
		if !valid {
			return empty, dberrors.ErrCorruptState
		}
		var found bool
		anchor, found, err = loadMQTTReplayAnchorFrom(view, l.key, position)
		if err != nil {
			return empty, err
		}
		if !found {
			return empty, dberrors.ErrCorruptState
		}
		out.AnchorPosition, out.RequiredThrough = position, anchor.Anchor.Through
	}
	if err = iter.Error(); err != nil {
		return empty, err
	}
	// The captured tail is an independently selected exact entry. A known anchor
	// there cannot disappear from the journal and turn into a native-only result.
	tail, _, err := mqttReplayCommittedEntry(view, l.key, through, through)
	if err != nil {
		return empty, err
	}
	if tail.Version == quorumlog.MQTTReplayAnchorProposalManifestVersion && out.AnchorPosition != through {
		return empty, dberrors.ErrCorruptState
	}
	if evidence.source.CopiedThrough > evidence.source.StartAfter && out.RequiredThrough < evidence.source.CopiedThrough {
		return empty, dberrors.ErrCorruptState
	}
	if out.AnchorPosition != 0 {
		out.Covered = present && current.Through >= out.RequiredThrough
		if out.Covered {
			if err = verifyMQTTRepairCovered(view, l.key, current, anchor); err != nil {
				return empty, err
			}
		}
	}
	if err = ctxErr(ctx); err != nil {
		return empty, err
	}
	return out, nil
}

// ReadMQTTReplayReadiness retains the compatibility lease through its pinned read.
func (s *ChannelStore) ReadMQTTReplayReadiness(ctx context.Context, through uint64) (MQTTReplayReadiness, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayReadiness{}, err
	}
	defer s.endUse()
	r, err := s.log.ReadMQTTReplayReadiness(ctx, through)
	return r, toChannelError(err)
}
