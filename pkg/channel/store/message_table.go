package store

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

// messageTable provides channel-scoped structured message row storage.
type messageTable struct {
	channelKey channel.ChannelKey
	db         *pebble.DB
	// appendLookupKey is a write-path scratch key buffer guarded by ChannelStore.writeMu.
	appendLookupKey []byte
}

type messageIdempotencyKey struct {
	fromUID     string
	clientMsgNo string
}

type messageAppendMode uint8

const (
	messageAppendStrict messageAppendMode = iota
	messageAppendTrustedContiguous
)

func (t *messageTable) append(writeBatch *pebble.Batch, rows []messageRow) error {
	return t.appendWithMode(writeBatch, rows, messageAppendStrict)
}

// appendTrustedContiguous writes rows whose caller has already proven they are
// the next contiguous channel log entries. It still rejects duplicate indexes
// inside the batch, but avoids per-row Pebble reads for existing index checks.
func (t *messageTable) appendTrustedContiguous(writeBatch *pebble.Batch, rows []messageRow) error {
	return t.appendWithMode(writeBatch, rows, messageAppendTrustedContiguous)
}

func (t *messageTable) appendWithMode(writeBatch *pebble.Batch, rows []messageRow, mode messageAppendMode) error {
	if err := t.validate(); err != nil {
		return err
	}
	if writeBatch == nil {
		return channel.ErrInvalidArgument
	}
	if len(rows) == 1 {
		return t.appendRow(writeBatch, rows[0], nil, nil, mode)
	}

	seenMessageIDs := make(map[uint64]struct{}, len(rows))
	seenIdempotencyKeys := make(map[messageIdempotencyKey]struct{}, len(rows))
	for _, row := range rows {
		if err := t.appendRow(writeBatch, row, seenMessageIDs, seenIdempotencyKeys, mode); err != nil {
			return err
		}
	}
	return nil
}

func (m messageAppendMode) checkExistingIndexes() bool {
	return m != messageAppendTrustedContiguous
}

func (t *messageTable) appendRow(writeBatch *pebble.Batch, row messageRow, seenMessageIDs map[uint64]struct{}, seenIdempotencyKeys map[messageIdempotencyKey]struct{}, mode messageAppendMode) error {
	if row.MessageSeq == 0 {
		return channel.ErrInvalidArgument
	}
	if seenMessageIDs != nil {
		if _, ok := seenMessageIDs[row.MessageID]; ok {
			return channel.ErrCorruptState
		}
		seenMessageIDs[row.MessageID] = struct{}{}
	}

	if mode.checkExistingIndexes() {
		if existingSeq, ok, err := t.lookupMessageIDSeqForAppend(row.MessageID); err != nil {
			return err
		} else if ok && existingSeq != row.MessageSeq {
			return channel.ErrCorruptState
		}
	}

	if err := setMessageFamiliesDeferred(writeBatch, t.channelKey, row); err != nil {
		return err
	}
	if err := setMessageIDIndexValue(writeBatch, t.channelKey, row.MessageID, row.MessageSeq); err != nil {
		return err
	}
	if row.ClientMsgNo != "" {
		if err := setMessageClientMsgNoIndexValue(writeBatch, t.channelKey, row.ClientMsgNo, row.MessageSeq); err != nil {
			return err
		}
	}
	if row.FromUID != "" && row.ClientMsgNo != "" {
		key := messageIdempotencyKey{fromUID: row.FromUID, clientMsgNo: row.ClientMsgNo}
		if seenIdempotencyKeys != nil {
			if _, ok := seenIdempotencyKeys[key]; ok {
				return channel.ErrCorruptState
			}
			seenIdempotencyKeys[key] = struct{}{}
		}
		if mode.checkExistingIndexes() {
			if existing, ok, err := t.lookupIdempotency(row.FromUID, row.ClientMsgNo); err != nil {
				return err
			} else if ok && existing.MessageSeq != row.MessageSeq {
				return channel.ErrCorruptState
			}
		}
		if err := setMessageIdempotencyIndexValue(writeBatch, t.channelKey, row); err != nil {
			return err
		}
	}
	return nil
}

func setTableStateValue(writeBatch *pebble.Batch, channelKey channel.ChannelKey, primaryKey uint64, familyID uint16, value []byte) error {
	op := writeBatch.SetDeferred(encodedTableStateKeyLen(channelKey), len(value))
	appendTableStateKeyTo(op.Key[:0], channelKey, TableIDMessage, primaryKey, familyID)
	copy(op.Value, value)
	return op.Finish()
}

func setMessageFamiliesDeferred(writeBatch *pebble.Batch, channelKey channel.ChannelKey, row messageRow) error {
	if err := row.validate(); err != nil {
		return err
	}
	table := canonicalMessageTable()
	if len(table.Families) != 2 {
		return channel.ErrInvalidArgument
	}
	payloadHash := row.PayloadHash
	if payloadHash == 0 {
		payloadHash = hashMessagePayload(row.Payload)
	}
	if err := setMessageFamilyDeferred(writeBatch, channelKey, row, messagePrimaryFamilyID, table.Families[0], payloadHash); err != nil {
		return err
	}
	return setMessageFamilyDeferred(writeBatch, channelKey, row, messagePayloadFamilyID, table.Families[1], payloadHash)
}

func setMessageFamilyDeferred(writeBatch *pebble.Batch, channelKey channel.ChannelKey, row messageRow, familyID uint16, family ColumnFamilyDesc, payloadHash uint64) error {
	valueLen, err := encodedMessageFamilyPayloadSize(family, row, payloadHash)
	if err != nil {
		return err
	}
	op := writeBatch.SetDeferred(encodedTableStateKeyLen(channelKey), valueLen)
	appendTableStateKeyTo(op.Key[:0], channelKey, TableIDMessage, row.MessageSeq, familyID)
	encoded, err := appendMessageFamilyPayloadTo(op.Value[:0], family, row, payloadHash)
	if err != nil {
		return err
	}
	if len(encoded) != valueLen {
		return channel.ErrCorruptState
	}
	return op.Finish()
}

func setMessageIDIndexValue(writeBatch *pebble.Batch, channelKey channel.ChannelKey, messageID uint64, seq uint64) error {
	op := writeBatch.SetDeferred(encodedTableIndexPrefixLen(channelKey)+8, 8)
	key := appendTableIndexPrefixTo(op.Key[:0], channelKey, TableIDMessage, messageIndexIDMessageID)
	binary.BigEndian.PutUint64(key[len(key):len(key)+8], messageID)
	binary.BigEndian.PutUint64(op.Value, seq)
	return op.Finish()
}

func setMessageClientMsgNoIndexValue(writeBatch *pebble.Batch, channelKey channel.ChannelKey, clientMsgNo string, seq uint64) error {
	keyLen := encodedTableIndexPrefixLen(channelKey) + uvarintLen(uint64(len(clientMsgNo))) + len(clientMsgNo) + 8
	op := writeBatch.SetDeferred(keyLen, 8)
	key := appendTableIndexPrefixTo(op.Key[:0], channelKey, TableIDMessage, messageIndexIDClientMsgNo)
	key = appendKeyString(key, clientMsgNo)
	binary.BigEndian.PutUint64(key[len(key):len(key)+8], seq)
	binary.BigEndian.PutUint64(op.Value, seq)
	return op.Finish()
}

func setMessageIdempotencyIndexValue(writeBatch *pebble.Batch, channelKey channel.ChannelKey, row messageRow) error {
	if err := row.validate(); err != nil {
		return err
	}
	payloadHash := row.PayloadHash
	if payloadHash == 0 {
		payloadHash = hashMessagePayload(row.Payload)
	}
	keyLen := encodedTableIndexPrefixLen(channelKey) +
		uvarintLen(uint64(len(row.FromUID))) + len(row.FromUID) +
		uvarintLen(uint64(len(row.ClientMsgNo))) + len(row.ClientMsgNo)
	op := writeBatch.SetDeferred(keyLen, 24)
	key := appendTableIndexPrefixTo(op.Key[:0], channelKey, TableIDMessage, messageIndexIDFromUIDClientMsgNo)
	key = appendKeyString(key, row.FromUID)
	appendKeyString(key, row.ClientMsgNo)
	binary.BigEndian.PutUint64(op.Value[0:8], row.MessageSeq)
	binary.BigEndian.PutUint64(op.Value[8:16], row.MessageID)
	binary.BigEndian.PutUint64(op.Value[16:24], payloadHash)
	return op.Finish()
}

func (t *messageTable) getBySeq(seq uint64) (messageRow, bool, error) {
	if err := t.validate(); err != nil {
		return messageRow{}, false, err
	}
	if seq == 0 {
		return messageRow{}, false, channel.ErrInvalidArgument
	}

	primary, okPrimary, err := t.getValue(encodeTableStateKey(t.channelKey, TableIDMessage, seq, messagePrimaryFamilyID))
	if err != nil {
		return messageRow{}, false, err
	}
	payload, okPayload, err := t.getValue(encodeTableStateKey(t.channelKey, TableIDMessage, seq, messagePayloadFamilyID))
	if err != nil {
		return messageRow{}, false, err
	}
	if !okPrimary && !okPayload {
		return messageRow{}, false, nil
	}
	if !okPrimary || !okPayload {
		return messageRow{}, false, channel.ErrCorruptState
	}
	row, err := decodeMessageFamilies(seq, primary, payload)
	if err != nil {
		return messageRow{}, false, err
	}
	if row.PayloadHash != hashMessagePayload(row.Payload) {
		return messageRow{}, false, channel.ErrCorruptState
	}
	return row, true, nil
}

func (t *messageTable) getByMessageID(messageID uint64) (messageRow, bool, error) {
	if err := t.validate(); err != nil {
		return messageRow{}, false, err
	}
	if messageID == 0 {
		return messageRow{}, false, channel.ErrInvalidArgument
	}

	seq, ok, err := t.lookupMessageIDSeq(messageID)
	if err != nil || !ok {
		return messageRow{}, ok, err
	}
	row, ok, err := t.getBySeq(seq)
	if err != nil || !ok {
		if err != nil {
			return messageRow{}, false, err
		}
		return messageRow{}, false, channel.ErrCorruptState
	}
	if row.MessageID != messageID {
		return messageRow{}, false, channel.ErrCorruptState
	}
	return row, true, nil
}

// compatibilityRecordBySeq materializes one compatibility record without
// building the generic scan row batch.
func (t *messageTable) compatibilityRecordBySeq(seq uint64) (channel.Record, bool, error) {
	if err := t.validate(); err != nil {
		return channel.Record{}, false, err
	}
	if seq == 0 {
		return channel.Record{}, false, channel.ErrInvalidArgument
	}

	key := encodeTableStateKey(t.channelKey, TableIDMessage, seq, messagePrimaryFamilyID)
	primary, primaryCloser, okPrimary, err := t.getBorrowedValue(key)
	if err != nil {
		return channel.Record{}, false, err
	}
	if okPrimary {
		defer primaryCloser.Close()
	}
	binary.BigEndian.PutUint16(key[len(key)-2:], messagePayloadFamilyID)
	payload, payloadCloser, okPayload, err := t.getBorrowedValue(key)
	if err != nil {
		return channel.Record{}, false, err
	}
	if okPayload {
		defer payloadCloser.Close()
	}
	if !okPrimary && !okPayload {
		return channel.Record{}, false, nil
	}
	if !okPrimary || !okPayload {
		return channel.Record{}, false, channel.ErrCorruptState
	}
	row, err := decodeMessageFamilies(seq, primary, payload)
	if err != nil {
		return channel.Record{}, false, err
	}
	if row.PayloadHash != hashMessagePayload(row.Payload) {
		return channel.Record{}, false, channel.ErrCorruptState
	}
	record, err := row.toCompatibilityRecord()
	if err != nil {
		return channel.Record{}, false, err
	}
	record.ID = row.MessageID
	record.Index = row.MessageSeq
	return record, true, nil
}

func (t *messageTable) scanBySeq(fromSeq uint64, limit int, maxBytes int) ([]messageRow, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	if limit <= 0 || maxBytes <= 0 {
		return nil, nil
	}
	if fromSeq == 0 {
		fromSeq = 1
	}

	prefix := encodeTableStatePrefix(t.channelKey, TableIDMessage)
	iter, err := t.db.NewIter(&pebble.IterOptions{
		LowerBound: encodeTableStateKey(t.channelKey, TableIDMessage, fromSeq, messagePrimaryFamilyID),
		UpperBound: keyUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	rows := make([]messageRow, 0, minInt(limit, logScanInitialCapacity))
	total := 0
	table := canonicalMessageTable()
	primaryFamily := table.Families[0]
	payloadFamily := table.Families[1]
	var (
		pendingSeq  uint64
		pendingRow  messageRow
		havePrimary bool
	)
scanLoop:
	for valid := iter.First(); valid && len(rows) < limit; valid = iter.Next() {
		seq, familyID, err := decodeTableStateKey(iter.Key(), t.channelKey, TableIDMessage)
		if err != nil {
			return nil, err
		}
		switch familyID {
		case messagePrimaryFamilyID:
			if havePrimary {
				return nil, channel.ErrCorruptState
			}
			pendingSeq = seq
			pendingRow = messageRow{MessageSeq: seq}
			if err := decodeMessageFamilyInto(&pendingRow, primaryFamily, iter.Value()); err != nil {
				return nil, err
			}
			havePrimary = true
		case messagePayloadFamilyID:
			if !havePrimary || pendingSeq != seq {
				return nil, channel.ErrCorruptState
			}
			if err := decodeMessageFamilyInto(&pendingRow, payloadFamily, iter.Value()); err != nil {
				return nil, err
			}
			row := pendingRow
			pendingSeq = 0
			pendingRow = messageRow{}
			havePrimary = false
			if err := validateMaterializedMessageRow(row); err != nil {
				return nil, err
			}
			size, err := row.compatibilityEncodedRecordSize()
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 && total+size > maxBytes {
				break scanLoop
			}
			rows = append(rows, row)
			total += size
		default:
			return nil, channel.ErrCorruptState
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	if havePrimary {
		return nil, channel.ErrCorruptState
	}
	return rows, nil
}

func (t *messageTable) scanBySeqReverse(fromSeq uint64, limit int, maxBytes int) ([]messageRow, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	if limit <= 0 || maxBytes <= 0 || fromSeq == 0 {
		return nil, nil
	}

	prefix := encodeTableStatePrefix(t.channelKey, TableIDMessage)
	upperBound := keyUpperBound(prefix)
	seekKey := upperBound
	if fromSeq < math.MaxUint64 {
		seekKey = encodeTableStateKey(t.channelKey, TableIDMessage, fromSeq+1, messagePrimaryFamilyID)
	}

	iter, err := t.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upperBound})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	rows := make([]messageRow, 0, minInt(limit, logScanInitialCapacity))
	total := 0
	table := canonicalMessageTable()
	primaryFamily := table.Families[0]
	payloadFamily := table.Families[1]
	var (
		pendingSeq  uint64
		pendingRow  messageRow
		havePayload bool
	)
scanLoop:
	for valid := iter.SeekLT(seekKey); valid && len(rows) < limit; valid = iter.Prev() {
		seq, familyID, err := decodeTableStateKey(iter.Key(), t.channelKey, TableIDMessage)
		if err != nil {
			return nil, err
		}
		switch familyID {
		case messagePayloadFamilyID:
			if havePayload {
				return nil, channel.ErrCorruptState
			}
			pendingSeq = seq
			pendingRow = messageRow{MessageSeq: seq}
			if err := decodeMessageFamilyInto(&pendingRow, payloadFamily, iter.Value()); err != nil {
				return nil, err
			}
			havePayload = true
		case messagePrimaryFamilyID:
			if !havePayload || pendingSeq != seq {
				return nil, channel.ErrCorruptState
			}
			if err := decodeMessageFamilyInto(&pendingRow, primaryFamily, iter.Value()); err != nil {
				return nil, err
			}
			row := pendingRow
			pendingSeq = 0
			pendingRow = messageRow{}
			havePayload = false
			if err := validateMaterializedMessageRow(row); err != nil {
				return nil, err
			}
			size, err := row.compatibilityEncodedRecordSize()
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 && total+size > maxBytes {
				break scanLoop
			}
			rows = append(rows, row)
			total += size
		default:
			return nil, channel.ErrCorruptState
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	if havePayload {
		return nil, channel.ErrCorruptState
	}
	return rows, nil
}

func (t *messageTable) scanByClientMsgNo(clientMsgNo string, beforeSeq uint64, limit int) ([]messageRow, uint64, bool, error) {
	if err := t.validate(); err != nil {
		return nil, 0, false, err
	}
	if clientMsgNo == "" || limit <= 0 {
		return nil, 0, false, channel.ErrInvalidArgument
	}

	prefix := encodeMessageClientMsgNoIndexPrefix(t.channelKey, clientMsgNo)
	upperBound := keyUpperBound(prefix)
	seekKey := upperBound
	if beforeSeq > 0 {
		seekKey = encodeMessageClientMsgNoIndexKey(t.channelKey, clientMsgNo, beforeSeq)
	}

	iter, err := t.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upperBound})
	if err != nil {
		return nil, 0, false, err
	}
	defer iter.Close()

	rows := make([]messageRow, 0, limit)
	hasMore := false
	for valid := iter.SeekLT(seekKey); valid; valid = iter.Prev() {
		seq, err := decodeMessageClientMsgNoIndexSeq(iter.Key(), t.channelKey, clientMsgNo)
		if err != nil {
			return nil, 0, false, err
		}
		row, ok, err := t.getBySeq(seq)
		if err != nil {
			return nil, 0, false, err
		}
		if !ok || row.ClientMsgNo != clientMsgNo {
			return nil, 0, false, channel.ErrCorruptState
		}
		if len(rows) == limit {
			hasMore = true
			break
		}
		rows = append(rows, row)
	}
	if err := iter.Error(); err != nil {
		return nil, 0, false, err
	}
	if !hasMore || len(rows) == 0 {
		return rows, 0, false, nil
	}
	return rows, rows[len(rows)-1].MessageSeq, true, nil
}

func (t *messageTable) truncateFromSeq(writeBatch *pebble.Batch, fromSeq uint64) error {
	if err := t.validate(); err != nil {
		return err
	}
	if writeBatch == nil {
		return channel.ErrInvalidArgument
	}
	if fromSeq == 0 {
		fromSeq = 1
	}

	prefix := encodeTableStatePrefix(t.channelKey, TableIDMessage)
	iter, err := t.db.NewIter(&pebble.IterOptions{
		LowerBound: encodeTableStateKey(t.channelKey, TableIDMessage, fromSeq, messagePrimaryFamilyID),
		UpperBound: keyUpperBound(prefix),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	var skipPayloadSeq uint64
	for valid := iter.First(); valid; valid = iter.Next() {
		seq, familyID, err := decodeTableStateKey(iter.Key(), t.channelKey, TableIDMessage)
		if err != nil {
			return err
		}
		processRow, err := consumeForwardMessageFamily(seq, familyID, &skipPayloadSeq)
		if err != nil {
			return err
		}
		if !processRow {
			continue
		}
		row, err := t.materializeRow(seq, iter.Value())
		if err != nil {
			return err
		}
		if err := writeBatch.Delete(encodeTableStateKey(t.channelKey, TableIDMessage, row.MessageSeq, messagePrimaryFamilyID), pebble.NoSync); err != nil {
			return err
		}
		if err := writeBatch.Delete(encodeTableStateKey(t.channelKey, TableIDMessage, row.MessageSeq, messagePayloadFamilyID), pebble.NoSync); err != nil {
			return err
		}
		if err := writeBatch.Delete(encodeMessageIDIndexKey(t.channelKey, row.MessageID), pebble.NoSync); err != nil {
			return err
		}
		if row.ClientMsgNo != "" {
			if err := writeBatch.Delete(encodeMessageClientMsgNoIndexKey(t.channelKey, row.ClientMsgNo, row.MessageSeq), pebble.NoSync); err != nil {
				return err
			}
		}
		if row.FromUID != "" && row.ClientMsgNo != "" {
			if err := writeBatch.Delete(encodeMessageIdempotencyIndexKey(t.channelKey, row.FromUID, row.ClientMsgNo), pebble.NoSync); err != nil {
				return err
			}
		}
		skipPayloadSeq = seq
	}
	return iter.Error()
}

// deletePrefixThrough removes every materialized message row at or below throughSeq.
func (t *messageTable) deletePrefixThrough(writeBatch *pebble.Batch, throughSeq uint64) (uint64, int, error) {
	if err := t.validate(); err != nil {
		return 0, 0, err
	}
	if writeBatch == nil || throughSeq == 0 {
		return 0, 0, channel.ErrInvalidArgument
	}

	prefix := encodeTableStatePrefix(t.channelKey, TableIDMessage)
	upperBound := keyUpperBound(prefix)
	if throughSeq < math.MaxUint64 {
		upperBound = encodeTableStateKey(t.channelKey, TableIDMessage, throughSeq+1, messagePrimaryFamilyID)
	}
	iter, err := t.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upperBound})
	if err != nil {
		return 0, 0, err
	}
	defer iter.Close()

	var (
		deletedThroughSeq uint64
		deleted           int
		skipPayloadSeq    uint64
	)
	for valid := iter.First(); valid; valid = iter.Next() {
		seq, familyID, err := decodeTableStateKey(iter.Key(), t.channelKey, TableIDMessage)
		if err != nil {
			return 0, 0, err
		}
		processRow, err := consumeForwardMessageFamily(seq, familyID, &skipPayloadSeq)
		if err != nil {
			return 0, 0, err
		}
		if !processRow {
			continue
		}
		row, err := t.materializeRow(seq, iter.Value())
		if err != nil {
			return 0, 0, err
		}
		if err := writeBatch.Delete(encodeTableStateKey(t.channelKey, TableIDMessage, row.MessageSeq, messagePrimaryFamilyID), pebble.NoSync); err != nil {
			return 0, 0, err
		}
		if err := writeBatch.Delete(encodeTableStateKey(t.channelKey, TableIDMessage, row.MessageSeq, messagePayloadFamilyID), pebble.NoSync); err != nil {
			return 0, 0, err
		}
		if err := writeBatch.Delete(encodeMessageIDIndexKey(t.channelKey, row.MessageID), pebble.NoSync); err != nil {
			return 0, 0, err
		}
		if row.ClientMsgNo != "" {
			if err := writeBatch.Delete(encodeMessageClientMsgNoIndexKey(t.channelKey, row.ClientMsgNo, row.MessageSeq), pebble.NoSync); err != nil {
				return 0, 0, err
			}
		}
		if row.FromUID != "" && row.ClientMsgNo != "" {
			if err := writeBatch.Delete(encodeMessageIdempotencyIndexKey(t.channelKey, row.FromUID, row.ClientMsgNo), pebble.NoSync); err != nil {
				return 0, 0, err
			}
		}
		deletedThroughSeq = row.MessageSeq
		deleted++
		skipPayloadSeq = seq
	}
	if err := iter.Error(); err != nil {
		return 0, 0, err
	}
	return deletedThroughSeq, deleted, nil
}

func (t *messageTable) lookupIdempotency(fromUID, clientMsgNo string) (messageIndexHit, bool, error) {
	if err := t.validate(); err != nil {
		return messageIndexHit{}, false, err
	}
	if fromUID == "" || clientMsgNo == "" {
		return messageIndexHit{}, false, channel.ErrInvalidArgument
	}

	value, ok, err := t.getValue(encodeMessageIdempotencyIndexKey(t.channelKey, fromUID, clientMsgNo))
	if err != nil || !ok {
		return messageIndexHit{}, ok, err
	}
	hit, err := decodeIdempotencyIndexValue(value)
	if err != nil {
		return messageIndexHit{}, false, err
	}
	row, ok, err := t.getBySeq(hit.MessageSeq)
	if err != nil {
		return messageIndexHit{}, false, err
	}
	if !ok || row.MessageID != hit.MessageID || row.PayloadHash != hit.PayloadHash || row.FromUID != fromUID || row.ClientMsgNo != clientMsgNo {
		return messageIndexHit{}, false, channel.ErrCorruptState
	}
	return hit, true, nil
}

func (t *messageTable) maxSeq() (uint64, error) {
	if err := t.validate(); err != nil {
		return 0, err
	}

	prefix := encodeTableStatePrefix(t.channelKey, TableIDMessage)
	iter, err := t.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: keyUpperBound(prefix)})
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	if !iter.Last() {
		return 0, nil
	}
	seq, _, err := decodeTableStateKey(iter.Key(), t.channelKey, TableIDMessage)
	if err != nil {
		return 0, err
	}
	if _, ok, err := t.getBySeq(seq); err != nil {
		return 0, err
	} else if !ok {
		return 0, channel.ErrCorruptState
	}
	return seq, nil
}

func (t *messageTable) lookupMessageIDSeq(messageID uint64) (uint64, bool, error) {
	value, closer, ok, err := t.getBorrowedValue(encodeMessageIDIndexKey(t.channelKey, messageID))
	if err != nil || !ok {
		return 0, ok, err
	}
	defer closer.Close()
	seq, err := decodeMessageIDIndexValue(value)
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

// lookupMessageIDSeqForAppend reuses write-path scratch key storage and borrows
// the Pebble value because appendRow only needs the decoded sequence.
func (t *messageTable) lookupMessageIDSeqForAppend(messageID uint64) (uint64, bool, error) {
	keyLen := encodedTableIndexPrefixLen(t.channelKey) + 8
	if cap(t.appendLookupKey) < keyLen {
		t.appendLookupKey = make([]byte, 0, keyLen)
	}
	key := appendTableIndexPrefixTo(t.appendLookupKey[:0], t.channelKey, TableIDMessage, messageIndexIDMessageID)
	key = key[:keyLen]
	binary.BigEndian.PutUint64(key[keyLen-8:], messageID)
	t.appendLookupKey = key

	value, closer, ok, err := t.getBorrowedValue(key)
	if err != nil || !ok {
		return 0, ok, err
	}
	defer closer.Close()
	seq, err := decodeMessageIDIndexValue(value)
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func (t *messageTable) materializeRow(seq uint64, primary []byte) (messageRow, error) {
	payload, ok, err := t.getValue(encodeTableStateKey(t.channelKey, TableIDMessage, seq, messagePayloadFamilyID))
	if err != nil {
		return messageRow{}, err
	}
	if !ok {
		return messageRow{}, channel.ErrCorruptState
	}
	row, err := decodeMessageFamilies(seq, append([]byte(nil), primary...), payload)
	if err != nil {
		return messageRow{}, err
	}
	if err := validateMaterializedMessageRow(row); err != nil {
		return messageRow{}, err
	}
	return row, nil
}

func validateMaterializedMessageRow(row messageRow) error {
	if row.MessageID == 0 {
		return channel.ErrCorruptValue
	}
	if row.PayloadHash != hashMessagePayload(row.Payload) {
		return channel.ErrCorruptState
	}
	return nil
}

func (t *messageTable) getValue(key []byte) ([]byte, bool, error) {
	value, closer, err := t.db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer closer.Close()
	return append([]byte(nil), value...), true, nil
}

// getBorrowedValue returns a Pebble-owned value view that must be closed by
// the caller before the next use of the slice escapes.
func (t *messageTable) getBorrowedValue(key []byte) ([]byte, io.Closer, bool, error) {
	value, closer, err := t.db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	return value, closer, true, nil
}

func (t *messageTable) validate() error {
	if t == nil || t.db == nil || t.channelKey == "" {
		return channel.ErrInvalidArgument
	}
	return nil
}

func encodeMessageIDIndexKey(channelKey channel.ChannelKey, messageID uint64) []byte {
	key := encodeTableIndexPrefixWithExtra(channelKey, TableIDMessage, messageIndexIDMessageID, 8)
	return binary.BigEndian.AppendUint64(key, messageID)
}

func encodeMessageClientMsgNoIndexPrefix(channelKey channel.ChannelKey, clientMsgNo string) []byte {
	key := encodeTableIndexPrefixWithExtra(channelKey, TableIDMessage, messageIndexIDClientMsgNo, uvarintLen(uint64(len(clientMsgNo)))+len(clientMsgNo))
	return appendKeyString(key, clientMsgNo)
}

func encodeMessageClientMsgNoIndexKey(channelKey channel.ChannelKey, clientMsgNo string, seq uint64) []byte {
	key := encodeTableIndexPrefixWithExtra(channelKey, TableIDMessage, messageIndexIDClientMsgNo, uvarintLen(uint64(len(clientMsgNo)))+len(clientMsgNo)+8)
	key = appendKeyString(key, clientMsgNo)
	return binary.BigEndian.AppendUint64(key, seq)
}

func decodeMessageClientMsgNoIndexSeq(key []byte, channelKey channel.ChannelKey, clientMsgNo string) (uint64, error) {
	prefix := encodeMessageClientMsgNoIndexPrefix(channelKey, clientMsgNo)
	if len(key) != len(prefix)+8 || !bytes.Equal(key[:len(prefix)], prefix) {
		return 0, channel.ErrCorruptValue
	}
	return binary.BigEndian.Uint64(key[len(prefix):]), nil
}

func encodeMessageIdempotencyIndexKey(channelKey channel.ChannelKey, fromUID, clientMsgNo string) []byte {
	key := encodeTableIndexPrefixWithExtra(
		channelKey,
		TableIDMessage,
		messageIndexIDFromUIDClientMsgNo,
		uvarintLen(uint64(len(fromUID)))+len(fromUID)+uvarintLen(uint64(len(clientMsgNo)))+len(clientMsgNo),
	)
	key = appendKeyString(key, fromUID)
	return appendKeyString(key, clientMsgNo)
}

func (r messageRow) compatibilityEncodedRecordSize() (int, error) {
	return compatibilityRecordPayloadSize(r)
}

// consumeForwardMessageFamily validates the expected primary -> payload family order.
func consumeForwardMessageFamily(seq uint64, familyID uint16, skipPayloadSeq *uint64) (bool, error) {
	switch familyID {
	case messagePrimaryFamilyID:
		if skipPayloadSeq != nil && *skipPayloadSeq != 0 {
			return false, channel.ErrCorruptState
		}
		return true, nil
	case messagePayloadFamilyID:
		if skipPayloadSeq == nil || *skipPayloadSeq != seq {
			return false, channel.ErrCorruptState
		}
		*skipPayloadSeq = 0
		return false, nil
	default:
		return false, channel.ErrCorruptState
	}
}

// consumeReverseMessageFamily validates the expected payload -> primary order during reverse scans.
func consumeReverseMessageFamily(seq uint64, familyID uint16, pendingPayloadSeq *uint64) (bool, error) {
	switch familyID {
	case messagePayloadFamilyID:
		if pendingPayloadSeq == nil || *pendingPayloadSeq != 0 {
			return false, channel.ErrCorruptState
		}
		*pendingPayloadSeq = seq
		return false, nil
	case messagePrimaryFamilyID:
		if pendingPayloadSeq != nil {
			if *pendingPayloadSeq != 0 && *pendingPayloadSeq != seq {
				return false, channel.ErrCorruptState
			}
			*pendingPayloadSeq = 0
		}
		return true, nil
	default:
		return false, channel.ErrCorruptState
	}
}
