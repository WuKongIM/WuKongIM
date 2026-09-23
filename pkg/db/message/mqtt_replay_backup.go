package message

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

const mqttReplayBackupVersion uint16 = 2

type mqttReplayBackupStats struct {
	count, storedBytes, maxMessageID uint64
}

func mqttReplayBackupState(view messageBackupReadView, cut BackupChannelCut) (MQTTReplayState, bool, error) {
	s, present, err := loadMQTTReplayState(view, cut.Key)
	if err != nil || !present {
		return s, present, err
	}
	value, ok, err := view.Get(mqttSourceKey(cut.Key))
	if err != nil {
		return s, false, err
	}
	if !ok {
		return s, false, dberrors.ErrCorruptState
	}
	source, err := decodeMQTTSourceState(mqttSourceKey(cut.Key), value)
	if err != nil {
		return s, false, err
	}
	if err := validateMQTTReplayCut(s, source, cut.Checkpoint.HW); err != nil {
		return s, false, err
	}
	return s, true, nil
}

func validateMQTTReplayCut(s MQTTReplayState, source MQTTSourceState, hw uint64) error {
	if s.Generation != source.Generation || s.StartAfter != source.StartAfter || s.Through > hw || s.Through < source.CopiedThrough {
		return dberrors.ErrCorruptState
	}
	return nil
}

func selectMessageBackupVersion(ctx context.Context, view messageBackupReadView, cuts []BackupChannelCut) (uint16, error) {
	version := messageBackupSnapshotVersion
	for _, cut := range cuts {
		if err := ctxErr(ctx); err != nil {
			return 0, err
		}
		_, ok, err := mqttReplayBackupState(view, cut)
		if err != nil {
			return 0, err
		}
		if ok {
			version = mqttReplayBackupVersion
		}
	}
	return version, nil
}

// visitMQTTReplayBackup validates each immutable row against the pinned prefix.
// Memory is bounded to one row, independently of the number of subscribers.
func visitMQTTReplayBackup(ctx context.Context, view messageBackupReadView, key ChannelKey, s MQTTReplayState, visit func([]byte) error) (mqttReplayBackupStats, error) {
	var stats mqttReplayBackupStats
	current := MQTTReplayState{Generation: s.Generation, StartAfter: s.StartAfter, Through: s.StartAfter}
	for pos := s.StartAfter + 1; ; pos++ {
		if err := ctxErr(ctx); err != nil {
			return stats, err
		}
		value, ok, err := view.Get(mqttReplayRowKey(key, s.Generation, pos))
		if err != nil {
			return stats, err
		}
		if !ok {
			return stats, dberrors.ErrCorruptState
		}
		r, err := decodeMQTTReplayRecord(key, s.Generation, pos, value)
		if err != nil {
			return stats, err
		}
		current, err = extendMQTTReplayState(current, r)
		if err != nil {
			return stats, err
		}
		stats.count++
		if r.MessageID > stats.maxMessageID {
			stats.maxMessageID = r.MessageID
		}
		if visit != nil {
			if err := visit(value); err != nil {
				return stats, err
			}
		}
		if pos == s.Through {
			break
		}
	}
	if current != s {
		return stats, dberrors.ErrCorruptState
	}
	stats.storedBytes = s.TotalStoredBytes
	return stats, nil
}

func writeMQTTReplayBackup(ctx context.Context, w io.Writer, view messageBackupReadView, cut BackupChannelCut) error {
	s, ok, err := mqttReplayBackupState(view, cut)
	if err != nil {
		return err
	}
	if !ok {
		return writeBackupBytes(w, nil)
	}
	if err := writeBackupBytes(w, encodeMQTTReplayState(cut.Key, s)); err != nil {
		return err
	}
	_, err = visitMQTTReplayBackup(ctx, view, cut.Key, s, func(value []byte) error { return writeBackupBytes(w, value) })
	return err
}

// readMQTTReplayBackup accepts only bounded fields and validates the full prefix.
// install is nil during preflight and inspection. Restore commits bounded row
// batches and publishes the frontier last; incomplete restore is never coverage.
func readMQTTReplayBackup(ctx context.Context, reader *bufio.Reader, header messageBackupChannelHeader, target *MessageDB, install bool) (mqttReplayBackupStats, error) {
	var stats mqttReplayBackupStats
	value, err := readMQTTReplayBackupField(reader, 256)
	if err != nil {
		return stats, err
	}
	if len(value) == 0 {
		return stats, rejectExistingMQTTReplay(target, header.key)
	}
	s, err := decodeMQTTReplayState(header.key, value)
	if err != nil {
		return stats, err
	}
	var source MQTTSourceState
	for _, entry := range header.systemEntries {
		if bytes.Equal(entry.Key, mqttSourceKey(header.key)) {
			source, err = decodeMQTTSourceState(entry.Key, entry.Value)
			if err != nil {
				return stats, err
			}
		}
	}
	if err := validateMQTTReplayCut(s, source, header.checkpoint.HW); err != nil {
		return stats, err
	}
	var batch *engine.Batch
	if target != nil {
		old, present, err := loadMQTTReplayState(target.engine, header.key)
		if err != nil {
			return stats, err
		}
		if present && old != s {
			return stats, dberrors.ErrConflict
		}
		if install {
			batch = target.engine.NewBatch()
			defer func() { _ = batch.Close() }()
		}
	}
	current := MQTTReplayState{Generation: s.Generation, StartAfter: s.StartAfter, Through: s.StartAfter}
	batchBytes := 0
	for pos := s.StartAfter + 1; ; pos++ {
		if err := ctxErr(ctx); err != nil {
			return stats, err
		}
		encoded, err := readMQTTReplayBackupField(reader, mqttReplayMaxBytes+256)
		if err != nil {
			return stats, err
		}
		r, err := decodeMQTTReplayRecord(header.key, s.Generation, pos, encoded)
		if err != nil {
			return stats, err
		}
		current, err = extendMQTTReplayState(current, r)
		if err != nil {
			return stats, err
		}
		stats.count++
		if r.MessageID > stats.maxMessageID {
			stats.maxMessageID = r.MessageID
		}
		if target != nil {
			key := mqttReplayRowKey(header.key, s.Generation, pos)
			previous, present, err := target.engine.Get(key)
			if err != nil {
				return stats, err
			}
			if present && !bytes.Equal(previous, encoded) {
				return stats, dberrors.ErrConflict
			}
		}
		if install {
			if err := stageMQTTReplayRecord(batch, header.key, s.Generation, r, encoded); err != nil {
				return stats, err
			}
			batchBytes += len(encoded)
			if pos < s.Through && (stats.count%mqttReplayMaxRows == 0 || batchBytes >= mqttReplayMaxBytes) {
				if err := batch.Commit(true); err != nil {
					return stats, err
				}
				if err := batch.Close(); err != nil {
					return stats, err
				}
				batch = target.engine.NewBatch()
				batchBytes = 0
			}
		}
		if pos == s.Through {
			break
		}
	}
	if current != s {
		return stats, dberrors.ErrCorruptState
	}
	if install {
		if err := batch.Set(mqttReplayStateKey(header.key), value); err != nil {
			return stats, err
		}
		if err := batch.Commit(true); err != nil {
			return stats, err
		}
	}
	stats.storedBytes = s.TotalStoredBytes
	return stats, nil
}

func rejectExistingMQTTReplay(target *MessageDB, key ChannelKey) error {
	if target == nil {
		return nil
	}
	_, present, err := loadMQTTReplayState(target.engine, key)
	if err != nil {
		return err
	}
	if present {
		return dberrors.ErrConflict
	}
	return nil
}

func readMQTTReplayBackupField(reader *bufio.Reader, max int) ([]byte, error) {
	n, err := binary.ReadUvarint(reader)
	if err != nil || n > uint64(max) {
		return nil, dberrors.ErrCorruptValue
	}
	value := make([]byte, int(n))
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, dberrors.ErrCorruptValue
	}
	return value, nil
}

func addMQTTReplayBackupStats(stats *BackupSnapshotStats, replay mqttReplayBackupStats) error {
	if math.MaxUint64-stats.ReplayMessageCount < replay.count || math.MaxUint64-stats.ReplayStoredBytes < replay.storedBytes {
		return dberrors.ErrCorruptValue
	}
	stats.ReplayMessageCount += replay.count
	stats.ReplayStoredBytes += replay.storedBytes
	if replay.maxMessageID > stats.MaxMessageID {
		stats.MaxMessageID = replay.maxMessageID
	}
	return nil
}
