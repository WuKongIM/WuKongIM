package message

import (
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

const mqttRetiredReplayBackupVersion uint16 = 3

// mqttReplayBackupBaseline normalizes cleanup progress because the archive
// contains no retired bodies, even when the source is still physically pruning.
func mqttReplayBackupBaseline(view proposalReadView, key ChannelKey, s MQTTReplayState, hw uint64) (MQTTReplayState, []byte, error) {
	base, position, err := mqttReplayBaseline(view, key, s)
	if err != nil {
		return base, nil, err
	}
	if position == 0 {
		return base, nil, nil
	}
	if position > hw {
		return base, nil, dberrors.ErrConflict
	}
	return base, encodeMQTTReplayRetired(key, position, base.Through), nil
}

type mqttBackupProofView map[string][]byte

func (v mqttBackupProofView) Get(key []byte) ([]byte, bool, error) {
	value, found := v[string(key)]
	return value, found, nil
}

// mqttReplayRestoreBaseline reuses the same independent point-proof validator
// over the already preflighted header. It never reads sender-supplied counters.
func mqttReplayRestoreBaseline(header messageBackupChannelHeader, value []byte, s MQTTReplayState) (mqttReplayRetired, error) {
	var empty mqttReplayRetired
	if len(value) == 0 {
		return empty, nil
	}
	view := make(mqttBackupProofView, len(header.systemEntries)+2)
	for _, entry := range header.systemEntries {
		if _, exists := view[string(entry.Key)]; exists {
			return empty, dberrors.ErrCorruptState
		}
		view[string(entry.Key)] = entry.Value
	}
	view[string(encodeCheckpointKey(header.key))] = encodeCheckpoint(header.checkpoint)
	view[string(mqttReplayRetiredKey(header.key))] = value
	r, found, err := loadMQTTReplayRetired(view, header.key)
	if err != nil {
		return empty, err
	}
	if !found || r.deletedThrough != r.prefix().Through || !mqttReplayBaselineContains(s, r.prefix()) {
		return empty, dberrors.ErrCorruptState
	}
	return r, nil
}

func validateMQTTReplayRestoreBaseline(target *MessageDB, key ChannelKey, incoming mqttReplayRetired) error {
	if target == nil {
		return nil
	}
	old, present, err := loadMQTTReplayRetired(target.engine, key)
	if err != nil || !present {
		return err
	}
	if incoming.position == 0 || !validMQTTRetirementAdvance(old.proof.Retirement, incoming.proof.Retirement) || incoming.position < old.position {
		return dberrors.ErrConflict
	}
	return nil
}

// stageRestoredMQTTReplayBaseline removes any obsolete target prefix with two
// constant-size tombstones before publishing a fully pruned archive's marker.
func stageRestoredMQTTReplayBaseline(batch *engine.Batch, key ChannelKey, r mqttReplayRetired) error {
	if r.position == 0 {
		return nil
	}
	p := r.prefix()
	if err := batch.DeleteRange(engine.Span{Start: mqttReplayPositionKey(key, p.Generation, p.StartAfter+1), End: mqttReplayPositionKey(key, p.Generation, p.Through+1)}); err != nil {
		return err
	}
	if err := batch.DeleteRange(engine.Span{Start: mqttReplayMeterKey(key, p.Generation, p.StartAfter+1), End: mqttReplayMeterKey(key, p.Generation, p.Through+1)}); err != nil {
		return err
	}
	return batch.Set(mqttReplayRetiredKey(key), encodeMQTTReplayRetired(key, r.position, p.Through))
}
