package message

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

var messageBackupSnapshotMagic = [4]byte{'W', 'K', 'M', 'B'}

const (
	messageBackupSnapshotVersion uint16 = 1
	backupImportBatchMessages           = 1024
)

// BackupChannelCut identifies one exact committed channel boundary selected by cluster coordination.
type BackupChannelCut struct {
	// Key is the stable channel storage partition key.
	Key ChannelKey
	// ID is the durable channel identity expected in the message catalog.
	ID ChannelID
	// Checkpoint is the authoritative committed boundary to export.
	Checkpoint Checkpoint
	// FromExclusive is the previously published committed boundary. Zero
	// materializes the retained channel state; a positive value exports only
	// committed rows after that exact base.
	FromExclusive uint64
}

// BackupSnapshotRequest selects one logical hash-slot message snapshot.
type BackupSnapshotRequest struct {
	// HashSlot identifies the logical partition represented by the stream.
	HashSlot uint16
	// Channels contains the exact channel cuts selected by the cluster layer.
	Channels []BackupChannelCut
}

// BackupSnapshotStats summarizes a decoded message backup snapshot.
type BackupSnapshotStats struct {
	// HashSlot is the logical partition encoded by the snapshot.
	HashSlot uint16
	// ChannelCount is the number of channel cuts encoded by the snapshot.
	ChannelCount uint64
	// MessageCount is the number of committed message rows encoded by the snapshot.
	MessageCount uint64
	// MaxMessageID is the greatest durable message ID encoded by the snapshot.
	MaxMessageID uint64
}

type messageBackupStream struct {
	reader io.ReadCloser
	cancel context.CancelFunc
}

func (s *messageBackupStream) Read(buffer []byte) (int, error) { return s.reader.Read(buffer) }

func (s *messageBackupStream) Close() error {
	s.cancel()
	return s.reader.Close()
}

type messageBackupReadView interface {
	Get(key []byte) ([]byte, bool, error)
	NewIter(span engine.Span, opts engine.IterOptions) (*engine.Iter, error)
}

// OpenBackupSnapshot pins and streams committed message data for exact cluster-selected channel cuts.
func (db *MessageDB) OpenBackupSnapshot(ctx context.Context, request BackupSnapshotRequest) (io.ReadCloser, error) {
	if err := db.beginUse(); err != nil {
		return nil, err
	}
	view, err := db.engine.NewSnapshot()
	db.endUse()
	if err != nil {
		return nil, err
	}
	channels, err := normalizeBackupChannelCuts(request.Channels)
	if err != nil {
		_ = view.Close()
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	streamContext, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	go func() {
		defer view.Close()
		if err := writeMessageBackupSnapshot(streamContext, writer, view, request.HashSlot, channels, nil); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		_ = writer.Close()
	}()
	return &messageBackupStream{reader: reader, cancel: cancel}, nil
}

// OpenBackupSnapshotWithStats pins and streams committed message data while
// returning exact signed-evidence inputs from the same read view.
func (db *MessageDB) OpenBackupSnapshotWithStats(ctx context.Context, request BackupSnapshotRequest) (io.ReadCloser, BackupSnapshotStats, error) {
	if err := db.beginUse(); err != nil {
		return nil, BackupSnapshotStats{}, err
	}
	view, err := db.engine.NewSnapshot()
	db.endUse()
	if err != nil {
		return nil, BackupSnapshotStats{}, err
	}
	channels, err := normalizeBackupChannelCuts(request.Channels)
	if err != nil {
		_ = view.Close()
		return nil, BackupSnapshotStats{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stats, messageCounts, err := inspectMessageBackupSnapshot(ctx, view, request.HashSlot, channels)
	if err != nil {
		_ = view.Close()
		return nil, BackupSnapshotStats{}, err
	}
	streamContext, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	go func() {
		defer view.Close()
		if err := writeMessageBackupSnapshot(streamContext, writer, view, request.HashSlot, channels, messageCounts); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		_ = writer.Close()
	}()
	return &messageBackupStream{reader: reader, cancel: cancel}, stats, nil
}

func normalizeBackupChannelCuts(channels []BackupChannelCut) ([]BackupChannelCut, error) {
	normalized := append([]BackupChannelCut(nil), channels...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Key < normalized[j].Key })
	for index, channel := range normalized {
		if channel.Key == "" || channel.ID.ID == "" {
			return nil, fmt.Errorf("%w: backup channel identity is incomplete", dberrors.ErrInvalidArgument)
		}
		if err := validateCheckpoint(channel.Checkpoint); err != nil {
			return nil, err
		}
		if channel.FromExclusive > channel.Checkpoint.HW {
			return nil, fmt.Errorf("%w: backup delta base %d exceeds hw %d", dberrors.ErrInvalidArgument, channel.FromExclusive, channel.Checkpoint.HW)
		}
		if index > 0 && normalized[index-1].Key == channel.Key {
			return nil, fmt.Errorf("%w: duplicate backup channel %q", dberrors.ErrInvalidArgument, channel.Key)
		}
	}
	return normalized, nil
}

func writeMessageBackupSnapshot(ctx context.Context, writer io.Writer, view messageBackupReadView, hashSlot uint16, channels []BackupChannelCut, messageCounts []uint64) error {
	if messageCounts != nil && len(messageCounts) != len(channels) {
		return dberrors.ErrCorruptState
	}
	checksum := crc32.NewIEEE()
	payload := io.MultiWriter(writer, checksum)
	header := make([]byte, 0, 12)
	header = append(header, messageBackupSnapshotMagic[:]...)
	header = binary.BigEndian.AppendUint16(header, messageBackupSnapshotVersion)
	header = binary.BigEndian.AppendUint16(header, hashSlot)
	header = binary.BigEndian.AppendUint32(header, uint32(len(channels)))
	if _, err := payload.Write(header); err != nil {
		return err
	}
	for index, channel := range channels {
		var messageCount *uint64
		if messageCounts != nil {
			messageCount = &messageCounts[index]
		}
		if err := writeBackupChannel(ctx, payload, view, channel, messageCount); err != nil {
			return err
		}
	}
	trailer := make([]byte, 4)
	binary.BigEndian.PutUint32(trailer, checksum.Sum32())
	_, err := writer.Write(trailer)
	return err
}

func writeBackupChannel(ctx context.Context, writer io.Writer, view messageBackupReadView, channel BackupChannelCut, knownMessageCount *uint64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	catalogValue, catalogPresent, err := view.Get(encodeCatalogKey(channel.Key))
	if err != nil {
		return err
	}
	if catalogPresent {
		catalogID, err := decodeCatalogValue(catalogValue)
		if err != nil {
			return err
		}
		if catalogID != channel.ID {
			return fmt.Errorf("%w: backup channel catalog identity mismatch", dberrors.ErrCorruptState)
		}
	}
	leo, err := snapshotChannelLEO(view, channel.Key)
	if err != nil {
		return err
	}
	if retention, ok, err := snapshotRetentionState(view, channel.Key); err != nil {
		return err
	} else if ok && retention.RetainedMaxSeq > leo {
		leo = retention.RetainedMaxSeq
	}
	if channel.Checkpoint.HW > leo {
		return fmt.Errorf("%w: backup checkpoint hw %d exceeds pinned leo %d for %q", dberrors.ErrCorruptState, channel.Checkpoint.HW, leo, channel.Key)
	}
	if err := writeBackupString(writer, string(channel.Key)); err != nil {
		return err
	}
	if err := writeBackupString(writer, channel.ID.ID); err != nil {
		return err
	}
	if _, err := writer.Write([]byte{channel.ID.Type}); err != nil {
		return err
	}
	if _, err := writer.Write(encodeCheckpoint(channel.Checkpoint)); err != nil {
		return err
	}
	if _, err := writer.Write(binary.BigEndian.AppendUint64(nil, channel.FromExclusive)); err != nil {
		return err
	}
	systemEntries, err := snapshotBackupSystemEntries(ctx, view, channel.Key, channel.Checkpoint.HW)
	if err != nil {
		return err
	}
	if err := writeBackupUvarint(writer, uint64(len(systemEntries))); err != nil {
		return err
	}
	for _, entry := range systemEntries {
		if err := writeBackupBytes(writer, entry.Key); err != nil {
			return err
		}
		if err := writeBackupBytes(writer, entry.Value); err != nil {
			return err
		}
	}
	messageCount := uint64(0)
	if knownMessageCount == nil {
		messageCount, err = countBackupMessages(ctx, view, channel.Key, channel.FromExclusive, channel.Checkpoint.HW)
		if err != nil {
			return err
		}
	} else {
		messageCount = *knownMessageCount
	}
	if err := writeBackupUvarint(writer, messageCount); err != nil {
		return err
	}
	return visitBackupMessages(ctx, view, channel.Key, channel.FromExclusive, channel.Checkpoint.HW, func(seq uint64, header, payload []byte) error {
		buffer := binary.BigEndian.AppendUint64(nil, seq)
		if _, err := writer.Write(buffer); err != nil {
			return err
		}
		if err := writeBackupBytes(writer, header); err != nil {
			return err
		}
		return writeBackupBytes(writer, payload)
	})
}

func inspectMessageBackupSnapshot(ctx context.Context, view messageBackupReadView, hashSlot uint16, channels []BackupChannelCut) (BackupSnapshotStats, []uint64, error) {
	stats := BackupSnapshotStats{HashSlot: hashSlot, ChannelCount: uint64(len(channels))}
	counts := make([]uint64, len(channels))
	for index, channel := range channels {
		catalogValue, present, err := view.Get(encodeCatalogKey(channel.Key))
		if err != nil {
			return BackupSnapshotStats{}, nil, err
		}
		if present {
			catalogID, err := decodeCatalogValue(catalogValue)
			if err != nil || catalogID != channel.ID {
				return BackupSnapshotStats{}, nil, fmt.Errorf("%w: backup channel catalog identity mismatch", dberrors.ErrCorruptState)
			}
		}
		leo, err := snapshotChannelLEO(view, channel.Key)
		if err != nil {
			return BackupSnapshotStats{}, nil, err
		}
		if retention, ok, err := snapshotRetentionState(view, channel.Key); err != nil {
			return BackupSnapshotStats{}, nil, err
		} else if ok && retention.RetainedMaxSeq > leo {
			leo = retention.RetainedMaxSeq
		}
		if channel.Checkpoint.HW > leo {
			return BackupSnapshotStats{}, nil, fmt.Errorf("%w: backup checkpoint hw %d exceeds pinned leo %d for %q", dberrors.ErrCorruptState, channel.Checkpoint.HW, leo, channel.Key)
		}
		var maxMessageID uint64
		err = visitBackupMessages(ctx, view, channel.Key, channel.FromExclusive, channel.Checkpoint.HW, func(seq uint64, header, _ []byte) error {
			row := messageRow{MessageSeq: seq}
			if err := decodeMessageHeader(encodeMessageRowKey(channel.Key, seq, messageHeaderFamilyID), header, &row); err != nil {
				return err
			}
			counts[index]++
			if row.MessageID > maxMessageID {
				maxMessageID = row.MessageID
			}
			return nil
		})
		if err != nil {
			return BackupSnapshotStats{}, nil, err
		}
		if math.MaxUint64-stats.MessageCount < counts[index] {
			return BackupSnapshotStats{}, nil, dberrors.ErrCorruptState
		}
		stats.MessageCount += counts[index]
		if maxMessageID > stats.MaxMessageID {
			stats.MaxMessageID = maxMessageID
		}
	}
	return stats, counts, nil
}

type backupRawEntry struct {
	Key   []byte
	Value []byte
}

func snapshotBackupSystemEntries(ctx context.Context, view messageBackupReadView, channelKey ChannelKey, hw uint64) ([]backupRawEntry, error) {
	prefix := encodeMessageSystemAllPrefix(channelKey)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := view.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	checkpointKey := encodeCheckpointKey(channelKey)
	historyPrefix := encodeHistoryPrefix(channelKey)
	entries := make([]backupRawEntry, 0, 8)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		key := iter.Key()
		if bytes.Equal(key, checkpointKey) {
			continue
		}
		if bytes.HasPrefix(key, historyPrefix) {
			value, err := iter.Value()
			if err != nil {
				return nil, err
			}
			point, err := decodeEpochPointFromKeyValue(channelKey, key, func() ([]byte, error) { return value, nil })
			if err != nil {
				return nil, err
			}
			if point.StartOffset > hw {
				continue
			}
			entries = append(entries, backupRawEntry{Key: key, Value: value})
			continue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		entries = append(entries, backupRawEntry{Key: key, Value: value})
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return entries, nil
}

func encodeMessageSystemAllPrefix(channelKey ChannelKey) []byte {
	var builder keycodec.Builder
	return append(builder.Reset().Domain(keycodec.DomainMessage).Partition(keycodec.PartitionChannel, channelPartitionID(channelKey)).Key(), byte(keycodec.SpaceSystem))
}

func snapshotRetentionState(view messageBackupReadView, channelKey ChannelKey) (RetentionState, bool, error) {
	value, ok, err := view.Get(encodeRetentionStateKey(channelKey))
	if err != nil || !ok {
		return RetentionState{}, ok, err
	}
	state, err := decodeRetentionState(value)
	return state, err == nil, err
}

func snapshotChannelLEO(view messageBackupReadView, channelKey ChannelKey) (uint64, error) {
	prefix := encodeMessageRowPrefix(channelKey)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := view.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	for ok := iter.Last(); ok; ok = iter.Prev() {
		seq, family, valid := decodeMessageRowKey(channelKey, iter.Key())
		if valid && family == messageHeaderFamilyID {
			return seq, nil
		}
	}
	return 0, iter.Error()
}

func countBackupMessages(ctx context.Context, view messageBackupReadView, channelKey ChannelKey, fromExclusive, hw uint64) (uint64, error) {
	var count uint64
	err := visitBackupMessages(ctx, view, channelKey, fromExclusive, hw, func(uint64, []byte, []byte) error {
		count++
		return nil
	})
	return count, err
}

func visitBackupMessages(ctx context.Context, view messageBackupReadView, channelKey ChannelKey, fromExclusive, hw uint64, visit func(uint64, []byte, []byte) error) error {
	if hw == 0 || fromExclusive == hw {
		return nil
	}
	prefix := encodeMessageRowPrefix(channelKey)
	span := keycodec.NewPrefixSpan(prefix)
	start := encodeMessageRowKey(channelKey, fromExclusive+1, messageHeaderFamilyID)
	iter, err := view.NewIter(engine.Span{Start: start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return err
	}
	defer iter.Close()
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		key := iter.Key()
		seq, family, valid := decodeMessageRowKey(channelKey, key)
		if !valid {
			return fmt.Errorf("%w: invalid message row key", dberrors.ErrCorruptValue)
		}
		if seq > hw {
			break
		}
		if family != messageHeaderFamilyID {
			continue
		}
		header, err := iter.Value()
		if err != nil {
			return err
		}
		payload, ok, err := view.Get(encodeMessageRowKey(channelKey, seq, messagePayloadFamilyID))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: message %d payload is missing", dberrors.ErrCorruptState, seq)
		}
		if err := visit(seq, header, payload); err != nil {
			return err
		}
	}
	return iter.Error()
}

// ImportBackupSnapshot verifies and installs one portable message snapshot into a restore target.
func (db *MessageDB) ImportBackupSnapshot(ctx context.Context, data []byte) (BackupSnapshotStats, error) {
	if err := db.beginUse(); err != nil {
		return BackupSnapshotStats{}, err
	}
	defer db.endUse()
	if len(data) < 16 {
		return BackupSnapshotStats{}, dberrors.ErrCorruptValue
	}
	payload := data[:len(data)-4]
	wantChecksum := binary.BigEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(payload) != wantChecksum {
		return BackupSnapshotStats{}, dberrors.ErrChecksumMismatch
	}
	reader := bytes.NewReader(payload)
	var magic [4]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil || magic != messageBackupSnapshotMagic {
		return BackupSnapshotStats{}, dberrors.ErrCorruptValue
	}
	version, err := readBackupUint16(reader)
	if err != nil || version != messageBackupSnapshotVersion {
		return BackupSnapshotStats{}, dberrors.ErrCorruptValue
	}
	hashSlot, err := readBackupUint16(reader)
	if err != nil {
		return BackupSnapshotStats{}, err
	}
	channelCount, err := readBackupUint32(reader)
	if err != nil {
		return BackupSnapshotStats{}, err
	}
	stats := BackupSnapshotStats{HashSlot: hashSlot, ChannelCount: uint64(channelCount)}
	previousKey := ChannelKey("")
	for index := uint32(0); index < channelCount; index++ {
		if err := ctxErr(ctx); err != nil {
			return BackupSnapshotStats{}, err
		}
		keyString, err := readBackupString(reader)
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		key := ChannelKey(keyString)
		idString, err := readBackupString(reader)
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		channelType, err := reader.ReadByte()
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		if key == "" || idString == "" || (previousKey != "" && key <= previousKey) {
			return BackupSnapshotStats{}, dberrors.ErrCorruptValue
		}
		previousKey = key
		checkpointBody := make([]byte, 24)
		if _, err := io.ReadFull(reader, checkpointBody); err != nil {
			return BackupSnapshotStats{}, err
		}
		checkpoint, err := decodeCheckpoint(checkpointBody)
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		fromExclusive, err := readBackupUint64(reader)
		if err != nil || fromExclusive > checkpoint.HW {
			return BackupSnapshotStats{}, dberrors.ErrCorruptValue
		}
		id := ChannelID{ID: idString, Type: channelType}
		systemCount, err := readBackupUvarint(reader)
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		systemEntries := make([]backupRawEntry, 0, systemCount)
		for systemIndex := uint64(0); systemIndex < systemCount; systemIndex++ {
			systemKey, err := readBackupBytes(reader)
			if err != nil {
				return BackupSnapshotStats{}, err
			}
			systemValue, err := readBackupBytes(reader)
			if err != nil {
				return BackupSnapshotStats{}, err
			}
			if !bytes.HasPrefix(systemKey, encodeMessageSystemAllPrefix(key)) || bytes.Equal(systemKey, encodeCheckpointKey(key)) {
				return BackupSnapshotStats{}, dberrors.ErrCorruptValue
			}
			systemEntries = append(systemEntries, backupRawEntry{Key: systemKey, Value: systemValue})
		}
		messageCount, err := readBackupUvarint(reader)
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		maxMessageID, err := db.importBackupChannel(ctx, reader, key, id, fromExclusive, checkpoint, systemEntries, messageCount)
		if err != nil {
			return BackupSnapshotStats{}, err
		}
		stats.MessageCount += messageCount
		if maxMessageID > stats.MaxMessageID {
			stats.MaxMessageID = maxMessageID
		}
	}
	if reader.Len() != 0 {
		return BackupSnapshotStats{}, dberrors.ErrCorruptValue
	}
	return stats, nil
}

func (db *MessageDB) importBackupChannel(ctx context.Context, reader *bytes.Reader, key ChannelKey, id ChannelID, fromExclusive uint64, checkpoint Checkpoint, systemEntries []backupRawEntry, messageCount uint64) (uint64, error) {
	if current, ok, err := db.engine.Get(encodeCatalogKey(key)); err != nil {
		return 0, err
	} else if ok {
		currentID, err := decodeCatalogValue(current)
		if err != nil || currentID != id {
			return 0, dberrors.ErrConflict
		}
	}
	currentCheckpoint, currentCheckpointPresent, err := db.loadBackupImportCheckpoint(key)
	if err != nil {
		return 0, err
	}
	if currentCheckpointPresent && currentCheckpoint == checkpoint {
		// Reapplying an already installed layer is idempotent.
	} else if fromExclusive == 0 {
		if currentCheckpointPresent {
			return 0, dberrors.ErrConflict
		}
	} else if !currentCheckpointPresent || currentCheckpoint.HW != fromExclusive || currentCheckpoint.Epoch > checkpoint.Epoch {
		return 0, dberrors.ErrConflict
	}
	entry := &channelEntry{key: key, id: id, appendKeyCache: newAppendKeyCache(key, id)}
	batch := db.engine.NewBatch()
	if err := batch.Set(encodeCatalogKey(key), encodeCatalogValue(id)); err != nil {
		batch.Close()
		return 0, err
	}
	if err := batch.Set(encodeCheckpointKey(key), encodeCheckpoint(checkpoint)); err != nil {
		batch.Close()
		return 0, err
	}
	for _, systemEntry := range systemEntries {
		if err := batch.Set(systemEntry.Key, systemEntry.Value); err != nil {
			batch.Close()
			return 0, err
		}
	}
	if err := batch.Commit(true); err != nil {
		batch.Close()
		return 0, err
	}
	if err := batch.Close(); err != nil {
		return 0, err
	}
	var previousSeq uint64
	var maxMessageID uint64
	messageBatch := db.engine.NewBatch()
	defer func() { _ = messageBatch.Close() }()
	for index := uint64(0); index < messageCount; index++ {
		if err := ctxErr(ctx); err != nil {
			return 0, err
		}
		seq, err := readBackupUint64(reader)
		if err != nil {
			return 0, err
		}
		if seq == 0 || seq <= fromExclusive || seq > checkpoint.HW || (previousSeq != 0 && seq <= previousSeq) {
			return 0, dberrors.ErrCorruptValue
		}
		previousSeq = seq
		header, err := readBackupBytes(reader)
		if err != nil {
			return 0, err
		}
		payload, err := readBackupBytes(reader)
		if err != nil {
			return 0, err
		}
		row := messageRow{MessageSeq: seq}
		if err := decodeMessageHeader(encodeMessageRowKey(key, seq, messageHeaderFamilyID), header, &row); err != nil {
			return 0, err
		}
		if err := decodeMessagePayload(encodeMessageRowKey(key, seq, messagePayloadFamilyID), payload, &row); err != nil {
			return 0, err
		}
		if row.ChannelID != id.ID || row.ChannelType != id.Type {
			return 0, dberrors.ErrCorruptState
		}
		if row.MessageID > maxMessageID {
			maxMessageID = row.MessageID
		}
		if err := entry.stageMessageRow(messageBatch, row, entry.appendKeyCache); err != nil {
			return 0, err
		}
		flush := (index+1)%backupImportBatchMessages == 0 || index+1 == messageCount
		if flush {
			if err := messageBatch.Commit(true); err != nil {
				return 0, err
			}
			if index+1 < messageCount {
				if err := messageBatch.Close(); err != nil {
					return 0, err
				}
				messageBatch = db.engine.NewBatch()
			}
		}
	}
	return maxMessageID, nil
}

func (db *MessageDB) loadBackupImportCheckpoint(key ChannelKey) (Checkpoint, bool, error) {
	value, ok, err := db.engine.Get(encodeCheckpointKey(key))
	if err != nil || !ok {
		return Checkpoint{}, ok, err
	}
	checkpoint, err := decodeCheckpoint(value)
	return checkpoint, err == nil, err
}

func writeBackupString(writer io.Writer, value string) error {
	return writeBackupBytes(writer, []byte(value))
}

func writeBackupBytes(writer io.Writer, value []byte) error {
	if err := writeBackupUvarint(writer, uint64(len(value))); err != nil {
		return err
	}
	_, err := writer.Write(value)
	return err
}

func writeBackupUvarint(writer io.Writer, value uint64) error {
	buffer := binary.AppendUvarint(nil, value)
	_, err := writer.Write(buffer)
	return err
}

func readBackupString(reader *bytes.Reader) (string, error) {
	value, err := readBackupBytes(reader)
	return string(value), err
}

func readBackupBytes(reader *bytes.Reader) ([]byte, error) {
	size, err := readBackupUvarint(reader)
	if err != nil || size > uint64(reader.Len()) {
		return nil, dberrors.ErrCorruptValue
	}
	value := make([]byte, int(size))
	_, err = io.ReadFull(reader, value)
	return value, err
}

func readBackupUvarint(reader *bytes.Reader) (uint64, error) {
	value, err := binary.ReadUvarint(reader)
	if err != nil {
		return 0, dberrors.ErrCorruptValue
	}
	return value, nil
}

func readBackupUint16(reader *bytes.Reader) (uint16, error) {
	var value uint16
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}

func readBackupUint32(reader *bytes.Reader) (uint32, error) {
	var value uint32
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}

func readBackupUint64(reader *bytes.Reader) (uint64, error) {
	var value uint64
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}
