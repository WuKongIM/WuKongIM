package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayAnchorState is one coherent source and committed journal view.
// Requested permits exact retry after later anchors and original-body removal.
type MQTTReplayAnchorState struct {
	Source                  MQTTSourceState
	CommittedThrough        uint64
	Latest, Requested       MQTTReplayAnchorProof
	HasLatest, HasRequested bool
	// MaintenanceOnly proves the bounded suffix to CommittedThrough contains
	// only anchors/retirements; it is computed for planning (zero command) reads.
	MaintenanceOnly bool
}

// ReadMQTTReplayAnchors uses one reverse seek plus bounded point proofs. Through
// is already checkpointed by the owner; this read never advances its HW. A zero
// command omits the exact retry lookup without changing the latest journal view.
func (l *ChannelLog) ReadMQTTReplayAnchors(ctx context.Context, through uint64, command quorumlog.CommandID) (MQTTReplayAnchorState, error) {
	var out MQTTReplayAnchorState
	if through == 0 {
		return out, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return out, err
	}
	defer l.endUse()
	if err := ctxErr(ctx); err != nil {
		return out, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return out, err
	}
	defer view.Close()
	evidence, err := readMQTTActivationEvidence(view, l.key)
	if err != nil {
		return out, err
	}
	if !evidence.present || !evidence.sourcePresent || evidence.manifest.LastOffset > through || evidence.checkpoint.HW < through {
		return out, dberrors.ErrConflict
	}
	_, activation, err := mqttReplayCommittedEntry(view, l.key, evidence.manifest.LastOffset, through)
	if err != nil {
		return out, err
	}
	if activation != evidence.manifest {
		return out, dberrors.ErrCorruptState
	}
	out.Source, out.CommittedThrough = evidence.source, through
	span := keycodec.NewPrefixSpan(mqttReplayAnchorPrefix(l.key))
	if through != ^uint64(0) {
		span.End = mqttReplayAnchorKey(l.key, through+1)
	}
	iter, err := view.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return out, err
	}
	defer iter.Close()
	if iter.Last() {
		pos, ok := mqttReplayAnchorPosition(l.key, iter.Key())
		if !ok {
			return out, dberrors.ErrCorruptState
		}
		out.Latest, out.HasLatest, err = loadMQTTReplayAnchorFrom(view, l.key, pos)
		if err != nil {
			return out, err
		}
		if !out.HasLatest {
			return out, dberrors.ErrCorruptState
		}
	}
	if err = iter.Error(); err != nil {
		return out, err
	}
	if command != (quorumlog.CommandID{}) {
		requested, found, err := loadDurableProposalFrom(view, encodeProposalByCommandKey(l.key, command))
		if err != nil {
			return out, err
		}
		if found {
			if requested.manifest.Version != quorumlog.MQTTReplayAnchorProposalManifestVersion {
				return out, dberrors.ErrConflict
			}
			if requested.manifest.LastOffset <= through {
				out.Requested, out.HasRequested, err = loadMQTTReplayAnchorFrom(view, l.key, requested.manifest.LastOffset)
				if err != nil {
					return out, err
				}
				if !out.HasRequested || out.Requested.Manifest != requested.manifest || !out.HasLatest || out.Requested.Manifest.LastOffset > out.Latest.Manifest.LastOffset {
					return out, dberrors.ErrCorruptState
				}
			}
		}
	}
	if command == (quorumlog.CommandID{}) && out.HasLatest {
		out.MaintenanceOnly, err = mqttMaintenanceOnly(ctx, view, l.key, out.Latest, through)
		if err != nil {
			return MQTTReplayAnchorState{}, err
		}
	}
	if err = ctxErr(ctx); err != nil {
		return MQTTReplayAnchorState{}, err
	}
	return out, nil
}

// ReadMQTTReplayAnchors retains the compatibility lease while reading proof.
func (s *ChannelStore) ReadMQTTReplayAnchors(ctx context.Context, through uint64, command quorumlog.CommandID) (MQTTReplayAnchorState, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayAnchorState{}, err
	}
	defer s.endUse()
	state, err := s.log.ReadMQTTReplayAnchors(ctx, through, command)
	return state, toChannelError(err)
}
