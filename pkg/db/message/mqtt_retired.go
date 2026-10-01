package message

import (
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

// MQTTReplayRetirementResult separates logical retirement from bounded engine
// cleanup. A completed cursor does not claim that compaction has reclaimed disk.
type MQTTReplayRetirementResult struct {
	// RetirementPosition identifies the committed decision backing Retired.
	RetirementPosition uint64
	Retired            MQTTReplayState
	// DeletedThrough tracks engine removal; Deleted counts this call's primary rows.
	DeletedThrough uint64
	Deleted        int
	Done           bool
}

type mqttReplayRetired struct {
	position, deletedThrough uint64
	proof                    MQTTReplayRetirementProof
}

func (r mqttReplayRetired) prefix() MQTTReplayState {
	return mqttAnchorPrefix(MQTTReplayAnchorProof{Anchor: r.proof.Retirement.Anchor})
}

func mqttReplayRetiredKey(key ChannelKey) []byte {
	b := newMessageKey(key, 7)
	b = append(b, byte(keycodec.SpaceSystem))
	b = keycodec.AppendUint32(b, TableIDMQTTReplay)
	return keycodec.AppendUint16(b, 2)
}

func encodeMQTTReplayRetired(key ChannelKey, position, deletedThrough uint64) []byte {
	b := binary.BigEndian.AppendUint64(nil, position)
	b = binary.BigEndian.AppendUint64(b, deletedThrough)
	return rowcodec.Wrap(mqttReplayRetiredKey(key), 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, b)
}

// loadMQTTReplayRetired independently proves the marker's committed decision;
// the marker alone never authorizes a baseline or deletion.
func loadMQTTReplayRetired(view proposalReadView, key ChannelKey) (mqttReplayRetired, bool, error) {
	var out mqttReplayRetired
	value, found, err := view.Get(mqttReplayRetiredKey(key))
	if err != nil || !found {
		return out, false, err
	}
	if len(value) != rowcodec.EnvelopeLen(16) {
		return out, false, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(mqttReplayRetiredKey(key), value)
	if err != nil {
		return out, false, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum || len(env.Payload) != 16 {
		return out, false, dberrors.ErrCorruptValue
	}
	out.position, out.deletedThrough = binary.BigEndian.Uint64(env.Payload), binary.BigEndian.Uint64(env.Payload[8:])
	if out.position == 0 {
		return out, false, dberrors.ErrCorruptValue
	}
	out.proof, found, err = loadMQTTReplayRetirementFrom(view, key, out.position)
	if err != nil {
		return out, false, err
	}
	if !found || out.deletedThrough < out.proof.Retirement.Anchor.StartAfter || out.deletedThrough > out.proof.Retirement.Anchor.Through {
		return out, false, dberrors.ErrCorruptState
	}
	return out, true, nil
}

func mqttReplayBaselineContains(current, base MQTTReplayState) bool {
	return current.Generation == base.Generation && current.StartAfter == base.StartAfter && current.Through >= base.Through &&
		(current == base || current.Through > base.Through && current.TotalBytes >= base.TotalBytes && current.TotalStoredBytes > base.TotalStoredBytes)
}

// mqttReplayBaseline substitutes only the exact retired endpoint. Earlier
// individual counters remain unavailable and must not be fabricated.
func mqttReplayBaseline(view proposalReadView, key ChannelKey, current MQTTReplayState) (MQTTReplayState, uint64, error) {
	r, found, err := loadMQTTReplayRetired(view, key)
	if err != nil {
		return MQTTReplayState{}, 0, err
	}
	if !found {
		return MQTTReplayState{Generation: current.Generation, StartAfter: current.StartAfter, Through: current.StartAfter}, 0, nil
	}
	base := r.prefix()
	if !mqttReplayBaselineContains(current, base) {
		return MQTTReplayState{}, 0, dberrors.ErrCorruptState
	}
	return base, r.position, nil
}

// RetireMQTTReplay materializes one committed retirement and removes a bounded
// prefix of local rows/meters atomically. Older retries retain the newer baseline.
// It never changes the native checkpoint, source release or consumer metadata.
func (l *ChannelLog) RetireMQTTReplay(ctx context.Context, generation string, position uint64, limit int) (MQTTReplayRetirementResult, error) {
	var empty MQTTReplayRetirementResult
	if position == 0 || limit < 1 || limit > mqttReplayMaxRows || !(MQTTSourceState{Generation: generation, Revision: 1}).valid() {
		return empty, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return empty, err
	}
	defer l.endUse()
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	l.checkpointMu.Lock()
	defer l.checkpointMu.Unlock()
	if err := ctxErr(ctx); err != nil {
		return empty, err
	}
	view := l.db.engine
	proof, found, err := loadMQTTReplayRetirementFrom(view, l.key, position)
	if err != nil {
		return empty, err
	}
	selected := mqttReplayRetired{position: position, proof: proof, deletedThrough: proof.Retirement.Anchor.StartAfter}
	if !found || selected.prefix().Generation != generation {
		return empty, dberrors.ErrConflict
	}
	old, hasOld, err := loadMQTTReplayRetired(view, l.key)
	if err != nil {
		return empty, err
	}
	current, present, err := loadMQTTReplayState(view, l.key)
	if err != nil {
		return empty, err
	}
	if hasOld {
		if !present || !mqttReplayBaselineContains(current, old.prefix()) {
			return empty, dberrors.ErrCorruptState
		}
		if old.prefix().Through >= selected.prefix().Through {
			if !validMQTTRetirementAdvance(selected.proof.Retirement, old.proof.Retirement) {
				return empty, dberrors.ErrCorruptState
			}
			selected = old
		} else {
			if selected.position <= old.position || !validMQTTRetirementAdvance(old.proof.Retirement, selected.proof.Retirement) {
				return empty, dberrors.ErrCorruptState
			}
			selected.deletedThrough = old.deletedThrough
		}
	}
	base := selected.prefix()
	if present && (current.Generation != generation || current.StartAfter != base.StartAfter) {
		return empty, dberrors.ErrCorruptState
	}
	if present && current.Through >= base.Through {
		if !mqttReplayBaselineContains(current, base) {
			return empty, dberrors.ErrCorruptState
		}
		if current.Through > base.Through {
			if _, err = mqttReplayTransferEvidence(view, l.key, current); err != nil {
				return empty, err
			}
			if err = validateMQTTReplayTail(view, l.key, current); err != nil {
				return empty, err
			}
			p := MQTTReplayAnchorProof{Anchor: selected.proof.Retirement.Anchor}
			if err = verifyMQTTRepairCovered(view, l.key, current, p); err != nil {
				return empty, err
			}
		}
	} else {
		current = base
	}
	deletedThrough, deleted, err := mqttReplayDeletionStep(ctx, view, l.key, generation, selected.deletedThrough, base.Through, limit)
	if err != nil {
		return empty, err
	}
	out := MQTTReplayRetirementResult{RetirementPosition: selected.position, Retired: base, DeletedThrough: deletedThrough, Deleted: deleted, Done: deletedThrough == base.Through}
	if hasOld && old.position == selected.position && old.deletedThrough == deletedThrough {
		if err = ctxErr(ctx); err != nil {
			return empty, err
		}
		return out, l.db.mqttStorage.release(ctx, 0)
	}
	batch := view.NewBatch()
	defer batch.Close()
	released, err := l.stageMQTTStorageRelease(ctx, batch, selected.deletedThrough+1, deletedThrough)
	if err != nil {
		return empty, err
	}
	if deletedThrough > selected.deletedThrough {
		if err = batch.DeleteRange(engine.Span{Start: mqttReplayPositionKey(l.key, generation, selected.deletedThrough+1), End: mqttReplayPositionKey(l.key, generation, deletedThrough+1)}); err != nil {
			return empty, err
		}
		if err = batch.DeleteRange(engine.Span{Start: mqttReplayMeterKey(l.key, generation, selected.deletedThrough+1), End: mqttReplayMeterKey(l.key, generation, deletedThrough+1)}); err != nil {
			return empty, err
		}
	}
	if err = batch.Set(mqttReplayRetiredKey(l.key), encodeMQTTReplayRetired(l.key, selected.position, deletedThrough)); err != nil {
		return empty, err
	}
	if err = batch.Set(mqttReplayStateKey(l.key), encodeMQTTReplayState(l.key, current)); err != nil {
		return empty, err
	}
	if err = l.stageCatalog(batch); err != nil {
		return empty, err
	}
	if err = ctxErr(ctx); err != nil {
		return empty, err
	}
	if err = l.channelEntry.commitMQTTStorageRetirement(ctx, batch, selected.proof, deletedThrough, released); err != nil {
		return empty, err
	}
	return out, l.db.mqttStorage.release(ctx, 0)
}

func mqttReplayPositionKey(key ChannelKey, generation string, position uint64) []byte {
	k := mqttReplayRowKey(key, generation, position)
	return k[:len(k)-10]
}

// mqttReplayDeletionStep reads keys only, skipping absent positions rather than
// iterating an arbitrarily long source sequence. One lookahead detects completion.
func mqttReplayDeletionStep(ctx context.Context, view messageBackupReadView, key ChannelKey, generation string, after, through uint64, limit int) (uint64, int, error) {
	if after == through {
		return through, 0, nil
	}
	start, end := mqttReplayPositionKey(key, generation, after+1), mqttReplayPositionKey(key, generation, through+1)
	iter, err := view.NewIter(engine.Span{Start: start, End: end}, engine.IterOptions{})
	if err != nil {
		return 0, 0, err
	}
	defer iter.Close()
	last, count := after, 0
	valid := iter.First()
	for valid && count < limit {
		if err = ctxErr(ctx); err != nil {
			return 0, 0, err
		}
		k := iter.Key()
		if len(k) != len(start)+10 || binary.BigEndian.Uint64(k[len(k)-10:]) != mqttReplayContentVersion || binary.BigEndian.Uint16(k[len(k)-2:]) != 0 {
			return 0, 0, dberrors.ErrCorruptState
		}
		last = binary.BigEndian.Uint64(k[len(k)-18:])
		count++
		valid = iter.Next()
	}
	if err = iter.Error(); err != nil {
		return 0, 0, err
	}
	if !valid {
		last = through
	}
	return last, count, ctxErr(ctx)
}

func (s *ChannelStore) RetireMQTTReplay(ctx context.Context, generation string, position uint64, limit int) (MQTTReplayRetirementResult, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayRetirementResult{}, err
	}
	defer s.endUse()
	out, err := s.log.RetireMQTTReplay(ctx, generation, position, limit)
	return out, toChannelError(err)
}
