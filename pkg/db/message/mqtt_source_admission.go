package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// LoadCommittedMQTTSourceState requires a consistent committed log-activation
// projection covering the caller's already-persisted boundary. It never updates
// HW, repairs missing evidence or treats a local source CAS as activation proof.
func (s *ChannelStore) LoadCommittedMQTTSourceState(ctx context.Context, through uint64) (state MQTTSourceState, found bool, err error) {
	if err := s.beginUse(); err != nil {
		return MQTTSourceState{}, false, err
	}
	defer s.endUse()
	defer func() { err = toChannelError(err) }()
	l := s.log
	if err := ctx.Err(); err != nil {
		return MQTTSourceState{}, false, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return MQTTSourceState{}, false, err
	}
	defer view.Close()
	evidence, err := readMQTTActivationEvidence(view, l.key)
	if err != nil {
		return MQTTSourceState{}, false, err
	}
	if evidence.checkpoint.HW < through || (through != 0 && !evidence.checkpointPresent) {
		return MQTTSourceState{}, false, dberrors.ErrCorruptState
	}
	if !evidence.present {
		if evidence.sourcePresent {
			return MQTTSourceState{}, false, dberrors.ErrConflict
		}
		return MQTTSourceState{}, false, nil
	}
	if evidence.manifest.LastOffset > through {
		return MQTTSourceState{}, false, nil
	}
	if !evidence.sourcePresent {
		return MQTTSourceState{}, false, dberrors.ErrCorruptState
	}
	return evidence.source, true, nil
}
