package message

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

const (
	maxMessageBackupStreamFieldBytes = 256 << 20
	maxMessageBackupStreamChannels   = 1 << 20
	maxMessageBackupSystemEntries    = 1 << 20
)

type messageBackupChannelHeader struct {
	key           ChannelKey
	id            ChannelID
	checkpoint    Checkpoint
	fromExclusive uint64
	systemEntries []backupRawEntry
	messageCount  uint64
}

// ImportBackupSnapshotReader validates the complete seekable stream before
// applying it in bounded batches. A failed semantic validation never mutates
// the target message database.
func (db *MessageDB) ImportBackupSnapshotReader(ctx context.Context, source io.ReadSeeker, size int64) (BackupSnapshotStats, error) {
	if err := db.beginUse(); err != nil {
		return BackupSnapshotStats{}, err
	}
	defer db.endUse()
	if err := verifyMessageBackupStreamChecksum(source, size); err != nil {
		return BackupSnapshotStats{}, err
	}
	stats, err := parseMessageBackupStream(ctx, source, size, validateMessageBackupChannel)
	if err != nil {
		return BackupSnapshotStats{}, err
	}
	installed, err := parseMessageBackupStream(ctx, source, size, db.importMessageBackupChannelStream)
	if err != nil {
		return BackupSnapshotStats{}, err
	}
	if installed != stats {
		return BackupSnapshotStats{}, dberrors.ErrCorruptState
	}
	return installed, nil
}

func verifyMessageBackupStreamChecksum(source io.ReadSeeker, size int64) error {
	if source == nil || size < 16 {
		return dberrors.ErrCorruptValue
	}
	end, err := source.Seek(0, io.SeekEnd)
	if err != nil || end != size {
		return dberrors.ErrCorruptValue
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	checksum := crc32.NewIEEE()
	if _, err := io.CopyN(checksum, source, size-4); err != nil {
		return dberrors.ErrCorruptValue
	}
	var trailer [4]byte
	if _, err := io.ReadFull(source, trailer[:]); err != nil {
		return dberrors.ErrCorruptValue
	}
	if checksum.Sum32() != binary.BigEndian.Uint32(trailer[:]) {
		return dberrors.ErrChecksumMismatch
	}
	return nil
}

func parseMessageBackupStream(ctx context.Context, source io.ReadSeeker, size int64, visit func(context.Context, *bufio.Reader, messageBackupChannelHeader) (uint64, error)) (BackupSnapshotStats, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return BackupSnapshotStats{}, err
	}
	reader := bufio.NewReaderSize(io.LimitReader(source, size-4), 64<<10)
	var magic [4]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil || magic != messageBackupSnapshotMagic {
		return BackupSnapshotStats{}, dberrors.ErrCorruptValue
	}
	version, err := readMessageBackupStreamUint16(reader)
	if err != nil || version != messageBackupSnapshotVersion {
		return BackupSnapshotStats{}, dberrors.ErrCorruptValue
	}
	hashSlot, err := readMessageBackupStreamUint16(reader)
	if err != nil {
		return BackupSnapshotStats{}, dberrors.ErrCorruptValue
	}
	channelCount, err := readMessageBackupStreamUint32(reader)
	if err != nil || channelCount > maxMessageBackupStreamChannels {
		return BackupSnapshotStats{}, dberrors.ErrCorruptValue
	}
	stats := BackupSnapshotStats{HashSlot: hashSlot, ChannelCount: uint64(channelCount)}
	previousKey := ChannelKey("")
	for channelIndex := uint32(0); channelIndex < channelCount; channelIndex++ {
		if err := ctxErr(ctx); err != nil {
			return BackupSnapshotStats{}, err
		}
		keyString, err := readMessageBackupStreamString(reader)
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		idString, err := readMessageBackupStreamString(reader)
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		channelType, err := reader.ReadByte()
		if err != nil {
			return BackupSnapshotStats{}, dberrors.ErrCorruptValue
		}
		key := ChannelKey(keyString)
		if key == "" || idString == "" || (previousKey != "" && key <= previousKey) {
			return BackupSnapshotStats{}, dberrors.ErrCorruptValue
		}
		previousKey = key
		var checkpointBody [24]byte
		if _, err := io.ReadFull(reader, checkpointBody[:]); err != nil {
			return BackupSnapshotStats{}, dberrors.ErrCorruptValue
		}
		checkpoint, err := decodeCheckpoint(checkpointBody[:])
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		fromExclusive, err := readMessageBackupStreamUint64(reader)
		if err != nil || fromExclusive > checkpoint.HW {
			return BackupSnapshotStats{}, dberrors.ErrCorruptValue
		}
		systemCount, err := binary.ReadUvarint(reader)
		if err != nil || systemCount > maxMessageBackupSystemEntries {
			return BackupSnapshotStats{}, dberrors.ErrCorruptValue
		}
		systemEntries := make([]backupRawEntry, 0, int(systemCount))
		for systemIndex := uint64(0); systemIndex < systemCount; systemIndex++ {
			systemKey, err := readMessageBackupStreamBytes(reader)
			if err != nil {
				return BackupSnapshotStats{}, err
			}
			systemValue, err := readMessageBackupStreamBytes(reader)
			if err != nil {
				return BackupSnapshotStats{}, err
			}
			if !bytes.HasPrefix(systemKey, encodeMessageSystemAllPrefix(key)) || bytes.Equal(systemKey, encodeCheckpointKey(key)) {
				return BackupSnapshotStats{}, dberrors.ErrCorruptValue
			}
			systemEntries = append(systemEntries, backupRawEntry{Key: systemKey, Value: systemValue})
		}
		messageCount, err := binary.ReadUvarint(reader)
		if err != nil || messageCount > math.MaxInt64 {
			return BackupSnapshotStats{}, dberrors.ErrCorruptValue
		}
		header := messageBackupChannelHeader{
			key: key, id: ChannelID{ID: idString, Type: channelType}, checkpoint: checkpoint,
			fromExclusive: fromExclusive, systemEntries: systemEntries, messageCount: messageCount,
		}
		maxMessageID, err := visit(ctx, reader, header)
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		if math.MaxUint64-stats.MessageCount < messageCount {
			return BackupSnapshotStats{}, dberrors.ErrCorruptValue
		}
		stats.MessageCount += messageCount
		if maxMessageID > stats.MaxMessageID {
			stats.MaxMessageID = maxMessageID
		}
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return BackupSnapshotStats{}, dberrors.ErrCorruptValue
	}
	return stats, nil
}

func validateMessageBackupChannel(ctx context.Context, reader *bufio.Reader, header messageBackupChannelHeader) (uint64, error) {
	var previousSeq uint64
	var maxMessageID uint64
	for index := uint64(0); index < header.messageCount; index++ {
		seq, row, _, _, err := readMessageBackupStreamRow(ctx, reader, header, previousSeq)
		if err != nil {
			return 0, err
		}
		previousSeq = seq
		if row.MessageID > maxMessageID {
			maxMessageID = row.MessageID
		}
	}
	return maxMessageID, nil
}

func (db *MessageDB) importMessageBackupChannelStream(ctx context.Context, reader *bufio.Reader, header messageBackupChannelHeader) (uint64, error) {
	if current, ok, err := db.engine.Get(encodeCatalogKey(header.key)); err != nil {
		return 0, err
	} else if ok {
		currentID, err := decodeCatalogValue(current)
		if err != nil || currentID != header.id {
			return 0, dberrors.ErrConflict
		}
	}
	currentCheckpoint, currentCheckpointPresent, err := db.loadBackupImportCheckpoint(header.key)
	if err != nil {
		return 0, err
	}
	if currentCheckpointPresent && currentCheckpoint.HW > header.checkpoint.HW && currentCheckpoint.Epoch >= header.checkpoint.Epoch {
		return db.verifyInstalledBackupChannelStream(ctx, reader, header)
	}
	if currentCheckpointPresent && currentCheckpoint == header.checkpoint {
		// Exact layer replay is idempotent.
	} else if header.fromExclusive == 0 {
		if currentCheckpointPresent {
			return 0, dberrors.ErrConflict
		}
	} else if !currentCheckpointPresent || currentCheckpoint.HW != header.fromExclusive || currentCheckpoint.Epoch > header.checkpoint.Epoch {
		return 0, dberrors.ErrConflict
	}
	metadataBatch := db.engine.NewBatch()
	if err := metadataBatch.Set(encodeCatalogKey(header.key), encodeCatalogValue(header.id)); err != nil {
		_ = metadataBatch.Close()
		return 0, err
	}
	if err := metadataBatch.Set(encodeCheckpointKey(header.key), encodeCheckpoint(header.checkpoint)); err != nil {
		_ = metadataBatch.Close()
		return 0, err
	}
	for _, entry := range header.systemEntries {
		if err := metadataBatch.Set(entry.Key, entry.Value); err != nil {
			_ = metadataBatch.Close()
			return 0, err
		}
	}
	if err := metadataBatch.Commit(true); err != nil {
		_ = metadataBatch.Close()
		return 0, err
	}
	if err := metadataBatch.Close(); err != nil {
		return 0, err
	}

	entry := &channelEntry{key: header.key, id: header.id, appendKeyCache: newAppendKeyCache(header.key, header.id)}
	messageBatch := db.engine.NewBatch()
	defer func() { _ = messageBatch.Close() }()
	var previousSeq uint64
	var maxMessageID uint64
	for index := uint64(0); index < header.messageCount; index++ {
		seq, row, _, _, err := readMessageBackupStreamRow(ctx, reader, header, previousSeq)
		if err != nil {
			return 0, err
		}
		previousSeq = seq
		if row.MessageID > maxMessageID {
			maxMessageID = row.MessageID
		}
		if err := entry.stageMessageRow(messageBatch, row, entry.appendKeyCache); err != nil {
			return 0, err
		}
		if (index+1)%backupImportBatchMessages == 0 || index+1 == header.messageCount {
			if err := messageBatch.Commit(true); err != nil {
				return 0, err
			}
			if index+1 < header.messageCount {
				if err := messageBatch.Close(); err != nil {
					return 0, err
				}
				messageBatch = db.engine.NewBatch()
			}
		}
	}
	return maxMessageID, nil
}

// verifyInstalledBackupChannelStream authenticates an already-covered layer
// against the current database without mutating the newer installed checkpoint.
func (db *MessageDB) verifyInstalledBackupChannelStream(ctx context.Context, reader *bufio.Reader, header messageBackupChannelHeader) (uint64, error) {
	var previousSeq uint64
	var maxMessageID uint64
	for index := uint64(0); index < header.messageCount; index++ {
		seq, row, headerBody, payload, err := readMessageBackupStreamRow(ctx, reader, header, previousSeq)
		if err != nil {
			return 0, err
		}
		previousSeq = seq
		if row.MessageID > maxMessageID {
			maxMessageID = row.MessageID
		}
		installedHeader, ok, err := db.engine.Get(encodeMessageRowKey(header.key, seq, messageHeaderFamilyID))
		if err != nil {
			return 0, err
		}
		if !ok || !bytes.Equal(installedHeader, headerBody) {
			return 0, dberrors.ErrConflict
		}
		installedPayload, ok, err := db.engine.Get(encodeMessageRowKey(header.key, seq, messagePayloadFamilyID))
		if err != nil {
			return 0, err
		}
		if !ok || !bytes.Equal(installedPayload, payload) {
			return 0, dberrors.ErrConflict
		}
	}
	return maxMessageID, nil
}

func readMessageBackupStreamRow(ctx context.Context, reader *bufio.Reader, channel messageBackupChannelHeader, previousSeq uint64) (uint64, messageRow, []byte, []byte, error) {
	if err := ctxErr(ctx); err != nil {
		return 0, messageRow{}, nil, nil, err
	}
	seq, err := readMessageBackupStreamUint64(reader)
	if err != nil || seq == 0 || seq <= channel.fromExclusive || seq > channel.checkpoint.HW || (previousSeq != 0 && seq <= previousSeq) {
		return 0, messageRow{}, nil, nil, dberrors.ErrCorruptValue
	}
	headerBody, err := readMessageBackupStreamBytes(reader)
	if err != nil {
		return 0, messageRow{}, nil, nil, err
	}
	payload, err := readMessageBackupStreamBytes(reader)
	if err != nil {
		return 0, messageRow{}, nil, nil, err
	}
	row := messageRow{MessageSeq: seq}
	if err := decodeMessageHeader(encodeMessageRowKey(channel.key, seq, messageHeaderFamilyID), headerBody, &row); err != nil {
		return 0, messageRow{}, nil, nil, err
	}
	if err := decodeMessagePayload(encodeMessageRowKey(channel.key, seq, messagePayloadFamilyID), payload, &row); err != nil {
		return 0, messageRow{}, nil, nil, err
	}
	if row.ChannelID != channel.id.ID || row.ChannelType != channel.id.Type {
		return 0, messageRow{}, nil, nil, dberrors.ErrCorruptState
	}
	return seq, row, headerBody, payload, nil
}

func readMessageBackupStreamString(reader *bufio.Reader) (string, error) {
	value, err := readMessageBackupStreamBytes(reader)
	return string(value), err
}

func readMessageBackupStreamBytes(reader *bufio.Reader) ([]byte, error) {
	size, err := binary.ReadUvarint(reader)
	if err != nil || size > maxMessageBackupStreamFieldBytes {
		return nil, dberrors.ErrCorruptValue
	}
	value := make([]byte, int(size))
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, dberrors.ErrCorruptValue
	}
	return value, nil
}

func readMessageBackupStreamUint16(reader io.Reader) (uint16, error) {
	var value uint16
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}

func readMessageBackupStreamUint32(reader io.Reader) (uint32, error) {
	var value uint32
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}

func readMessageBackupStreamUint64(reader io.Reader) (uint64, error) {
	var value uint64
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}
